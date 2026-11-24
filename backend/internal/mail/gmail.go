package mail

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/textproto"
	"slices"
	"strings"
	"sync"
	"time"

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

// labelChunk is the number of UIDs per Message-ID header FETCH.
const labelChunk = 500

// defaultLabelInterval is how often label membership is refreshed while a long
// pass is still fetching mail.
const defaultLabelInterval = 60 * time.Second

// labelCache remembers, for one label folder, which Message-ID each UID has, so
// a refresh only fetches UIDs it has not seen. It is in-memory only; after a
// restart it is rebuilt by the next refresh.
type labelCache struct {
	uv  uint32
	ids map[imap.UID]string // "" = message has no Message-ID header

	// Last fully mapped state (every Message-ID matched a stored message).
	synced bool
	modseq uint64
}

// update reconciles the cache with the server's current UID list: it resets on
// a UIDVALIDITY change, drops UIDs that no longer exist and returns the UIDs
// that are not cached yet, newest first.
func (lc *labelCache) update(uv uint32, current []imap.UID) (missing []imap.UID) {
	if lc.ids == nil || lc.uv != uv {
		lc.uv, lc.ids = uv, map[imap.UID]string{}
		lc.synced = false
	}
	have := make(map[imap.UID]struct{}, len(current))
	for _, u := range current {
		have[u] = struct{}{}
		if _, ok := lc.ids[u]; !ok {
			missing = append(missing, u)
		}
	}
	for u := range lc.ids {
		if _, ok := have[u]; !ok {
			delete(lc.ids, u)
		}
	}
	slices.Sort(missing)
	slices.Reverse(missing)
	return missing
}

// messageIDs returns the non-empty Message-IDs currently cached.
func (lc *labelCache) messageIDs() []string {
	out := make([]string, 0, len(lc.ids))
	for _, id := range lc.ids {
		if id != "" {
			out = append(out, id)
		}
	}
	return out
}

// fetchMessageIDs reads only the Message-ID header of the given UIDs (BODY.PEEK,
// so \Seen is never touched). If the server refuses header-field sections it
// falls back to ENVELOPE.
func (c *conn) fetchMessageIDs(uids []imap.UID) (map[imap.UID]string, error) {
	var set imap.UIDSet
	for _, u := range uids {
		set.AddNum(u)
	}
	sect := &imap.FetchItemBodySection{
		Specifier: imap.PartSpecifierHeader, HeaderFields: []string{"Message-ID"}, Peek: true,
	}
	msgs, err := c.Fetch(set, &imap.FetchOptions{UID: true, BodySection: []*imap.FetchItemBodySection{sect}}).Collect()
	out := make(map[imap.UID]string, len(uids))
	if err == nil {
		for _, m := range msgs {
			out[m.UID] = parseMessageID(m.FindBodySection(sect))
		}
		return out, nil
	}
	var ie *imap.Error
	if !errors.As(err, &ie) {
		return nil, err
	}
	msgs, err = c.Fetch(set, &imap.FetchOptions{UID: true, Envelope: true}).Collect()
	if err != nil {
		return nil, err
	}
	for _, m := range msgs {
		if m.Envelope != nil {
			out[m.UID] = m.Envelope.MessageID
		}
	}
	return out, nil
}

func parseMessageID(hdr []byte) string {
	h, _ := textproto.NewReader(bufio.NewReader(bytes.NewReader(hdr))).ReadMIMEHeader()
	return strings.TrimSpace(h.Get("Message-Id"))
}

// syncLabels refreshes label membership using c (which must not be busy with
// another command). A label folder is skipped when its UIDVALIDITY and
// HIGHESTMODSEQ are unchanged (CONDSTORE), unless force is set (new mail was
// stored, whose membership was just reset to its source). Otherwise only UIDs
// not yet in the per-label cache are fetched (Message-ID header only).
// Folder state is only persisted when every label Message-ID was matched, so a
// label seen before its message was synced is retried next time. Runs are
// serialised per account.
func (w *worker) syncLabels(ctx context.Context, c *conn, folders []store.Folder, force bool) error {
	w.labelMu.Lock()
	defer w.labelMu.Unlock()
	if w.labels == nil {
		w.labels = map[string]*labelCache{}
	}
	type state struct {
		uv, uidNext, modseq int64
	}
	fetched := map[string][]string{}
	pending := map[string]state{}
	cond := c.caps.Has(imap.CapCondStore)

	for _, f := range folders {
		if f.Kind != "gmail_label" {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
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
		lc := w.labels[f.ID]
		if lc == nil {
			lc = &labelCache{}
			w.labels[f.ID] = lc
		}
		if !force && cond {
			if lc.synced && lc.uv == sel.UIDValidity && lc.modseq == sel.HighestModSeq && sel.HighestModSeq > 0 {
				continue
			}
			if !lc.synced && f.HighestModSeq > 0 && f.UIDValidity == st.uv && f.HighestModSeq == st.modseq {
				continue
			}
		}
		var current []imap.UID
		if sel.NumMessages > 0 {
			res, err := c.UIDSearch(&imap.SearchCriteria{}, nil).Wait()
			if err != nil {
				return fmt.Errorf("labels %q: %w", f.Path, err)
			}
			current = res.AllUIDs()
		}
		missing := lc.update(sel.UIDValidity, current)
		for i := 0; i < len(missing); i += labelChunk {
			if err := ctx.Err(); err != nil {
				return err
			}
			got, err := c.fetchMessageIDs(missing[i:min(i+labelChunk, len(missing))])
			if err != nil {
				return fmt.Errorf("labels %q: %w", f.Path, err)
			}
			for _, u := range missing[i:min(i+labelChunk, len(missing))] {
				lc.ids[u] = got[u] // absent from the answer (expunged meanwhile) = no ID
			}
		}
		fetched[f.ID] = lc.messageIDs()
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
		for id := range pending {
			w.labels[id].synced = false
		}
		return nil
	}
	for id, st := range pending {
		if err := w.m.st.UpdateFolderSyncState(ctx, id, st.uv, st.uidNext, st.modseq); err != nil {
			return err
		}
		lc := w.labels[id]
		lc.synced, lc.modseq = true, uint64(st.modseq)
	}
	return nil
}

// startLabelRefresher runs periodic label refreshes while a long pass is still
// fetching mail, on its own connection (the main one has a source folder
// selected and is mid-fetch). A tick refreshes only if messages were stored
// since the last successful refresh. Errors are logged and retried on the next
// tick. The returned stop function cancels, waits for the goroutine and thereby
// closes the extra connection; it is idempotent.
func (w *worker) startLabelRefresher(ctx context.Context, folders []store.Folder) (stop func()) {
	interval := w.m.labelInterval
	if interval <= 0 {
		return func() {}
	}
	rctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		var c *conn
		defer func() {
			if c != nil {
				c.close()
			}
		}()
		tick := time.NewTicker(interval)
		defer tick.Stop()
		last := w.stored.Load()
		for {
			select {
			case <-rctx.Done():
				return
			case <-tick.C:
			}
			cur := w.stored.Load()
			if cur == last {
				continue
			}
			if c == nil {
				var err error
				if c, err = w.connect(rctx, nil); err != nil {
					c = nil
					if rctx.Err() == nil {
						w.log.Warn("label refresh: connect failed", "err", err)
					}
					continue
				}
				if w.m.onLabelConn != nil {
					w.m.onLabelConn(c)
				}
			}
			if err := w.syncLabels(rctx, c, folders, true); err != nil {
				if rctx.Err() != nil {
					return
				}
				w.log.Warn("label refresh failed", "err", err)
				if isConnErr(err) {
					c.close()
					c = nil
				}
				continue
			}
			last = cur
		}
	}()
	var once sync.Once
	return func() { once.Do(func() { cancel(); <-done }) }
}
