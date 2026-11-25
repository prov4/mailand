package rpc

import (
	"context"
	"strings"
	"time"

	"mailand/backend/internal/dav"

	"mailand/backend/internal/rpc/pb"
	"mailand/backend/internal/store"
)

// OfflineSyncer is a store-only Syncer for `mailand-daemon -offline`: flag
// changes are written to the store and announced, TriggerSync only emits
// SYNC_STARTED / SYNC_FINISHED.
type OfflineSyncer struct {
	st     *store.Store
	broker *Broker
}

// NewOfflineSyncer returns a Syncer that never talks to IMAP.
func NewOfflineSyncer(st *store.Store, broker *Broker) *OfflineSyncer {
	return &OfflineSyncer{st: st, broker: broker}
}

// SetFlags writes flags to the store and publishes FLAGS_UPDATED once per
// affected account. Unknown message ids are skipped.
func (o *OfflineSyncer) SetFlags(ctx context.Context, ids []string, isRead, isFlagged *bool) error {
	accounts := map[string]bool{}
	var order []string
	for _, id := range ids {
		m, err := o.st.GetMessage(ctx, id)
		if err != nil {
			continue
		}
		if !accounts[m.AccountID] {
			accounts[m.AccountID] = true
			order = append(order, m.AccountID)
		}
	}
	if err := o.st.SetFlags(ctx, ids, isRead, isFlagged); err != nil {
		return err
	}
	for _, acc := range order {
		o.broker.Publish(&pb.DaemonEvent{Type: pb.DaemonEvent_FLAGS_UPDATED, AccountId: acc})
	}
	return nil
}

// ArchiveMessages removes the message's INBOX membership in the store and
// publishes FLAGS_UPDATED.
func (o *OfflineSyncer) ArchiveMessages(ctx context.Context, ids []string) error {
	accounts := map[string]bool{}
	var order []string
	for _, id := range ids {
		m, err := o.st.GetMessage(ctx, id)
		if err != nil {
			continue
		}
		folders, err := o.st.ListFolders(ctx, m.AccountID)
		if err != nil {
			return err
		}
		for _, f := range folders {
			if f.SpecialUse == `\Inbox` || strings.EqualFold(f.Path, "INBOX") {
				if err := o.st.RemoveMessageFromFolder(ctx, m.Seq, f.ID); err != nil {
					return err
				}
				break
			}
		}
		if !accounts[m.AccountID] {
			accounts[m.AccountID] = true
			order = append(order, m.AccountID)
		}
	}
	for _, acc := range order {
		o.broker.Publish(&pb.DaemonEvent{Type: pb.DaemonEvent_FLAGS_UPDATED, AccountId: acc, Detail: "labels"})
	}
	return nil
}

// TriggerSync reports an unknown account as store.ErrNotFound.
func (o *OfflineSyncer) TriggerSync(accountID string) error {
	if _, err := o.st.GetAccount(context.Background(), accountID); err != nil {
		return err
	}
	o.broker.Publish(&pb.DaemonEvent{Type: pb.DaemonEvent_SYNC_STARTED, AccountId: accountID})
	o.broker.Publish(&pb.DaemonEvent{Type: pb.DaemonEvent_SYNC_FINISHED, AccountId: accountID})
	return nil
}

// OfflineResponder answers invitations in the store only (`-offline`).
type OfflineResponder struct {
	st     *store.Store
	broker *Broker
}

// NewOfflineResponder returns a Responder that never talks to a server.
func NewOfflineResponder(st *store.Store, broker *Broker) *OfflineResponder {
	return &OfflineResponder{st: st, broker: broker}
}

// Respond rewrites the stored object and publishes CALENDARS_UPDATED.
func (o *OfflineResponder) Respond(ctx context.Context, seq int64, status string) error {
	obj, cal, err := o.st.GetCalendarObject(ctx, seq)
	if err != nil {
		return err
	}
	acc, err := o.st.GetAccount(ctx, cal.AccountID)
	if err != nil {
		return err
	}
	data, err := dav.RespondData(obj.Data, acc.Email, status, time.Now())
	if err != nil {
		return err
	}
	obj.Data = data
	if err := o.st.UpsertCalendarObjects(ctx, []store.CalendarObject{obj}); err != nil {
		return err
	}
	o.broker.Publish(&pb.DaemonEvent{Type: pb.DaemonEvent_CALENDARS_UPDATED, AccountId: cal.AccountID})
	return nil
}

// OfflineContacts edits contacts in the store only (`-offline`).
type OfflineContacts struct {
	st     *store.Store
	broker *Broker
}

// NewOfflineContacts returns a ContactEditor that never talks to a server.
func NewOfflineContacts(st *store.Store, broker *Broker) *OfflineContacts {
	return &OfflineContacts{st: st, broker: broker}
}

// SaveContact creates or updates the stored card.
func (o *OfflineContacts) SaveContact(ctx context.Context, seq int64, accountID string, in dav.ContactInput) (int64, error) {
	var book store.AddressBook
	var oldData, path string
	if seq != 0 {
		c, b, err := o.st.GetContact(ctx, seq)
		if err != nil {
			return 0, err
		}
		book, oldData, path = b, c.Data, c.Path
	} else {
		books, err := o.st.ListAddressBooks(ctx, accountID)
		if err != nil {
			return 0, err
		}
		for _, b := range books {
			if !b.ReadOnly {
				book = b
				break
			}
		}
		if book.ID == "" {
			return 0, dav.ErrNoAddressBook
		}
	}
	if book.ReadOnly {
		return 0, dav.ErrReadOnly
	}
	data, uid, err := dav.BuildCard(oldData, in)
	if err != nil {
		return 0, err
	}
	if path == "" {
		path = book.Path + uid + ".vcf"
	}
	row, err := dav.ContactRow(book.ID, path, "", data)
	if err != nil {
		return 0, err
	}
	if err := o.st.UpsertContacts(ctx, []store.Contact{row}); err != nil {
		return 0, err
	}
	o.broker.Publish(&pb.DaemonEvent{Type: pb.DaemonEvent_CONTACTS_UPDATED, AccountId: book.AccountID})
	return o.st.ContactSeq(ctx, book.ID, path)
}

// DeleteContact removes the stored card.
func (o *OfflineContacts) DeleteContact(ctx context.Context, seq int64) error {
	c, b, err := o.st.GetContact(ctx, seq)
	if err != nil {
		return err
	}
	if b.ReadOnly {
		return dav.ErrReadOnly
	}
	if err := o.st.DeleteContacts(ctx, b.ID, []string{c.Path}); err != nil {
		return err
	}
	o.broker.Publish(&pb.DaemonEvent{Type: pb.DaemonEvent_CONTACTS_UPDATED, AccountId: b.AccountID})
	return nil
}
