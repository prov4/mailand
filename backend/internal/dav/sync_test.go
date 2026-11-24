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
	puts  int
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
func (b *memBackend) PutCalendarObject(_ context.Context, p string, cal *ical.Calendar, opts *caldav.PutCalendarObjectOptions) (*caldav.CalendarObject, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if opts != nil && opts.IfMatch.IsSet() {
		if ok, _ := opts.IfMatch.MatchETag(b.etag(p)); !ok {
			return nil, webdav.NewHTTPError(http.StatusPreconditionFailed, errors.New("etag mismatch"))
		}
	}
	var buf strings.Builder
	if err := ical.NewEncoder(&buf).Encode(cal); err != nil {
		return nil, err
	}
	b.ics[p] = buf.String()
	b.gen[p]++
	b.puts++
	return b.calObj(p)
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
func (b *memBackend) PutAddressObject(_ context.Context, p string, card vcard.Card, opts *carddav.PutAddressObjectOptions) (*carddav.AddressObject, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	_, exists := b.vcf[p]
	if opts != nil && opts.IfNoneMatch.IsWildcard() && exists {
		return nil, webdav.NewHTTPError(http.StatusPreconditionFailed, errors.New("exists"))
	}
	if opts != nil && opts.IfMatch.IsSet() {
		if ok, _ := opts.IfMatch.MatchETag(b.etag(p)); !ok || !exists {
			return nil, webdav.NewHTTPError(http.StatusPreconditionFailed, errors.New("etag mismatch"))
		}
	}
	var buf strings.Builder
	if err := vcard.NewEncoder(&buf).Encode(card); err != nil {
		return nil, err
	}
	b.vcf[p] = buf.String()
	b.gen[p]++
	b.puts++
	return b.cardObj(p)
}
func (b *memBackend) DeleteAddressObject(_ context.Context, p string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.vcf[p]; !ok {
		return errNotFound
	}
	delete(b.vcf, p)
	return nil
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

const invite = "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//t//EN\r\nBEGIN:VEVENT\r\nUID:inv\r\nDTSTAMP:20260101T000000Z\r\n" +
	"SUMMARY:Planning\r\nDTSTART:20261201T100000Z\r\nDURATION:PT1H\r\nRRULE:FREQ=WEEKLY;COUNT=3\r\n" +
	"ORGANIZER;CN=Boss:mailto:boss@x\r\n" +
	"ATTENDEE;CN=Boss;PARTSTAT=ACCEPTED;ROLE=CHAIR:mailto:boss@x\r\n" +
	"ATTENDEE;CN=Me;PARTSTAT=NEEDS-ACTION;RSVP=TRUE:mailto:A@X\r\n" +
	"ATTENDEE;PARTSTAT=DECLINED:mailto:other@x\r\nEND:VEVENT\r\n" +
	"BEGIN:VEVENT\r\nUID:inv\r\nDTSTAMP:20260101T000000Z\r\nRECURRENCE-ID:20261208T100000Z\r\nSUMMARY:Planning (moved)\r\n" +
	"DTSTART:20261209T100000Z\r\nDURATION:PT1H\r\nORGANIZER:mailto:boss@x\r\nATTENDEE;PARTSTAT=NEEDS-ACTION:mailto:a@x\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"

func TestInviteAttendeesAndRespond(t *testing.T) {
	e := newDavEnv(t)
	ctx := context.Background()
	e.mem.putICS("/u/cal/work/inv.ics", invite)
	e.sync()

	objs, _ := e.st.CalendarObjectsInRange(ctx, nil, 0, 1<<62)
	if len(objs) != 1 {
		t.Fatalf("objects: %d", len(objs))
	}
	inst, err := ExpandStored(objs[0].Data, time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC), time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC), time.UTC)
	if err != nil || len(inst) != 3 {
		t.Fatalf("instances: %d %v", len(inst), err)
	}
	if inst[0].OrganizerEmail != "boss@x" || len(inst[0].Attendees) != 3 || inst[0].Attendees[0].Role != "CHAIR" || inst[0].Attendees[1].Name != "Me" {
		t.Fatalf("attendees: %+v", inst[0])
	}
	if st, can := MyStatus(inst[0], "a@x"); st != NeedsAction || !can {
		t.Fatalf("my status %q %v", st, can)
	}
	if _, can := MyStatus(inst[0], "boss@x"); can {
		t.Error("organizer must not respond")
	}
	if st, can := MyStatus(inst[0], "nobody@x"); st != "" || can {
		t.Error("stranger")
	}

	// Accept the whole series: master and override change on the server.
	if err := e.m.Respond(ctx, objs[0].Seq, Accepted); err != nil {
		t.Fatal(err)
	}
	if e.mem.puts != 1 {
		t.Fatalf("puts = %d", e.mem.puts)
	}
	srv := e.mem.ics["/u/cal/work/inv.ics"]
	if strings.Count(srv, "PARTSTAT=ACCEPTED") != 3 || strings.Contains(srv, "RSVP=TRUE") || !strings.Contains(srv, "PARTSTAT=DECLINED") {
		t.Fatalf("server copy:\n%s", srv)
	}
	objs, _ = e.st.CalendarObjectsInRange(ctx, nil, 0, 1<<62)
	inst, _ = ExpandStored(objs[0].Data, time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC), time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC), time.UTC)
	for _, in := range inst {
		if st, _ := MyStatus(in, "a@x"); st != Accepted {
			t.Errorf("stored %s: %q", in.Start, st)
		}
	}
	// The stored ETag is the server's new one: the next sync downloads nothing.
	before := e.client.reports
	e.sync()
	if e.client.reports != before {
		t.Error("sync after respond refetched the event")
	}

	// The organizer edits the event meanwhile: the response is refused, not overwritten.
	e.mem.putICS("/u/cal/work/inv.ics", strings.Replace(invite, "SUMMARY:Planning\r\n", "SUMMARY:Planning v2\r\n", 1))
	if err := e.m.Respond(ctx, objs[0].Seq, Declined); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflict: %v", err)
	}
	if !strings.Contains(e.mem.ics["/u/cal/work/inv.ics"], "Planning v2") {
		t.Error("organizer change overwritten")
	}
	if err := e.m.Respond(ctx, objs[0].Seq, "MAYBE"); !errors.Is(err, ErrBadResponse) {
		t.Errorf("bad status: %v", err)
	}
}

func TestRespondNotInvited(t *testing.T) {
	_, err := RespondData(invite, "stranger@x", Accepted, time.Now())
	if !errors.Is(err, ErrNotInvited) {
		t.Errorf("stranger: %v", err)
	}
	if _, err := RespondData(invite, "boss@x", Declined, time.Now()); !errors.Is(err, ErrNotInvited) {
		t.Errorf("organizer: %v", err)
	}
}

func TestBuildCardKeepsUnknownFields(t *testing.T) {
	old := "BEGIN:VCARD\r\nVERSION:3.0\r\nUID:u1\r\nFN:Ana Horvat\r\nN:Horvat;Ana;;;\r\n" +
		"EMAIL;TYPE=WORK:ana@work.hr\r\nEMAIL;TYPE=HOME:ana@home.hr\r\nTEL;TYPE=CELL:091 111\r\n" +
		"BDAY:1990-03-10\r\nADR;TYPE=HOME:;;Ilica 1;Zagreb;;10000;Croatia\r\nORG:Acme;R&D\r\nEND:VCARD\r\n"
	data, uid, err := BuildCard(old, ContactInput{FullName: "Ana Horvat", Organization: "Acme",
		Emails: []string{"ana@home.hr", " ana@new.hr "}, Phones: []string{"091 111"}})
	if err != nil || uid != "u1" {
		t.Fatal(uid, err)
	}
	for _, want := range []string{"BDAY:1990-03-10", "ADR;TYPE=HOME", "EMAIL;TYPE=HOME:ana@home.hr", "ana@new.hr", "TEL;TYPE=CELL:091 111", "ORG:Acme;R&D", "N:Horvat;Ana"} {
		if !strings.Contains(data, want) {
			t.Errorf("missing %q in\n%s", want, data)
		}
	}
	if strings.Contains(data, "ana@work.hr") {
		t.Error("removed email kept")
	}
	// A new card gets VERSION, a UID, FN and N.
	data, uid, err = BuildCard("", ContactInput{FullName: "Ivan Ivić Novak", Emails: []string{"i@x.hr"}})
	if err != nil || uid == "" || !strings.Contains(data, "VERSION:3.0") || !strings.Contains(data, "FN:Ivan Ivić Novak") || !strings.Contains(data, "N:Novak;Ivan Ivić") {
		t.Fatalf("%v\n%s", err, data)
	}
	for _, bad := range []ContactInput{{}, {FullName: "x", Emails: []string{"not an email"}}, {FullName: "a\r\nBCC:x"}} {
		if _, _, err := BuildCard("", bad); !errors.Is(err, ErrInvalidContact) {
			t.Errorf("%+v: %v", bad, err)
		}
	}
}

func TestSaveAndDeleteContact(t *testing.T) {
	e := newDavEnv(t)
	ctx := context.Background()
	e.mem.putVCF("/u/card/main/x.vcf", card("x", "Ana Anić", "ana@example.com"))
	e.sync()

	seq, err := e.m.SaveContact(ctx, 0, "acc", ContactInput{FullName: "Novi Kontakt", Emails: []string{"novi@example.com"}, Phones: []string{"+385 1"}})
	if err != nil {
		t.Fatal(err)
	}
	c, _, err := e.st.GetContact(ctx, seq)
	if err != nil || c.FullName != "Novi Kontakt" || !strings.HasPrefix(c.Path, "/u/card/main/") || c.ETag == "" {
		t.Fatalf("new contact: %+v %v", c, err)
	}
	if !strings.Contains(e.mem.vcf[c.Path], "novi@example.com") {
		t.Fatalf("not on the server: %v", e.mem.vcf)
	}
	before := e.client.reports
	e.sync()
	if e.client.reports != before {
		t.Error("sync after create refetched")
	}

	ana, _ := e.st.SearchContacts(ctx, "Ana", false, 0)
	if _, err := e.m.SaveContact(ctx, ana[0].Seq, "", ContactInput{FullName: "Ana Anić-Horvat", Emails: []string{"ana@example.com"}}); err != nil {
		t.Fatal(err)
	}
	srv := e.mem.vcf["/u/card/main/x.vcf"]
	// The input is the full editable set: no phone and no organization means both were cleared.
	if !strings.Contains(srv, "FN:Ana Anić-Horvat") || strings.Contains(srv, "TEL") || strings.Contains(srv, "ORG") {
		t.Errorf("server card after edit:\n%s", srv)
	}

	// Edited elsewhere meanwhile: refused, not overwritten.
	e.mem.putVCF("/u/card/main/x.vcf", card("x", "Ana (phone)", "ana@example.com"))
	if _, err := e.m.SaveContact(ctx, ana[0].Seq, "", ContactInput{FullName: "Mine"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflict: %v", err)
	}
	if !strings.Contains(e.mem.vcf["/u/card/main/x.vcf"], "Ana (phone)") {
		t.Error("server change overwritten")
	}

	if err := e.m.DeleteContact(ctx, seq); err != nil {
		t.Fatal(err)
	}
	if _, ok := e.mem.vcf[c.Path]; ok {
		t.Error("still on the server")
	}
	if _, _, err := e.st.GetContact(ctx, seq); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("still stored: %v", err)
	}
	if _, err := e.m.SaveContact(ctx, 0, "nope", ContactInput{FullName: "x"}); !errors.Is(err, ErrNoAddressBook) {
		t.Errorf("unknown account: %v", err)
	}
}
