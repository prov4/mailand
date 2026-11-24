package mail

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"golang.org/x/oauth2"

	"mailand/backend/internal/auth"
	"mailand/backend/internal/config"
)

var bg = context.Background()

func seedInbox(e *env) {
	e.appendMsg("INBOX", rawMsg("First", "m1@x", "hello one"), imap.FlagSeen)
	e.appendMsg("INBOX", rawMsg("Second", "m2@x", "hello two"), imap.FlagFlagged)
	html := "From: a@example.com\r\nSubject: HTML only\r\nMessage-ID: <m3@x>\r\nContent-Type: text/html\r\n\r\n<p>Hi <b>there</b></p>\r\n"
	e.appendMsg("INBOX", html)
}

func TestInitialSync(t *testing.T) {
	e := newEnv(t)
	seedInbox(e)
	e.start(nil)
	e.waitCount(inboxID, 3)
	e.settle()

	hs := e.msgs(inboxID)
	h1, _ := byUID(hs, 1)
	h2, _ := byUID(hs, 2)
	h3, _ := byUID(hs, 3)
	if h1.Subject != "First" || !h1.IsRead || h1.IsFlagged {
		t.Errorf("msg1: %+v", h1)
	}
	if !h2.IsFlagged || h2.IsRead {
		t.Errorf("msg2: %+v", h2)
	}
	if h1.TimestampUnix != 1136214245 {
		t.Errorf("date not from header: %d", h1.TimestampUnix)
	}
	full, err := e.st.GetMessage(bg, h3.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(full.TextBody, "there") || full.HTMLBody == "" {
		t.Errorf("html-only body: text=%q html=%q", full.TextBody, full.HTMLBody)
	}
	full1, _ := e.st.GetMessage(bg, h1.ID)
	if full1.FromAddress != "alice@example.com" || !strings.Contains(full1.ToJSON, "bob@example.com") || full1.MessageIDHeader != "m1@x" {
		t.Errorf("headers: %+v", full1)
	}

	fi, err := os.Stat(full1.RawBlobPath)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("blob perm %v", fi.Mode().Perm())
	}
	di, _ := os.Stat(filepath.Dir(full1.RawBlobPath))
	if di.Mode().Perm() != 0o700 {
		t.Errorf("blob dir perm %v", di.Mode().Perm())
	}
	raw, _ := os.ReadFile(full1.RawBlobPath)
	if string(raw) != rawMsg("First", "m1@x", "hello one") {
		t.Errorf("blob content mismatch: %q", raw)
	}
	if left, _ := filepath.Glob(filepath.Join(e.blobDir, "acc", ".incoming-*")); len(left) != 0 {
		t.Errorf("temp files left: %v", left)
	}

	f, _ := e.st.GetFolder(bg, inboxID)
	if f.UIDValidity == 0 || f.LastSyncedUID != 3 || f.UnreadCount != 2 || !f.IsSyncSource || f.SpecialUse != `\Inbox` {
		t.Errorf("folder state: %+v", f)
	}
	// \Seen must not have been set by the fetch.
	if fl := e.serverFlags("INBOX", 3); len(fl) != 0 {
		t.Errorf("server flags of untouched message: %v", fl)
	}
	if got := e.eventsOf(EventNewMessages); len(got) == 0 {
		t.Error("no EventNewMessages")
	}
	if acc, err := e.st.GetAccount(bg, "acc"); err != nil || acc.Email != testUser {
		t.Errorf("account not upserted: %v %+v", err, acc)
	}
}

func TestIncrementalSyncOnlyNewMail(t *testing.T) {
	e := newEnv(t)
	seedInbox(e)
	e.start(nil)
	e.waitCount(inboxID, 3)
	e.settle()
	before := len(e.eventsOf(EventNewMessages))
	h1, _ := byUID(e.msgs(inboxID), 1)
	m1, _ := e.st.GetMessage(bg, h1.ID)
	st1, _ := os.Stat(m1.RawBlobPath)

	e.appendMsg("INBOX", rawMsg("Fourth", "m4@x", "four"))
	e.sync()
	e.waitCount(inboxID, 4)
	e.settle()

	evs := e.eventsOf(EventNewMessages)
	if len(evs) != before+1 || evs[len(evs)-1].Detail != "1" || evs[len(evs)-1].FolderID != inboxID {
		t.Errorf("new-message events: %+v", evs[before:])
	}
	st2, _ := os.Stat(m1.RawBlobPath)
	if !st1.ModTime().Equal(st2.ModTime()) {
		t.Error("existing blob was rewritten")
	}
	f, _ := e.st.GetFolder(bg, inboxID)
	if f.LastSyncedUID != 4 {
		t.Errorf("last_synced_uid %d", f.LastSyncedUID)
	}
}

func TestResumeAfterPartialSync(t *testing.T) {
	// A crash mid-sync leaves last_synced_uid behind with some newer UIDs stored.
	// The next pass must fetch only the missing ones.
	e := newEnv(t)
	seedInbox(e)
	e.start(nil)
	e.waitCount(inboxID, 3)
	e.settle()
	h2, _ := byUID(e.msgs(inboxID), 2)
	if err := e.st.DeleteByUIDs(bg, inboxID, []uint32{2}); err != nil {
		t.Fatal(err)
	}
	_ = h2
	f, _ := e.st.GetFolder(bg, inboxID)
	e.st.UpdateFolderSyncState(bg, inboxID, f.UIDValidity, 0, 0) // as if never advanced
	n := len(e.eventsOf(EventNewMessages))
	e.settle()
	e.waitCount(inboxID, 3)
	evs := e.eventsOf(EventNewMessages)
	if len(evs) != n+1 || evs[n].Detail != "1" {
		t.Errorf("expected one refetch of one message, got %+v", evs[n:])
	}
}

func TestServerFlagChange(t *testing.T) {
	e := newEnv(t)
	seedInbox(e)
	e.start(nil)
	e.waitCount(inboxID, 3)
	e.settle()

	c := e.client()
	c.Select("INBOX", nil).Wait()
	c.Store(imap.UIDSetNum(3), &imap.StoreFlags{Op: imap.StoreFlagsAdd, Silent: true, Flags: []imap.Flag{imap.FlagSeen, imap.FlagFlagged}}, nil).Close()
	c.Store(imap.UIDSetNum(1), &imap.StoreFlags{Op: imap.StoreFlagsDel, Silent: true, Flags: []imap.Flag{imap.FlagSeen}}, nil).Close()
	e.sync()
	waitFor(t, "flags", func() bool {
		h3, _ := byUID(e.msgs(inboxID), 3)
		h1, _ := byUID(e.msgs(inboxID), 1)
		return h3.IsRead && h3.IsFlagged && !h1.IsRead
	})
	if len(e.eventsOf(EventFlagsUpdated)) == 0 {
		t.Error("no EventFlagsUpdated")
	}
	f, _ := e.st.GetFolder(bg, inboxID)
	if f.UnreadCount != 2 { // msg1 and msg2 unread, msg3 read
		t.Errorf("unread %d", f.UnreadCount)
	}
}

func TestServerExpunge(t *testing.T) {
	e := newEnv(t)
	seedInbox(e)
	e.start(nil)
	e.waitCount(inboxID, 3)
	e.settle()
	h2, _ := byUID(e.msgs(inboxID), 2)
	m2, _ := e.st.GetMessage(bg, h2.ID)

	c := e.client()
	c.Select("INBOX", nil).Wait()
	c.Store(imap.UIDSetNum(2), &imap.StoreFlags{Op: imap.StoreFlagsAdd, Silent: true, Flags: []imap.Flag{imap.FlagDeleted}}, nil).Close()
	if err := c.Expunge().Close(); err != nil {
		t.Fatal(err)
	}
	e.sync()
	e.waitCount(inboxID, 2)
	if _, ok := byUID(e.msgs(inboxID), 2); ok {
		t.Error("uid 2 still present")
	}
	if _, err := os.Stat(m2.RawBlobPath); !os.IsNotExist(err) {
		t.Errorf("blob of expunged message not removed: %v", err)
	}
}

func TestUIDValidityChange(t *testing.T) {
	e := newEnv(t)
	seedInbox(e)
	e.start(nil)
	e.waitCount(inboxID, 3)
	e.settle()
	f0, _ := e.st.GetFolder(bg, inboxID)
	h1, _ := byUID(e.msgs(inboxID), 1)
	m1, _ := e.st.GetMessage(bg, h1.ID)

	if err := e.user.Delete("INBOX"); err != nil {
		t.Fatal(err)
	}
	if err := e.user.Create("INBOX", nil); err != nil {
		t.Fatal(err)
	}
	e.appendMsg("INBOX", rawMsg("Brand new", "n1@x", "fresh"))
	e.sync()
	waitFor(t, "resync", func() bool {
		hs := e.msgs(inboxID)
		return len(hs) == 1 && hs[0].Subject == "Brand new"
	})
	f1, _ := e.st.GetFolder(bg, inboxID)
	if f1.UIDValidity == f0.UIDValidity || f1.LastSyncedUID != 1 {
		t.Errorf("state after reset: before %+v after %+v", f0, f1)
	}
	if _, err := os.Stat(m1.RawBlobPath); !os.IsNotExist(err) {
		t.Errorf("old blob not removed: %v", err)
	}
}

func TestSetFlagsPushesToServerAndStore(t *testing.T) {
	e := newEnv(t)
	seedInbox(e)
	e.start(nil)
	e.waitCount(inboxID, 3)
	e.settle()
	h3, _ := byUID(e.msgs(inboxID), 3)
	h1, _ := byUID(e.msgs(inboxID), 1)
	n := len(e.eventsOf(EventFlagsUpdated))

	yes, no := true, false
	if err := e.m.SetFlags(bg, []string{h3.ID}, &yes, &yes); err != nil {
		t.Fatal(err)
	}
	if err := e.m.SetFlags(bg, []string{h1.ID}, &no, nil); err != nil {
		t.Fatal(err)
	}
	has := func(fl []imap.Flag, f imap.Flag) bool {
		for _, x := range fl {
			if x == f {
				return true
			}
		}
		return false
	}
	if fl := e.serverFlags("INBOX", 3); !has(fl, imap.FlagSeen) || !has(fl, imap.FlagFlagged) {
		t.Errorf("server flags uid3: %v", fl)
	}
	if fl := e.serverFlags("INBOX", 1); has(fl, imap.FlagSeen) {
		t.Errorf("server flags uid1: %v", fl)
	}
	g3, _ := e.st.GetMessage(bg, h3.ID)
	g1, _ := e.st.GetMessage(bg, h1.ID)
	if !g3.IsRead || !g3.IsFlagged || g1.IsRead {
		t.Errorf("store flags: %+v %+v", g3.MessageHeader, g1.MessageHeader)
	}
	if len(e.eventsOf(EventFlagsUpdated)) < n+2 {
		t.Error("EventFlagsUpdated missing")
	}
	// Unknown ids are skipped; both-nil is a no-op.
	if err := e.m.SetFlags(bg, []string{"nope"}, &yes, nil); err != nil {
		t.Error(err)
	}
	if err := e.m.SetFlags(bg, []string{h1.ID}, nil, nil); err != nil {
		t.Error(err)
	}
}

func TestFolderDeletedOnServer(t *testing.T) {
	e := newEnv(t)
	if err := e.user.Create("Archive/2024", nil); err != nil {
		t.Fatal(err)
	}
	e.appendMsg("Archive/2024", rawMsg("Old", "o1@x", "old"))
	e.start(nil)
	e.waitCount("acc:Archive/2024", 1)
	fs, _ := e.st.ListFolders(bg, "acc")
	if len(fs) != 2 {
		t.Fatalf("folders: %+v", fs)
	}
	for _, f := range fs {
		if f.Path == "Archive/2024" && (f.Name != "2024" || f.Kind != "folder" || !f.IsSyncSource) {
			t.Errorf("folder: %+v", f)
		}
	}
	h := e.msgs("acc:Archive/2024")[0]
	m, _ := e.st.GetMessage(bg, h.ID)

	if err := e.user.Delete("Archive/2024"); err != nil {
		t.Fatal(err)
	}
	e.sync()
	waitFor(t, "folder removal", func() bool {
		fs, _ := e.st.ListFolders(bg, "acc")
		return len(fs) == 1
	})
	if _, err := e.st.GetMessage(bg, h.ID); err == nil {
		t.Error("message survived folder deletion")
	}
	if _, err := os.Stat(m.RawBlobPath); !os.IsNotExist(err) {
		t.Errorf("blob survived: %v", err)
	}
}

func TestAuthFailureEmitsError(t *testing.T) {
	e := newEnv(t)
	e.tweak = func(m *Manager) { m.password = func(string) (string, error) { return "wrong", nil } }
	e.start(nil)
	waitFor(t, "EventError", func() bool { return len(e.eventsOf(EventError)) > 0 })
	ev := e.eventsOf(EventError)[0]
	if ev.AccountID != "acc" || ev.Detail == "reauth_required" || ev.Detail == "" || strings.Contains(ev.Detail, "wrong") {
		t.Errorf("event: %+v", ev)
	}
	if len(e.eventsOf(EventSyncStarted)) != 0 {
		t.Error("sync ran without auth")
	}
}

func TestReauthRequiredStopsUntilTrigger(t *testing.T) {
	e := newEnv(t)
	e.cfg.Accounts[0].Auth = config.AuthOAuth2Google
	var mu sync.Mutex
	calls := 0
	e.tweak = func(m *Manager) {
		m.tokenSource = func(context.Context, *config.Account) (oauth2.TokenSource, error) {
			mu.Lock()
			defer mu.Unlock()
			calls++
			return nil, errors.Join(errors.New("no token"), auth.ErrReauthRequired)
		}
	}
	e.start(nil)
	count := func() int { mu.Lock(); defer mu.Unlock(); return calls }
	waitFor(t, "reauth event", func() bool { return len(e.eventsOf(EventError)) == 1 })
	if d := e.eventsOf(EventError)[0].Detail; d != "reauth_required" {
		t.Fatalf("detail %q", d)
	}
	time.Sleep(1500 * time.Millisecond) // longer than the first backoff step
	if count() != 1 {
		t.Fatalf("worker retried while parked: %d calls", count())
	}
	if err := e.m.TriggerSync("acc"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "second attempt", func() bool { return count() == 2 })
	waitFor(t, "second reauth event", func() bool { return len(e.eventsOf(EventError)) == 2 })
}

func TestTriggerSyncUnknownAccount(t *testing.T) {
	e := newEnv(t)
	e.start(nil)
	if err := e.m.TriggerSync("nope"); err == nil {
		t.Error("expected error")
	}
}

func TestStopIsCleanAndLeakFree(t *testing.T) {
	e := newEnv(t)
	seedInbox(e)
	runtime.GC()
	time.Sleep(100 * time.Millisecond)
	base := runtime.NumGoroutine()

	e.start(nil)
	e.waitCount(inboxID, 3)
	e.settle()
	done := make(chan struct{})
	go func() { e.m.Stop(); e.m.Stop(); close(done) }() // idempotent
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop hung")
	}
	if err := e.m.TriggerSync("acc"); err != nil {
		t.Log("TriggerSync after Stop:", err) // allowed either way
	}
	e.srv.Close()
	waitFor(t, "goroutines to exit", func() bool {
		return runtime.NumGoroutine() <= base
	})
}

func TestStopWhileConnecting(t *testing.T) {
	e := newEnv(t)
	e.cfg.Accounts[0].IMAPPort = 1 // refused: worker sits in backoff
	e.start(nil)
	waitFor(t, "error", func() bool { return len(e.eventsOf(EventError)) > 0 })
	done := make(chan struct{})
	go func() { e.m.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop hung during backoff")
	}
}

func TestIdleWakesSync(t *testing.T) {
	e := newEnv(t)
	seedInbox(e)
	e.start(nil)
	e.waitCount(inboxID, 3)
	e.settle()
	time.Sleep(300 * time.Millisecond) // let the IDLE connection establish
	e.appendMsg("INBOX", rawMsg("Pushed", "p1@x", "via idle"))
	// No TriggerSync and a 1h poll interval: only IDLE can deliver this.
	e.waitCount(inboxID, 4)
}

func TestSlowNotifyDoesNotBlockSync(t *testing.T) {
	e := newEnv(t)
	seedInbox(e)
	release := make(chan struct{})
	var once sync.Once
	e.start(func(Event) { once.Do(func() { <-release }) })
	t.Cleanup(func() { close(release) }) // runs before Stop (LIFO)
	e.waitCount(inboxID, 3)
}
