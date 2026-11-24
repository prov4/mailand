package mail

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"golang.org/x/oauth2"

	"mailand/backend/internal/auth"
	"mailand/backend/internal/config"
	"mailand/backend/internal/store"
)

// EventType classifies a daemon event.
type EventType int

const (
	EventSyncStarted EventType = iota
	EventSyncFinished
	// EventNewMessages is emitted per fetched batch; FolderID is the source
	// folder and Detail the number of new messages.
	EventNewMessages
	// EventFlagsUpdated means flags changed server-side or via SetFlags, or
	// (Detail "expunged") messages vanished, or (Detail "labels") Gmail label
	// membership changed. UIs should refresh the affected lists.
	EventFlagsUpdated
	// EventError carries Detail "reauth_required" when the account needs a new
	// login (the account then stops retrying until TriggerSync); otherwise a
	// short human-readable message that never contains secrets.
	EventError
)

// Event is delivered to the notify callback given to NewManager.
type Event struct {
	Type                        EventType
	AccountID, FolderID, Detail string
}

// Folder IDs are "<accountID>:<IMAP path>" (Gmail label folders use their label
// path), so they are stable across restarts and re-syncs.

// Manager runs one sync worker per configured account.
type Manager struct {
	cfg     *config.Config
	st      *store.Store
	blobDir string
	log     *slog.Logger

	notify func(Event)
	events chan Event // decouples workers from a slow notify callback

	mu      sync.RWMutex
	closed  bool
	workers map[string]*worker

	cancel   context.CancelFunc
	wg       sync.WaitGroup // workers
	dispWG   sync.WaitGroup // event dispatcher
	stopOnce sync.Once

	// Test hooks; never set in production, never exposed through config.
	plain bool // dial without TLS
	// labelInterval is the period of Gmail label refreshes during a pass.
	labelInterval time.Duration
	onLabelConn   func(*conn)           // called with each extra label-refresh connection
	batchHook     func(context.Context) // called after each fetched source batch
	password      func(accountID string) (string, error)
	tokenSource   func(ctx context.Context, a *config.Account) (oauth2.TokenSource, error)
}

// NewManager creates a Manager. notify may be nil. Events are queued (bounded,
// oldest data kept, overflow dropped) and delivered from a separate goroutine,
// so a slow notify never blocks a sync worker; it should still return quickly.
func NewManager(cfg *config.Config, st *store.Store, blobDir string, notify func(Event), log *slog.Logger) *Manager {
	if log == nil {
		log = slog.Default()
	}
	m := &Manager{
		cfg: cfg, st: st, blobDir: blobDir, log: log, notify: notify,
		events:   make(chan Event, 256),
		workers:  map[string]*worker{},
		password: auth.GetPassword, labelInterval: defaultLabelInterval,
	}
	m.tokenSource = func(ctx context.Context, a *config.Account) (oauth2.TokenSource, error) {
		return auth.TokenSource(ctx, m.cfg, a)
	}
	return m
}

// Start upserts the configured accounts into the store and launches one worker
// per account. It returns quickly; ctx bounds the workers' lifetime.
func (m *Manager) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cancel != nil || m.closed {
		return errors.New("mail: manager already started")
	}
	for i := range m.cfg.Accounts {
		a := &m.cfg.Accounts[i]
		if err := m.st.UpsertAccount(ctx, store.Account{
			ID: a.ID, Name: a.Name, Email: a.Email, IMAPHost: a.IMAPHost, IMAPPort: a.IMAPPort,
			SMTPHost: a.SMTPHost, SMTPPort: a.SMTPPort, AuthType: a.Auth,
			CalDAVURL: a.CalDAVURL, CardDAVURL: a.CardDAVURL,
		}); err != nil {
			return fmt.Errorf("upsert account %q: %w", a.ID, err)
		}
	}
	ctx, m.cancel = context.WithCancel(ctx)
	m.dispWG.Add(1)
	go m.dispatch()
	for i := range m.cfg.Accounts {
		w := newWorker(m, &m.cfg.Accounts[i])
		m.workers[w.acct.ID] = w
		m.wg.Add(1)
		go func() { defer m.wg.Done(); w.run(ctx) }()
	}
	return nil
}

// Stop cancels all workers, waits for them and closes their connections.
func (m *Manager) Stop() {
	m.stopOnce.Do(func() {
		m.mu.Lock()
		cancel := m.cancel
		m.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		m.wg.Wait()
		m.mu.Lock()
		m.closed = true
		close(m.events)
		m.mu.Unlock()
		m.dispWG.Wait()
	})
}

// TriggerSync requests an immediate full sync pass; it does not block. It also
// re-arms an account that stopped after reauth_required.
func (m *Manager) TriggerSync(accountID string) error {
	m.mu.RLock()
	w := m.workers[accountID]
	m.mu.RUnlock()
	if w == nil {
		return fmt.Errorf("unknown account %q", accountID)
	}
	select {
	case w.trigger <- struct{}{}:
	default: // one already pending
	}
	return nil
}

func (m *Manager) emit(e Event) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return
	}
	select {
	case m.events <- e:
	default:
		m.log.Warn("event queue full, dropping event", "type", int(e.Type), "account", e.AccountID)
	}
}

func (m *Manager) dispatch() {
	defer m.dispWG.Done()
	for e := range m.events {
		if m.notify != nil {
			m.notify(e)
		}
	}
}
