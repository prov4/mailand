package mail

import (
	"context"
	"errors"
	"fmt"

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
