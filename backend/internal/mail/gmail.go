package mail

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/emersion/go-imap/v2"

	"mailand/backend/internal/store"
)

// Gmail label sync (fallback path).
//
// go-imap v2 beta.9 cannot fetch X-GM-MSGID / X-GM-THRID / X-GM-LABELS: its
// FETCH response parser returns "unsupported msg-att name" for unknown
// attributes and FetchOptions has no way to request them. Instead of
// hand-rolling wire protocol, each email is stored once, keyed by
// (source folder, uid) with GmMsgID nil (the source being All Mail, or
// Spam/Trash), and label membership is rebuilt by fetching ENVELOPE+UID
// (Message-ID only, no bodies) from every label folder and matching the
// Message-ID header against the stored rows.

// normMsgID makes Message-IDs comparable across parser and ENVELOPE output.
func normMsgID(s string) string {
	return strings.ToLower(strings.Trim(strings.TrimSpace(s), "<>"))
}

// applyLabels computes new memberships. refs are the stored messages, current
// the stored memberships, and fetched maps each label folder that was read this
// pass to the Message-IDs it contains. Folders not in fetched keep their
// membership. The result holds only messages whose membership changed, each
// with the full sorted new folder list (source folder + labels). unmapped
// counts Message-IDs in labels that match no stored message. Messages sharing a
// Message-ID all receive its labels.
func applyLabels(refs []store.MessageRef, current map[int64][]string, fetched map[string][]string) (changes map[int64][]string, unmapped int) {
	byID := map[string][]int{}
	for i, r := range refs {
		byID[normMsgID(r.MessageIDHeader)] = append(byID[normMsgID(r.MessageIDHeader)], i)
	}
	add := map[int][]string{}
	for folder, ids := range fetched {
		for _, id := range ids {
			idx, ok := byID[normMsgID(id)]
			if !ok {
				unmapped++
				continue
			}
			for _, i := range idx {
				add[i] = append(add[i], folder)
			}
		}
	}
	changes = map[int64][]string{}
	for i, r := range refs {
		set := map[string]bool{r.FolderID: true}
		for _, f := range current[r.Seq] {
			if _, wasFetched := fetched[f]; !wasFetched {
				set[f] = true
			}
		}
		for _, f := range add[i] {
			set[f] = true
		}
		next := make([]string, 0, len(set))
		for f := range set {
			next = append(next, f)
		}
		slices.Sort(next)
		prev := slices.Clone(current[r.Seq])
		slices.Sort(prev)
		if !slices.Equal(prev, next) {
			changes[r.Seq] = next
		}
	}
	return changes, unmapped
}

// syncLabels refreshes label membership. A label folder is skipped when its
// UIDVALIDITY and HIGHESTMODSEQ are unchanged (CONDSTORE), unless force is set
// (new mail arrived this pass, whose membership was just reset to its source).
// Folder state is only persisted when every label Message-ID was matched, so a
// label seen before its message was synced is retried next pass.
func (w *worker) syncLabels(ctx context.Context, c *conn, folders []store.Folder, force bool) error {
	type state struct{ uv, uidNext, modseq int64 }
	fetched := map[string][]string{}
	pending := map[string]state{}
	cond := c.caps.Has(imap.CapCondStore)

	for _, f := range folders {
		if f.Kind != "gmail_label" {
			continue
		}
		sel, err := c.selectRO(f.Path)
		if err != nil {
			if isConnErr(err) {
				return err
			}
			w.log.Warn("label select failed", "folder", f.Path, "err", err)
			continue
		}
		st := state{int64(sel.UIDValidity), int64(sel.UIDNext) - 1, int64(sel.HighestModSeq)}
		if !force && cond && f.HighestModSeq > 0 && f.UIDValidity == st.uv && f.HighestModSeq == st.modseq {
			continue
		}
		var ids []string
		if sel.NumMessages > 0 {
			var all imap.UIDSet
			all.AddRange(1, 0)
			msgs, err := c.Fetch(all, &imap.FetchOptions{UID: true, Envelope: true}).Collect()
			if err != nil {
				return fmt.Errorf("labels %q: %w", f.Path, err)
			}
			for _, m := range msgs {
				if m.Envelope != nil && m.Envelope.MessageID != "" {
					ids = append(ids, m.Envelope.MessageID)
				}
			}
		}
		fetched[f.ID] = ids
		pending[f.ID] = st
	}
	if len(fetched) == 0 {
		return nil
	}

	refs, err := w.m.st.MessageRefs(ctx, w.acct.ID)
	if err != nil {
		return err
	}
	current, err := w.m.st.Memberships(ctx, w.acct.ID)
	if err != nil {
		return err
	}
	changes, unmapped := applyLabels(refs, current, fetched)
	for seq, fs := range changes {
		if err := w.m.st.SetMessageFolders(ctx, seq, fs); err != nil {
			return err
		}
	}
	if len(changes) > 0 {
		w.emit(EventFlagsUpdated, "", "labels")
	}
	if unmapped > 0 {
		w.log.Debug("label messages not synced yet", "count", unmapped)
		return nil
	}
	for id, st := range pending {
		if err := w.m.st.UpdateFolderSyncState(ctx, id, st.uv, st.uidNext, st.modseq); err != nil {
			return err
		}
	}
	return nil
}
