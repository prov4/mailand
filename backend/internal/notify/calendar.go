package notify

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"mailand/backend/internal/dav"
	"mailand/backend/internal/store"
)

const (
	defaultCalendarTick  = 30 * time.Second
	calendarGracePast    = 1 * time.Minute
	calendarPruneAge     = 48 * time.Hour
	calendarPruneEvery   = 1 * time.Hour
	calendarWindowBuffer = 2 * time.Minute
)

// CalendarNotifier periodically checks upcoming calendar occurrences and sends
// desktop notifications before meetings start.
type CalendarNotifier struct {
	st       *store.Store
	sender   Sender
	log      *slog.Logger
	leadTime time.Duration
	allDay   bool

	tickInterval time.Duration
	now          func() time.Time
	local        *time.Location

	wake      chan struct{}
	lastPrune time.Time
}

// NewCalendarNotifier creates a CalendarNotifier.
func NewCalendarNotifier(st *store.Store, sender Sender, leadTime time.Duration, allDay bool, log *slog.Logger) *CalendarNotifier {
	if log == nil {
		log = slog.Default()
	}
	return &CalendarNotifier{
		st:           st,
		sender:       sender,
		log:          log,
		leadTime:     leadTime,
		allDay:       allDay,
		tickInterval: defaultCalendarTick,
		now:          time.Now,
		local:        time.Local,
		wake:         make(chan struct{}, 1),
	}
}

// Wake triggers an immediate check (e.g. after a CalDAV sync updates events).
func (c *CalendarNotifier) Wake() {
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

// Run loops until ctx is cancelled, checking on startup, on every tick, and
// whenever Wake is called.
func (c *CalendarNotifier) Run(ctx context.Context) {
	c.Check(ctx)
	t := time.NewTicker(c.tickInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			c.Check(ctx)
		case <-c.wake:
			c.Check(ctx)
		}
	}
}

// Check scans for upcoming events within leadTime and notifies any that have
// not already been notified.
func (c *CalendarNotifier) Check(ctx context.Context) {
	now := c.now()
	from := now.Add(-calendarGracePast)
	to := now.Add(c.leadTime + calendarWindowBuffer)

	objs, err := c.st.CalendarObjectsInRange(ctx, nil, from.Unix(), to.Unix())
	if err != nil {
		c.log.Warn("calendar notify: query objects", "err", err)
		return
	}
	if len(objs) > 0 {
		calEmail, err := c.calendarAccountEmails(ctx)
		if err != nil {
			c.log.Warn("calendar notify: load calendars", "err", err)
			return
		}
		deadline := now.Add(c.leadTime)
		for _, o := range objs {
			insts, err := dav.ExpandStored(o.Data, from, to, c.local)
			if err != nil {
				continue
			}
			acctEmail := calEmail[o.CalendarID]
			for _, in := range insts {
				if !c.shouldNotify(in, acctEmail, now, deadline) {
					continue
				}
				uid := in.UID
				if uid == "" {
					uid = o.Path
				}
				startUnix := in.Start.Unix()
				already, err := c.st.WasEventNotified(ctx, o.CalendarID, uid, startUnix)
				if err != nil {
					c.log.Warn("calendar notify: check notified", "err", err)
					continue
				}
				if already {
					continue
				}
				n := formatEventNotification(in, now, c.local)
				if err := c.sender.Send(ctx, n); err != nil {
					c.log.Warn("calendar notify: send failed", "event", in.Summary, "err", err)
					continue
				}
				if err := c.st.MarkEventNotified(ctx, o.CalendarID, uid, startUnix, now.Unix()); err != nil {
					c.log.Warn("calendar notify: mark notified", "err", err)
				}
			}
		}
	}

	if c.lastPrune.IsZero() || now.Sub(c.lastPrune) >= calendarPruneEvery {
		if err := c.st.PruneNotifiedEvents(ctx, now.Add(-calendarPruneAge).Unix()); err != nil {
			c.log.Warn("calendar notify: prune", "err", err)
		} else {
			c.lastPrune = now
		}
	}
}

func (c *CalendarNotifier) shouldNotify(in dav.Instance, acctEmail string, now, deadline time.Time) bool {
	if strings.EqualFold(in.Status, "CANCELLED") {
		return false
	}
	if in.AllDay && !c.allDay {
		return false
	}
	if mine, _ := dav.MyStatus(in, acctEmail); mine == dav.Declined {
		return false
	}
	// Notify when start is within [now - grace, now + leadTime].
	if in.Start.After(deadline) || in.Start.Before(now.Add(-calendarGracePast)) {
		return false
	}
	return true
}

func (c *CalendarNotifier) calendarAccountEmails(ctx context.Context) (map[string]string, error) {
	cals, err := c.st.ListCalendars(ctx, "")
	if err != nil {
		return nil, err
	}
	accs, err := c.st.ListAccounts(ctx)
	if err != nil {
		return nil, err
	}
	byAcct := make(map[string]string, len(accs))
	for _, a := range accs {
		byAcct[a.ID] = strings.ToLower(a.Email)
	}
	out := make(map[string]string, len(cals))
	for _, cal := range cals {
		out[cal.ID] = byAcct[cal.AccountID]
	}
	return out, nil
}

func formatEventNotification(in dav.Instance, now time.Time, local *time.Location) Notification {
	summary := strings.TrimSpace(in.Summary)
	if summary == "" {
		summary = "(No title)"
	}
	startLocal := in.Start.In(local)
	timeStr := startLocal.Format("15:04")

	var when string
	if in.AllDay {
		when = "All day today"
	} else {
		until := in.Start.Sub(now)
		mins := int((until + 30*time.Second) / time.Minute)
		switch {
		case mins <= 0:
			when = fmt.Sprintf("Starting now (%s)", timeStr)
		case mins == 1:
			when = fmt.Sprintf("In 1 min (%s)", timeStr)
		default:
			when = fmt.Sprintf("In %d min (%s)", mins, timeStr)
		}
	}

	body := when
	if loc := strings.TrimSpace(in.Location); loc != "" {
		body += "\n" + loc
	}
	return Notification{
		Summary:  summary,
		Body:     body,
		Category: "x-mailand.calendar",
		Urgency:  UrgencyNormal,
		Timeout:  -1,
	}
}
