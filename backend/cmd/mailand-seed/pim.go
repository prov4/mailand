package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"mailand/backend/internal/dav"
	"mailand/backend/internal/store"
)

// seedPIM adds sample calendars (with recurring and all-day events around
// now) and contacts, so the Calendar and Contacts views work offline.
func seedPIM(ctx context.Context, st *store.Store, now time.Time) (ncal, nev, ncon int, err error) {
	type calDef struct{ account, path, name, color string }
	cals := []calDef{
		{"gmail", "/caldav/v2/demo/personal/", "Personal", "#4f8cff"},
		{"gmail", "/caldav/v2/demo/family/", "Obitelj", "#e5a03a"},
		{"work", "/dav/calendars/me/work/", "Work", "#43b581"},
	}
	ids, paths := map[string]string{}, map[string]string{}
	for _, c := range cals {
		paths[c.name] = c.path
		id, err := st.UpsertCalendar(ctx, store.Calendar{AccountID: c.account, Path: c.path, Name: c.name, Color: c.color})
		if err != nil {
			return 0, 0, 0, err
		}
		ids[c.name] = id
	}

	day := func(offset int) time.Time {
		d := now.AddDate(0, 0, offset)
		return time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, now.Location())
	}
	at := func(offset, h, m int) time.Time {
		return day(offset).Add(time.Duration(h)*time.Hour + time.Duration(m)*time.Minute)
	}
	// Floating local times (no TZID) are read in the daemon's zone, like the seed's `now`.
	dt := func(t time.Time) string { return t.Format("20060102T150405") }
	date := func(t time.Time) string { return t.Format("20060102") }
	monday := day(-int((now.Weekday() + 6) % 7))

	type evDef struct {
		cal, uid, props string
	}
	evs := []evDef{
		{"Work", "standup", "SUMMARY:Daily standup\nLOCATION:Meet\nDTSTART:" + dt(monday.Add(9*time.Hour+30*time.Minute)) +
			"\nDTEND:" + dt(monday.Add(9*time.Hour+45*time.Minute)) + "\nRRULE:FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR\nEXDATE:" +
			dt(monday.AddDate(0, 0, 9).Add(9*time.Hour+30*time.Minute))},
		{"Work", "review", "SUMMARY:Sprint review\nDESCRIPTION:Demo of the calendar view.\\nBring coffee.\nORGANIZER;CN=Tomislav Božić:mailto:tomislav@example.hr\nDTSTART:" +
			dt(at(2, 14, 0)) + "\nDTEND:" + dt(at(2, 15, 30))},
		{"Work", "1on1", "SUMMARY:1:1 with Željka\nDTSTART:" + dt(at(0, 11, 0)) + "\nDTEND:" + dt(at(0, 11, 30)) + "\nRRULE:FREQ=WEEKLY;INTERVAL=2"},
		{"Work", "offsite", "SUMMARY:Team offsite\nLOCATION:Rijeka\nDTSTART;VALUE=DATE:" + date(day(8)) + "\nDTEND;VALUE=DATE:" + date(day(11))},
		{"Personal", "lunch", "SUMMARY:Lunch with Ana\nLOCATION:Ćevabdžinica Zagreb\nDTSTART:" + dt(at(0, 12, 30)) + "\nDTEND:" + dt(at(0, 13, 30))},
		{"Personal", "dentist", "SUMMARY:Zubar\nLOCATION:Ilica 10\nDTSTART:" + dt(at(3, 15, 0)) + "\nDTEND:" + dt(at(3, 15, 45))},
		{"Personal", "gym", "SUMMARY:Teretana\nDTSTART:" + dt(at(-14, 18, 0)) + "\nDTEND:" + dt(at(-14, 19, 0)) + "\nRRULE:FREQ=WEEKLY;BYDAY=TU,TH"},
		{"Personal", "bills", "SUMMARY:Platiti račune\nDTSTART;VALUE=DATE:" + date(time.Date(now.Year(), now.Month(), 15, 0, 0, 0, 0, now.Location()).AddDate(0, -1, 0)) + "\nRRULE:FREQ=MONTHLY;BYMONTHDAY=15"},
		{"Personal", "flight", "SUMMARY:Let ZAG → LHR\nLOCATION:Zračna luka Franjo Tuđman\nSTATUS:TENTATIVE\nDTSTART:" + dt(at(5, 7, 10)) + "\nDTEND:" + dt(at(5, 9, 25))},
		{"Obitelj", "bday", "SUMMARY:Rođendan – Ana\nDTSTART;VALUE=DATE:" + date(day(1).AddDate(-30, 0, 0)) + "\nRRULE:FREQ=YEARLY"},
		{"Obitelj", "dinner", "SUMMARY:Nedjeljni ručak\nDTSTART:" + dt(at(int((7-now.Weekday())%7), 13, 0)) + "\nDTEND:" + dt(at(int((7-now.Weekday())%7), 15, 0)) + "\nRRULE:FREQ=WEEKLY"},
		{"Obitelj", "late", "SUMMARY:Koncert\nDTSTART:" + dt(at(4, 20, 0)) + "\nDTEND:" + dt(at(4, 23, 0))},
	}
	byCal := map[string][]store.CalendarObject{}
	for _, e := range evs {
		data := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//mailand//seed//EN\r\nBEGIN:VEVENT\r\nUID:" + e.uid +
			"@seed\r\nDTSTAMP:" + now.UTC().Format("20060102T150405Z") + "\r\n" + strings.ReplaceAll(e.props, "\n", "\r\n") +
			"\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
		info, err := dav.StoredInfo(data, now.Location())
		if err != nil {
			return 0, 0, 0, fmt.Errorf("seed event %s: %w", e.uid, err)
		}
		byCal[e.cal] = append(byCal[e.cal], store.CalendarObject{CalendarID: ids[e.cal], Path: paths[e.cal] + e.uid + ".ics",
			ETag: "1", UID: info.UID, Summary: info.Summary, FirstStart: info.FirstStart, LastEnd: info.LastEnd, Data: data})
	}
	for _, objs := range byCal {
		if err := st.UpsertCalendarObjects(ctx, objs); err != nil {
			return 0, 0, 0, err
		}
	}

	books := map[string]string{}
	for _, b := range []store.AddressBook{
		{AccountID: "gmail", Path: "/carddav/v1/principals/demo/lists/default/", Name: "Contacts"},
		{AccountID: "work", Path: "/dav/addressbooks/me/work/", Name: "Work"},
	} {
		id, err := st.UpsertAddressBook(ctx, b)
		if err != nil {
			return 0, 0, 0, err
		}
		books[b.AccountID] = id
	}
	people := []struct {
		account, name, org string
		emails, phones     []string
	}{
		{"gmail", "Ana Horvat", "", []string{"ana.horvat@example.hr"}, []string{"+385 91 123 4567"}},
		{"gmail", "Tomislav Božić", "Posao d.o.o.", []string{"tomislav@example.hr", "tbozic@posao.example"}, []string{"+385 98 765 432"}},
		{"gmail", "Željka Vuković", "", []string{"zeljka.vukovic@example.hr"}, nil},
		{"gmail", "James Carter", "", []string{"james.carter@example.com"}, []string{"+44 20 7946 0958"}},
		{"gmail", "Marko Marić", "", []string{"marko.maric@example.hr"}, []string{"+385 95 555 0101"}},
		{"gmail", "Ivana Kovačević", "Klinika Ilica", []string{"ivana@klinika.example"}, []string{"+385 1 4800 100"}},
		{"gmail", "Luka Novak", "", []string{"luka.novak@example.si"}, nil},
		{"gmail", "Petra Jurić", "", nil, []string{"+385 99 222 3344"}},
		{"gmail", "HT Račun", "Hrvatski Telekom", []string{"racun@ht.example.hr"}, nil},
		{"gmail", "Booking Demo", "", []string{"noreply@booking.example"}, nil},
		{"work", "Nikola Babić", "Work Inc.", []string{"nikola.babic@work.example"}, []string{"+385 1 6000 200"}},
		{"work", "Sara Perić", "Work Inc.", []string{"sara.peric@work.example"}, nil},
		{"work", "Ops On-call", "Work Inc.", []string{"oncall@work.example"}, []string{"+385 1 6000 999"}},
		{"work", "Dario Knežević", "Vendor Ltd", []string{"dario@vendor.example"}, nil},
	}
	var rows []store.Contact
	for i, p := range people {
		var vc strings.Builder
		vc.WriteString("BEGIN:VCARD\r\nVERSION:3.0\r\nUID:seed-" + fmt.Sprint(i) + "\r\nFN:" + p.name + "\r\n")
		for _, e := range p.emails {
			vc.WriteString("EMAIL:" + e + "\r\n")
		}
		for _, t := range p.phones {
			vc.WriteString("TEL:" + t + "\r\n")
		}
		if p.org != "" {
			vc.WriteString("ORG:" + p.org + "\r\n")
		}
		vc.WriteString("END:VCARD\r\n")
		rows = append(rows, store.Contact{AddressBookID: books[p.account], Path: fmt.Sprintf("/seed/%d.vcf", i), ETag: "1",
			UID: fmt.Sprintf("seed-%d", i), FullName: p.name, Emails: p.emails, Phones: p.phones, Organization: p.org, Data: vc.String()})
	}
	if err := st.UpsertContacts(ctx, rows); err != nil {
		return 0, 0, 0, err
	}
	return len(cals), len(evs), len(rows), nil
}
