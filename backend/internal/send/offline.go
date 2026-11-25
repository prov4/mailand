package send

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	mailpkg "mailand/backend/internal/mail"
	"mailand/backend/internal/store"
)

// OfflineSender backs `mailand-daemon -offline`: nothing is transmitted; the
// composed message is stored as a read message in the account's \Sent folder
// (blob included) so the UI can be exercised end to end.
type OfflineSender struct {
	st      *store.Store
	blobDir string
	notify  func(accountID, folderID string) // called after the message is stored; may be nil

	mu sync.Mutex // serialises UID allocation
}

// NewOfflineSender creates the sender. notify is typically a NEW_MESSAGES publisher.
func NewOfflineSender(st *store.Store, blobDir string, notify func(accountID, folderID string)) *OfflineSender {
	return &OfflineSender{st: st, blobDir: blobDir, notify: notify}
}

// Send implements the same contract as Sender.Send.
func (o *OfflineSender) Send(ctx context.Context, req Request) (string, error) {
	if err := Validate(req); err != nil {
		return "", err
	}
	acct, err := o.st.GetAccount(ctx, req.AccountID)
	if errors.Is(err, store.ErrNotFound) {
		return "", invalid("unknown account %q", req.AccountID)
	} else if err != nil {
		return "", err
	}
	now := time.Now()
	b, err := Build(ctx, o.st, acct, req, now)
	if err != nil {
		return "", err
	}
	parsed, _ := mailpkg.Parse(bytes.NewReader(b.Raw))
	if parsed == nil {
		parsed = &mailpkg.Parsed{}
	}

	o.mu.Lock()
	defer o.mu.Unlock()
	source, memberships, err := o.sentFolders(ctx, acct.ID)
	if err != nil {
		return "", err
	}
	uid, err := o.st.MaxUID(ctx, source)
	if err != nil {
		return "", err
	}
	uid++

	dir := filepath.Join(o.blobDir, acct.ID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	blob := filepath.Join(dir, fmt.Sprintf("offline-sent-%d-%d.eml", now.UnixNano(), uid))
	if err := os.WriteFile(blob, b.Raw, 0o600); err != nil {
		return "", err
	}
	type addr struct{ Name, Address string }
	toJSON := func(l []addr) string {
		if len(l) == 0 {
			return "[]"
		}
		j, _ := json.Marshal(l)
		return string(j)
	}
	var to, cc []addr
	for _, a := range b.To {
		to = append(to, addr{a.Name, a.Address})
	}
	for _, a := range b.Cc {
		cc = append(cc, addr{a.Name, a.Address})
	}
	_, err = o.st.UpsertMessage(ctx, store.Message{
		MessageHeader: store.MessageHeader{
			AccountID: acct.ID, FolderID: source, UID: uid, Subject: parsed.Subject,
			FromName: acct.Name, FromAddress: acct.Email, TimestampUnix: now.Unix(),
			IsRead: true, HasAttachments: parsed.HasAttachments, Snippet: parsed.Snippet,
		},
		MessageIDHeader: b.MessageID, ToJSON: toJSON(to), CcJSON: toJSON(cc),
		TextBody: parsed.TextBody, HTMLBody: parsed.HTMLBody, RawBlobPath: blob,
	}, memberships)
	if err != nil {
		_ = os.Remove(blob)
		return "", err
	}
	if req.ReplyToID != "" {
		_ = o.st.SetAnswered(ctx, req.ReplyToID)
	}
	if o.notify != nil {
		o.notify(acct.ID, source)
	}
	return b.MessageID, nil
}

// sentFolders picks the message's source folder and memberships. Normally the
// \Sent folder itself; for Gmail-style accounts, where \Sent is a label, the
// sync-source \All folder plus the label. If no \Sent folder exists, one named
// "Sent" is created.
func (o *OfflineSender) sentFolders(ctx context.Context, accountID string) (string, []string, error) {
	fs, err := o.st.ListFolders(ctx, accountID)
	if err != nil {
		return "", nil, err
	}
	var sent, all *store.Folder
	for i := range fs {
		switch fs[i].SpecialUse {
		case `\Sent`:
			if sent == nil || (fs[i].IsSyncSource && !sent.IsSyncSource) {
				sent = &fs[i]
			}
		case `\All`:
			all = &fs[i]
		}
	}
	if sent == nil {
		f, err := o.st.UpsertFolder(ctx, store.Folder{AccountID: accountID, Name: "Sent", Path: "Sent",
			Kind: "folder", SpecialUse: `\Sent`, IsSyncSource: true})
		if err != nil {
			return "", nil, err
		}
		return f.ID, nil, nil
	}
	if sent.IsSyncSource || all == nil {
		return sent.ID, nil, nil
	}
	return all.ID, []string{all.ID, sent.ID}, nil
}
