package rpc

import (
	"context"

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

// TriggerSync reports an unknown account as store.ErrNotFound.
func (o *OfflineSyncer) TriggerSync(accountID string) error {
	if _, err := o.st.GetAccount(context.Background(), accountID); err != nil {
		return err
	}
	o.broker.Publish(&pb.DaemonEvent{Type: pb.DaemonEvent_SYNC_STARTED, AccountId: accountID})
	o.broker.Publish(&pb.DaemonEvent{Type: pb.DaemonEvent_SYNC_FINISHED, AccountId: accountID})
	return nil
}
