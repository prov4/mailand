package mail

import (
	"context"
	"fmt"
	"reflect"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"

	"mailand/backend/internal/config"
	"mailand/backend/internal/store"
)

func TestLabelCacheUpdate(t *testing.T) {
	var lc labelCache
	if got := lc.update(7, []imap.UID{1, 2, 3}); !reflect.DeepEqual(got, []imap.UID{3, 2, 1}) {
		t.Fatalf("first: %v", got)
	}
	for u, id := range map[imap.UID]string{1: "a", 2: "b", 3: ""} {
		lc.ids[u] = id
	}
	// Cached UIDs (even without a Message-ID) are never fetched again.
	if got := lc.update(7, []imap.UID{1, 2, 3}); len(got) != 0 {
		t.Fatalf("second: %v", got)
	}
	// New UID fetched, removed UID dropped.
	if got := lc.update(7, []imap.UID{2, 3, 4}); !reflect.DeepEqual(got, []imap.UID{4}) {
		t.Fatalf("third: %v", got)
	}
	if _, ok := lc.ids[1]; ok {
		t.Error("removed UID kept")
	}
	lc.ids[4] = "d"
	if got := lc.messageIDs(); len(got) != 2 {
		t.Errorf("ids %v", got)
	}
	// UIDVALIDITY change resets everything.
	lc.synced = true
	if got := lc.update(8, []imap.UID{2, 3, 4}); !reflect.DeepEqual(got, []imap.UID{4, 3, 2}) || lc.synced || len(lc.ids) != 0 {
		t.Fatalf("reset: %v %v", got, lc.ids)
	}
}

func TestParseMessageID(t *testing.T) {
	if got := parseMessageID([]byte("Message-ID: <a@x>\r\n\r\n")); got != "<a@x>" {
		t.Error(got)
	}
	if got := parseMessageID([]byte("Message-ID:\r\n <folded@x>\r\n\r\n")); got != "<folded@x>" {
		t.Error(got)
	}
	if got := parseMessageID(nil); got != "" {
		t.Error(got)
	}
}

// gmailWorker builds a worker in Gmail mode with the given folders in the
// store (the memory server has no SPECIAL-USE / X-GM extensions).
func gmailWorker(t *testing.T, e *env, labels ...string) (*worker, []store.Folder) {
	t.Helper()
	e.cfg.Accounts[0].Provider = config.ProviderGmail
	e.m = NewManager(e.cfg, e.st, e.blobDir, nil, nil)
	e.m.plain = true
	e.m.password = func(string) (string, error) { return testPass, nil }
	a := &e.cfg.Accounts[0]
	if err := e.st.UpsertAccount(bg, store.Account{ID: a.ID, Name: "n", Email: a.Email, IMAPHost: a.IMAPHost, IMAPPort: a.IMAPPort, SMTPHost: "s", SMTPPort: 1}); err != nil {
		t.Fatal(err)
	}
	up := func(path, kind, sp string, src bool) store.Folder {
		f, err := e.st.UpsertFolder(bg, store.Folder{ID: folderID("acc", path), AccountID: "acc", Name: path, Path: path, Kind: kind, SpecialUse: sp, IsSyncSource: src})
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	folders := []store.Folder{up("All", "folder", `\All`, true)}
	for _, l := range labels {
		folders = append(folders, up(l, "gmail_label", "", false))
	}
	return newWorker(e.m, a), folders
}

// Label refreshes fetch only UIDs they have not seen, and notice removals.
func TestSyncLabelsIncremental(t *testing.T) {
	e := newEnv(t)
	e.user.Create("All", nil)
	w, folders := gmailWorker(t, e, "INBOX")
	for i := 1; i <= 3; i++ {
		raw := rawMsg("s", fmt.Sprintf("%d@x", i), "b")
		e.appendMsg("All", raw)
		e.appendMsg("INBOX", raw)
	}
	c, err := w.connect(bg, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.close()
	if _, err := w.syncSource(bg, c, folders[0]); err != nil {
		t.Fatal(err)
	}
	if err := w.syncLabels(bg, c, folders, true); err != nil {
		t.Fatal(err)
	}
	lc := w.labels[inboxID]
	if lc == nil || len(lc.ids) != 3 || !lc.synced {
		t.Fatalf("cache after first refresh: %+v", lc)
	}
	// Poison a cached entry: a refetch would overwrite it.
	lc.ids[1] = "<poison@x>"
	e.appendMsg("INBOX", rawMsg("s", "4@x", "b"))
	if err := w.syncLabels(bg, c, folders, true); err != nil {
		t.Fatal(err)
	}
	if lc.ids[1] != "<poison@x>" || lc.ids[4] != "<4@x>" {
		t.Fatalf("cache: %v", lc.ids)
	}
	// Expunge UID 2 on the server.
	c2 := e.client()
	c2.Select("INBOX", nil).Wait()
	c2.Store(imap.UIDSetNum(2), &imap.StoreFlags{Op: imap.StoreFlagsAdd, Silent: true, Flags: []imap.Flag{imap.FlagDeleted}}, nil).Close()
	c2.Expunge().Close()
	if err := w.syncLabels(bg, c, folders, true); err != nil {
		t.Fatal(err)
	}
	if _, ok := lc.ids[2]; ok || len(lc.ids) != 3 {
		t.Fatalf("cache after expunge: %v", lc.ids)
	}
}

// A long first sync: INBOX membership must appear while the All-Mail-like
// source is still being fetched, via a second connection that is closed after
// the pass.
func TestLabelRefreshDuringLongPass(t *testing.T) {
	e := newEnv(t)
	e.user.Create("All", nil)
	w, _ := gmailWorker(t, e, "INBOX")
	const n = 400 // 4 batches
	c0 := e.client()
	for i := 1; i <= n; i++ {
		raw := rawMsg(fmt.Sprintf("m%d", i), fmt.Sprintf("%d@x", i), "b")
		for _, box := range []string{"All"} {
			cmd := c0.Append(box, int64(len(raw)), nil)
			cmd.Write([]byte(raw))
			cmd.Close()
			if _, err := cmd.Wait(); err != nil {
				t.Fatal(err)
			}
		}
		if i > n-5 { // INBOX holds the newest five
			cmd := c0.Append("INBOX", int64(len(raw)), nil)
			cmd.Write([]byte(raw))
			cmd.Close()
			if _, err := cmd.Wait(); err != nil {
				t.Fatal(err)
			}
		}
	}

	w.m.labelInterval = 50 * time.Millisecond
	var (
		mu       sync.Mutex
		extra    []*conn
		batches  int
		inboxHit = make(chan struct{})
		hitOnce  sync.Once
	)
	w.m.onLabelConn = func(c *conn) { mu.Lock(); extra = append(extra, c); mu.Unlock() }
	passDone := make(chan struct{})
	w.m.batchHook = func(ctx context.Context) {
		mu.Lock()
		batches++
		mu.Unlock()
		// Slow fetch; after the first batch wait until INBOX has shown up.
		select {
		case <-time.After(300 * time.Millisecond):
		case <-ctx.Done():
		}
		if len(e.msgs(inboxID)) == 5 {
			hitOnce.Do(func() {
				select {
				case <-passDone:
					t.Error("INBOX membership appeared only after the pass finished")
				default:
				}
				close(inboxHit)
			})
		}
	}

	runtime.GC()
	base := runtime.NumGoroutine()
	c, err := w.connect(bg, nil)
	if err != nil {
		t.Fatal(err)
	}
	err = w.pass(bg, c, passInbox)
	close(passDone)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-inboxHit:
	default:
		t.Fatal("INBOX membership never appeared before the source pass completed")
	}
	if got := len(e.msgs(inboxID)); got != 5 {
		t.Errorf("INBOX has %d messages", got)
	}
	if got := len(e.msgs("acc:All")); got != n {
		t.Errorf("All has %d messages", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(extra) != 1 {
		t.Fatalf("extra connections: %d", len(extra))
	}
	select {
	case <-extra[0].Closed():
	case <-time.After(2 * time.Second):
		t.Error("extra connection not closed after the pass")
	}
	c.close()
	waitFor(t, "goroutines to exit", func() bool { return runtime.NumGoroutine() <= base })
}
