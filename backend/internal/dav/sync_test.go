package dav

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emersion/go-ical"
	"github.com/emersion/go-vcard"
	"github.com/emersion/go-webdav"
	"github.com/emersion/go-webdav/caldav"
	"github.com/emersion/go-webdav/carddav"

	"mailand/backend/internal/config"
	"mailand/backend/internal/store"
)

// memBackend is a tiny in-memory CalDAV + CardDAV server:
// principal /u/, calendars under /u/cal/, address books under /u/card/.
type memBackend struct {
	mu    sync.Mutex
	cals  map[string]*caldav.Calendar
	books map[string]*carddav.AddressBook
	ics   map[string]string // path -> iCalendar text
	vcf   map[string]string // path -> vCard text
	gen   map[string]int    // path -> version (ETag)
}

func newMem() *memBackend {
	return &memBackend{cals: map[string]*caldav.Calendar{}, books: map[string]*carddav.AddressBook{},
		ics: map[string]string{}, vcf: map[string]string{}, gen: map[string]int{}}
}

func (b *memBackend) putICS(p, data string) { b.mu.Lock(); b.ics[p] = data; b.gen[p]++; b.mu.Unlock() }
func (b *memBackend) putVCF(p, data string) { b.mu.Lock(); b.vcf[p] = data; b.gen[p]++; b.mu.Unlock() }
func (b *memBackend) del(p string) {
	b.mu.Lock()
	delete(b.ics, p)
	delete(b.vcf, p)
	b.mu.Unlock()
}

func (b *memBackend) etag(p string) string { return fmt.Sprintf("v%d", b.gen[p]) }

var errNotFound = webdav.NewHTTPError(http.StatusNotFound, errors.New("not found"))

func (b *memBackend) CurrentUserPrincipal(context.Context) (string, error) { return "/u/", nil }
func (b *memBackend) CalendarHomeSetPath(context.Context) (string, error)  { return "/u/cal/", nil }
func (b *memBackend) AddressBookHomeSetPath(context.Context) (string, error) {
	return "/u/card/", nil
}

func (b *memBackend) CreateCalendar(context.Context, *caldav.Calendar) error { return nil }
func (b *memBackend) ListCalendars(context.Context) ([]caldav.Calendar, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []caldav.Calendar
	for _, c := range b.cals {
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}
func (b *memBackend) GetCalendar(_ context.Context, p string) (*caldav.Calendar, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if c, ok := b.cals[p]; ok {
		return c, nil
	}
	return nil, errNotFound
}
func (b *memBackend) calObj(p string) (*caldav.CalendarObject, error) {
	data, ok := b.ics[p]
	if !ok {
		return nil, errNotFound
	}
	cal, err := ical.NewDecoder(strings.NewReader(data)).Decode()
	if err != nil {
		return nil, err
	}
	return &caldav.CalendarObject{Path: p, ETag: b.etag(p), Data: cal, ModTime: time.Now()}, nil
}
func (b *memBackend) GetCalendarObject(_ context.Context, p string, _ *caldav.CalendarCompRequest) (*caldav.CalendarObject, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.calObj(p)
}
func (b *memBackend) ListCalendarObjects(_ context.Context, coll string, _ *caldav.CalendarCompRequest) ([]caldav.CalendarObject, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []caldav.CalendarObject
	for p := range b.ics {
		if strings.HasPrefix(p, coll) {
			o, err := b.calObj(p)
			if err != nil {
				return nil, err
			}
			out = append(out, *o)
		}
	}
	return out, nil
}
func (b *memBackend) QueryCalendarObjects(ctx context.Context, coll string, _ *caldav.CalendarQuery) ([]caldav.CalendarObject, error) {
	return b.ListCalendarObjects(ctx, coll, nil)
}
func (b *memBackend) PutCalendarObject(context.Context, string, *ical.Calendar, *caldav.PutCalendarObjectOptions) (*caldav.CalendarObject, error) {
	return nil, errors.New("read-only")
}
func (b *memBackend) DeleteCalendarObject(context.Context, string) error {
	return errors.New("read-only")
}

func (b *memBackend) ListAddressBooks(context.Context) ([]carddav.AddressBook, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []carddav.AddressBook
	for _, a := range b.books {
		out = append(out, *a)
	}
	return out, nil
}
func (b *memBackend) GetAddressBook(_ context.Context, p string) (*carddav.AddressBook, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if a, ok := b.books[p]; ok {
		return a, nil
	}
	return nil, errNotFound
}
func (b *memBackend) CreateAddressBook(context.Context, *carddav.AddressBook) error { return nil }
func (b *memBackend) DeleteAddressBook(context.Context, string) error               { return nil }
func (b *memBackend) cardObj(p string) (*carddav.AddressObject, error) {
	data, ok := b.vcf[p]
	if !ok {
		return nil, errNotFound
	}
	card, err := vcard.NewDecoder(strings.NewReader(data)).Decode()
	if err != nil {
		return nil, err
	}
	return &carddav.AddressObject{Path: p, ETag: b.etag(p), Card: card, ModTime: time.Now()}, nil
}
func (b *memBackend) GetAddressObject(_ context.Context, p string, _ *carddav.AddressDataRequest) (*carddav.AddressObject, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.cardObj(p)
}
func (b *memBackend) ListAddressObjects(_ context.Context, coll string, _ *carddav.AddressDataRequest) ([]carddav.AddressObject, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []carddav.AddressObject
	for p := range b.vcf {
		if strings.HasPrefix(p, coll) {
			o, err := b.cardObj(p)
			if err != nil {
				return nil, err
			}
			out = append(out, *o)
		}
	}
	return out, nil
}
func (b *memBackend) QueryAddressObjects(ctx context.Context, coll string, _ *carddav.AddressBookQuery) ([]carddav.AddressObject, error) {
	return b.ListAddressObjects(ctx, coll, nil)
}
func (b *memBackend) PutAddressObject(context.Context, string, vcard.Card, *carddav.PutAddressObjectOptions) (*carddav.AddressObject, error) {
	return nil, errors.New("read-only")
}
func (b *memBackend) DeleteAddressObject(context.Context, string) error {
	return errors.New("read-only")
}

// countingClient counts requests so tests can check that unchanged
// collections are not downloaded again.
type countingClient struct {
	mu      sync.Mutex
	reports int
}

func (c *countingClient) Do(r *http.Request) (*http.Response, error) {
	if r.Method == "REPORT" {
		c.mu.Lock()
		c.reports++
		c.mu.Unlock()
	}
	return http.DefaultClient.Do(r)
}

func ev(uid, summary, start string) string {
	return "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//t//EN\r\nBEGIN:VEVENT\r\nUID:" + uid + "\r\nSUMMARY:" + summary +
		"\r\nDTSTAMP:20260101T000000Z\r\nDTSTART:" + start + "\r\nDURATION:PT1H\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
}

func card(uid, fn, email string) string {
	return "BEGIN:VCARD\r\nVERSION:3.0\r\nUID:" + uid + "\r\nFN:" + fn + "\r\nEMAIL;TYPE=work:" + email + "\r\nTEL:+385 1 234\r\nORG:Acme;R&D\r\nEND:VCARD\r\n"
}

type davEnv struct {
	t      *testing.T
	mem    *memBackend
	st     *store.Store
	m      *Manager
	w      *worker
	client *countingClient
	events []Event
	mu     sync.Mutex
}

func newDavEnv(t *testing.T) *davEnv {
	mem := newMem()
	mem.cals["/u/cal/work/"] = &caldav.Calendar{Path: "/u/cal/work/", Name: "Work", Color: "#FF8800FF", SupportedComponentSet: []string{"VEVENT"}}
	mem.cals["/u/cal/tasks/"] = &caldav.Calendar{Path: "/u/cal/tasks/", Name: "Tasks", SupportedComponentSet: []string{"VTODO"}}
	mem.books["/u/card/main/"] = &carddav.AddressBook{Path: "/u/card/main/", Name: "Main"}

	mux := http.NewServeMux()
	mux.Handle("/u/card/", &carddav.Handler{Backend: mem})
	mux.Handle("/.well-known/carddav", &carddav.Handler{Backend: mem})
	mux.Handle("/", &caldav.Handler{Backend: mem})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	st, err := store.Open(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ctx := context.Background()
	if err := st.UpsertAccount(ctx, store.Account{ID: "acc", Name: "A", Email: "a@x", IMAPHost: "h", IMAPPort: 1, SMTPHost: "h", SMTPPort: 1}); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Accounts: []config.Account{{ID: "acc", Email: "a@x", Provider: config.ProviderIMAP,
		CalDAVURL: srv.URL + "/", CardDAVURL: srv.URL + "/u/card/"}}}
	e := &davEnv{t: t, mem: mem, st: st, client: &countingClient{}}
	e.m = NewManager(cfg, st, func(ev Event) { e.mu.Lock(); e.events = append(e.events, ev); e.mu.Unlock() }, nil)
	e.m.local = time.UTC
	e.m.httpClient = func(context.Context, *config.Account) (webdav.HTTPClient, error) { return e.client, nil }
	e.w = &worker{m: e.m, acct: &cfg.Accounts[0], log: e.m.log, trigger: make(chan struct{}, 1)}
	return e
}

func (e *davEnv) sync() {
	e.t.Helper()
	e.w.syncOnce(context.Background())
	if e.w.lastErr != "" {
		e.t.Fatalf("sync error: %s", e.w.lastErr)
	}
}

func (e *davEnv) summaries() []string {
	e.t.Helper()
	objs, err := e.st.CalendarObjectsInRange(context.Background(), nil, 0, 1<<62)
	if err != nil {
		e.t.Fatal(err)
	}
	var out []string
	for _, o := range objs {
		out = append(out, o.Summary)
	}
	sort.Strings(out)
	return out
}

func TestSyncCalendarsAndContacts(t *testing.T) {
	e := newDavEnv(t)
	ctx := context.Background()
	e.mem.putICS("/u/cal/work/a.ics", ev("a", "Alpha", "20261124T100000Z"))
	e.mem.putICS("/u/cal/work/b.ics", ev("b", "Beta", "20261125T100000Z"))
	e.mem.putVCF("/u/card/main/x.vcf", card("x", "Ana Anić", "ana@example.com"))
	e.mem.putVCF("/u/card/main/y.vcf", "BEGIN:VCARD\r\nVERSION:3.0\r\nUID:y\r\nN:Babić;Bruno;;;\r\nEMAIL:bruno@example.com\r\nEND:VCARD\r\n") // no FN
	e.sync()

	cals, _ := e.st.ListCalendars(ctx, "")
	if len(cals) != 1 || cals[0].Name != "Work" || cals[0].Color != "#ff8800" { // VTODO-only calendar skipped
		t.Fatalf("calendars: %+v", cals)
	}
	if got := fmt.Sprint(e.summaries()); got != "[Alpha Beta]" {
		t.Fatalf("events: %s", got)
	}
	cs, err := e.st.SearchContacts(ctx, "", false, 0)
	if err != nil || len(cs) != 2 {
		t.Fatalf("contacts: %+v %v", cs, err)
	}
	if cs[0].FullName != "Ana Anić" || cs[0].Emails[0] != "ana@example.com" || cs[0].Organization != "Acme" || cs[0].Phones[0] != "+385 1 234" || cs[0].AccountID != "acc" {
		t.Errorf("card fields: %+v", cs[0])
	}
	if cs[1].FullName != "Bruno Babić" {
		t.Errorf("name from N: %+v", cs[1])
	}
	if hits, _ := e.st.SearchContacts(ctx, "BRU", true, 5); len(hits) != 1 || hits[0].FullName != "Bruno Babić" {
		t.Errorf("search: %+v", hits)
	}

	// Nothing changed: no multiget REPORTs.
	before := e.client.reports
	n := len(e.events)
	e.sync()
	if e.client.reports != before {
		t.Errorf("unchanged sync issued %d REPORTs", e.client.reports-before)
	}
	if len(e.events) != n {
		t.Errorf("unchanged sync emitted events: %+v", e.events[n:])
	}

	// Change one, delete one, add one.
	e.mem.putICS("/u/cal/work/a.ics", ev("a", "Alpha v2", "20261124T100000Z"))
	e.mem.del("/u/cal/work/b.ics")
	e.mem.putICS("/u/cal/work/c.ics", ev("c", "Gamma", "20261126T100000Z"))
	e.mem.del("/u/card/main/y.vcf")
	e.sync()
	if got := fmt.Sprint(e.summaries()); got != "[Alpha v2 Gamma]" {
		t.Fatalf("events after change: %s", got)
	}
	if cs, _ := e.st.SearchContacts(ctx, "", false, 0); len(cs) != 1 {
		t.Fatalf("contacts after delete: %+v", cs)
	}
	var kinds []EventKind
	for _, ev := range e.events {
		kinds = append(kinds, ev.Kind)
	}
	if fmt.Sprint(kinds) != fmt.Sprint([]EventKind{CalendarsUpdated, ContactsUpdated, CalendarsUpdated, ContactsUpdated}) {
		t.Errorf("events: %v", kinds)
	}

	// A calendar removed on the server is removed locally with its events.
	e.mem.mu.Lock()
	delete(e.mem.cals, "/u/cal/work/")
	e.mem.cals["/u/cal/home/"] = &caldav.Calendar{Path: "/u/cal/home/", Name: "Home"}
	e.mem.mu.Unlock()
	e.sync()
	cals, _ = e.st.ListCalendars(ctx, "")
	if len(cals) != 1 || cals[0].Name != "Home" || len(e.summaries()) != 0 {
		t.Fatalf("after calendar removal: %+v %v", cals, e.summaries())
	}
}

func TestSyncErrorReportedOnce(t *testing.T) {
	e := newDavEnv(t)
	e.w.acct.CalDAVURL = "http://127.0.0.1:1/" // nothing listens
	e.w.acct.CardDAVURL = "off"
	ctx := context.Background()
	e.w.syncOnce(ctx)
	e.w.syncOnce(ctx)
	if len(e.events) != 1 || e.events[0].Kind != SyncError || !strings.HasPrefix(e.events[0].Detail, "calendar: ") {
		t.Fatalf("events: %+v", e.events)
	}
}

func TestEncodeVCardAddsVersion(t *testing.T) {
	c, err := vcard.NewDecoder(strings.NewReader("BEGIN:VCARD\r\nFN:X\r\nEND:VCARD\r\n")).Decode()
	if err != nil {
		t.Fatal(err)
	}
	out, err := encodeVCard(c)
	if err != nil || !strings.Contains(out, "VERSION:3.0") || !strings.Contains(out, "FN:X") {
		t.Fatalf("%q %v", out, err)
	}
	if c.Get(vcard.FieldVersion) != nil {
		t.Error("input card modified")
	}
}
