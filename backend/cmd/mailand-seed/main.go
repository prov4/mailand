// Command mailand-seed writes a realistic, deterministic sample dataset
// (accounts, folders, ~400 messages with real .eml blobs) into the daemon's
// data directory, for UI development with `mailand-daemon -offline`.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"mailand/backend/internal/config"
	"mailand/backend/internal/mail"
	"mailand/backend/internal/store"
)

func main() {
	force := flag.Bool("force", false, "delete an existing database and blobs first")
	flag.Parse()
	if err := run(context.Background(), *force, time.Now(), os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "mailand-seed:", err)
		os.Exit(1)
	}
}

type folderDef struct {
	key, name, path, kind, special string
	source                         bool
}

var gmailFolders = []folderDef{
	{"inbox", "INBOX", "INBOX", "gmail_label", `\Inbox`, false},
	{"all", "All Mail", "[Gmail]/All Mail", "folder", `\All`, true},
	{"sent", "Sent Mail", "[Gmail]/Sent Mail", "gmail_label", `\Sent`, false},
	{"drafts", "Drafts", "[Gmail]/Drafts", "gmail_label", `\Drafts`, false},
	{"starred", "Starred", "[Gmail]/Starred", "gmail_label", `\Flagged`, false},
	{"spam", "Spam", "[Gmail]/Spam", "folder", `\Junk`, true},
	{"trash", "Trash", "[Gmail]/Trash", "folder", `\Trash`, true},
	{"posao", "Posao", "Posao", "gmail_label", "", false},
	{"racuni", "Računi", "Računi", "gmail_label", "", false},
	{"travel", "Travel", "Travel", "gmail_label", "", false},
}

var workFolders = []folderDef{
	{"inbox", "INBOX", "INBOX", "folder", `\Inbox`, true},
	{"sent", "Sent", "Sent", "folder", `\Sent`, true},
	{"archive", "Archive", "Archive", "folder", `\Archive`, true},
}

func run(ctx context.Context, force bool, now time.Time, out io.Writer) error {
	dbPath, blobDir := config.DBPath(), config.BlobDir()

	if _, err := os.Stat(dbPath); err == nil {
		st, err := store.Open(dbPath)
		if err != nil {
			return err
		}
		accs, err := st.ListAccounts(ctx)
		st.Close()
		if err != nil {
			return err
		}
		if len(accs) > 0 && !force {
			return fmt.Errorf("%s already has %d account(s); use -force to delete it and the blobs first", dbPath, len(accs))
		}
	}
	if force {
		for _, suffix := range []string{"", "-wal", "-shm"} {
			if err := os.Remove(dbPath + suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
		if err := os.RemoveAll(blobDir); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(blobDir, 0o700); err != nil {
		return err
	}

	st, err := store.Open(dbPath)
	if err != nil {
		return err
	}
	defer st.Close()

	accounts := []struct {
		acc     store.Account
		folders []folderDef
	}{
		{store.Account{ID: "gmail", Name: "Personal Gmail", Email: "demo@example.com", IMAPHost: "imap.gmail.com", IMAPPort: 993,
			SMTPHost: "smtp.gmail.com", SMTPPort: 465, AuthType: config.AuthOAuth2Google}, gmailFolders},
		{store.Account{ID: "work", Name: "Work", Email: "me@work.example", IMAPHost: "imap.work.example", IMAPPort: 993,
			SMTPHost: "smtp.work.example", SMTPPort: 465, AuthType: config.AuthPassword}, workFolders},
	}
	folderIDs := map[string]map[string]string{} // account -> key -> folder id
	nfolders := 0
	for _, a := range accounts {
		if err := st.UpsertAccount(ctx, a.acc); err != nil {
			return err
		}
		folderIDs[a.acc.ID] = map[string]string{}
		for _, d := range a.folders {
			f, err := st.UpsertFolder(ctx, store.Folder{AccountID: a.acc.ID, Name: d.name, Path: d.path, Kind: d.kind,
				SpecialUse: d.special, IsSyncSource: d.source, UIDValidity: 1})
			if err != nil {
				return err
			}
			folderIDs[a.acc.ID][d.key] = f.ID
			nfolders++
		}
	}

	msgs := build(now)
	uids := map[string]uint32{}
	counts := map[string]int{}
	for i, m := range msgs {
		p, err := mail.Parse(bytesReader(m.eml))
		if err != nil {
			return fmt.Errorf("parse generated message %d: %w", i, err)
		}
		dir := filepath.Join(blobDir, m.account)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		blob := filepath.Join(dir, fmt.Sprintf("%05d.eml", i+1))
		if err := os.WriteFile(blob, m.eml, 0o600); err != nil {
			return err
		}
		toJSON, _ := json.Marshal(nonNil(p.To))
		ccJSON, _ := json.Marshal(nonNil(p.Cc))

		src := folderIDs[m.account][m.source]
		uids[src]++
		memberships := []string{src}
		for _, l := range m.labels {
			memberships = append(memberships, folderIDs[m.account][l])
		}
		ts := m.ts
		if p.HasDate {
			ts = p.Date
		}
		msg := store.Message{
			MessageHeader: store.MessageHeader{
				AccountID: m.account, FolderID: src, UID: uids[src], Subject: p.Subject,
				FromName: p.FromName, FromAddress: p.FromAddress, TimestampUnix: ts.Unix(),
				IsRead: m.read, IsFlagged: m.flagged, HasAttachments: p.HasAttachments, Snippet: p.Snippet,
			},
			MessageIDHeader: p.MessageID, ToJSON: string(toJSON), CcJSON: string(ccJSON),
			TextBody: p.TextBody, HTMLBody: p.HTMLBody, RawBlobPath: blob,
		}
		if _, err := st.UpsertMessage(ctx, msg, memberships); err != nil {
			return fmt.Errorf("store message %d: %w", i, err)
		}
		counts[m.account]++
	}

	fmt.Fprintf(out, "Seeded %d accounts, %d folders, %d messages\n", len(accounts), nfolders, len(msgs))
	for _, a := range accounts {
		fs, err := st.ListFolders(ctx, a.acc.ID)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "  %s (%s): %d messages\n", a.acc.ID, a.acc.Email, counts[a.acc.ID])
		for _, f := range fs {
			_, total, err := st.ListMessages(ctx, f.ID, 1, 0)
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "    %-22s %-9s %4d messages, %3d unread\n", f.Path, f.SpecialUse, total, f.UnreadCount)
		}
	}
	fmt.Fprintf(out, "Database: %s\nBlobs:    %s\n", dbPath, blobDir)
	return nil
}

func nonNil(a []mail.Address) []mail.Address {
	if a == nil {
		return []mail.Address{}
	}
	return a
}

func bytesReader(b []byte) io.Reader { return bytes.NewReader(b) }
