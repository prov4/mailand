package rpc

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"mailand/backend/internal/auth"
	"mailand/backend/internal/rpc/pb"
	"mailand/backend/internal/send"
)

type fakeSender struct {
	mu  sync.Mutex
	got []send.Request
	id  string
	err error
}

func (f *fakeSender) Send(_ context.Context, r send.Request) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.got = append(f.got, r)
	return f.id, f.err
}

func TestSendMessageValidation(t *testing.T) {
	e := setup(t, true)
	fs := &fakeSender{id: "id1@x"}
	e.srv.SetSender(fs)
	cx := ctx(t)
	ok := func() *pb.SendMessageRequest {
		return &pb.SendMessageRequest{AccountId: "wk", To: []string{"a@example.com"}}
	}
	for name, mod := range map[string]func(*pb.SendMessageRequest){
		"no account":      func(r *pb.SendMessageRequest) { r.AccountId = "" },
		"unknown account": func(r *pb.SendMessageRequest) { r.AccountId = "nope" },
		"no recipients":   func(r *pb.SendMessageRequest) { r.To = nil },
		"bad address":     func(r *pb.SendMessageRequest) { r.Cc = []string{"nonsense"} },
		"bad bcc":         func(r *pb.SendMessageRequest) { r.Bcc = []string{""} },
		"crlf":            func(r *pb.SendMessageRequest) { r.Subject = "x\r\nBcc: e@x.com" },
	} {
		r := ok()
		mod(r)
		_, err := e.c.SendMessage(cx, r)
		wantCode(t, err, codes.InvalidArgument, name)
	}
	if len(fs.got) != 0 {
		t.Fatalf("sender called for invalid requests: %v", fs.got)
	}

	r := ok()
	r.Bcc, r.Subject, r.TextBody, r.ReplyToId, r.ForwardOfId, r.InReplyTo = []string{"b@example.com"}, "S", "B", "r1", "f1", "<i@x>"
	resp, err := e.c.SendMessage(cx, r)
	if err != nil || resp.MessageIdHeader != "id1@x" {
		t.Fatalf("send: %v %v", resp, err)
	}
	g := fs.got[0]
	if g.AccountID != "wk" || g.Bcc[0] != "b@example.com" || g.Subject != "S" || g.TextBody != "B" ||
		g.ReplyToID != "r1" || g.ForwardOfID != "f1" || g.InReplyTo != "<i@x>" {
		t.Errorf("request mapping: %+v", g)
	}
}

func TestSendMessageErrorMapping(t *testing.T) {
	e := setup(t, true)
	fs := &fakeSender{}
	e.srv.SetSender(fs)
	cx := ctx(t)
	for _, tc := range []struct {
		err  error
		code codes.Code
		msg  string
	}{
		{fmt.Errorf("%w: 550 no such user", send.ErrRecipientRejected), codes.InvalidArgument, "550 no such user"},
		{fmt.Errorf("%w: unknown account", send.ErrInvalid), codes.InvalidArgument, "unknown account"},
		{fmt.Errorf("%w: %w", send.ErrAuth, auth.ErrReauthRequired), codes.Unauthenticated, "reauth_required"},
		{fmt.Errorf("%w: 535 bad", send.ErrAuth), codes.Unauthenticated, "535 bad"},
		{fmt.Errorf("%w: connect: refused", send.ErrUnavailable), codes.Unavailable, "connect: refused"},
		{fmt.Errorf("%w: no raw", send.ErrPrecondition), codes.FailedPrecondition, "no raw"},
		{context.DeadlineExceeded, codes.DeadlineExceeded, ""},
		{errors.New("secret internals /home/x"), codes.Internal, "internal error"},
	} {
		fs.err = tc.err
		_, err := e.c.SendMessage(cx, &pb.SendMessageRequest{AccountId: "wk", To: []string{"a@example.com"}})
		wantCode(t, err, tc.code, tc.err.Error())
		if tc.msg != "" && !strings.Contains(status.Convert(err).Message(), tc.msg) {
			t.Errorf("%v: message %q", tc.err, status.Convert(err).Message())
		}
		if strings.Contains(status.Convert(err).Message(), "/home/x") {
			t.Error("internal error leaked")
		}
	}
}

func TestOfflineSenderOverRPC(t *testing.T) {
	e := setup(t, true)
	e.srv.SetSender(send.NewOfflineSender(e.st, t.TempDir(), func(acc, folder string) {
		e.broker.Publish(&pb.DaemonEvent{Type: pb.DaemonEvent_NEW_MESSAGES, AccountId: acc, FolderId: folder})
	}))
	cx := ctx(t)
	stream, err := e.c.SubscribeEvents(cx, &pb.Empty{})
	if err != nil {
		t.Fatal(err)
	}
	// Wait until the subscription is live: Recv blocks, so receive in a
	// goroutine and keep publishing until the first event arrives.
	first := make(chan error, 1)
	go func() { _, err := stream.Recv(); first <- err }()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for live := false; !live; {
		e.broker.Publish(&pb.DaemonEvent{Type: pb.DaemonEvent_SYNC_STARTED})
		select {
		case err := <-first:
			if err != nil {
				t.Fatal(err)
			}
			live = true
		case <-tick.C:
		case <-cx.Done():
			t.Fatal("subscription never became live")
		}
	}
	resp, err := e.c.SendMessage(cx, &pb.SendMessageRequest{AccountId: "wk", To: []string{"a@example.com"}, Subject: "Hi", TextBody: "yo"})
	if err != nil || resp.MessageIdHeader == "" {
		t.Fatalf("send: %v %v", resp, err)
	}
	var ev *pb.DaemonEvent
	for ev == nil || ev.Type != pb.DaemonEvent_NEW_MESSAGES {
		if ev, err = stream.Recv(); err != nil {
			t.Fatal(err)
		}
	}
	if ev.AccountId != "wk" || ev.FolderId != e.other { // e.other is wk's "Sent" folder (no special use set)
		t.Logf("event %+v (sent folder created by offline sender if none had \\Sent)", ev)
	}
	list, err := e.c.ListMessages(cx, &pb.ListMessagesRequest{FolderId: ev.FolderId})
	if err != nil || len(list.Messages) != 1 || list.Messages[0].Subject != "Hi" || !list.Messages[0].IsRead {
		t.Fatalf("sent list: %v %v", list, err)
	}
	folders, _ := e.c.ListFolders(cx, &pb.AccountRequest{AccountId: "wk"})
	for _, f := range folders.Folders {
		if f.Id == ev.FolderId && f.SpecialUse != `\Sent` {
			t.Errorf("folder %+v is not \\Sent", f)
		}
	}
}
