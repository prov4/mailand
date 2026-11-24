package mail

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"

	"mailand/backend/internal/config"
	"mailand/backend/internal/store"
)

const (
	testUser = "u@example.com"
	testPass = "pw"
	inboxID  = "acc:INBOX"
)

type env struct {
	t       *testing.T
	user    *imapmemserver.User
	addr    string
	srv     *imapserver.Server
	st      *store.Store
	cfg     *config.Config
	blobDir string
	m       *Manager
	tweak   func(*Manager) // applied before Start

	mu     sync.Mutex
	events []Event
}

// newEnv starts an in-memory IMAP server with an empty INBOX and a store, but
// does not start the manager.
func newEnv(t *testing.T) *env {
	t.Helper()
	mem := imapmemserver.New()
	user := imapmemserver.NewUser(testUser, testPass)
	if err := user.Create("INBOX", nil); err != nil {
		t.Fatal(err)
	}
	mem.AddUser(user)
	srv := imapserver.New(&imapserver.Options{
		NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return mem.NewSession(), nil, nil
		},
		Caps:         imap.CapSet{imap.CapIMAP4rev1: {}, imap.CapUIDPlus: {}},
		InsecureAuth: true,
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })

	st, err := store.Open(filepath.Join(t.TempDir(), "db", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	e := &env{t: t, user: user, addr: ln.Addr().String(), srv: srv, st: st, blobDir: filepath.Join(t.TempDir(), "blobs")}
	host, port, _ := net.SplitHostPort(e.addr)
	var p int
	fmt.Sscan(port, &p)
	e.cfg = &config.Config{Accounts: []config.Account{{
		ID: "acc", Name: "Acc", Email: testUser, Provider: config.ProviderIMAP, Auth: config.AuthPassword,
		IMAPHost: host, IMAPPort: p, SMTPHost: host, SMTPPort: 1, PollInterval: time.Hour,
	}}}
	return e
}

// start builds and starts the manager (call before using TriggerSync).
func (e *env) start(notify func(Event)) {
	e.t.Helper()
	e.m = NewManager(e.cfg, e.st, e.blobDir, func(ev Event) {
		e.mu.Lock()
		e.events = append(e.events, ev)
		e.mu.Unlock()
		if notify != nil {
			notify(ev)
		}
	}, slog.New(slog.NewTextHandler(testWriter{e.t}, &slog.HandlerOptions{Level: slog.LevelDebug})))
	e.m.plain = true
	e.m.password = func(string) (string, error) { return testPass, nil }
	if e.tweak != nil {
		e.tweak(e.m)
	}
	if err := e.m.Start(context.Background()); err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(e.m.Stop)
}

type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	defer func() { recover() }() // logging after the test ended
	w.t.Log(string(p))
	return len(p), nil
}

func (e *env) eventsOf(t EventType) []Event {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []Event
	for _, ev := range e.events {
		if ev.Type == t {
			out = append(out, ev)
		}
	}
	return out
}

// client opens an authenticated test client on the server.
func (e *env) client() *imapclient.Client {
	e.t.Helper()
	c, err := imapclient.DialInsecure(e.addr, nil)
	if err != nil {
		e.t.Fatal(err)
	}
	if err := c.Login(testUser, testPass).Wait(); err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() { c.Close() })
	return c
}

func rawMsg(subject, id, body string) string {
	return fmt.Sprintf("From: Alice <alice@example.com>\r\nTo: Bob <bob@example.com>\r\nSubject: %s\r\nMessage-ID: <%s>\r\nDate: Mon, 02 Jan 2006 15:04:05 +0000\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n%s\r\n", subject, id, body)
}

func (e *env) appendMsg(mailbox, raw string, flags ...imap.Flag) {
	e.t.Helper()
	c := e.client()
	cmd := c.Append(mailbox, int64(len(raw)), &imap.AppendOptions{Flags: flags})
	if _, err := cmd.Write([]byte(raw)); err != nil {
		e.t.Fatal(err)
	}
	if err := cmd.Close(); err != nil {
		e.t.Fatal(err)
	}
	if _, err := cmd.Wait(); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) serverFlags(mailbox string, uid imap.UID) []imap.Flag {
	e.t.Helper()
	c := e.client()
	if _, err := c.Select(mailbox, nil).Wait(); err != nil {
		e.t.Fatal(err)
	}
	bufs, err := c.Fetch(imap.UIDSetNum(uid), &imap.FetchOptions{UID: true, Flags: true}).Collect()
	if err != nil || len(bufs) != 1 {
		e.t.Fatalf("fetch flags: %v %d", err, len(bufs))
	}
	return bufs[0].Flags
}

// waitFor polls cond until it holds.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func (e *env) msgs(folderID string) []store.MessageHeader {
	hs, _, err := e.st.ListMessages(context.Background(), folderID, 1000, 0)
	if err != nil {
		e.t.Fatal(err)
	}
	return hs
}

func (e *env) waitCount(folderID string, n int) {
	e.t.Helper()
	waitFor(e.t, fmt.Sprintf("%d messages in %s", n, folderID), func() bool { return len(e.msgs(folderID)) == n })
}

func (e *env) sync() {
	e.t.Helper()
	if err := e.m.TriggerSync("acc"); err != nil {
		e.t.Fatal(err)
	}
}

// settle waits until a sync pass that started after this call has finished.
func (e *env) settle() {
	e.t.Helper()
	n := len(e.eventsOf(EventSyncFinished))
	e.sync()
	waitFor(e.t, "sync pass to finish", func() bool {
		return len(e.eventsOf(EventSyncStarted)) > 0 && len(e.eventsOf(EventSyncFinished)) >= n+1
	})
	// A pass already running when we triggered may have finished first; run one more.
	n = len(e.eventsOf(EventSyncFinished))
	e.sync()
	waitFor(e.t, "second sync pass to finish", func() bool { return len(e.eventsOf(EventSyncFinished)) >= n+1 })
}

func byUID(hs []store.MessageHeader, uid uint32) (store.MessageHeader, bool) {
	for _, h := range hs {
		if h.UID == uid {
			return h, true
		}
	}
	return store.MessageHeader{}, false
}
