package mail

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"

	"mailand/backend/internal/store"
)

const (
	fetchBatch = 100 // messages per UID FETCH
	flagChunk  = 500 // UIDs per flag-refresh FETCH when CONDSTORE is unavailable
)

type passKind int

const (
	passFull  passKind = iota // LIST + every source folder + Gmail labels
	passInbox                 // IDLE wake: INBOX (Gmail: All Mail + label refresh)
)

// pass runs one sync pass. Per-folder IMAP errors (NO/BAD) are logged and
// skipped; connection-level errors abort the pass so the session reconnects.
//
// At most Manager.passBudget new messages are downloaded (newest first); if
// more remain, w.backlog is set and the pass ends with SyncFinished "backlog".
func (w *worker) pass(ctx context.Context, c *conn, kind passKind) error {
	w.backlog = false
	start := time.Now()
	fetched := 0
	w.emit(EventSyncStarted, "", "")
	defer func() {
		detail := ""
		if w.backlog {
			detail = "backlog"
		}
		w.emit(EventSyncFinished, "", detail)
		w.log.Info("sync pass done", "full", kind == passFull, "fetched", fetched,
			"backlog", w.backlog, "took", time.Since(start).Round(time.Millisecond))
	}()
	var budget *int
	if w.m.passBudget > 0 {
		b := w.m.passBudget
		budget = &b
	}

	var folders []store.Folder
	var err error
	if kind == passFull {
		folders, err = w.syncFolders(ctx, c)
	} else {
		folders, err = w.m.st.ListFolders(ctx, w.acct.ID)
	}
	if err != nil {
		return err
	}
	gmail := c.gmail(w.acct.Provider)
	if gmail && !slices.ContainsFunc(folders, func(f store.Folder) bool { return f.SpecialUse == `\All` }) {
		return errors.New(`gmail: no \All mailbox visible over IMAP (enable "Show in IMAP" for All Mail in Gmail settings)`)
	}

	stopRefresher := func() {}
	if gmail {
		stop := w.startLabelRefresher(ctx, folders)
		defer stop()
		// stopped before the final refresh so the main connection is free again
		stopRefresher = stop
	}
	for _, f := range folders {
		if !f.IsSyncSource || (kind == passInbox && !inboxPassWants(f, gmail)) {
			continue
		}
		n, more, err := w.syncSourceN(ctx, c, f, budget)
		fetched += n
		w.backlog = w.backlog || more
		if err != nil {
			if ctx.Err() != nil || isConnErr(err) {
				return err
			}
			w.log.Warn("folder sync failed", "folder", f.Path, "err", err)
		}
	}
	stopRefresher()
	if gmail {
		if err := w.syncLabels(ctx, c, folders, fetched > 0); err != nil {
			if ctx.Err() != nil || isConnErr(err) {
				return err
			}
			w.log.Warn("label sync failed", "err", err)
		}
	}
	return ctx.Err()
}

func inboxPassWants(f store.Folder, gmail bool) bool {
	if gmail {
		return f.SpecialUse == `\All`
	}
	return f.SpecialUse == `\Inbox`
}

// isConnErr reports whether err is not a plain server NO/BAD answer.
func isConnErr(err error) bool {
	var ie *imap.Error
	return !errors.As(err, &ie)
}

func (c *conn) selectRO(path string) (*imap.SelectData, error) {
	return c.Select(path, &imap.SelectOptions{ReadOnly: true, CondStore: c.caps.Has(imap.CapCondStore)}).Wait()
}

// syncSource brings one source folder up to date and returns how many new
// messages were stored.
//
// Progress scheme: last_synced_uid = L means "every server UID <= L is stored".
// New mail (UID > L) is fetched newest first, so L cannot advance while the
// batches run. Instead each pass diffs the server's UID list against the UIDs
// already in the store, so after a crash only the missing ones are fetched
// again; L jumps to the highest server UID only after the whole range is done.
func (w *worker) syncSource(ctx context.Context, c *conn, f store.Folder) (int, error) {
	n, _, err := w.syncSourceN(ctx, c, f, nil)
	return n, err
}

// syncSourceN is syncSource with a download budget shared across the pass
// (nil: unlimited). It downloads at most *budget new messages, newest first,
// and reports more=true if some were left; last_synced_uid then stays put so
// the next pass picks up the rest.
func (w *worker) syncSourceN(ctx context.Context, c *conn, f store.Folder, budget *int) (int, bool, error) {
	sel, err := c.selectRO(f.Path)
	if err != nil {
		return 0, false, fmt.Errorf("select %q: %w", f.Path, err)
	}
	if f.UIDValidity != 0 && f.UIDValidity != int64(sel.UIDValidity) {
		w.log.Info("UIDVALIDITY changed, resyncing folder", "folder", f.Path)
		if err := w.m.st.ClearFolderMessages(ctx, f.ID); err != nil {
			return 0, false, err
		}
		w.removeFolderBlobs(f.Path)
		f.LastSyncedUID, f.HighestModSeq = 0, 0
		w.emit(EventFlagsUpdated, f.ID, "expunged")
	}
	uv := sel.UIDValidity

	res, err := c.UIDSearch(&imap.SearchCriteria{}, nil).Wait()
	if err != nil {
		return 0, false, fmt.Errorf("search %q: %w", f.Path, err)
	}
	server := make(map[uint32]struct{}, len(res.AllUIDs()))
	var serverUIDs []uint32
	for _, u := range res.AllUIDs() {
		server[uint32(u)] = struct{}{}
		serverUIDs = append(serverUIDs, uint32(u))
	}

	stored, err := w.m.st.FlagStates(ctx, f.ID)
	if err != nil {
		return 0, false, err
	}

	// Expunges.
	var gone []uint32
	for u := range stored {
		if _, ok := server[u]; !ok {
			gone = append(gone, u)
		}
	}
	if len(gone) > 0 {
		if err := w.m.st.DeleteByUIDs(ctx, f.ID, gone); err != nil {
			return 0, false, err
		}
		for _, u := range gone {
			delete(stored, u)
			_ = os.Remove(w.blobPath(f.Path, uv, u))
		}
		w.emit(EventFlagsUpdated, f.ID, "expunged")
	}

	// Flag changes on mail we already have.
	if len(stored) > 0 {
		if err := w.refreshFlags(ctx, c, f, sel, stored); err != nil {
			return 0, false, err
		}
	}

	// New mail, newest first.
	var todo []uint32
	for _, u := range serverUIDs {
		if _, have := stored[u]; !have && int64(u) > f.LastSyncedUID {
			todo = append(todo, u)
		}
	}
	slices.Sort(todo)
	slices.Reverse(todo)
	remaining := len(todo)
	more := false
	if budget != nil {
		if len(todo) > *budget {
			todo, more = todo[:max(*budget, 0)], true
		}
		*budget -= len(todo)
	}
	if remaining > fetchBatch {
		w.emit(EventSyncProgress, f.ID, strconv.Itoa(remaining))
	}
	total := 0
	for i := 0; i < len(todo); i += fetchBatch {
		batch := todo[i:min(i+fetchBatch, len(todo))]
		n, err := w.fetchBatch(ctx, c, f, uv, batch)
		total += n
		if n > 0 {
			w.emit(EventNewMessages, f.ID, strconv.Itoa(n))
		}
		if err != nil {
			return total, false, fmt.Errorf("fetch %q: %w", f.Path, err)
		}
		remaining -= len(batch)
		if more || remaining > 0 {
			w.emit(EventSyncProgress, f.ID, strconv.Itoa(remaining))
		}
		if w.m.batchHook != nil {
			w.m.batchHook(ctx)
		}
	}

	last := f.LastSyncedUID
	if len(serverUIDs) > 0 && !more { // with a backlog, older UIDs are still missing
		last = max(last, int64(slices.Max(serverUIDs)))
	}
	err = w.m.st.UpdateFolderSyncState(ctx, f.ID, int64(uv), last, int64(sel.HighestModSeq))
	return total, more, err
}

// refreshFlags applies server-side flag changes to stored messages: with
// CONDSTORE only those changed since the stored HIGHESTMODSEQ, otherwise all
// known UIDs in chunks.
func (w *worker) refreshFlags(ctx context.Context, c *conn, f store.Folder, sel *imap.SelectData, stored map[uint32]store.FlagState) error {
	changed := 0
	apply := func(cmd *imapclient.FetchCommand) error {
		defer cmd.Close()
		for {
			md := cmd.Next()
			if md == nil {
				break
			}
			buf, err := md.Collect()
			if err != nil {
				return err
			}
			cur, ok := stored[uint32(buf.UID)]
			if !ok {
				continue
			}
			rd, fl, ans := flagsOf(buf.Flags)
			if cur.IsRead == rd && cur.IsFlagged == fl && cur.IsAnswered == ans {
				continue
			}
			if err := w.m.st.UpdateFlags(ctx, f.ID, uint32(buf.UID), rd, fl, ans); err != nil {
				return err
			}
			changed++
		}
		return cmd.Close()
	}

	opts := &imap.FetchOptions{UID: true, Flags: true}
	if c.caps.Has(imap.CapCondStore) && f.HighestModSeq > 0 {
		var all imap.UIDSet
		all.AddRange(1, 0)
		opts.ChangedSince = uint64(f.HighestModSeq)
		if err := apply(c.Fetch(all, opts)); err != nil {
			return fmt.Errorf("flags %q: %w", f.Path, err)
		}
	} else {
		uids := make([]uint32, 0, len(stored))
		for u := range stored {
			uids = append(uids, u)
		}
		slices.Sort(uids)
		for i := 0; i < len(uids); i += flagChunk {
			var set imap.UIDSet
			for _, u := range uids[i:min(i+flagChunk, len(uids))] {
				set.AddNum(imap.UID(u))
			}
			if err := apply(c.Fetch(set, opts)); err != nil {
				return fmt.Errorf("flags %q: %w", f.Path, err)
			}
		}
	}
	if changed > 0 {
		w.emit(EventFlagsUpdated, f.ID, strconv.Itoa(changed))
	}
	return nil
}

func flagsOf(flags []imap.Flag) (read, flagged, answered bool) {
	for _, fl := range flags {
		switch strings.ToLower(string(fl)) {
		case `\seen`:
			read = true
		case `\flagged`:
			flagged = true
		case `\answered`:
			answered = true
		}
	}
	return
}

// fetchBatch downloads the given UIDs (already ordered newest first) and
// stores each message. Bodies are streamed: one message is in memory at a time.
func (w *worker) fetchBatch(ctx context.Context, c *conn, f store.Folder, uv uint32, uids []uint32) (int, error) {
	var set imap.UIDSet
	for _, u := range uids {
		set.AddNum(imap.UID(u))
	}
	cmd := c.Fetch(set, &imap.FetchOptions{
		UID: true, Flags: true, InternalDate: true, RFC822Size: true,
		BodySection: []*imap.FetchItemBodySection{{Peek: true}}, // PEEK: never sets \Seen
	})
	n := 0
	for {
		md := cmd.Next()
		if md == nil {
			break
		}
		if err := w.storeMessage(ctx, f, uv, md); err != nil {
			_ = cmd.Close()
			return n, err
		}
		n++
	}
	return n, cmd.Close()
}

func (w *worker) storeMessage(ctx context.Context, f store.Folder, uv uint32, md *imapclient.FetchMessageData) error {
	var (
		uid      imap.UID
		flags    []imap.Flag
		internal time.Time
		parsed   *Parsed
		tmp      string
	)
	defer func() {
		if tmp != "" {
			_ = os.Remove(tmp)
		}
	}()
	for {
		switch it := md.Next().(type) {
		case nil:
			goto done
		case imapclient.FetchItemDataUID:
			uid = it.UID
		case imapclient.FetchItemDataFlags:
			flags = it.Flags
		case imapclient.FetchItemDataInternalDate:
			internal = it.Time
		case imapclient.FetchItemDataBodySection:
			if it.Literal == nil {
				continue
			}
			var err error
			if parsed, tmp, err = w.spool(it.Literal); err != nil {
				return err
			}
		}
	}
done:
	if uid == 0 || tmp == "" {
		return errors.New("incomplete FETCH response")
	}
	blob := w.blobPath(f.Path, uv, uint32(uid))
	if err := os.Rename(tmp, blob); err != nil {
		return err
	}
	tmp = ""

	rd, fl, ans := flagsOf(flags)
	ts := internal
	if parsed.HasDate {
		ts = parsed.Date
	}
	if ts.IsZero() {
		ts = time.Now()
	}
	m := store.Message{
		MessageHeader: store.MessageHeader{
			AccountID: w.acct.ID, FolderID: f.ID, UID: uint32(uid),
			Subject: parsed.Subject, FromName: parsed.FromName, FromAddress: parsed.FromAddress,
			TimestampUnix: ts.Unix(), IsRead: rd, IsFlagged: fl, IsAnswered: ans,
			HasAttachments: parsed.HasAttachments, Snippet: parsed.Snippet,
		},
		MessageIDHeader: parsed.MessageID,
		ToJSON:          addrJSON(parsed.To), CcJSON: addrJSON(parsed.Cc),
		TextBody: parsed.TextBody, HTMLBody: parsed.HTMLBody, RawBlobPath: blob,
	}
	_, err := w.m.st.UpsertMessage(ctx, m, nil)
	if err == nil {
		w.stored.Add(1)
	}
	return err
}

// spool copies a message literal to a private temp file while parsing it.
func (w *worker) spool(r io.Reader) (*Parsed, string, error) {
	dir := w.blobDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, "", err
	}
	tf, err := os.CreateTemp(dir, ".incoming-*") // created 0600
	if err != nil {
		return nil, "", err
	}
	name := tf.Name()
	tee := io.TeeReader(r, tf)
	p, perr := Parse(tee)
	_, cerr := io.Copy(io.Discard, tee) // Parse may stop early; the blob must be complete
	if err := errors.Join(cerr, tf.Close()); err != nil {
		_ = os.Remove(name)
		return nil, "", err
	}
	if perr != nil {
		w.log.Warn("message parse problem, storing what was extracted", "err", perr)
	}
	return p, name, nil
}

func addrJSON(as []Address) string {
	if len(as) == 0 {
		return "[]"
	}
	b, err := json.Marshal(as)
	if err != nil {
		return "[]"
	}
	return string(b)
}
