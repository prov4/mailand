package rpc

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"mailand/backend/internal/rpc/pb"
)

const (
	defaultLimit  = 50
	maxLimit      = 500
	mockPerFolder = 120
)

// Server implements pb.MailandServiceServer with mock data (Milestone 1).
// Unimplemented RPCs return codes.Unimplemented via the embedded type.
type Server struct {
	pb.UnimplementedMailandServiceServer
	stop     chan struct{} // closed by Close to end SubscribeEvents streams
	stopOnce sync.Once
}

// NewServer returns a mock MailandService.
func NewServer() *Server { return &Server{stop: make(chan struct{})} }

// Register creates a grpc.Server with s registered.
func (s *Server) Register() *grpc.Server {
	g := grpc.NewServer()
	pb.RegisterMailandServiceServer(g, s)
	return g
}

// Close ends open event streams; call before GracefulStop.
func (s *Server) Close() {
	s.stopOnce.Do(func() { close(s.stop) })
}

var mockAccounts = []*pb.Account{
	{Id: "acc-gmail", Name: "Personal Gmail", Email: "me@gmail.example"},
	{Id: "acc-work", Name: "Work", Email: "me@work.example"},
}

type mockFolder struct {
	suffix, name, path string
	unread             uint32
}

var mockFolders = []mockFolder{
	{"inbox", "INBOX", "INBOX", 7},
	{"sent", "Sent", "Sent", 0},
	{"label-projects", "Projects", "Projects", 3},
}

func accountExists(id string) bool {
	for _, a := range mockAccounts {
		if a.Id == id {
			return true
		}
	}
	return false
}

func folderID(acc, suffix string) string { return acc + ":" + suffix }

// splitFolderID parses "<account>:<suffix>" and validates both parts.
func splitFolderID(id string) (acc, suffix string, ok bool) {
	acc, suffix, found := strings.Cut(id, ":")
	if !found || !accountExists(acc) {
		return "", "", false
	}
	for _, f := range mockFolders {
		if f.suffix == suffix {
			return acc, suffix, true
		}
	}
	return "", "", false
}

func messageID(folder string, n int) string { return fmt.Sprintf("%s/%d", folder, n) }

// mockHeader builds the n-th (1-based, newest first) header of a folder.
func mockHeader(folder string, n int) *pb.MessageHeader {
	return &pb.MessageHeader{
		Id:             messageID(folder, n),
		FolderId:       folder,
		Uid:            uint32(mockPerFolder - n + 1),
		Subject:        fmt.Sprintf("Mock message #%d", n),
		FromName:       "Alice Example",
		FromAddress:    "alice@example.com",
		TimestampUnix:  time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC).Add(-time.Duration(n) * time.Hour).Unix(),
		IsRead:         n%3 != 0,
		IsFlagged:      n%10 == 0,
		HasAttachments: n%7 == 0,
		Snippet:        fmt.Sprintf("This is the preview of mock message #%d.", n),
		FolderIds:      []string{folder},
	}
}

func (s *Server) ListAccounts(context.Context, *pb.Empty) (*pb.AccountList, error) {
	return &pb.AccountList{Accounts: mockAccounts}, nil
}

func (s *Server) ListFolders(_ context.Context, req *pb.AccountRequest) (*pb.FolderList, error) {
	if req.AccountId == "" {
		return nil, status.Error(codes.InvalidArgument, "account_id is required")
	}
	if !accountExists(req.AccountId) {
		return nil, status.Errorf(codes.NotFound, "account %q not found", req.AccountId)
	}
	out := &pb.FolderList{}
	for _, f := range mockFolders {
		out.Folders = append(out.Folders, &pb.Folder{
			Id: folderID(req.AccountId, f.suffix), AccountId: req.AccountId,
			Name: f.name, Path: f.path, UnreadCount: f.unread,
		})
	}
	return out, nil
}

func (s *Server) ListMessages(_ context.Context, req *pb.ListMessagesRequest) (*pb.MessageHeaderList, error) {
	if req.FolderId == "" {
		return nil, status.Error(codes.InvalidArgument, "folder_id is required")
	}
	if _, _, ok := splitFolderID(req.FolderId); !ok {
		return nil, status.Errorf(codes.NotFound, "folder %q not found", req.FolderId)
	}
	limit := int(req.Limit)
	if limit == 0 {
		limit = defaultLimit
	}
	limit = min(limit, maxLimit)

	out := &pb.MessageHeaderList{TotalCount: mockPerFolder}
	for n := int(req.Offset) + 1; n <= mockPerFolder && len(out.Messages) < limit; n++ {
		out.Messages = append(out.Messages, mockHeader(req.FolderId, n))
	}
	return out, nil
}

func (s *Server) GetMessage(_ context.Context, req *pb.MessageRequest) (*pb.MessageDetail, error) {
	if req.MessageId == "" {
		return nil, status.Error(codes.InvalidArgument, "message_id is required")
	}
	folder, num, ok := strings.Cut(req.MessageId, "/")
	n, err := strconv.Atoi(num)
	if _, _, fok := splitFolderID(folder); !ok || !fok || err != nil || n < 1 || n > mockPerFolder || num != strconv.Itoa(n) {
		return nil, status.Errorf(codes.NotFound, "message %q not found", req.MessageId)
	}
	h := mockHeader(folder, n)
	return &pb.MessageDetail{
		Header:      h,
		ToAddresses: []string{"me@example.com"},
		CcAddresses: []string{"bob@example.com"},
		TextBody:    fmt.Sprintf("Hello,\n\nThis is the body of mock message #%d.\n\nAlice\n", n),
		HtmlBody:    fmt.Sprintf("<p>Hello,</p><p>This is the body of <b>mock message #%d</b>.</p><p>Alice</p>", n),
		Attachments: attachmentsFor(h),
	}, nil
}

func attachmentsFor(h *pb.MessageHeader) []*pb.AttachmentInfo {
	if !h.HasAttachments {
		return nil
	}
	return []*pb.AttachmentInfo{{Id: h.Id + "#1", Filename: "notes.pdf", MimeType: "application/pdf", SizeBytes: 48213}}
}

// SubscribeEvents holds the stream open; no events are produced yet.
func (s *Server) SubscribeEvents(_ *pb.Empty, stream grpc.ServerStreamingServer[pb.DaemonEvent]) error {
	select {
	case <-stream.Context().Done():
		return stream.Context().Err()
	case <-s.stop:
		return status.Error(codes.Unavailable, "server shutting down")
	}
}
