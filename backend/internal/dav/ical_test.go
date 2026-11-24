package dav

import (
	"strings"
	"testing"
	"time"
)

func mustCal(t *testing.T, body string) string {
	t.Helper()
	return "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//test//EN\r\n" + strings.ReplaceAll(strings.TrimSpace(body), "\n", "\r\n") + "\r\nEND:VCALENDAR\r\n"
}

func expand(t *testing.T, data string, from, to time.Time, loc *time.Location) []Instance {
	t.Helper()
	cal, err := decodeICal(data)
	if err != nil {
		t.Fatal(err)
	}
	return Expand(cal, from, to, loc)
}

func TestExpandWeeklyWithOverrideAndExdate(t *testing.T) {
	zg, _ := time.LoadLocation("Europe/Zagreb")
	data := mustCal(t, `
BEGIN:VEVENT
UID:standup
SUMMARY:Standup
DTSTART;TZID=Europe/Zagreb:20261102T093000
DTEND;TZID=Europe/Zagreb:20261102T094500
RRULE:FREQ=WEEKLY;BYDAY=MO;COUNT=6
EXDATE;TZID=Europe/Zagreb:20261116T093000
END:VEVENT
BEGIN:VEVENT
UID:standup
RECURRENCE-ID;TZID=Europe/Zagreb:20261109T093000
SUMMARY:Standup (moved)
DTSTART;TZID=Europe/Zagreb:20261110T100000
DTEND;TZID=Europe/Zagreb:20261110T101500
END:VEVENT
BEGIN:VEVENT
UID:standup
RECURRENCE-ID;TZID=Europe/Zagreb:20261123T093000
STATUS:CANCELLED
DTSTART;TZID=Europe/Zagreb:20261123T093000
END:VEVENT`)
	from := time.Date(2026, 11, 1, 0, 0, 0, 0, zg)
	got := expand(t, data, from, from.AddDate(0, 2, 0), zg)
	var days []string
	for _, in := range got {
		days = append(days, in.Start.In(zg).Format("01-02 15:04")+" "+in.Summary)
		if !in.Recurring {
			t.Errorf("%v not marked recurring", in)
		}
	}
	// 6 Mondays from 11-02: 02, 09 (moved to 10th), 16 (EXDATE), 23 (cancelled), 30, 12-07.
	want := "[11-02 09:30 Standup 11-10 10:00 Standup (moved) 11-30 09:30 Standup 12-07 09:30 Standup]"
	if got := strings.Join([]string{"[", strings.Join(days, " "), "]"}, ""); got != want {
		t.Errorf("instances\n got %s\nwant %s", got, want)
	}
	if got[0].End.Sub(got[0].Start) != 15*time.Minute {
		t.Errorf("duration %v", got[0].End.Sub(got[0].Start))
	}
}

func TestExpandAllDayAndRangeEdges(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	data := mustCal(t, `
BEGIN:VEVENT
UID:trip
SUMMARY:Trip
DTSTART;VALUE=DATE:20261124
DTEND;VALUE=DATE:20261127
END:VEVENT
BEGIN:VEVENT
UID:bday
SUMMARY:Birthday
DTSTART;VALUE=DATE:20000310
RRULE:FREQ=YEARLY
END:VEVENT`)
	day := func(m time.Month, d int) time.Time { return time.Date(2026, m, d, 0, 0, 0, 0, loc) }
	got := expand(t, data, day(11, 26), day(11, 27), loc)
	if len(got) != 1 || got[0].Summary != "Trip" || !got[0].AllDay || !got[0].Start.Equal(day(11, 24)) || !got[0].End.Equal(day(11, 27)) {
		t.Fatalf("trip on its last day: %+v", got)
	}
	if got := expand(t, data, day(11, 27), day(11, 28), loc); len(got) != 0 {
		t.Fatalf("exclusive DTEND: %+v", got)
	}
	got = expand(t, data, day(3, 1), day(4, 1), loc)
	if len(got) != 1 || got[0].Summary != "Birthday" || !got[0].Start.Equal(day(3, 10)) || !got[0].End.Equal(day(3, 11)) {
		t.Fatalf("yearly all-day: %+v", got)
	}
}

func TestUnknownAndWindowsTimeZones(t *testing.T) {
	data := mustCal(t, `
BEGIN:VEVENT
UID:a
SUMMARY:Outlook
DTSTART;TZID=W. Europe Standard Time:20261124T150000
DTEND;TZID=W. Europe Standard Time:20261124T160000
END:VEVENT
BEGIN:VEVENT
UID:b
SUMMARY:Weird zone
DTSTART;TZID=Mars/Olympus:20261124T150000
DTEND;TZID=Mars/Olympus:20261124T160000
END:VEVENT`)
	got := expand(t, data, time.Date(2026, 11, 24, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 25, 0, 0, 0, 0, time.UTC), time.UTC)
	if len(got) != 2 {
		t.Fatalf("instances: %+v", got)
	}
	if got[0].Summary != "Outlook" || got[0].Start.UTC().Hour() != 14 { // CET = UTC+1
		t.Errorf("windows zone: %+v", got[0])
	}
	if got[1].Start.UTC().Hour() != 15 { // unknown zone: floating, read as local (UTC here)
		t.Errorf("unknown zone: %+v", got[1])
	}
}

func TestObjectInfoBounds(t *testing.T) {
	cases := map[string]struct {
		body    string
		forever bool
		last    string
	}{
		"single":  {"DTSTART:20261124T100000Z\nDTEND:20261124T110000Z", false, "2026-11-24T11:00:00Z"},
		"count":   {"DTSTART:20261124T100000Z\nDTEND:20261124T110000Z\nRRULE:FREQ=DAILY;COUNT=3", false, "2026-11-26T11:00:00Z"},
		"until":   {"DTSTART:20261124T100000Z\nDTEND:20261124T110000Z\nRRULE:FREQ=DAILY;UNTIL=20261130T100000Z", false, "2026-11-30T11:00:00Z"},
		"forever": {"DTSTART:20261124T100000Z\nRRULE:FREQ=WEEKLY", true, ""},
	}
	for name, tc := range cases {
		cal, err := decodeICal(mustCal(t, "BEGIN:VEVENT\nUID:x\nSUMMARY:S\n"+tc.body+"\nEND:VEVENT"))
		if err != nil {
			t.Fatal(name, err)
		}
		info := ObjectInfo(cal, time.UTC)
		if !info.HasEvents || info.UID != "x" || info.Summary != "S" || info.FirstStart != time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC).Unix() {
			t.Errorf("%s: %+v", name, info)
		}
		if tc.forever != (info.LastEnd == nil) {
			t.Errorf("%s: forever=%v", name, info.LastEnd == nil)
		} else if !tc.forever && time.Unix(*info.LastEnd, 0).UTC().Format(time.RFC3339) != tc.last {
			t.Errorf("%s: last=%s want %s", name, time.Unix(*info.LastEnd, 0).UTC().Format(time.RFC3339), tc.last)
		}
	}
}

func TestEncodeICalRoundTripsLenient(t *testing.T) {
	// No DTSTAMP and a long folded description: go-ical's encoder would refuse.
	long := strings.Repeat("Zagreb–Rijeka žđčć ", 12)
	cal, err := decodeICal(mustCal(t, "BEGIN:VEVENT\nUID:x\nSUMMARY:S\nDESCRIPTION:"+long+"\nDTSTART:20261124T100000Z\nEND:VEVENT"))
	if err != nil {
		t.Fatal(err)
	}
	out := encodeICal(cal)
	for _, line := range strings.Split(out, "\r\n") {
		if len(line) > 76 {
			t.Fatalf("unfolded line (%d): %q", len(line), line)
		}
	}
	again, err := decodeICal(out)
	if err != nil {
		t.Fatal(err)
	}
	if d, _ := again.Events()[0].Props.Text("DESCRIPTION"); d != long {
		t.Errorf("description changed:\n%q\n%q", d, long)
	}
}
