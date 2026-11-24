package send

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-sasl"
	"github.com/emersion/go-smtp"
	"golang.org/x/oauth2"

	"mailand/backend/internal/auth"
	"mailand/backend/internal/config"
	"mailand/backend/internal/store"
)

const (
	dialTimeout = 15 * time.Second
	// SendTimeout bounds one whole SMTP transaction.
	SendTimeout = 60 * time.Second
	sentTimeout = 30 * time.Second
)

// Sender delivers mail over SMTP, stores the Sent copy and triggers a sync.
//
// The IMAP APPEND for the Sent copy uses a small dial helper of this package
// (appendSent) rather than mail.Manager, so this package does not depend on
// the sync engine's unexported connection code.
type Sender struct {
	cfg  *config.Config
	st   *store.Store
	log  *slog.Logger
	base context.Context // long-lived: OAuth token sources keep using it for refreshes

	triggerSync func(accountID string) error // may be nil

	tsMu sync.Mutex
	ts   map[string]oauth2.TokenSource

	// Test hooks, never exposed through config.
	plain       bool // dial SMTP and IMAP without TLS
	password    func(accountID string) (string, error)
	tokenSource func(ctx context.Context, a *config.Account) (oauth2.TokenSource, error)
}

// NewSender creates a Sender. base must outlive individual requests (daemon
// lifetime). triggerSync may be nil.
func NewSender(base context.Context, cfg *config.Config, st *store.Store, triggerSync func(string) error, log *slog.Logger) *Sender {
	if log == nil {
		log = slog.Default()
	}
	s := &Sender{cfg: cfg, st: st, log: log, base: base, triggerSync: triggerSync,
		ts: map[string]oauth2.TokenSource{}, password: auth.GetPassword}
	s.tokenSource = func(ctx context.Context, a *config.Account) (oauth2.TokenSource, error) {
		return auth.TokenSource(ctx, s.cfg, a)
	}
	return s
}

// Send composes and delivers req and returns the Message-ID (no angle brackets).
func (s *Sender) Send(ctx context.Context, req Request) (string, error) {
	if err := Validate(req); err != nil {
		return "", err
	}
	acct, ok := s.cfg.Account(req.AccountID)
	if !ok {
		return "", invalid("unknown account %q", req.AccountID)
	}
	sa, err := s.st.GetAccount(ctx, acct.ID)
	if errors.Is(err, store.ErrNotFound) {
		return "", invalid("unknown account %q", req.AccountID)
	} else if err != nil {
		return "", err
	}
	b, err := Build(ctx, s.st, sa, req, time.Now())
	if err != nil {
		return "", err
	}

	if err := s.deliver(ctx, acct, b); err != nil {
		return "", err
	}

	if acct.Provider != config.ProviderGmail {
		if err := s.appendSent(ctx, acct, b.Raw); err != nil {
			// The mail is out; failing the RPC would invite a duplicate send.
			s.log.Warn("could not save copy to Sent", "account", acct.ID, "err", err)
		}
	}
	if s.triggerSync != nil {
		if err := s.triggerSync(acct.ID); err != nil {
			s.log.Warn("trigger sync after send", "account", acct.ID, "err", err)
		}
	}
	return b.MessageID, nil
}

func (s *Sender) saslClient(ctx context.Context, a *config.Account) (sasl.Client, error) {
	if a.Auth == config.AuthOAuth2Google {
		ts, err := s.getTokenSource(a)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrAuth, err)
		}
		c, err := auth.SASLClient(ctx, ts, a.Email)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if errors.Is(err, auth.ErrReauthRequired) {
				return nil, fmt.Errorf("%w: %w", ErrAuth, err)
			}
			return nil, fmt.Errorf("%w: token: %v", ErrUnavailable, err) // refresh endpoint trouble
		}
		return c, nil
	}
	pw, err := s.password(a.ID)
	if err != nil {
		return nil, fmt.Errorf("%w: no stored password", ErrAuth)
	}
	return auth.PlainClient(a.Email, pw), nil
}

func (s *Sender) getTokenSource(a *config.Account) (oauth2.TokenSource, error) {
	s.tsMu.Lock()
	defer s.tsMu.Unlock()
	if ts, ok := s.ts[a.ID]; ok {
		return ts, nil
	}
	ts, err := s.tokenSource(s.base, a)
	if err != nil {
		return nil, err
	}
	s.ts[a.ID] = ts
	return ts, nil
}

// dialConn opens a TCP connection, with implicit TLS when wantTLS.
func (s *Sender) dialConn(ctx context.Context, host string, port int, wantTLS bool) (net.Conn, error) {
	d := &net.Dialer{Timeout: dialTimeout}
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("%w: connect %s: %v", ErrUnavailable, addr, err)
	}
	if !wantTLS {
		return conn, nil
	}
	tc := tls.Client(conn, &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
	if err := tc.HandshakeContext(ctx); err != nil {
		conn.Close()
		return nil, fmt.Errorf("%w: TLS handshake with %s: %v", ErrUnavailable, addr, err)
	}
	return tc, nil
}

func (s *Sender) deliver(ctx context.Context, a *config.Account, b *Built) error {
	ctx, cancel := context.WithTimeout(ctx, SendTimeout)
	defer cancel()

	implicit := !s.plain && a.SMTPTLS != config.TLSStartTLS
	conn, err := s.dialConn(ctx, a.SMTPHost, a.SMTPPort, implicit)
	if err != nil {
		return err
	}
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	_ = conn.SetDeadline(time.Now().Add(SendTimeout))

	var c *smtp.Client
	if !s.plain && a.SMTPTLS == config.TLSStartTLS {
		c, err = smtp.NewClientStartTLS(conn, &tls.Config{ServerName: a.SMTPHost, MinVersion: tls.VersionTLS12})
	} else {
		c = smtp.NewClient(conn)
	}
	if err != nil {
		conn.Close()
		return s.smtpErr(ctx, "connect", err)
	}
	defer c.Close()

	sc, err := s.saslClient(ctx, a)
	if err != nil {
		return err
	}
	if err := c.Auth(sc); err != nil {
		return s.smtpErr(ctx, "auth", err)
	}
	if err := c.Mail(a.Email, nil); err != nil {
		return s.smtpErr(ctx, "sender", err)
	}
	for _, r := range b.Recipients {
		if err := c.Rcpt(r, nil); err != nil {
			return s.smtpErr(ctx, "rcpt", err)
		}
	}
	w, err := c.Data()
	if err != nil {
		return s.smtpErr(ctx, "data", err)
	}
	if _, err := w.Write(b.Raw); err != nil {
		return s.smtpErr(ctx, "data", err)
	}
	if err := w.Close(); err != nil {
		return s.smtpErr(ctx, "data", err)
	}
	_ = c.Quit()
	return nil
}

// smtpErr classifies a go-smtp error. stage is one of connect, auth, sender,
// rcpt, data.
func (s *Sender) smtpErr(ctx context.Context, stage string, err error) error {
	if ctx.Err() != nil {
		return fmt.Errorf("%w: %s: %v", ErrUnavailable, stage, ctx.Err())
	}
	var se *smtp.SMTPError
	if errors.As(err, &se) {
		text := fmt.Sprintf("%d %s", se.Code, se.Message)
		switch {
		case stage == "auth" || se.Code == 530 || se.Code == 534 || se.Code == 535:
			return fmt.Errorf("%w: %s", ErrAuth, text)
		case stage == "rcpt" && se.Code >= 500:
			return fmt.Errorf("%w: %s", ErrRecipientRejected, text)
		}
		return fmt.Errorf("%w: SMTP %s: %s", ErrUnavailable, stage, text)
	}
	return fmt.Errorf("%w: SMTP %s: %v", ErrUnavailable, stage, err)
}

// appendSent saves raw into the account's \Sent folder with \Seen via IMAP
// APPEND. Without a known \Sent folder it does nothing.
func (s *Sender) appendSent(ctx context.Context, a *config.Account, raw []byte) error {
	folders, err := s.st.ListFolders(ctx, a.ID)
	if err != nil {
		return err
	}
	path := ""
	for _, f := range folders {
		if f.SpecialUse == `\Sent` && f.Kind == "folder" {
			path = f.Path
			break
		}
	}
	if path == "" {
		return errors.New("no \\Sent folder known for account")
	}

	ctx, cancel := context.WithTimeout(ctx, sentTimeout)
	defer cancel()
	conn, err := s.dialConn(ctx, a.IMAPHost, a.IMAPPort, !s.plain && a.IMAPTLS != config.TLSStartTLS)
	if err != nil {
		return err
	}
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	var c *imapclient.Client
	if !s.plain && a.IMAPTLS == config.TLSStartTLS {
		c, err = imapclient.NewStartTLS(conn, &imapclient.Options{TLSConfig: &tls.Config{ServerName: a.IMAPHost, MinVersion: tls.VersionTLS12}})
	} else {
		c = imapclient.New(conn, nil)
	}
	if err != nil {
		conn.Close()
		return err
	}
	defer c.Close()
	sc, err := s.saslClient(ctx, a)
	if err != nil {
		return err
	}
	if err := c.Authenticate(sc); err != nil {
		return fmt.Errorf("imap authenticate: %w", err)
	}
	cmd := c.Append(path, int64(len(raw)), &imap.AppendOptions{Flags: []imap.Flag{imap.FlagSeen}, Time: time.Now()})
	if _, err := cmd.Write(raw); err != nil {
		return err
	}
	if err := cmd.Close(); err != nil {
		return err
	}
	if _, err := cmd.Wait(); err != nil {
		return fmt.Errorf("imap append: %w", err)
	}
	_ = c.Logout().Wait()
	return nil
}
