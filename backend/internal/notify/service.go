package notify

import (
	"context"
	"io"
	"log/slog"
	"sync"

	"mailand/backend/internal/config"
	"mailand/backend/internal/dav"
	"mailand/backend/internal/mail"
	"mailand/backend/internal/store"
)

// Service coordinates desktop notifications for both mail and calendar events.
type Service struct {
	cfg    config.ResolvedNotifications
	sender Sender
	mail   *MailNotifier
	cal    *CalendarNotifier
	log    *slog.Logger

	mu     sync.Mutex
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// New creates a notification Service. If cfg.Enabled is false, Start and event
// callbacks are no-ops.
func New(cfg config.ResolvedNotifications, st *store.Store, sender Sender, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	s := &Service{cfg: cfg, sender: sender, log: log}
	if !cfg.Enabled || sender == nil {
		return s
	}
	if cfg.Mail {
		s.mail = NewMailNotifier(st, sender, log)
	}
	if cfg.Calendar {
		s.cal = NewCalendarNotifier(st, sender, cfg.CalendarLead, cfg.AllDay, log)
	}
	return s
}

// Start initializes the mail watermark and starts the calendar reminder loop.
func (s *Service) Start(ctx context.Context) error {
	if !s.cfg.Enabled || s.sender == nil {
		return nil
	}
	if s.mail != nil {
		if err := s.mail.Init(ctx); err != nil {
			return err
		}
	}
	ctx, cancel := context.WithCancel(ctx)
	s.mu.Lock()
	s.ctx, s.cancel = ctx, cancel
	s.mu.Unlock()

	if s.cal != nil {
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.cal.Run(ctx)
		}()
	}
	return nil
}

// Stop halts background loops and closes the sender if it implements io.Closer.
func (s *Service) Stop() {
	s.mu.Lock()
	cancel := s.cancel
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	s.wg.Wait()
	if c, ok := s.sender.(io.Closer); ok {
		_ = c.Close()
	}
}

// OnMailEvent handles events from mail.Manager.
func (s *Service) OnMailEvent(ev mail.Event) {
	if s.mail == nil || ev.Type != mail.EventSyncFinished {
		return
	}
	s.mu.Lock()
	ctx := s.ctx
	s.mu.Unlock()
	if ctx == nil || ctx.Err() != nil {
		return
	}
	s.mail.OnSyncFinished(ctx, ev.AccountID, ev.Detail)
}

// OnDavEvent handles events from dav.Manager.
func (s *Service) OnDavEvent(ev dav.Event) {
	if s.cal == nil || ev.Kind != dav.CalendarsUpdated {
		return
	}
	s.cal.Wake()
}
