package rpc

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	"mailand/backend/internal/rpc/pb"
	"mailand/backend/internal/store"
)

type fakeSyncer struct {
	mu        sync.Mutex
	triggered []string
	ids       []string
	archived  []string
	read, flg *bool
	err       error
}

func (f *fakeSyncer) TriggerSync(id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.triggered = append(f.triggered, id)
	return f.err
}

func (f *fakeSyncer) SetFlags(_ context.Context, ids []string, r, fl *bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ids, f.read, f.flg = ids, r, fl
	return f.err
}

func (f *fakeSyncer) ArchiveMessages(_ context.Context, ids []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.archived = ids
	return f.err
}

type env struct {
	c                         pb.MailandServiceClient
	srv                       *Server
	broker                    *Broker
	st                        *store.Store
	sync                      *fakeSyncer
	inbox, work, label, other string
	msgs                      map[string]string // name -> id
}

const eml = "From: a@example.com\r\nTo: b@example.com\r\nSubject: Invoice\r\nMIME-Version: 1.0\r\n" +
	"Content-Type: multipart/mixed; boundary=\"MIX\"\r\n\r\n--MIX\r\nContent-Type: text/plain\r\n\r\nSee attached.\r\n" +
	"--MIX\r\nContent-Type: application/pdf; name=\"inv.pdf\"\r\nContent-Disposition: attachment; filename=\"inv.pdf\"\r\n" +
	"Content-Transfer-Encoding: base64\r\n\r\nJVBERi0xLjQ=\r\n--MIX--\r\n"

func setup(t *testing.T, withSync bool) *env {
	t.Helper()
	bg := context.Background()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	e := &env{st: st, msgs: map[string]string{}, broker: NewBroker()}
	for _, a := range []store.Account{
		{ID: "gm", Name: "Gmail", Email: "me@gmail.example", IMAPHost: "h", IMAPPort: 993, SMTPHost: "h", SMTPPort: 465},
		{ID: "wk", Name: "Work", Email: "me@work.example", IMAPHost: "h", IMAPPort: 993, SMTPHost: "h", SMTPPort: 465},
	} {
		if err := st.UpsertAccount(bg, a); err != nil {
			t.Fatal(err)
		}
	}
	mk := func(acc, name, path, kind string) string {
		f, err := st.UpsertFolder(bg, store.Folder{AccountID: acc, Name: name, Path: path, Kind: kind, IsSyncSource: kind == "folder"})
		if err != nil {
			t.Fatal(err)
		}
		return f.ID
	}
	e.inbox = mk("gm", "INBOX", "INBOX", "folder")
	e.label = mk("gm", "Projects", "Projects", "gmail_label")
	e.work = mk("wk", "INBOX", "INBOX", "folder")
	e.other = mk("wk", "Sent", "Sent", "folder")

	blob := filepath.Join(dir, "a.eml")
	if err := os.WriteFile(blob, []byte(eml), 0o600); err != nil {
		t.Fatal(err)
	}
	put := func(name, folder string, uid uint32, ts int64, subj, body string, read bool, blobPath string, folders []string) {
		m := store.Message{
			MessageHeader: store.MessageHeader{AccountID: "gm", FolderID: folder, UID: uid, Subject: subj,
				FromName: "Alice", FromAddress: "alice@example.com", TimestampUnix: ts, IsRead: read,
				HasAttachments: blobPath != "", Snippet: "snip " + name},
			ToJSON:   `[{"Name":"Bob B","Address":"bob@example.com"},{"Name":"","Address":"carol@example.com"}]`,
			CcJSON:   `["dave@example.com"]`,
			TextBody: body, RawBlobPath: blobPath,
		}
		if _, err := st.UpsertMessage(bg, m, folders); err != nil {
			t.Fatal(err)
		}
		hs, _, _ := st.ListMessages(bg, folder, 500, 0)
		for _, h := range hs {
			if h.UID == uid {
				e.msgs[name] = h.ID
			}
		}
	}
	// 60 plain messages in INBOX; "multi" is also in the label.
	for i := 1; i <= 60; i++ {
		put("m"+string(rune('A'+i%26))+string(rune('a'+i/26)), e.inbox, uint32(i), int64(1000+i), "Subject", "body text", i%2 == 0, "", nil)
	}
	put("multi", e.inbox, 100, 5000, "Quarterly zebra report", "the zebra migrates north", false, "", []string{e.inbox, e.label})
	put("att", e.inbox, 101, 5001, "Invoice", "See attached.", true, blob, nil)

	if withSync {
		e.sync = &fakeSyncer{}
	}
	var sy Syncer
	if e.sync != nil {
		sy = e.sync
	}
	e.srv = NewServer(st, sy, e.broker, slog.New(slog.NewTextHandler(io.Discard, nil)))
	path := sockPath(t)
	lis, err := ListenUnix(path)
	if err != nil {
		t.Fatal(err)
	}
	g := e.srv.Register()
	go g.Serve(lis)
	t.Cleanup(func() { e.srv.Close(); e.broker.Close(); g.Stop() })
	conn, err := grpc.NewClient("unix://"+path, grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(MaxMessageSize)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	e.c = pb.NewMailandServiceClient(conn)
	return e
}

func ctx(t *testing.T) context.Context {
	c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return c
}

func wantCode(t *testing.T, err error, c codes.Code, what string) {
	t.Helper()
	if status.Code(err) != c {
		t.Errorf("%s: got %v, want code %v", what, err, c)
	}
}

func TestAccountsFoldersMessages(t *testing.T) {
	e := setup(t, true)
	cx := ctx(t)

	accs, err := e.c.ListAccounts(cx, &pb.Empty{})
	if err != nil || len(accs.Accounts) != 2 || accs.Accounts[0].Id != "gm" || accs.Accounts[0].Email != "me@gmail.example" {
		t.Fatalf("accounts: %v %v", accs, err)
	}
	fl, err := e.c.ListFolders(cx, &pb.AccountRequest{AccountId: "gm"})
	if err != nil || len(fl.Folders) != 2 {
		t.Fatalf("folders: %v %v", fl, err)
	}
	for _, f := range fl.Folders {
		if f.Id == e.inbox && f.UnreadCount == 0 {
			t.Error("inbox should have unread")
		}
		if f.Id == e.label && f.UnreadCount != 1 {
			t.Errorf("label unread = %d, want 1", f.UnreadCount)
		}
	}
	_, err = e.c.ListFolders(cx, &pb.AccountRequest{AccountId: "nope"})
	wantCode(t, err, codes.NotFound, "unknown account")
	_, err = e.c.ListFolders(cx, &pb.AccountRequest{})
	wantCode(t, err, codes.InvalidArgument, "empty account")

	// Default limit, total, ordering newest first.
	l, err := e.c.ListMessages(cx, &pb.ListMessagesRequest{FolderId: e.inbox})
	if err != nil || len(l.Messages) != defaultLimit || l.TotalCount != 62 {
		t.Fatalf("default page: %v %v", l, err)
	}
	if l.Messages[0].Id != e.msgs["att"] || l.Messages[1].Id != e.msgs["multi"] {
		t.Error("not newest first")
	}
	p2, err := e.c.ListMessages(cx, &pb.ListMessagesRequest{FolderId: e.inbox, Limit: 10, Offset: 10})
	if err != nil || len(p2.Messages) != 10 || p2.Messages[0].Id != l.Messages[10].Id {
		t.Fatalf("page 2: %v %v", p2, err)
	}
	last, _ := e.c.ListMessages(cx, &pb.ListMessagesRequest{FolderId: e.inbox, Offset: 57})
	if len(last.Messages) != 5 {
		t.Errorf("last page = %d", len(last.Messages))
	}
	past, err := e.c.ListMessages(cx, &pb.ListMessagesRequest{FolderId: e.inbox, Offset: 1000})
	if err != nil || len(past.Messages) != 0 || past.TotalCount != 62 {
		t.Errorf("past end: %v %v", past, err)
	}
	// Label view includes the multi-label message.
	lab, err := e.c.ListMessages(cx, &pb.ListMessagesRequest{FolderId: e.label})
	if err != nil || lab.TotalCount != 1 || lab.Messages[0].Id != e.msgs["multi"] {
		t.Errorf("label list: %v %v", lab, err)
	}
	_, err = e.c.ListMessages(cx, &pb.ListMessagesRequest{FolderId: "bogus"})
	wantCode(t, err, codes.NotFound, "unknown folder")
	_, err = e.c.ListMessages(cx, &pb.ListMessagesRequest{})
	wantCode(t, err, codes.InvalidArgument, "empty folder")
}

func TestGetMessage(t *testing.T) {
	e := setup(t, true)
	cx := ctx(t)

	d, err := e.c.GetMessage(cx, &pb.MessageRequest{MessageId: e.msgs["multi"]})
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Header.FolderIds) != 2 || d.TextBody != "the zebra migrates north" || len(d.Attachments) != 0 {
		t.Errorf("multi: %v", d)
	}
	if len(d.ToAddresses) != 2 || d.ToAddresses[0] != `Bob B <bob@example.com>` || d.ToAddresses[1] != "carol@example.com" {
		t.Errorf("to = %q", d.ToAddresses)
	}
	// Non-ASCII display names must stay readable UTF-8 (not RFC 2047 Q-encoded).
	if got := formatAddrs(`[{"Name":"Ivana Rajković Pavlić","Address":"ivana@zicer.hr"},{"Name":"Doe, Jane","Address":"jane@example.com"}]`); len(got) != 2 ||
		got[0] != "Ivana Rajković Pavlić <ivana@zicer.hr>" ||
		got[1] != `"Doe, Jane" <jane@example.com>` {
		t.Errorf("formatAddrs utf8/quoted = %q", got)
	}
	if len(d.CcAddresses) != 1 || d.CcAddresses[0] != "dave@example.com" {
		t.Errorf("cc = %q", d.CcAddresses)
	}

	d, err = e.c.GetMessage(cx, &pb.MessageRequest{MessageId: e.msgs["att"]})
	if err != nil || len(d.Attachments) != 1 {
		t.Fatalf("att: %v %v", d, err)
	}
	a := d.Attachments[0]
	if a.Id != "2" || a.Filename != "inv.pdf" || a.MimeType != "application/pdf" || a.SizeBytes != 8 {
		t.Errorf("attachment = %v", a)
	}

	// Missing blob is not an error.
	m, _ := e.st.GetMessage(cx, e.msgs["att"])
	m.RawBlobPath = "/nonexistent/x.eml"
	if _, err := e.st.UpsertMessage(cx, m, m.FolderIDs); err != nil {
		t.Fatal(err)
	}
	d, err = e.c.GetMessage(cx, &pb.MessageRequest{MessageId: e.msgs["att"]})
	if err != nil || len(d.Attachments) != 0 {
		t.Errorf("missing blob: %v %v", d, err)
	}

	_, err = e.c.GetMessage(cx, &pb.MessageRequest{MessageId: "missing"})
	wantCode(t, err, codes.NotFound, "missing")
	_, err = e.c.GetMessage(cx, &pb.MessageRequest{})
	wantCode(t, err, codes.InvalidArgument, "empty")
}

func TestSearch(t *testing.T) {
	e := setup(t, true)
	cx := ctx(t)
	r, err := e.c.SearchMessages(cx, &pb.SearchRequest{Query: "zebra"})
	if err != nil || len(r.Messages) != 1 || r.Messages[0].Id != e.msgs["multi"] {
		t.Fatalf("search: %v %v", r, err)
	}
	sn := r.Messages[0].Snippet
	if !strings.Contains(sn, store.HighlightStart+"zebra"+store.HighlightEnd) {
		t.Errorf("snippet lacks markers: %q", sn)
	}
	r, err = e.c.SearchMessages(cx, &pb.SearchRequest{Query: "zebra", AccountId: "wk"})
	if err != nil || len(r.Messages) != 0 {
		t.Errorf("account filter: %v %v", r, err)
	}
	r, err = e.c.SearchMessages(cx, &pb.SearchRequest{Query: "-: \""})
	if err != nil || len(r.Messages) != 0 {
		t.Errorf("symbol query: %v %v", r, err)
	}
}

func TestSetMessageFlags(t *testing.T) {
	e := setup(t, true)
	cx := ctx(t)
	tr := true
	if _, err := e.c.SetMessageFlags(cx, &pb.SetFlagsRequest{MessageIds: []string{"a", "b"}, IsRead: &tr}); err != nil {
		t.Fatal(err)
	}
	if len(e.sync.ids) != 2 || e.sync.read == nil || !*e.sync.read || e.sync.flg != nil {
		t.Errorf("syncer args: %v %v %v", e.sync.ids, e.sync.read, e.sync.flg)
	}
	_, err := e.c.SetMessageFlags(cx, &pb.SetFlagsRequest{IsRead: &tr})
	wantCode(t, err, codes.InvalidArgument, "no ids")
	_, err = e.c.SetMessageFlags(cx, &pb.SetFlagsRequest{MessageIds: []string{"a"}})
	wantCode(t, err, codes.InvalidArgument, "no flags")
	_, err = e.c.SetMessageFlags(cx, &pb.SetFlagsRequest{MessageIds: []string{""}, IsRead: &tr})
	wantCode(t, err, codes.InvalidArgument, "empty id")

	e.sync.err = errors.New("boom /secret/path")
	_, err = e.c.SetMessageFlags(cx, &pb.SetFlagsRequest{MessageIds: []string{"a"}, IsRead: &tr})
	wantCode(t, err, codes.Internal, "syncer failure")
	if strings.Contains(err.Error(), "secret") {
		t.Errorf("leaked detail: %v", err)
	}
	e.sync.err = store.ErrNotFound
	_, err = e.c.SetMessageFlags(cx, &pb.SetFlagsRequest{MessageIds: []string{"a"}, IsRead: &tr})
	wantCode(t, err, codes.NotFound, "syncer notfound")
}

func TestTriggerSync(t *testing.T) {
	e := setup(t, true)
	cx := ctx(t)
	if _, err := e.c.TriggerSync(cx, &pb.AccountRequest{AccountId: "gm"}); err != nil {
		t.Fatal(err)
	}
	if len(e.sync.triggered) != 1 || e.sync.triggered[0] != "gm" {
		t.Errorf("triggered = %v", e.sync.triggered)
	}
	_, err := e.c.TriggerSync(cx, &pb.AccountRequest{AccountId: "nope"})
	wantCode(t, err, codes.NotFound, "unknown account")
	_, err = e.c.TriggerSync(cx, &pb.AccountRequest{})
	wantCode(t, err, codes.InvalidArgument, "empty")
}

func TestNilSyncer(t *testing.T) {
	e := setup(t, false)
	cx := ctx(t)
	tr := true
	_, err := e.c.SetMessageFlags(cx, &pb.SetFlagsRequest{MessageIds: []string{"a"}, IsRead: &tr})
	wantCode(t, err, codes.FailedPrecondition, "flags")
	_, err = e.c.TriggerSync(cx, &pb.AccountRequest{AccountId: "gm"})
	wantCode(t, err, codes.FailedPrecondition, "sync")
	_, err = e.c.SendMessage(cx, &pb.SendMessageRequest{AccountId: "gm", To: []string{"a@example.com"}})
	wantCode(t, err, codes.FailedPrecondition, "send")
}

func TestSubscribeEvents(t *testing.T) {
	e := setup(t, true)
	stream, err := e.c.SubscribeEvents(ctx(t), &pb.Empty{})
	if err != nil {
		t.Fatal(err)
	}
	// Publish until the subscription is live, then expect delivery.
	got := make(chan *pb.DaemonEvent, 1)
	errc := make(chan error, 1)
	go func() {
		ev, err := stream.Recv()
		if err != nil {
			errc <- err
			return
		}
		got <- ev
	}()
	deadline := time.After(3 * time.Second)
	for sent := false; !sent; {
		e.broker.Publish(&pb.DaemonEvent{Type: pb.DaemonEvent_NEW_MESSAGES, AccountId: "gm", Detail: "x"})
		select {
		case ev := <-got:
			if ev.Type != pb.DaemonEvent_NEW_MESSAGES || ev.AccountId != "gm" {
				t.Errorf("event = %v", ev)
			}
			sent = true
		case err := <-errc:
			t.Fatal(err)
		case <-time.After(50 * time.Millisecond):
		case <-deadline:
			t.Fatal("no event received")
		}
	}

	go func() { _, err := stream.Recv(); errc <- err }()
	e.srv.Close()
	select {
	case err := <-errc:
		wantCode(t, err, codes.Unavailable, "after Close")
	case <-time.After(2 * time.Second):
		t.Fatal("stream did not end on Close")
	}
}

func TestSubscribeEndsOnClientCancel(t *testing.T) {
	e := setup(t, true)
	cx, cancel := context.WithCancel(ctx(t))
	stream, err := e.c.SubscribeEvents(cx, &pb.Empty{})
	if err != nil {
		t.Fatal(err)
	}
	errc := make(chan error, 1)
	go func() { _, err := stream.Recv(); errc <- err }()
	time.Sleep(100 * time.Millisecond)
	cancel()
	select {
	case err := <-errc:
		wantCode(t, err, codes.Canceled, "cancel")
	case <-time.After(2 * time.Second):
		t.Fatal("stream did not end after cancel")
	}
}

func TestBrokerSlowSubscriberDoesNotBlock(t *testing.T) {
	b := NewBroker()
	slow, cancelSlow := b.Subscribe()
	defer cancelSlow()
	fast, cancelFast := b.Subscribe()
	defer cancelFast()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < subscriberBuffer*3; i++ {
			b.Publish(&pb.DaemonEvent{Detail: "e"})
			select { // fast subscriber keeps draining
			case <-fast:
			default:
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Publish blocked on slow subscriber")
	}
	if len(slow) != subscriberBuffer {
		t.Errorf("slow buffer = %d", len(slow))
	}
	if b.Dropped() == 0 {
		t.Error("expected drops")
	}
	cancelSlow()
	cancelSlow() // idempotent
	b.Close()
	b.Close()
	b.Publish(&pb.DaemonEvent{}) // no panic after close
	if _, ok := <-fast; ok {
		// buffered leftovers are fine; channel must eventually close
		for range fast {
		}
	}
	ch, _ := b.Subscribe()
	if _, ok := <-ch; ok {
		t.Error("subscribe after close should yield closed channel")
	}
}
