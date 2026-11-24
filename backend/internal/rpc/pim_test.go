package rpc

import (
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
