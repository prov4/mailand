package rpc

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	"mailand/backend/internal/rpc/pb"
)

func startServer(t *testing.T) (pb.MailandServiceClient, *Server) {
	t.Helper()
	path := sockPath(t)
	lis, err := ListenUnix(path)
	if err != nil {
		t.Fatal(err)
	}
	srv := NewServer()
	g := srv.Register()
	go g.Serve(lis)
	t.Cleanup(func() { srv.Close(); g.Stop() })

	conn, err := grpc.NewClient("unix://"+path, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return pb.NewMailandServiceClient(conn), srv
}

func ctx(t *testing.T) context.Context {
	c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return c
}

func TestIntegration(t *testing.T) {
	c, _ := startServer(t)
	cx := ctx(t)

	accs, err := c.ListAccounts(cx, &pb.Empty{})
	if err != nil {
		t.Fatal(err)
	}
	if len(accs.Accounts) != 2 || accs.Accounts[0].Id != "acc-gmail" || accs.Accounts[1].Id != "acc-work" ||
		accs.Accounts[0].Email != "me@gmail.example" || accs.Accounts[1].Name != "Work" {
		t.Fatalf("unexpected accounts: %v", accs.Accounts)
	}

	var inbox string
	for _, a := range accs.Accounts {
		fl, err := c.ListFolders(cx, &pb.AccountRequest{AccountId: a.Id})
		if err != nil {
			t.Fatal(err)
		}
		if len(fl.Folders) < 3 {
			t.Fatalf("want >=3 folders, got %d", len(fl.Folders))
		}
		for _, f := range fl.Folders {
			if f.AccountId != a.Id {
				t.Errorf("folder %s has account %s", f.Id, f.AccountId)
			}
			if f.Name == "INBOX" {
				inbox = f.Id
				if f.UnreadCount == 0 {
					t.Error("INBOX should have unread")
				}
			}
		}
	}

	if _, err := c.ListFolders(cx, &pb.AccountRequest{AccountId: "nope"}); status.Code(err) != codes.NotFound {
		t.Errorf("unknown account: %v", err)
	}
	if _, err := c.ListFolders(cx, &pb.AccountRequest{}); status.Code(err) != codes.InvalidArgument {
		t.Errorf("empty account: %v", err)
	}

	// Default limit.
	l, err := c.ListMessages(cx, &pb.ListMessagesRequest{FolderId: inbox})
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Messages) != defaultLimit || l.TotalCount != mockPerFolder {
		t.Fatalf("default page: %d msgs, total %d", len(l.Messages), l.TotalCount)
	}

	// Pagination: pages must not overlap and the last page is short.
	p1, _ := c.ListMessages(cx, &pb.ListMessagesRequest{FolderId: inbox, Limit: 10})
	p2, err := c.ListMessages(cx, &pb.ListMessagesRequest{FolderId: inbox, Limit: 10, Offset: 10})
	if err != nil || len(p1.Messages) != 10 || len(p2.Messages) != 10 {
		t.Fatalf("pages: %v %v %v", p1, p2, err)
	}
	if p1.Messages[9].Id == p2.Messages[0].Id || p2.Messages[0].Id != l.Messages[10].Id {
		t.Error("pagination offsets inconsistent")
	}
	last, _ := c.ListMessages(cx, &pb.ListMessagesRequest{FolderId: inbox, Limit: 50, Offset: mockPerFolder - 5})
	if len(last.Messages) != 5 {
		t.Errorf("last page = %d, want 5", len(last.Messages))
	}
	past, err := c.ListMessages(cx, &pb.ListMessagesRequest{FolderId: inbox, Offset: 1000})
	if err != nil || len(past.Messages) != 0 || past.TotalCount != mockPerFolder {
		t.Errorf("past end: %v %v", past, err)
	}
	if _, err := c.ListMessages(cx, &pb.ListMessagesRequest{FolderId: "acc-gmail:bogus"}); status.Code(err) != codes.NotFound {
		t.Errorf("unknown folder: %v", err)
	}
	if _, err := c.ListMessages(cx, &pb.ListMessagesRequest{}); status.Code(err) != codes.InvalidArgument {
		t.Errorf("empty folder: %v", err)
	}

	// GetMessage round trip and NotFound.
	d, err := c.GetMessage(cx, &pb.MessageRequest{MessageId: l.Messages[0].Id})
	if err != nil || d.Header.Id != l.Messages[0].Id || d.TextBody == "" {
		t.Fatalf("GetMessage: %v %v", d, err)
	}
	for _, id := range []string{"missing", "acc-gmail:inbox/9999", "nope:inbox/1", "acc-gmail:inbox/01"} {
		if _, err := c.GetMessage(cx, &pb.MessageRequest{MessageId: id}); status.Code(err) != codes.NotFound {
			t.Errorf("GetMessage(%q) = %v, want NotFound", id, err)
		}
	}
	if _, err := c.GetMessage(cx, &pb.MessageRequest{}); status.Code(err) != codes.InvalidArgument {
		t.Errorf("empty id: %v", err)
	}

	// Unimplemented RPCs.
	if _, err := c.TriggerSync(cx, &pb.AccountRequest{}); status.Code(err) != codes.Unimplemented {
		t.Errorf("TriggerSync: %v", err)
	}
}

func TestSubscribeEventsStaysOpenUntilCancel(t *testing.T) {
	c, _ := startServer(t)
	cx, cancel := context.WithCancel(ctx(t))
	stream, err := c.SubscribeEvents(cx, &pb.Empty{})
	if err != nil {
		t.Fatal(err)
	}
	errc := make(chan error, 1)
	go func() { _, err := stream.Recv(); errc <- err }()
	select {
	case err := <-errc:
		t.Fatalf("stream ended early: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	cancel()
	select {
	case err := <-errc:
		if status.Code(err) != codes.Canceled {
			t.Errorf("err = %v, want Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("stream did not end after cancel")
	}
}
