package mail

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/emersion/go-imap/v2"

	"mailand/backend/internal/store"
)

// SetFlags pushes \Seen / \Flagged changes to the IMAP server (UID STORE), then
// updates the store and emits EventFlagsUpdated per affected account. nil
// leaves a flag unchanged; unknown message ids are skipped. Each account gets a
// short-lived dedicated connection so a long sync pass never delays the user's
// action. If the server push fails the store is left untouched.
func (m *Manager) SetFlags(ctx context.Context, messageIDs []string, isRead, isFlagged *bool) error {
	if isRead == nil && isFlagged == nil {
		return nil
	}
	// account -> folder path -> UIDs
	type target map[string][]imap.UID
	byAcct := map[string]target{}
	for _, id := range messageIDs {
		msg, err := m.st.GetMessage(ctx, id)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		f, err := m.st.GetFolder(ctx, msg.FolderID)
		if err != nil {
			return err
		}
		if byAcct[msg.AccountID] == nil {
			byAcct[msg.AccountID] = target{}
		}
		byAcct[msg.AccountID][f.Path] = append(byAcct[msg.AccountID][f.Path], imap.UID(msg.UID))
	}

	for acct, folders := range byAcct {
		m.mu.RLock()
		w := m.workers[acct]
		m.mu.RUnlock()
		if w == nil {
			return fmt.Errorf("account %q is not running", acct)
		}
		if err := w.pushFlags(ctx, folders, isRead, isFlagged); err != nil {
			return fmt.Errorf("account %q: %w", acct, err)
		}
	}
	if err := m.st.SetFlags(ctx, messageIDs, isRead, isFlagged); err != nil {
		return err
	}
	for acct := range byAcct {
		m.emit(Event{Type: EventFlagsUpdated, AccountID: acct})
	}
	return nil
}

func (w *worker) pushFlags(ctx context.Context, folders map[string][]imap.UID, isRead, isFlagged *bool) error {
	c, err := w.connect(ctx, nil)
	if err != nil {
		return err
	}
	defer c.close()
	for path, uids := range folders {
		if _, err := c.Select(path, nil).Wait(); err != nil {
			return fmt.Errorf("select %q: %w", path, err)
		}
		set := imap.UIDSetNum(uids...)
		for _, op := range []struct {
			on   *bool
			flag imap.Flag
		}{{isRead, imap.FlagSeen}, {isFlagged, imap.FlagFlagged}} {
			if op.on == nil {
				continue
			}
			sf := &imap.StoreFlags{Op: imap.StoreFlagsAdd, Silent: true, Flags: []imap.Flag{op.flag}}
			if !*op.on {
				sf.Op = imap.StoreFlagsDel
			}
			if err := c.Store(set, sf, nil).Close(); err != nil {
				return fmt.Errorf("store flags: %w", err)
			}
		}
	}
	return nil
}

// MarkAnswered sets the IMAP \Answered flag on messageID and updates the store.
func (m *Manager) MarkAnswered(ctx context.Context, messageID string) error {
	msg, err := m.st.GetMessage(ctx, messageID)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	f, err := m.st.GetFolder(ctx, msg.FolderID)
	if err != nil {
		return err
	}
	m.mu.RLock()
	w := m.workers[msg.AccountID]
	m.mu.RUnlock()
	if w == nil {
		return fmt.Errorf("account %q is not running", msg.AccountID)
	}
	c, err := w.connect(ctx, nil)
	if err != nil {
		return err
	}
	defer c.close()
	if _, err := c.Select(f.Path, nil).Wait(); err != nil {
		return fmt.Errorf("select %q: %w", f.Path, err)
	}
	set := imap.UIDSetNum(imap.UID(msg.UID))
	if err := c.Store(set, &imap.StoreFlags{Op: imap.StoreFlagsAdd, Silent: true, Flags: []imap.Flag{imap.FlagAnswered}}, nil).Close(); err != nil {
		return fmt.Errorf("store answered: %w", err)
	}
	if err := m.st.SetAnswered(ctx, messageID); err != nil {
		return err
	}
	m.emit(Event{Type: EventFlagsUpdated, AccountID: msg.AccountID})
	return nil
}

// ArchiveMessages archives the given messages on the IMAP server and updates
// the local store:
//   - Gmail: removes them from INBOX (they stay in [Gmail]/All Mail).
//   - Standard IMAP: moves them to the account's \Archive (or "Archive") folder.
func (m *Manager) ArchiveMessages(ctx context.Context, messageIDs []string) error {
	byAcct := map[string][]store.Message{}
	for _, id := range messageIDs {
		msg, err := m.st.GetMessage(ctx, id)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		byAcct[msg.AccountID] = append(byAcct[msg.AccountID], msg)
	}
	for acct, msgs := range byAcct {
		m.mu.RLock()
		w := m.workers[acct]
		m.mu.RUnlock()
		if w == nil {
			return fmt.Errorf("account %q is not running", acct)
		}
		if err := w.archiveMessages(ctx, msgs); err != nil {
			return fmt.Errorf("account %q: %w", acct, err)
		}
		m.emit(Event{Type: EventFlagsUpdated, AccountID: acct, Detail: "labels"})
	}
	return nil
}

func (w *worker) archiveMessages(ctx context.Context, msgs []store.Message) error {
	folders, err := w.m.st.ListFolders(ctx, w.acct.ID)
	if err != nil {
		return err
	}
	c, err := w.connect(ctx, nil)
	if err != nil {
		return err
	}
	defer c.close()

	if c.gmail(w.acct.Provider) {
		var inbox *store.Folder
		for i := range folders {
			if folders[i].SpecialUse == `\Inbox` || strings.EqualFold(folders[i].Path, "INBOX") {
				inbox = &folders[i]
				break
			}
		}
		if inbox == nil {
			return errors.New("INBOX folder not found")
		}
		if _, err := c.Select(inbox.Path, nil).Wait(); err != nil {
			return fmt.Errorf("select %q: %w", inbox.Path, err)
		}
		var inboxUIDs []imap.UID
		for _, msg := range msgs {
			mid := strings.Trim(strings.TrimSpace(msg.MessageIDHeader), "<>")
			if mid == "" {
				continue
			}
			res, err := c.UIDSearch(&imap.SearchCriteria{
				Header: []imap.SearchCriteriaHeaderField{{Key: "Message-ID", Value: mid}},
			}, nil).Wait()
			if err != nil {
				return fmt.Errorf("search INBOX: %w", err)
			}
			inboxUIDs = append(inboxUIDs, res.AllUIDs()...)
		}
		if len(inboxUIDs) > 0 {
			set := imap.UIDSetNum(inboxUIDs...)
			if err := c.Store(set, &imap.StoreFlags{Op: imap.StoreFlagsAdd, Silent: true, Flags: []imap.Flag{imap.FlagDeleted}}, nil).Close(); err != nil {
				return fmt.Errorf("mark deleted in INBOX: %w", err)
			}
			if c.caps.Has(imap.CapUIDPlus) {
				if err := c.UIDExpunge(set).Close(); err != nil {
					return fmt.Errorf("uid expunge INBOX: %w", err)
				}
			} else if err := c.Expunge().Close(); err != nil {
				return fmt.Errorf("expunge INBOX: %w", err)
			}
		}
		for _, msg := range msgs {
			if err := w.m.st.RemoveMessageFromFolder(ctx, msg.Seq, inbox.ID); err != nil {
				return err
			}
		}
		return nil
	}

	// Non-Gmail IMAP: find Archive folder and move messages there.
	var archive *store.Folder
	for i := range folders {
		if folders[i].SpecialUse == `\Archive` {
			archive = &folders[i]
			break
		}
	}
	if archive == nil {
		for i := range folders {
			if strings.EqualFold(folders[i].Path, "Archive") || strings.EqualFold(folders[i].Path, "Archives") {
				archive = &folders[i]
				break
			}
		}
	}
	if archive == nil {
		return errors.New("no Archive folder found")
	}

	byFolder := map[string][]store.Message{}
	for _, msg := range msgs {
		if msg.FolderID == archive.ID {
			continue
		}
		byFolder[msg.FolderID] = append(byFolder[msg.FolderID], msg)
	}
	folderByID := map[string]store.Folder{}
	for _, f := range folders {
		folderByID[f.ID] = f
	}
	movedAny := false
	for fid, list := range byFolder {
		src, ok := folderByID[fid]
		if !ok {
			continue
		}
		if _, err := c.Select(src.Path, nil).Wait(); err != nil {
			return fmt.Errorf("select %q: %w", src.Path, err)
		}
		uids := make([]imap.UID, len(list))
		rawUIDs := make([]uint32, len(list))
		for i, msg := range list {
			uids[i] = imap.UID(msg.UID)
			rawUIDs[i] = msg.UID
		}
		set := imap.UIDSetNum(uids...)
		if c.caps.Has(imap.CapMove) {
			if _, err := c.Move(set, archive.Path).Wait(); err != nil {
				return fmt.Errorf("move to %q: %w", archive.Path, err)
			}
		} else {
			if _, err := c.Copy(set, archive.Path).Wait(); err != nil {
				return fmt.Errorf("copy to %q: %w", archive.Path, err)
			}
			if err := c.Store(set, &imap.StoreFlags{Op: imap.StoreFlagsAdd, Silent: true, Flags: []imap.Flag{imap.FlagDeleted}}, nil).Close(); err != nil {
				return fmt.Errorf("mark deleted in %q: %w", src.Path, err)
			}
			if c.caps.Has(imap.CapUIDPlus) {
				if err := c.UIDExpunge(set).Close(); err != nil {
					return fmt.Errorf("uid expunge %q: %w", src.Path, err)
				}
			} else if err := c.Expunge().Close(); err != nil {
				return fmt.Errorf("expunge %q: %w", src.Path, err)
			}
		}
		if err := w.m.st.DeleteByUIDs(ctx, src.ID, rawUIDs); err != nil {
			return err
		}
		movedAny = true
	}
	if movedAny {
		select {
		case w.trigger <- struct{}{}:
		default:
		}
	}
	return nil
}
