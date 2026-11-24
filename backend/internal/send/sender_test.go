package send

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
	"github.com/emersion/go-sasl"
	"github.com/emersion/go-smtp"
	"golang.org/x/oauth2"

	"mailand/backend/internal/auth"
	"mailand/backend/internal/config"
	"mailand/backend/internal/store"
)

type delivery struct {
	from, mech, user, secret string
	rcpts                    []string
	data                     []byte
}

type smtpBE struct {
	mu         sync.Mutex
	got        []delivery
	rejectRcpt string
	failAuth   bool
}

func (b *smtpBE) NewSession(*smtp.Conn) (smtp.Session, error) { return &smtpSess{be: b}, nil }

type smtpSess struct {
	be *smtpBE
	d  delivery
}

func (s *smtpSess) AuthMechanisms() []string { return []string{sasl.Plain, "XOAUTH2"} }
func (s *smtpSess) Auth(mech string) (sasl.Server, error) {
	s.d.mech = mech
	if mech == "XOAUTH2" {
		return &xoauthServer{s}, nil
	}
	return sasl.NewPlainServer(func(_, user, pw string) error {
		s.d.user, s.d.secret = user, pw
		if s.be.failAuth {
			return errors.New("bad credentials")
		}
		return nil
	}), nil
}
func (s *smtpSess) Reset()        {}
func (s *smtpSess) Logout() error { return nil }
func (s *smtpSess) Mail(from string, _ *smtp.MailOptions) error {
	s.d.from = from
	return nil
}
func (s *smtpSess) Rcpt(to string, _ *smtp.RcptOptions) error {
	if to == s.be.rejectRcpt {
		return &smtp.SMTPError{Code: 550, EnhancedCode: smtp.EnhancedCode{5, 1, 1}, Message: "no such user here"}
	}
	s.d.rcpts = append(s.d.rcpts, to)
	return nil
}
func (s *smtpSess) Data(r io.Reader) error {
	b, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	s.d.data = b
	s.be.mu.Lock()
	s.be.got = append(s.be.got, s.d)
	s.be.mu.Unlock()
	return nil
}

type xoauthServer struct{ s *smtpSess }

func (x *xoauthServer) Next(resp []byte) ([]byte, bool, error) {
	parts := strings.Split(string(resp), "\x01")
	for _, p := range parts {
		if v, ok := strings.CutPrefix(p, "user="); ok {
			x.s.d.user = v
		}
		if v, ok := strings.CutPrefix(p, "auth=Bearer "); ok {
			x.s.d.secret = v
		}
	}
	return nil, true, nil
}

func startSMTP(t *testing.T) (*smtpBE, string, int) {
	t.Helper()
	be := &smtpBE{}
	srv := smtp.NewServer(be)
	srv.Domain = "localhost"
	srv.AllowInsecureAuth = true
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	h, p, _ := net.SplitHostPort(ln.Addr().String())
	var port int
	fmt.Sscan(p, &port)
	return be, h, port
}

func startIMAP(t *testing.T, mailboxes ...string) (*imapmemserver.User, string, int) {
	t.Helper()
	mem := imapmemserver.New()
	user := imapmemserver.NewUser("me@example.com", "pw")
	for _, m := range mailboxes {
		if err := user.Create(m, nil); err != nil {
			t.Fatal(err)
		}
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
	h, p, _ := net.SplitHostPort(ln.Addr().String())
	var port int
	fmt.Sscan(p, &port)
	return user, h, port
}

type senderEnv struct {
	*fixture
	s    *Sender
	be   *smtpBE
	user *imapmemserver.User
	acct *config.Account
	sync []string
}

func newSenderEnv(t *testing.T, provider, authType string) *senderEnv {
	t.Helper()
	fx := newFixture(t)
	be, sh, sp := startSMTP(t)
	user, ih, ip := startIMAP(t, "INBOX", "Sent")
	cfg := &config.Config{Accounts: []config.Account{{
		ID: "acc", Name: "Me Myself", Email: "me@example.com", Provider: provider, Auth: authType,
		IMAPHost: ih, IMAPPort: ip, SMTPHost: sh, SMTPPort: sp,
	}}}
	if _, err := fx.st.UpsertFolder(context.Background(), store.Folder{AccountID: "acc", Name: "Sent", Path: "Sent",
		Kind: "folder", SpecialUse: `\Sent`, IsSyncSource: true}); err != nil {
		t.Fatal(err)
	}
	e := &senderEnv{fixture: fx, be: be, user: user, acct: &cfg.Accounts[0]}
	e.s = NewSender(context.Background(), cfg, fx.st, func(id string) error { e.sync = append(e.sync, id); return nil },
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	e.s.plain = true
	e.s.password = func(string) (string, error) { return "pw", nil }
	return e
}

func (e *senderEnv) sentCount(t *testing.T) (int, []imap.Flag) {
	t.Helper()
	c, err := imapclient.DialInsecure(e.acct.IMAPHost+fmt.Sprintf(":%d", e.acct.IMAPPort), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Login("me@example.com", "pw").Wait(); err != nil {
		t.Fatal(err)
	}
	sel, err := c.Select("Sent", nil).Wait()
	if err != nil {
		t.Fatal(err)
	}
	var flags []imap.Flag
	if sel.NumMessages > 0 {
		bufs, err := c.Fetch(imap.SeqSetNum(1), &imap.FetchOptions{Flags: true}).Collect()
		if err != nil {
			t.Fatal(err)
		}
		flags = bufs[0].Flags
	}
	return int(sel.NumMessages), flags
}

func TestSendPlainAuthEnvelopeAndSentCopy(t *testing.T) {
	e := newSenderEnv(t, config.ProviderIMAP, config.AuthPassword)
	id, err := e.s.Send(context.Background(), Request{AccountID: "acc", To: []string{"Ana <ana@example.com>"},
		Cc: []string{"cc@example.com"}, Bcc: []string{"hidden@example.com"}, Subject: "Ćao", TextBody: "tijelo žaba",
		ReplyToID: e.withBlob})
	if err != nil {
		t.Fatal(err)
	}
	if len(e.be.got) != 1 {
		t.Fatalf("deliveries: %d", len(e.be.got))
	}
	d := e.be.got[0]
	if d.mech != "PLAIN" || d.user != "me@example.com" || d.secret != "pw" {
		t.Errorf("auth %q %q %q", d.mech, d.user, d.secret)
	}
	if d.from != "me@example.com" || strings.Join(d.rcpts, ",") != "ana@example.com,cc@example.com,hidden@example.com" {
		t.Errorf("envelope %q %v", d.from, d.rcpts)
	}
	_, p := parseRaw(t, d.data)
	if p.MessageID != id || p.Subject != "Ćao" || p.TextBody != "tijelo žaba" || p.InReplyTo != "orig@x" {
		t.Errorf("message %+v (id %q)", p, id)
	}
	if bytes.Contains(d.data, []byte("hidden@example.com")) {
		t.Error("Bcc in message")
	}
	n, flags := e.sentCount(t)
	if n != 1 || len(flags) != 1 || flags[0] != imap.FlagSeen {
		t.Errorf("sent copy: n=%d flags=%v", n, flags)
	}
	if len(e.sync) != 1 || e.sync[0] != "acc" {
		t.Errorf("sync triggers %v", e.sync)
	}
}

func TestSendGmailDoesNotAppend(t *testing.T) {
	e := newSenderEnv(t, config.ProviderGmail, config.AuthOAuth2Google)
	e.s.tokenSource = func(context.Context, *config.Account) (oauth2.TokenSource, error) {
		return oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "tok123"}), nil
	}
	if _, err := e.s.Send(context.Background(), Request{AccountID: "acc", To: []string{"a@example.com"}, TextBody: "x"}); err != nil {
		t.Fatal(err)
	}
	d := e.be.got[0]
	if d.mech != "XOAUTH2" || d.user != "me@example.com" || d.secret != "tok123" {
		t.Errorf("xoauth2 %q %q %q", d.mech, d.user, d.secret)
	}
	if n, _ := e.sentCount(t); n != 0 {
		t.Errorf("gmail send appended %d copies", n)
	}
	if len(e.sync) != 1 {
		t.Errorf("sync %v", e.sync)
	}
}

type errSource struct{ err error }

func (e errSource) Token() (*oauth2.Token, error) { return nil, e.err }

func TestSendErrors(t *testing.T) {
	ctx := context.Background()
	req := Request{AccountID: "acc", To: []string{"a@example.com"}, TextBody: "x"}

	e := newSenderEnv(t, config.ProviderIMAP, config.AuthPassword)
	e.be.rejectRcpt = "a@example.com"
	if _, err := e.s.Send(ctx, req); !errors.Is(err, ErrRecipientRejected) || !strings.Contains(err.Error(), "no such user here") {
		t.Errorf("rejected rcpt: %v", err)
	}

	e = newSenderEnv(t, config.ProviderIMAP, config.AuthPassword)
	e.be.failAuth = true
	if _, err := e.s.Send(ctx, req); !errors.Is(err, ErrAuth) || errors.Is(err, auth.ErrReauthRequired) {
		t.Errorf("bad password: %v", err)
	}

	e = newSenderEnv(t, config.ProviderGmail, config.AuthOAuth2Google)
	e.s.tokenSource = func(context.Context, *config.Account) (oauth2.TokenSource, error) {
		return errSource{fmt.Errorf("revoked: %w", auth.ErrReauthRequired)}, nil
	}
	if _, err := e.s.Send(ctx, req); !errors.Is(err, ErrAuth) || !errors.Is(err, auth.ErrReauthRequired) {
		t.Errorf("reauth: %v", err)
	}
	if len(e.be.got) != 0 {
		t.Error("delivered despite auth failure")
	}

	e = newSenderEnv(t, config.ProviderIMAP, config.AuthPassword)
	e.s.cfg.Accounts[0].SMTPPort = 1 // nothing listens
	if _, err := e.s.Send(ctx, req); !errors.Is(err, ErrUnavailable) {
		t.Errorf("network: %v", err)
	}

	if _, err := e.s.Send(ctx, Request{AccountID: "nope", To: []string{"a@example.com"}}); !errors.Is(err, ErrInvalid) {
		t.Errorf("unknown account: %v", err)
	}
}

func TestSentCopyFailureDoesNotFailSend(t *testing.T) {
	e := newSenderEnv(t, config.ProviderIMAP, config.AuthPassword)
	e.s.cfg.Accounts[0].IMAPPort = 1
	if _, err := e.s.Send(context.Background(), Request{AccountID: "acc", To: []string{"a@example.com"}}); err != nil {
		t.Fatal(err)
	}
	if len(e.be.got) != 1 || len(e.sync) != 1 {
		t.Errorf("got %d deliveries, sync %v", len(e.be.got), e.sync)
	}
}

func TestOfflineSender(t *testing.T) {
	fx := newFixture(t)
	bg := context.Background()
	var notified []string
	o := NewOfflineSender(fx.st, t.TempDir(), func(acc, folder string) { notified = append(notified, acc+"|"+folder) })
	id, err := o.Send(bg, Request{AccountID: "acc", To: []string{"Ana <ana@example.com>"}, Cc: []string{"c@example.com"},
		Subject: "Hej", TextBody: "tekst", ReplyToID: fx.withBlob})
	if err != nil {
		t.Fatal(err)
	}
	fs, _ := fx.st.ListFolders(bg, "acc")
	var sent store.Folder
	for _, f := range fs {
		if f.SpecialUse == `\Sent` {
			sent = f
		}
	}
	if sent.ID == "" {
		t.Fatal("no Sent folder created")
	}
	hs, total, err := fx.st.ListMessages(bg, sent.ID, 10, 0)
	if err != nil || total != 1 {
		t.Fatalf("sent list %v %d", err, total)
	}
	m, err := fx.st.GetMessage(bg, hs[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if !m.IsRead || m.Subject != "Hej" || m.MessageIDHeader != id || m.TextBody != "tekst" || m.FromAddress != "me@example.com" {
		t.Errorf("stored %+v", m)
	}
	if !strings.Contains(m.ToJSON, "ana@example.com") || !strings.Contains(m.CcJSON, "c@example.com") {
		t.Errorf("recipients %q %q", m.ToJSON, m.CcJSON)
	}
	if m.RawBlobPath == "" {
		t.Error("no blob")
	}
	if len(notified) != 1 || notified[0] != "acc|"+sent.ID {
		t.Errorf("notified %v", notified)
	}
	// A second send gets a new UID.
	if _, err := o.Send(bg, Request{AccountID: "acc", To: []string{"a@example.com"}}); err != nil {
		t.Fatal(err)
	}
	if _, total, _ := fx.st.ListMessages(bg, sent.ID, 10, 0); total != 2 {
		t.Errorf("total %d", total)
	}
	if _, err := o.Send(bg, Request{AccountID: "zzz", To: []string{"a@example.com"}}); !errors.Is(err, ErrInvalid) {
		t.Errorf("unknown account: %v", err)
	}
}
