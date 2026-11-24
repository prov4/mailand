package mail

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"golang.org/x/oauth2"

	"mailand/backend/internal/auth"
	"mailand/backend/internal/config"
)

const (
	minBackoff  = time.Second
	maxBackoff  = 5 * time.Minute
	dialTimeout = 10 * time.Second
	capGmailExt = imap.Cap("X-GM-EXT-1")
)

type worker struct {
	m    *Manager
	acct *config.Account
	log  *slog.Logger

	trigger chan struct{} // TriggerSync
	wake    chan struct{} // IDLE saw an INBOX change

	tsMu sync.Mutex
	ts   oauth2.TokenSource

	labelMu sync.Mutex // serialises label refreshes (one at a time per account)
	labels  map[string]*labelCache
	stored  atomic.Int64 // messages stored by this worker, ever

	healthy bool // set by session after a successful pass; run goroutine only
	backlog bool // set by pass: new mail was left for a later pass; run goroutine only
}

func newWorker(m *Manager, a *config.Account) *worker {
	return &worker{
		m: m, acct: a,
		log:     m.log.With("account", a.ID),
		trigger: make(chan struct{}, 1),
		wake:    make(chan struct{}, 1),
	}
}

func (w *worker) emit(t EventType, folderID, detail string) {
	w.m.emit(Event{Type: t, AccountID: w.acct.ID, FolderID: folderID, Detail: detail})
}

// run is the account's supervisor: it (re)starts sessions with exponential
// backoff and parks the account on reauth_required until TriggerSync.
func (w *worker) run(ctx context.Context) {
	backoff := minBackoff
	for ctx.Err() == nil {
		w.healthy = false
		err := w.session(ctx)
		if ctx.Err() != nil {
			return
		}
		if w.healthy {
			backoff = minBackoff
		}
		if errors.Is(err, auth.ErrReauthRequired) {
			w.log.Warn("reauthentication required")
			w.emit(EventError, "", "reauth_required")
			select {
			case <-w.trigger:
				backoff = minBackoff
				continue
			case <-ctx.Done():
				return
			}
		}
		w.log.Warn("session ended", "err", err, "retry_in", backoff)
		w.emit(EventError, "", shortErr(err))
		t := time.NewTimer(backoff)
		select {
		case <-t.C:
		case <-w.trigger:
			backoff = minBackoff
		case <-ctx.Done():
			t.Stop()
			return
		}
		t.Stop()
		backoff = min(backoff*2, maxBackoff)
	}
}

// session connects, runs passes until the connection fails or ctx ends.
func (w *worker) session(ctx context.Context) error {
	w.tsMu.Lock()
	w.ts = nil
	w.tsMu.Unlock()

	c, err := w.connect(ctx, nil)
	if err != nil {
		return err
	}
	defer c.close()

	sctx, cancel := context.WithCancel(ctx)
	var idleWG sync.WaitGroup
	defer idleWG.Wait()
	defer cancel()
	if c.caps.Has(imap.CapIdle) {
		idleWG.Add(1)
		go func() { defer idleWG.Done(); w.idleLoop(sctx) }()
	}

	kind := passFull
	backlog := false // a full pass left mail undownloaded
	for {
		if kind == passFull {
			select { // a full pass covers any pending IDLE wake
			case <-w.wake:
			default:
			}
		}
		if err := w.pass(sctx, c, kind); err != nil {
			return err
		}
		w.healthy = true
		if kind == passFull {
			backlog = w.backlog
		} else {
			backlog = backlog || w.backlog
		}
		if backlog { // keep going without waiting; serve new mail first
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-w.wake:
				kind = passInbox
			default:
				kind = passFull
			}
			continue
		}

		t := time.NewTimer(w.acct.PollInterval)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-w.trigger:
			kind = passFull
		case <-w.wake:
			kind = passInbox
		case <-t.C:
			kind = passFull
		}
		t.Stop()
	}
}

// conn is an authenticated IMAP connection that is closed when its ctx ends.
type conn struct {
	*imapclient.Client
	caps imap.CapSet
	stop func() bool
}

func (c *conn) close() {
	c.stop()
	_ = c.Client.Close()
}

func (c *conn) gmail(provider string) bool {
	return provider == config.ProviderGmail || c.caps.Has(capGmailExt)
}

// connect dials, authenticates and reads capabilities. Closing ctx closes the
// connection, which unblocks any command in flight.
func (w *worker) connect(ctx context.Context, opts *imapclient.Options) (*conn, error) {
	if opts == nil {
		opts = &imapclient.Options{}
	}
	opts.Dialer = &net.Dialer{Timeout: dialTimeout}
	addr := net.JoinHostPort(w.acct.IMAPHost, strconv.Itoa(w.acct.IMAPPort))
	var (
		c   *imapclient.Client
		err error
	)
	switch {
	case w.m.plain:
		c, err = imapclient.DialInsecure(addr, opts)
	case w.acct.IMAPTLS == config.TLSStartTLS:
		c, err = imapclient.DialStartTLS(addr, opts)
	default:
		c, err = imapclient.DialTLS(addr, opts)
	}
	if err != nil {
		return nil, fmt.Errorf("connect %s: %w", addr, err)
	}
	cn := &conn{Client: c}
	cn.stop = context.AfterFunc(ctx, func() { _ = c.Close() })
	if err := w.authenticate(ctx, c); err != nil {
		cn.close()
		return nil, err
	}
	if cn.caps, err = c.Capability().Wait(); err != nil {
		cn.close()
		return nil, fmt.Errorf("capability: %w", err)
	}
	return cn, nil
}

func (w *worker) tokenSource(ctx context.Context) (oauth2.TokenSource, error) {
	w.tsMu.Lock()
	defer w.tsMu.Unlock()
	if w.ts == nil {
		// ctx must outlive single calls: it is used for later token refreshes.
		ts, err := w.m.tokenSource(ctx, w.acct)
		if err != nil {
			return nil, err
		}
		w.ts = ts
	}
	return w.ts, nil
}

func (w *worker) authenticate(ctx context.Context, c *imapclient.Client) error {
	if w.acct.Auth == config.AuthOAuth2Google {
		ts, err := w.tokenSource(ctx)
		if err != nil {
			return fmt.Errorf("token: %w", err)
		}
		sc, err := auth.SASLClient(ctx, ts, w.acct.Email)
		if err != nil {
			return fmt.Errorf("token: %w", err)
		}
		if err := c.Authenticate(sc); err != nil {
			return fmt.Errorf("authenticate: %w", err)
		}
		return nil
	}
	pw, err := w.m.password(w.acct.ID)
	if err != nil {
		return fmt.Errorf("no stored password: %w", err)
	}
	if err := c.Authenticate(auth.PlainClient(w.acct.Email, pw)); err != nil {
		return fmt.Errorf("authenticate: %w", err)
	}
	return nil
}

// shortErr renders an error for EventError. Errors come from IMAP/network
// layers and never contain credentials.
func shortErr(err error) string {
	s := err.Error()
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}
