package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"strings"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"mailand/backend/internal/auth"
	mailpkg "mailand/backend/internal/mail"
	"mailand/backend/internal/rpc/pb"
	"mailand/backend/internal/send"
	"mailand/backend/internal/store"
)

const (
	defaultLimit       = 50
	maxLimit           = 500
	defaultSearchLimit = 50
	maxSearchLimit     = 200

	// MaxMessageSize is the gRPC send/receive limit: HTML bodies and
	// attachments exceed the 4 MiB default.
	MaxMessageSize = 64 << 20
	// MaxAttachmentBytes caps GetAttachment payloads.
	MaxAttachmentBytes = 50 << 20
)

// Syncer is implemented by mail.Manager.
type Syncer interface {
	TriggerSync(accountID string) error
	SetFlags(ctx context.Context, messageIDs []string, isRead, isFlagged *bool) error
	ArchiveMessages(ctx context.Context, messageIDs []string) error
}

// Sender delivers a composed message (implemented by send.Sender and
// send.OfflineSender). It returns the Message-ID of the sent mail and reports
// failures wrapping the send.Err* sentinels.
type Sender interface {
	Send(ctx context.Context, req send.Request) (messageIDHeader string, err error)
}

// Server implements pb.MailandServiceServer on top of the store.
type Server struct {
	pb.UnimplementedMailandServiceServer
	st        *store.Store
	sync      Syncer // may be nil
	sendMu    sync.RWMutex
	sender    Sender    // may be nil; see SetSender
	responder Responder // may be nil; see SetResponder
	contacts  ContactEditor
	broker    *Broker
	log       *slog.Logger

	stop     chan struct{} // closed by Close to end SubscribeEvents streams
	stopOnce sync.Once
}

// NewServer returns a MailandService. sync may be nil (no accounts / tests):
// SetMessageFlags and TriggerSync then return FailedPrecondition.
func NewServer(st *store.Store, sync Syncer, broker *Broker, log *slog.Logger) *Server {
	if log == nil {
		log = slog.Default()
	}
	if broker == nil {
		broker = NewBroker()
	}
	return &Server{st: st, sync: sync, broker: broker, log: log, stop: make(chan struct{})}
}

// SetSender installs the SendMessage backend. Call before serving; with no
// sender SendMessage returns FailedPrecondition.
func (s *Server) SetSender(snd Sender) {
	s.sendMu.Lock()
	s.sender = snd
	s.sendMu.Unlock()
}

// Register creates a grpc.Server with s registered.
func (s *Server) Register() *grpc.Server {
	g := grpc.NewServer(grpc.MaxRecvMsgSize(MaxMessageSize), grpc.MaxSendMsgSize(MaxMessageSize))
	pb.RegisterMailandServiceServer(g, s)
	return g
}

// Close ends open event streams; call before GracefulStop. It does not close
// the Broker (the owner does).
func (s *Server) Close() {
	s.stopOnce.Do(func() { close(s.stop) })
}

// internal logs err and returns a generic Internal status (no paths/SQL leaked).
func (s *Server) internal(op string, err error) error {
	s.log.Error("rpc failed", "op", op, "err", err)
	return status.Error(codes.Internal, "internal error")
}

func (s *Server) mapErr(op string, err error, what string) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, store.ErrNotFound):
		return status.Errorf(codes.NotFound, "%s not found", what)
	case errors.Is(err, context.Canceled):
		return status.Error(codes.Canceled, "canceled")
	case errors.Is(err, context.DeadlineExceeded):
		return status.Error(codes.DeadlineExceeded, "deadline exceeded")
	}
	return s.internal(op, err)
}

func header(h store.MessageHeader) *pb.MessageHeader {
	return &pb.MessageHeader{
		Id: h.ID, FolderId: h.FolderID, Uid: h.UID, Subject: h.Subject,
		FromName: h.FromName, FromAddress: h.FromAddress, TimestampUnix: h.TimestampUnix,
		IsRead: h.IsRead, IsFlagged: h.IsFlagged, IsAnswered: h.IsAnswered, HasAttachments: h.HasAttachments, Snippet: h.Snippet,
	}
}

func (s *Server) ListAccounts(ctx context.Context, _ *pb.Empty) (*pb.AccountList, error) {
	accs, err := s.st.ListAccounts(ctx)
	if err != nil {
		return nil, s.mapErr("ListAccounts", err, "account")
	}
	out := &pb.AccountList{}
	for _, a := range accs {
		out.Accounts = append(out.Accounts, &pb.Account{Id: a.ID, Name: a.Name, Email: a.Email, Signature: a.Signature})
	}
	return out, nil
}

func (s *Server) ListFolders(ctx context.Context, req *pb.AccountRequest) (*pb.FolderList, error) {
	if req.AccountId == "" {
		return nil, status.Error(codes.InvalidArgument, "account_id is required")
	}
	if _, err := s.st.GetAccount(ctx, req.AccountId); err != nil {
		return nil, s.mapErr("ListFolders", err, "account")
	}
	fs, err := s.st.ListFolders(ctx, req.AccountId)
	if err != nil {
		return nil, s.mapErr("ListFolders", err, "account")
	}
	out := &pb.FolderList{}
	for _, f := range fs {
		out.Folders = append(out.Folders, &pb.Folder{
			Id: f.ID, AccountId: f.AccountID, Name: f.Name, Path: f.Path, UnreadCount: uint32(max(f.UnreadCount, 0)),
			Kind: f.Kind, SpecialUse: f.SpecialUse,
		})
	}
	return out, nil
}

// ListMessages returns one page, newest first. For cost reasons the returned
// headers have folder_id (the source mailbox) but leave folder_ids empty; use
// GetMessage for the full membership list.
func (s *Server) ListMessages(ctx context.Context, req *pb.ListMessagesRequest) (*pb.MessageHeaderList, error) {
	if req.FolderId == "" {
		return nil, status.Error(codes.InvalidArgument, "folder_id is required")
	}
	if _, err := s.st.GetFolder(ctx, req.FolderId); err != nil {
		return nil, s.mapErr("ListMessages", err, "folder")
	}
	limit := int(req.Limit)
	if limit == 0 {
		limit = defaultLimit
	}
	limit = min(limit, maxLimit)
	hs, total, err := s.st.ListMessagesFiltered(ctx, req.FolderId, limit, int(req.Offset), req.UnreadOnly)
	if err != nil {
		return nil, s.mapErr("ListMessages", err, "folder")
	}
	out := &pb.MessageHeaderList{TotalCount: uint32(total)}
	for _, h := range hs {
		out.Messages = append(out.Messages, header(h))
	}
	return out, nil
}

// formatAddrs decodes a to_json/cc_json column. The sync engine writes a JSON
// array of {Name, Address}; plain string elements are tolerated. Garbage yields nil.
func formatAddrs(js string) []string {
	if strings.TrimSpace(js) == "" {
		return nil
	}
	var raw []json.RawMessage
	if err := json.Unmarshal([]byte(js), &raw); err != nil {
		return nil
	}
	var out []string
	for _, r := range raw {
		var a struct{ Name, Address string }
		if err := json.Unmarshal(r, &a); err != nil {
			var str string
			if json.Unmarshal(r, &str) == nil && str != "" {
				out = append(out, str)
			}
			continue
		}
		switch {
		case a.Address == "":
			continue
		case a.Name == "":
			out = append(out, a.Address)
		default:
			out = append(out, formatDisplayAddr(a.Name, a.Address))
		}
	}
	return out
}

// formatDisplayAddr formats a human-readable "Name <addr>" without RFC 2047
// encoding (net/mail.Address.String Q-encodes non-ASCII names for wire headers).
// Names containing RFC 5322 specials are quoted so address lists split cleanly.
func formatDisplayAddr(name, addr string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return addr
	}
	if strings.ContainsAny(name, `,;<>@()[]\":.\`) {
		name = `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(name) + `"`
	}
	return name + " <" + addr + ">"
}

func (s *Server) attachments(m store.Message) []*pb.AttachmentInfo {
	if m.RawBlobPath == "" {
		return nil
	}
	f, err := os.Open(m.RawBlobPath)
	if err != nil {
		s.log.Debug("raw blob unavailable", "message", m.ID, "err", err)
		return nil
	}
	defer f.Close()
	p, err := mailpkg.Parse(f)
	if err != nil || p == nil {
		s.log.Debug("raw blob unparsable", "message", m.ID, "err", err)
		return nil
	}
	var out []*pb.AttachmentInfo
	for _, a := range p.Attachments {
		out = append(out, &pb.AttachmentInfo{Id: a.PartPath, Filename: a.Filename, MimeType: a.MIMEType,
			SizeBytes: uint64(max(a.Size, 0)), ContentId: a.ContentID, Inline: a.Inline})
	}
	return out
}

func (s *Server) GetMessage(ctx context.Context, req *pb.MessageRequest) (*pb.MessageDetail, error) {
	if req.MessageId == "" {
		return nil, status.Error(codes.InvalidArgument, "message_id is required")
	}
	m, err := s.st.GetMessage(ctx, req.MessageId)
	if err != nil {
		return nil, s.mapErr("GetMessage", err, "message")
	}
	h := header(m.MessageHeader)
	h.FolderIds = m.FolderIDs
	return &pb.MessageDetail{
		Header:      h,
		ToAddresses: formatAddrs(m.ToJSON),
		CcAddresses: formatAddrs(m.CcJSON),
		TextBody:    m.TextBody,
		HtmlBody:    m.HTMLBody,
		Attachments: s.attachments(m),
	}, nil
}

// GetAttachment returns the decoded content of one MIME part of a message.
func (s *Server) GetAttachment(ctx context.Context, req *pb.AttachmentRequest) (*pb.AttachmentData, error) {
	if req.MessageId == "" || req.AttachmentId == "" {
		return nil, status.Error(codes.InvalidArgument, "message_id and attachment_id are required")
	}
	m, err := s.st.GetMessage(ctx, req.MessageId)
	if err != nil {
		return nil, s.mapErr("GetAttachment", err, "message")
	}
	if m.RawBlobPath == "" {
		return nil, status.Error(codes.FailedPrecondition, "message has no stored raw content")
	}
	f, err := os.Open(m.RawBlobPath)
	if err != nil {
		s.log.Warn("raw blob unavailable", "message", m.ID, "err", err)
		return nil, status.Error(codes.FailedPrecondition, "message has no stored raw content")
	}
	defer f.Close()
	data, meta, err := mailpkg.ExtractPart(f, req.AttachmentId)
	if err != nil {
		if errors.Is(err, mailpkg.ErrNoPart) {
			return nil, status.Error(codes.NotFound, "attachment not found")
		}
		return nil, s.internal("GetAttachment", err)
	}
	if len(data) > MaxAttachmentBytes {
		return nil, status.Errorf(codes.ResourceExhausted, "attachment larger than %d MiB", MaxAttachmentBytes>>20)
	}
	return &pb.AttachmentData{
		Info: &pb.AttachmentInfo{Id: meta.PartPath, Filename: meta.Filename, MimeType: meta.MIMEType,
			SizeBytes: uint64(len(data)), ContentId: meta.ContentID, Inline: meta.Inline},
		Data: data,
	}, nil
}

// SearchMessages runs a full-text search; each email appears once. The snippet
// of every header is the match snippet with matches wrapped in
// store.HighlightStart ("\x02") and store.HighlightEnd ("\x03"); the UI must
// render these markers (and strip them elsewhere). folder_ids is left empty.
func (s *Server) SearchMessages(ctx context.Context, req *pb.SearchRequest) (*pb.MessageHeaderList, error) {
	limit := int(req.Limit)
	if limit == 0 {
		limit = defaultSearchLimit
	}
	limit = min(limit, maxSearchLimit)
	hits, err := s.st.SearchFiltered(ctx, req.Query, req.AccountId, limit, req.UnreadOnly)
	if err != nil {
		return nil, s.mapErr("SearchMessages", err, "account")
	}
	out := &pb.MessageHeaderList{TotalCount: uint32(len(hits))}
	for _, h := range hits {
		ph := header(h.MessageHeader)
		ph.Snippet = h.MatchSnippet
		out.Messages = append(out.Messages, ph)
	}
	return out, nil
}

func (s *Server) SetMessageFlags(ctx context.Context, req *pb.SetFlagsRequest) (*pb.Empty, error) {
	if len(req.MessageIds) == 0 {
		return nil, status.Error(codes.InvalidArgument, "message_ids is required")
	}
	for _, id := range req.MessageIds {
		if id == "" {
			return nil, status.Error(codes.InvalidArgument, "message_ids must not contain empty ids")
		}
	}
	if req.IsRead == nil && req.IsFlagged == nil {
		return nil, status.Error(codes.InvalidArgument, "at least one of is_read, is_flagged is required")
	}
	if s.sync == nil {
		return nil, status.Error(codes.FailedPrecondition, "no accounts configured")
	}
	if err := s.sync.SetFlags(ctx, req.MessageIds, req.IsRead, req.IsFlagged); err != nil {
		return nil, s.mapErr("SetMessageFlags", err, "message")
	}
	return &pb.Empty{}, nil
}

func (s *Server) ArchiveMessages(ctx context.Context, req *pb.ArchiveRequest) (*pb.Empty, error) {
	if len(req.MessageIds) == 0 {
		return nil, status.Error(codes.InvalidArgument, "message_ids is required")
	}
	for _, id := range req.MessageIds {
		if id == "" {
			return nil, status.Error(codes.InvalidArgument, "message_ids must not contain empty ids")
		}
	}
	if s.sync == nil {
		return nil, status.Error(codes.FailedPrecondition, "no accounts configured")
	}
	if err := s.sync.ArchiveMessages(ctx, req.MessageIds); err != nil {
		return nil, s.mapErr("ArchiveMessages", err, "message")
	}
	return &pb.Empty{}, nil
}

func (s *Server) TriggerSync(ctx context.Context, req *pb.AccountRequest) (*pb.Empty, error) {
	if req.AccountId == "" {
		return nil, status.Error(codes.InvalidArgument, "account_id is required")
	}
	if s.sync == nil {
		return nil, status.Error(codes.FailedPrecondition, "no accounts configured")
	}
	// Check existence in the store so unknown accounts map to NotFound
	// regardless of how the Syncer reports them.
	if _, err := s.st.GetAccount(ctx, req.AccountId); err != nil {
		return nil, s.mapErr("TriggerSync", err, "account")
	}
	if err := s.sync.TriggerSync(req.AccountId); err != nil {
		return nil, s.mapErr("TriggerSync", err, "account")
	}
	return &pb.Empty{}, nil
}

// SubscribeEvents streams broker events until the client goes away or Close.
func (s *Server) SubscribeEvents(_ *pb.Empty, stream grpc.ServerStreamingServer[pb.DaemonEvent]) error {
	ch, cancel := s.broker.Subscribe()
	defer cancel()
	for {
		select {
		case <-stream.Context().Done():
			return stream.Context().Err()
		case <-s.stop:
			return status.Error(codes.Unavailable, "server shutting down")
		case ev, ok := <-ch:
			if !ok {
				return status.Error(codes.Unavailable, "event stream closed")
			}
			if err := stream.Send(ev); err != nil {
				return err
			}
		}
	}
}

// SendMessage validates the request and hands it to the Sender.
func (s *Server) SendMessage(ctx context.Context, req *pb.SendMessageRequest) (*pb.SendMessageResponse, error) {
	r := send.Request{
		AccountID: req.AccountId, To: req.To, Cc: req.Cc, Bcc: req.Bcc, Subject: req.Subject,
		TextBody: req.TextBody, HTMLBody: req.HtmlBody, InReplyTo: req.InReplyTo, ReplyToID: req.ReplyToId, ForwardOfID: req.ForwardOfId,
	}
	if len(req.Attachments) > 0 {
		r.Attachments = make([]send.Attachment, len(req.Attachments))
		for i, a := range req.Attachments {
			r.Attachments[i] = send.Attachment{
				Filename: a.Filename,
				MIMEType: a.MimeType,
				Data:     a.Data,
			}
		}
	}
	if err := send.Validate(r); err != nil {
		return nil, s.mapSendErr(err)
	}
	if _, err := s.st.GetAccount(ctx, req.AccountId); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Errorf(codes.InvalidArgument, "unknown account %q", req.AccountId)
		}
		return nil, s.mapErr("SendMessage", err, "account")
	}
	s.sendMu.RLock()
	snd := s.sender
	s.sendMu.RUnlock()
	if snd == nil {
		return nil, status.Error(codes.FailedPrecondition, "sending is not configured")
	}
	id, err := snd.Send(ctx, r)
	if err != nil {
		return nil, s.mapSendErr(err)
	}
	return &pb.SendMessageResponse{MessageIdHeader: id}, nil
}

// trimSentinel removes the "<sentinel>: " prefix that send errors carry.
func trimSentinel(err, sentinel error) string {
	return strings.TrimPrefix(err.Error(), sentinel.Error()+": ")
}

func (s *Server) mapSendErr(err error) error {
	switch {
	case errors.Is(err, context.Canceled):
		return status.Error(codes.Canceled, "canceled")
	case errors.Is(err, context.DeadlineExceeded):
		return status.Error(codes.DeadlineExceeded, "deadline exceeded")
	case errors.Is(err, send.ErrInvalid):
		return status.Error(codes.InvalidArgument, trimSentinel(err, send.ErrInvalid))
	case errors.Is(err, send.ErrRecipientRejected):
		return status.Error(codes.InvalidArgument, trimSentinel(err, send.ErrRecipientRejected))
	case errors.Is(err, send.ErrAuth):
		if errors.Is(err, auth.ErrReauthRequired) {
			return status.Error(codes.Unauthenticated, "reauth_required")
		}
		return status.Error(codes.Unauthenticated, trimSentinel(err, send.ErrAuth))
	case errors.Is(err, send.ErrPrecondition):
		return status.Error(codes.FailedPrecondition, trimSentinel(err, send.ErrPrecondition))
	case errors.Is(err, send.ErrUnavailable):
		return status.Error(codes.Unavailable, trimSentinel(err, send.ErrUnavailable))
	}
	return s.mapErr("SendMessage", err, "message")
}
