package mail

import (
	"context"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"strings"

	"github.com/emersion/go-imap/v2"

	"mailand/backend/internal/store"
)

var specialUseAttrs = map[imap.MailboxAttr]string{
	imap.MailboxAttrAll:       `\All`,
	imap.MailboxAttrArchive:   `\Archive`,
	imap.MailboxAttrDrafts:    `\Drafts`,
	imap.MailboxAttrFlagged:   `\Flagged`,
	imap.MailboxAttrJunk:      `\Junk`,
	imap.MailboxAttrSent:      `\Sent`,
	imap.MailboxAttrTrash:     `\Trash`,
	imap.MailboxAttrImportant: `\Important`,
}

func folderID(accountID, path string) string { return accountID + ":" + path }

// classify derives folder kind, special-use and sync-source status from a LIST
// entry. Gmail rule (STARTUP 6D): only \All, \Junk, \Trash are sync sources;
// everything else is a label. Detection is by SPECIAL-USE, never by name.
func classify(l *imap.ListData, gmail bool) (special, kind string, source bool) {
	for _, a := range l.Attrs {
		if s, ok := specialUseAttrs[a]; ok {
			special = s
			break
		}
	}
	if strings.EqualFold(l.Mailbox, "INBOX") {
		special = `\Inbox`
	}
	if !gmail {
		return special, "folder", true
	}
	switch special {
	case `\All`, `\Junk`, `\Trash`:
		return special, "folder", true
	}
	return special, "gmail_label", false
}

// syncFolders mirrors the server's mailbox list into the store and removes
// folders that disappeared. It returns the folders now in the store.
func (w *worker) syncFolders(ctx context.Context, c *conn) ([]store.Folder, error) {
	var opts *imap.ListOptions
	if c.caps.Has(imap.CapSpecialUse) {
		opts = &imap.ListOptions{ReturnSpecialUse: true}
	}
	list, err := c.List("", "*", opts).Collect()
	if err != nil {
		return nil, fmt.Errorf("list: %w", err)
	}
	gmail := c.gmail(w.acct.Provider)
	seen := map[string]bool{}
	for _, l := range list {
		if hasAttr(l, imap.MailboxAttrNoSelect) || hasAttr(l, imap.MailboxAttrNonExistent) {
			continue
		}
		special, kind, source := classify(l, gmail)
		name := l.Mailbox
		if l.Delim != 0 {
			if i := strings.LastIndexByte(name, byte(l.Delim)); i >= 0 {
				name = name[i+1:]
			}
		}
		f, err := w.m.st.UpsertFolder(ctx, store.Folder{
			ID: folderID(w.acct.ID, l.Mailbox), AccountID: w.acct.ID, Name: name, Path: l.Mailbox,
			Kind: kind, SpecialUse: special, IsSyncSource: source,
		})
		if err != nil {
			return nil, err
		}
		seen[f.ID] = true
	}
	stored, err := w.m.st.ListFolders(ctx, w.acct.ID)
	if err != nil {
		return nil, err
	}
	out := stored[:0]
	for _, f := range stored {
		if seen[f.ID] {
			out = append(out, f)
			continue
		}
		if err := w.m.st.DeleteFolder(ctx, f.ID); err != nil {
			return nil, err
		}
		w.removeFolderBlobs(f.Path)
		w.log.Info("folder removed on server", "path", f.Path)
	}
	return out, nil
}

func hasAttr(l *imap.ListData, a imap.MailboxAttr) bool {
	for _, x := range l.Attrs {
		if x == a {
			return true
		}
	}
	return false
}

// Raw messages live in <blobDir>/<account>/<pathhash>_<uidvalidity>_<uid>.eml.
// The name is derived from (folder, UIDVALIDITY, UID), so expunges and
// UIDVALIDITY resets can delete blobs without a store lookup.
func (w *worker) blobDir() string { return filepath.Join(w.m.blobDir, w.acct.ID) }

func pathHash(path string) string {
	h := fnv.New32a()
	h.Write([]byte(path))
	return fmt.Sprintf("%08x", h.Sum32())
}

func (w *worker) blobPath(path string, uidValidity, uid uint32) string {
	return filepath.Join(w.blobDir(), fmt.Sprintf("%s_%d_%d.eml", pathHash(path), uidValidity, uid))
}

func (w *worker) removeFolderBlobs(path string) {
	files, _ := filepath.Glob(filepath.Join(w.blobDir(), pathHash(path)+"_*.eml"))
	for _, f := range files {
		_ = os.Remove(f)
	}
}
