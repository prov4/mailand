package rpc

import (
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"

	"mailand/backend/internal/rpc/pb"
	"mailand/backend/internal/store"
)

func TestCalendarAndContactRPCs(t *testing.T) {
	e := setup(t, true)
	cx := ctx(t)
	zg, _ := time.LoadLocation("Europe/Zagreb")
	old := eventZone
	eventZone = zg
	t.Cleanup(func() { eventZone = old })

	calID, err := e.st.UpsertCalendar(cx, store.Calendar{AccountID: "gm", Path: "/cal/home/", Name: "Home", Color: "#336699"})
	if err != nil {
		t.Fatal(err)
	}
	ics := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//t//EN\r\nBEGIN:VEVENT\r\nUID:yoga\r\nSUMMARY:Yoga\r\n" +
		"DTSTART;TZID=Europe/Zagreb:20261020T180000\r\nDTEND;TZID=Europe/Zagreb:20261020T190000\r\nRRULE:FREQ=WEEKLY\r\nEND:VEVENT\r\n" +
		"BEGIN:VEVENT\r\nUID:holiday\r\nSUMMARY:Holiday\r\nDTSTART;VALUE=DATE:20261027\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	start := time.Date(2026, 10, 20, 18, 0, 0, 0, zg).Unix()
	if err := e.st.UpsertCalendarObjects(cx, []store.CalendarObject{{CalendarID: calID, Path: "/cal/home/y.ics", ETag: "1", UID: "yoga", Summary: "Yoga", FirstStart: start, Data: ics}}); err != nil {
		t.Fatal(err)
	}

	cals, err := e.c.ListCalendars(cx, &pb.Empty{})
	if err != nil || len(cals.Calendars) != 1 || cals.Calendars[0].Color != "#336699" || cals.Calendars[0].AccountId != "gm" {
		t.Fatalf("calendars: %v %v", cals, err)
	}

	// The week across the end of DST (2026-10-25): the weekly 18:00 stays 18:00 local.
	from := time.Date(2026, 10, 26, 0, 0, 0, 0, zg)
	evs, err := e.c.ListEvents(cx, &pb.ListEventsRequest{StartUnix: from.Unix(), EndUnix: from.AddDate(0, 0, 7).Unix()})
	if err != nil || len(evs.Events) != 2 {
		t.Fatalf("events: %v %v", evs, err)
	}
	hol, yoga := evs.Events[0], evs.Events[1]
	if !hol.AllDay || hol.Summary != "Holiday" || time.Unix(hol.StartUnix, 0).In(zg).Format("01-02 15:04") != "10-27 00:00" {
		t.Errorf("all-day: %v", hol)
	}
	if got := time.Unix(yoga.StartUnix, 0).In(zg).Format("01-02 15:04"); got != "10-27 18:00" || !yoga.Recurring || yoga.EndUnix-yoga.StartUnix != 3600 {
		t.Errorf("weekly after DST end: %s %v", got, yoga)
	}
	if yoga.Id == "" || yoga.Id == hol.Id || yoga.CalendarId != calID {
		t.Errorf("ids: %v %v", yoga, hol)
	}
	// Filter by calendar.
	if evs, _ := e.c.ListEvents(cx, &pb.ListEventsRequest{StartUnix: from.Unix(), EndUnix: from.AddDate(0, 0, 7).Unix(), CalendarIds: []string{"nope"}}); len(evs.Events) != 0 {
		t.Errorf("calendar filter: %v", evs)
	}
	_, err = e.c.ListEvents(cx, &pb.ListEventsRequest{StartUnix: 10, EndUnix: 5})
	wantCode(t, err, codes.InvalidArgument, "reversed range")
	_, err = e.c.ListEvents(cx, &pb.ListEventsRequest{StartUnix: 0, EndUnix: 500 * 86400})
	wantCode(t, err, codes.InvalidArgument, "long range")

	bookID, err := e.st.UpsertAddressBook(cx, store.AddressBook{AccountID: "gm", Path: "/card/", Name: "Contacts"})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.st.UpsertContacts(cx, []store.Contact{
		{AddressBookID: bookID, Path: "/card/1", FullName: "Marko Marić", Emails: []string{"marko@example.hr", "m@posao.hr"}, Phones: []string{"091"}, Data: "x"},
		{AddressBookID: bookID, Path: "/card/2", FullName: "Ana Marković", Organization: "Posao d.o.o.", Data: "x"},
		{AddressBookID: bookID, Path: "/card/3", FullName: "Zoran", Emails: []string{"zoran@posao.hr"}, Data: "x"},
	}); err != nil {
		t.Fatal(err)
	}
	cs, err := e.c.ListContacts(cx, &pb.ListContactsRequest{Query: "mar"})
	if err != nil || len(cs.Contacts) != 2 || cs.Contacts[0].FullName != "Marko Marić" || len(cs.Contacts[0].Emails) != 2 {
		t.Fatalf("search mar: %v %v", cs, err)
	}
	cs, _ = e.c.ListContacts(cx, &pb.ListContactsRequest{Query: "posao", EmailOnly: true})
	if len(cs.Contacts) != 2 { // Marko (2nd email) and Zoran; Ana has no email
		t.Errorf("email-only: %v", cs)
	}
	cs, _ = e.c.ListContacts(cx, &pb.ListContactsRequest{Query: "100%_"})
	if len(cs.Contacts) != 0 {
		t.Errorf("LIKE wildcards must be literal: %v", cs)
	}
}

func TestRespondToEventRPC(t *testing.T) {
	e := setup(t, true)
	cx := ctx(t)
	e.srv.SetResponder(NewOfflineResponder(e.st, e.broker))
	calID, _ := e.st.UpsertCalendar(cx, store.Calendar{AccountID: "gm", Path: "/c/", Name: "C"})
	ics := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//t//EN\r\nBEGIN:VEVENT\r\nUID:i\r\nSUMMARY:Review\r\n" +
		"DTSTART:20261201T100000Z\r\nDTEND:20261201T110000Z\r\nORGANIZER;CN=Boss:mailto:boss@x\r\n" +
		"ATTENDEE;CN=Boss;PARTSTAT=ACCEPTED:mailto:boss@x\r\nATTENDEE;PARTSTAT=NEEDS-ACTION;RSVP=TRUE:mailto:ME@gmail.example\r\n" +
		"END:VEVENT\r\nEND:VCALENDAR\r\n"
	start := time.Date(2026, 12, 1, 10, 0, 0, 0, time.UTC).Unix()
	if err := e.st.UpsertCalendarObjects(cx, []store.CalendarObject{{CalendarID: calID, Path: "/c/i.ics", FirstStart: start, Data: ics}}); err != nil {
		t.Fatal(err)
	}
	list := func() *pb.CalendarEvent {
		t.Helper()
		evs, err := e.c.ListEvents(cx, &pb.ListEventsRequest{StartUnix: start - 3600, EndUnix: start + 7200})
		if err != nil || len(evs.Events) != 1 {
			t.Fatalf("events: %v %v", evs, err)
		}
		return evs.Events[0]
	}
	ev := list()
	if ev.MyStatus != "NEEDS-ACTION" || !ev.CanRespond || ev.OrganizerEmail != "boss@x" || len(ev.Attendees) != 2 ||
		!ev.Attendees[1].IsMe || ev.Attendees[0].IsMe || ev.Attendees[0].Name != "Boss" {
		t.Fatalf("invite fields: %v", ev)
	}
	if _, err := e.c.RespondToEvent(cx, &pb.RespondToEventRequest{EventId: ev.Id, Status: "TENTATIVE"}); err != nil {
		t.Fatal(err)
	}
	if ev = list(); ev.MyStatus != "TENTATIVE" || !ev.CanRespond {
		t.Fatalf("after respond: %v", ev)
	}
	_, err := e.c.RespondToEvent(cx, &pb.RespondToEventRequest{EventId: ev.Id, Status: "MAYBE"})
	wantCode(t, err, codes.InvalidArgument, "bad status")
	_, err = e.c.RespondToEvent(cx, &pb.RespondToEventRequest{EventId: "x", Status: "ACCEPTED"})
	wantCode(t, err, codes.InvalidArgument, "bad id")
	_, err = e.c.RespondToEvent(cx, &pb.RespondToEventRequest{EventId: "999-1", Status: "ACCEPTED"})
	wantCode(t, err, codes.NotFound, "unknown event")

	// Not invited: organizer-only event.
	solo := strings.Replace(ics, "ATTENDEE;PARTSTAT=NEEDS-ACTION;RSVP=TRUE:mailto:ME@gmail.example\r\n", "", 1)
	if err := e.st.UpsertCalendarObjects(cx, []store.CalendarObject{{CalendarID: calID, Path: "/c/i.ics", FirstStart: start, Data: solo}}); err != nil {
		t.Fatal(err)
	}
	if ev = list(); ev.CanRespond || ev.MyStatus != "" {
		t.Fatalf("not invited: %v", ev)
	}
	_, err = e.c.RespondToEvent(cx, &pb.RespondToEventRequest{EventId: ev.Id, Status: "ACCEPTED"})
	wantCode(t, err, codes.FailedPrecondition, "not invited")
}

func TestContactEditingRPCs(t *testing.T) {
	e := setup(t, true)
	cx := ctx(t)
	e.srv.SetContactEditor(NewOfflineContacts(e.st, e.broker))
	if _, err := e.st.UpsertAddressBook(cx, store.AddressBook{AccountID: "gm", Path: "/card/", Name: "Contacts"}); err != nil {
		t.Fatal(err)
	}
	c, err := e.c.SaveContact(cx, &pb.SaveContactRequest{AccountId: "gm", FullName: "Ana Horvat", Emails: []string{"ana@x.hr"}, Phones: []string{"091"}})
	if err != nil || c.Id == "" || c.FullName != "Ana Horvat" || c.Emails[0] != "ana@x.hr" || c.ReadOnly {
		t.Fatalf("create: %v %v", c, err)
	}
	c, err = e.c.SaveContact(cx, &pb.SaveContactRequest{ContactId: c.Id, FullName: "Ana Horvat", Organization: "Acme", Emails: []string{"ana@x.hr", "a2@x.hr"}})
	if err != nil || c.Organization != "Acme" || len(c.Emails) != 2 || len(c.Phones) != 0 {
		t.Fatalf("update: %v %v", c, err)
	}
	_, err = e.c.SaveContact(cx, &pb.SaveContactRequest{AccountId: "gm", Emails: []string{"bad"}})
	wantCode(t, err, codes.InvalidArgument, "invalid email")
	_, err = e.c.SaveContact(cx, &pb.SaveContactRequest{FullName: "x"})
	wantCode(t, err, codes.InvalidArgument, "no account")
	_, err = e.c.SaveContact(cx, &pb.SaveContactRequest{AccountId: "wk", FullName: "x"})
	wantCode(t, err, codes.FailedPrecondition, "account without address book")

	if _, err := e.c.DeleteContact(cx, &pb.ContactRequest{ContactId: c.Id}); err != nil {
		t.Fatal(err)
	}
	cs, _ := e.c.ListContacts(cx, &pb.ListContactsRequest{})
	if len(cs.Contacts) != 0 {
		t.Fatalf("after delete: %v", cs)
	}
	_, err = e.c.DeleteContact(cx, &pb.ContactRequest{ContactId: c.Id})
	wantCode(t, err, codes.NotFound, "delete twice")
}

func TestListMessagesByAddress(t *testing.T) {
	e := setup(t, true)
	cx := ctx(t)
	all, err := e.c.ListMessages(cx, &pb.ListMessagesRequest{FolderId: e.inbox, Limit: 100})
	if err != nil || len(all.Messages) == 0 {
		t.Fatalf("inbox: %v %v", all, err)
	}
	from := all.Messages[0].FromAddress
	got, err := e.c.ListMessagesByAddress(cx, &pb.AddressMessagesRequest{Addresses: []string{strings.ToUpper(from)}})
	if err != nil || len(got.Messages) == 0 {
		t.Fatalf("by %s: %v %v", from, got, err)
	}
	for i, m := range got.Messages {
		if i > 0 && m.TimestampUnix > got.Messages[i-1].TimestampUnix {
			t.Error("not newest first")
		}
	}
	none, _ := e.c.ListMessagesByAddress(cx, &pb.AddressMessagesRequest{Addresses: []string{"nobody@nowhere.example"}})
	if len(none.Messages) != 0 {
		t.Errorf("unknown address: %v", none)
	}
	// A substring of an address must not match ("x.com" vs "a@x.com").
	part := from[strings.Index(from, "@")+1:]
	sub, _ := e.c.ListMessagesByAddress(cx, &pb.AddressMessagesRequest{Addresses: []string{part}})
	if len(sub.Messages) != 0 {
		t.Errorf("domain-only matched: %v", sub)
	}
}
