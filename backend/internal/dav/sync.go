package dav

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/emersion/go-webdav"
	"github.com/emersion/go-webdav/caldav"
	"github.com/emersion/go-webdav/carddav"
	"golang.org/x/oauth2"

	"mailand/backend/internal/auth"
	"mailand/backend/internal/config"
	"mailand/backend/internal/store"
)

// EventKind classifies Manager events.
type EventKind int

const (
	// CalendarsUpdated: calendars or events of AccountID changed.
	CalendarsUpdated EventKind = iota
	// ContactsUpdated: address books or contacts of AccountID changed.
	ContactsUpdated
	// SyncError: Detail is a short message without secrets.
	SyncError
)

// Event is delivered to the notify callback.
type Event struct {
	Kind      EventKind
	AccountID string
	Detail    string
}

const (
	// DefaultInterval is how often calendars and contacts are re-synced.
	DefaultInterval = 15 * time.Minute
	multiGetChunk   = 100
	httpTimeout     = 90 * time.Second
)

// Manager runs one CalDAV/CardDAV sync loop per account that has endpoints.
type Manager struct {
	cfg    *config.Config
	st     *store.Store
	log    *slog.Logger
	notify func(Event)
	local  *time.Location

	mu           sync.Mutex
	workers      map[string]*worker
	writeClients map[string]webdav.HTTPClient
	ctx          context.Context // set by Start; outlives single requests
	cancel       context.CancelFunc
	wg           sync.WaitGroup

	// Interval between syncs; settable before Start (tests).
	Interval time.Duration
	// httpClient builds the authenticated client; replaced in tests.
	httpClient func(ctx context.Context, a *config.Account) (webdav.HTTPClient, error)
}

// NewManager creates a Manager; notify may be nil and must not block.
func NewManager(cfg *config.Config, st *store.Store, notify func(Event), log *slog.Logger) *Manager {
	if log == nil {
		log = slog.Default()
	}
	m := &Manager{cfg: cfg, st: st, log: log, notify: notify, local: time.Local,
		workers: map[string]*worker{}, Interval: DefaultInterval}
	m.httpClient = m.defaultHTTPClient
	return m
}

func (m *Manager) defaultHTTPClient(ctx context.Context, a *config.Account) (webdav.HTTPClient, error) {
	base := &http.Client{Timeout: httpTimeout}
	if a.Auth == config.AuthOAuth2Google {
		ts, err := auth.TokenSource(ctx, m.cfg, a)
		if err != nil {
			return nil, err
		}
		c := oauth2.NewClient(context.WithValue(ctx, oauth2.HTTPClient, base), ts)
		c.Timeout = httpTimeout
		return c, nil
	}
	pw, err := auth.GetPassword(a.ID)
	if err != nil {
		return nil, fmt.Errorf("password: %w", err)
	}
	return webdav.HTTPClientWithBasicAuth(base, a.Email, pw), nil
}

// Start launches the workers. Accounts without CalDAV and CardDAV URLs are skipped.
func (m *Manager) Start(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ctx, m.cancel = ctx, cancel
	for i := range m.cfg.Accounts {
		a := &m.cfg.Accounts[i]
		if a.CalendarURL() == "" && a.ContactsURL() == "" {
			continue
		}
		w := &worker{m: m, acct: a, log: m.log.With("account", a.ID, "part", "dav"), trigger: make(chan struct{}, 1)}
		m.workers[a.ID] = w
		m.wg.Add(1)
		go func() { defer m.wg.Done(); w.run(ctx) }()
	}
}

// Stop ends all workers and waits for them.
func (m *Manager) Stop() {
	m.mu.Lock()
	if m.cancel != nil {
		m.cancel()
	}
	m.mu.Unlock()
	m.wg.Wait()
}

// TriggerSync requests an immediate sync; accounts without DAV are ignored.
func (m *Manager) TriggerSync(accountID string) {
	m.mu.Lock()
	w := m.workers[accountID]
	m.mu.Unlock()
	if w == nil {
		return
	}
	select {
	case w.trigger <- struct{}{}:
	default:
	}
}

func (m *Manager) emit(e Event) {
	if m.notify != nil {
		m.notify(e)
	}
}

type worker struct {
	m       *Manager
	acct    *config.Account
	log     *slog.Logger
	trigger chan struct{}

	client webdav.HTTPClient
	// Discovered collection homes; cleared on failure so discovery reruns.
	calHome, cardHome string
	lastErr           string
}

func (w *worker) run(ctx context.Context) {
	for {
		w.syncOnce(ctx)
		t := time.NewTimer(w.m.Interval)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-w.trigger:
		case <-t.C:
		}
		t.Stop()
	}
}

func (w *worker) syncOnce(ctx context.Context) {
	if w.client == nil {
		c, err := w.m.httpClient(ctx, w.acct)
		if err != nil {
			w.fail(ctx, "calendar/contacts", err)
			return
		}
		w.client = c
	}
	var errs []string
	if u := w.acct.CalendarURL(); u != "" {
		start := time.Now()
		n, err := w.syncCalendars(ctx, u)
		if err != nil {
			w.calHome = ""
			errs = append(errs, "calendar: "+short(err))
			w.log.Warn("calendar sync failed", "err", err)
		} else {
			w.log.Info("calendar sync done", "changed", n, "took", time.Since(start).Round(time.Millisecond))
		}
	}
	if u := w.acct.ContactsURL(); u != "" {
		start := time.Now()
		n, err := w.syncContacts(ctx, u)
		if err != nil {
			w.cardHome = ""
			errs = append(errs, "contacts: "+short(err))
			w.log.Warn("contacts sync failed", "err", err)
		} else {
			w.log.Info("contacts sync done", "changed", n, "took", time.Since(start).Round(time.Millisecond))
		}
	}
	if ctx.Err() != nil {
		return
	}
	msg := strings.Join(errs, "; ")
	if msg != "" && msg != w.lastErr { // report each new problem once
		w.m.emit(Event{Kind: SyncError, AccountID: w.acct.ID, Detail: msg})
	}
	w.lastErr = msg
}

func (w *worker) fail(ctx context.Context, what string, err error) {
	if ctx.Err() != nil {
		return
	}
	w.log.Warn(what+" sync failed", "err", err)
	if errors.Is(err, auth.ErrReauthRequired) {
		return // mail sync reports reauth_required for the account
	}
	if msg := what + ": " + short(err); msg != w.lastErr {
		w.m.emit(Event{Kind: SyncError, AccountID: w.acct.ID, Detail: msg})
		w.lastErr = msg
	}
}

// short turns an error into a one-line message with a hint for common HTTP failures.
func short(err error) string {
	s := err.Error()
	switch {
	case errors.Is(err, auth.ErrReauthRequired):
		return "login required (run `mailand-daemon login`)"
	case strings.Contains(s, "403"):
		return "access denied (403): for Google, enable the CalDAV and CardDAV APIs in Cloud Console and log in again"
	case strings.Contains(s, "401"):
		return "not authorized (401): check the password or log in again"
	}
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}

// collectionPath turns a configured URL into a path the clients understand.
func baseAndPath(raw string) (endpoint, p string) {
	i := strings.Index(raw, "://")
	if i < 0 {
		return raw, "/"
	}
	j := strings.Index(raw[i+3:], "/")
	if j < 0 {
		return raw, "/"
	}
	return raw, raw[i+3+j:]
}

// --- CalDAV ------------------------------------------------------------------

func (w *worker) syncCalendars(ctx context.Context, rawURL string) (int, error) {
	cc, err := caldav.NewClient(w.client, rawURL)
	if err != nil {
		return 0, err
	}
	cals, err := w.findCalendars(ctx, cc, rawURL)
	if err != nil {
		return 0, err
	}
	var keep []string
	changed := 0
	var errs []error
	for _, c := range cals {
		if !supportsEvents(c) {
			continue
		}
		name := c.Name
		if name == "" {
			name = path.Base(strings.TrimSuffix(c.Path, "/"))
		}
		id, err := w.m.st.UpsertCalendar(ctx, store.Calendar{AccountID: w.acct.ID, Path: c.Path, Name: name,
			Color: normColor(c.Color), ReadOnly: c.ReadOnly})
		if err != nil {
			return changed, err
		}
		keep = append(keep, id)
		n, err := w.syncCalendar(ctx, cc, id, c.Path)
		changed += n
		if err != nil {
			if ctx.Err() != nil {
				return changed, ctx.Err()
			}
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
		}
	}
	if len(errs) == 0 { // only prune after a clean listing
		if err := w.m.st.DeleteCalendarsExcept(ctx, w.acct.ID, keep); err != nil {
			return changed, err
		}
	}
	if changed > 0 {
		w.m.emit(Event{Kind: CalendarsUpdated, AccountID: w.acct.ID})
	}
	return changed, errors.Join(errs...)
}

func supportsEvents(c caldav.Calendar) bool {
	if len(c.SupportedComponentSet) == 0 {
		return true
	}
	for _, s := range c.SupportedComponentSet {
		if strings.EqualFold(s, "VEVENT") {
			return true
		}
	}
	return false
}

func normColor(c string) string {
	c = strings.TrimSpace(c)
	if len(c) == 9 && c[0] == '#' { // #rrggbbaa
		c = c[:7]
	}
	if len(c) != 7 || c[0] != '#' {
		return ""
	}
	return strings.ToLower(c)
}

// findCalendars discovers calendars: principal -> calendar-home-set -> list,
// falling back to treating the configured URL as a home set or as a single
// calendar. The home set is remembered until a sync fails.
func (w *worker) findCalendars(ctx context.Context, cc *caldav.Client, rawURL string) ([]caldav.Calendar, error) {
	_, base := baseAndPath(rawURL)
	if w.calHome != "" {
		if cals, err := cc.FindCalendars(ctx, w.calHome); err == nil && len(cals) > 0 {
			return cals, nil
		}
	}
	home := ""
	principal, err := cc.FindCurrentUserPrincipal(ctx)
	if err != nil {
		w.log.Debug("caldav: no current-user-principal", "err", err)
		principal = base
	}
	if home, err = cc.FindCalendarHomeSet(ctx, principal); err != nil {
		w.log.Debug("caldav: no calendar-home-set", "principal", principal, "err", err)
		home = base
	}
	cals, err := cc.FindCalendars(ctx, home)
	if err == nil && len(cals) > 0 {
		w.calHome = home
		return cals, nil
	}
	if c, err2 := cc.FindCalendar(ctx, base); err2 == nil {
		return []caldav.Calendar{*c}, nil
	}
	if err == nil {
		err = errors.New("no calendars found at " + rawURL)
	}
	return nil, err
}

// diff returns remote paths to fetch (new or ETag changed; an empty ETag is
// always refetched) and local paths gone on the server.
func diff(remote, local map[string]string) (fetch, gone []string) {
	for p, e := range remote {
		if le, ok := local[p]; !ok || e == "" || le != e {
			fetch = append(fetch, p)
		}
	}
	for p := range local {
		if _, ok := remote[p]; !ok {
			gone = append(gone, p)
		}
	}
	return fetch, gone
}

func (w *worker) syncCalendar(ctx context.Context, cc *caldav.Client, calID, coll string) (int, error) {
	remote, err := listETags(ctx, w.client, w.acct.CalendarURL(), coll)
	if err != nil {
		return 0, err
	}
	local, err := w.m.st.CalendarETags(ctx, calID)
	if err != nil {
		return 0, err
	}
	fetch, gone := diff(remote, local)
	changed := 0
	var errs []error
	for i := 0; i < len(fetch); i += multiGetChunk {
		chunk := fetch[i:min(i+multiGetChunk, len(fetch))]
		objs, err := cc.MultiGetCalendar(ctx, coll, &caldav.CalendarMultiGet{
			Paths:       chunk,
			CompRequest: caldav.CalendarCompRequest{Name: "VCALENDAR", AllProps: true, AllComps: true},
		})
		if err != nil {
			if ctx.Err() != nil {
				return changed, ctx.Err()
			}
			errs = append(errs, err) // partial results (one bad object) are still stored
		}
		rows := make([]store.CalendarObject, 0, len(objs))
		for _, o := range objs {
			if o.Data == nil {
				continue
			}
			etag := remote[o.Path] // the listing's value is what the next diff compares
			if etag == "" {
				etag = normETag(o.ETag)
			}
			info := ObjectInfo(o.Data, w.m.local)
			row := store.CalendarObject{CalendarID: calID, Path: o.Path, ETag: etag, UID: info.UID,
				Summary: info.Summary, FirstStart: info.FirstStart, LastEnd: info.LastEnd, Data: encodeICal(o.Data)}
			if !info.HasEvents { // remember the ETag, never match a range
				zero := int64(0)
				row.FirstStart, row.LastEnd = 0, &zero
			}
			rows = append(rows, row)
		}
		if err := w.m.st.UpsertCalendarObjects(ctx, rows); err != nil {
			return changed, err
		}
		changed += len(rows)
	}
	if len(gone) > 0 {
		if err := w.m.st.DeleteCalendarObjects(ctx, calID, gone); err != nil {
			return changed, err
		}
		changed += len(gone)
	}
	if len(errs) > 0 {
		w.log.Warn("some calendar objects could not be read", "calendar", coll, "err", errors.Join(errs...))
	}
	return changed, nil
}

// --- CardDAV -----------------------------------------------------------------

func (w *worker) syncContacts(ctx context.Context, rawURL string) (int, error) {
	cd, err := carddav.NewClient(w.client, rawURL)
	if err != nil {
		return 0, err
	}
	books, err := w.findAddressBooks(ctx, cd, rawURL)
	if err != nil {
		return 0, err
	}
	var keep []string
	changed := 0
	var errs []error
	for _, b := range books {
		name := b.Name
		if name == "" {
			name = "Contacts"
		}
		id, err := w.m.st.UpsertAddressBook(ctx, store.AddressBook{AccountID: w.acct.ID, Path: b.Path, Name: name, ReadOnly: b.ReadOnly})
		if err != nil {
			return changed, err
		}
		keep = append(keep, id)
		n, err := w.syncAddressBook(ctx, cd, id, b.Path)
		changed += n
		if err != nil {
			if ctx.Err() != nil {
				return changed, ctx.Err()
			}
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
		}
	}
	if len(errs) == 0 {
		if err := w.m.st.DeleteAddressBooksExcept(ctx, w.acct.ID, keep); err != nil {
			return changed, err
		}
	}
	if changed > 0 {
		w.m.emit(Event{Kind: ContactsUpdated, AccountID: w.acct.ID})
	}
	return changed, errors.Join(errs...)
}

func (w *worker) findAddressBooks(ctx context.Context, cd *carddav.Client, rawURL string) ([]carddav.AddressBook, error) {
	_, base := baseAndPath(rawURL)
	if w.cardHome != "" {
		if books, err := cd.FindAddressBooks(ctx, w.cardHome); err == nil && len(books) > 0 {
			return books, nil
		}
	}
	principal, err := cd.FindCurrentUserPrincipal(ctx)
	if err != nil {
		w.log.Debug("carddav: no current-user-principal", "err", err)
		principal = base
	}
	home, err := cd.FindAddressBookHomeSet(ctx, principal)
	if err != nil {
		w.log.Debug("carddav: no addressbook-home-set", "principal", principal, "err", err)
		home = base
	}
	books, err := cd.FindAddressBooks(ctx, home)
	if err == nil && len(books) > 0 {
		w.cardHome = home
		return books, nil
	}
	// The configured URL may be the address book itself (Google's default list).
	if _, err2 := listETags(ctx, w.client, rawURL, base); err2 == nil {
		return []carddav.AddressBook{{Path: base, Name: "Contacts"}}, nil
	}
	if err == nil {
		err = errors.New("no address books found at " + rawURL)
	}
	return nil, err
}

func (w *worker) syncAddressBook(ctx context.Context, cd *carddav.Client, bookID, coll string) (int, error) {
	remote, err := listETags(ctx, w.client, w.acct.ContactsURL(), coll)
	if err != nil {
		return 0, err
	}
	local, err := w.m.st.ContactETags(ctx, bookID)
	if err != nil {
		return 0, err
	}
	fetch, gone := diff(remote, local)
	changed := 0
	var errs []error
	for i := 0; i < len(fetch); i += multiGetChunk {
		chunk := fetch[i:min(i+multiGetChunk, len(fetch))]
		objs, err := cd.MultiGetAddressBook(ctx, coll, &carddav.AddressBookMultiGet{
			Paths: chunk, DataRequest: carddav.AddressDataRequest{AllProp: true},
		})
		if err != nil {
			if ctx.Err() != nil {
				return changed, ctx.Err()
			}
			errs = append(errs, err)
		}
		rows := make([]store.Contact, 0, len(objs))
		for _, o := range objs {
			if o.Card == nil {
				continue
			}
			data, err := encodeVCard(o.Card)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", o.Path, err))
				continue
			}
			etag := remote[o.Path] // the listing's value is what the next diff compares
			if etag == "" {
				etag = normETag(o.ETag)
			}
			f := CardFields(o.Card)
			rows = append(rows, store.Contact{AddressBookID: bookID, Path: o.Path, ETag: etag, UID: f.UID,
				FullName: f.FullName, Emails: f.Emails, Phones: f.Phones, Organization: f.Organization, Data: data})
		}
		if err := w.m.st.UpsertContacts(ctx, rows); err != nil {
			return changed, err
		}
		changed += len(rows)
	}
	if len(gone) > 0 {
		if err := w.m.st.DeleteContacts(ctx, bookID, gone); err != nil {
			return changed, err
		}
		changed += len(gone)
	}
	if len(errs) > 0 {
		w.log.Warn("some contacts could not be read", "address_book", coll, "err", errors.Join(errs...))
	}
	return changed, nil
}

// Respond sets the account's attendance (Accepted, Tentative, Declined) on the
// stored event object seq (the whole series), writes it back to the server
// with If-Match, and updates the store. With CalDAV scheduling (Google and
// most servers) the server then notifies the organizer. ErrConflict means the
// event changed on the server; a sync is started so a retry sees the new data.
func (m *Manager) Respond(ctx context.Context, seq int64, status string) error {
	obj, cal, err := m.st.GetCalendarObject(ctx, seq)
	if err != nil {
		return err
	}
	acct, ok := m.cfg.Account(cal.AccountID)
	if !ok || acct.CalendarURL() == "" {
		return fmt.Errorf("account %q has no calendar access", cal.AccountID)
	}
	if cal.ReadOnly {
		return ErrNotInvited
	}
	data, err := RespondData(obj.Data, acct.Email, status, time.Now())
	if err != nil {
		return err
	}
	client, err := m.respondClient(ctx, acct)
	if err != nil {
		return err
	}
	etag, err := putObject(ctx, client, acct.CalendarURL(), obj.Path, "text/calendar; charset=utf-8", data, obj.ETag, false)
	if errors.Is(err, ErrConflict) {
		m.TriggerSync(acct.ID)
		return err
	}
	if err != nil {
		return err
	}
	obj.Data, obj.ETag = data, etag // empty ETag: the next sync fetches the server's version
	if err := m.st.UpsertCalendarObjects(ctx, []store.CalendarObject{obj}); err != nil {
		return err
	}
	m.emit(Event{Kind: CalendarsUpdated, AccountID: acct.ID})
	return nil
}

// respondClient returns a per-account HTTP client for writes (the sync
// worker's client belongs to its goroutine).
func (m *Manager) respondClient(ctx context.Context, a *config.Account) (webdav.HTTPClient, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.writeClients == nil {
		m.writeClients = map[string]webdav.HTTPClient{}
	}
	if c := m.writeClients[a.ID]; c != nil {
		return c, nil
	}
	base := ctx
	if m.ctx != nil {
		base = m.ctx // token refreshes must outlive this request
	}
	c, err := m.httpClient(base, a)
	if err != nil {
		return nil, err
	}
	m.writeClients[a.ID] = c
	return c, nil
}
