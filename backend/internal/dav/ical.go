// Package dav syncs calendars (CalDAV) and contacts (CardDAV) into the store
// and turns stored iCalendar objects into event instances for the UI.
package dav

import (
	"errors"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/emersion/go-ical"
)

// Instance is one occurrence of an event.
type Instance struct {
	UID, Summary, Location, Description string
	Status, Organizer                   string
	Start, End                          time.Time
	AllDay, Recurring                   bool
}

// maxInstances bounds the instances of one object returned for one query;
// maxSteps bounds the occurrences walked to get there (an hourly rule that
// started years ago).
const (
	maxInstances = 2000
	maxSteps     = 200000
)

var errNoStart = errors.New("dav: event has no DTSTART")

// windowsZones maps Windows time zone names (Outlook/Exchange TZIDs) to IANA.
var windowsZones = map[string]string{
	"UTC": "UTC", "GMT Standard Time": "Europe/London", "Greenwich Standard Time": "Atlantic/Reykjavik",
	"W. Europe Standard Time": "Europe/Berlin", "Central Europe Standard Time": "Europe/Budapest",
	"Central European Standard Time": "Europe/Warsaw", "Romance Standard Time": "Europe/Paris",
	"E. Europe Standard Time": "Europe/Chisinau", "FLE Standard Time": "Europe/Kiev",
	"GTB Standard Time": "Europe/Bucharest", "Russian Standard Time": "Europe/Moscow",
	"Turkey Standard Time": "Europe/Istanbul", "Eastern Standard Time": "America/New_York",
	"Central Standard Time": "America/Chicago", "Mountain Standard Time": "America/Denver",
	"Pacific Standard Time": "America/Los_Angeles", "India Standard Time": "Asia/Kolkata",
	"China Standard Time": "Asia/Shanghai", "Tokyo Standard Time": "Asia/Tokyo",
	"AUS Eastern Standard Time": "Australia/Sydney", "Singapore Standard Time": "Asia/Singapore",
}

var (
	zoneMu    sync.Mutex
	zoneCache = map[string]*time.Location{}
)

// lookupZone resolves a TZID to a location, or nil if it is unknown. Besides
// IANA names it accepts Windows names and prefixed forms such as
// "/mozilla.org/20050126_1/Europe/Berlin".
func lookupZone(tzid string) *time.Location {
	zoneMu.Lock()
	defer zoneMu.Unlock()
	if loc, ok := zoneCache[tzid]; ok {
		return loc
	}
	var loc *time.Location
	cands := []string{tzid, windowsZones[tzid]}
	if parts := strings.Split(strings.Trim(tzid, "/"), "/"); len(parts) >= 2 {
		cands = append(cands, strings.Join(parts[len(parts)-2:], "/"), strings.Join(parts[len(parts)-1:], "/"))
	}
	for _, c := range cands {
		if c == "" {
			continue
		}
		if l, err := time.LoadLocation(c); err == nil {
			loc = l
			break
		}
	}
	zoneCache[tzid] = loc
	return loc
}

var dateProps = []string{ical.PropDateTimeStart, ical.PropDateTimeEnd, ical.PropRecurrenceID,
	ical.PropExceptionDates, ical.PropRecurrenceDates}

// normalizeZones rewrites TZIDs so go-ical can parse every date: known
// aliases become IANA names; unknown ones are dropped (floating = local time).
func normalizeZones(cal *ical.Calendar) {
	for _, ev := range cal.Events() {
		for _, name := range dateProps {
			for i := range ev.Props[name] {
				p := &ev.Props[name][i]
				tzid := p.Params.Get(ical.PropTimezoneID)
				if tzid == "" {
					continue
				}
				if loc := lookupZone(tzid); loc != nil {
					p.Params.Set(ical.PropTimezoneID, loc.String())
				} else {
					p.Params.Del(ical.PropTimezoneID)
				}
			}
		}
	}
}

func isDate(p *ical.Prop) bool {
	vt := p.ValueType()
	return vt == ical.ValueDate || (vt == ical.ValueDefault && len(p.Value) == 8)
}

type span struct {
	start, end time.Time
	allDay     bool
	days       int // all-day length in days
}

// eventSpan reads DTSTART and DTEND/DURATION; all-day dates are local midnights.
func eventSpan(ev ical.Event, local *time.Location) (span, error) {
	sp := ev.Props.Get(ical.PropDateTimeStart)
	if sp == nil {
		return span{}, errNoStart
	}
	start, err := sp.DateTime(local)
	if err != nil {
		return span{}, err
	}
	s := span{start: start, allDay: isDate(sp)}
	switch {
	case ev.Props.Get(ical.PropDateTimeEnd) != nil:
		if s.end, err = ev.Props.Get(ical.PropDateTimeEnd).DateTime(local); err != nil {
			return span{}, err
		}
	case ev.Props.Get(ical.PropDuration) != nil:
		d, err := ev.Props.Get(ical.PropDuration).Duration()
		if err != nil {
			return span{}, err
		}
		s.end = start.Add(d)
	case s.allDay:
		s.end = start.AddDate(0, 0, 1)
	default:
		s.end = start
	}
	if s.end.Before(s.start) {
		s.end = s.start
	}
	if s.allDay {
		s.days = max(1, int((s.end.Sub(s.start)+12*time.Hour)/(24*time.Hour)))
		s.end = s.start.AddDate(0, 0, s.days)
	}
	return s, nil
}

func (s span) at(occ time.Time) (time.Time, time.Time) {
	if s.allDay {
		return occ, occ.AddDate(0, 0, s.days)
	}
	return occ, occ.Add(s.end.Sub(s.start))
}

func text(ev ical.Event, name string) string {
	t, _ := ev.Props.Text(name)
	return strings.TrimSpace(t)
}

func cancelled(ev ical.Event) bool {
	return strings.EqualFold(text(ev, ical.PropStatus), "CANCELLED")
}

func organizer(ev ical.Event) string {
	p := ev.Props.Get(ical.PropOrganizer)
	if p == nil {
		return ""
	}
	if cn := p.Params.Get(ical.ParamCommonName); cn != "" {
		return cn
	}
	return strings.TrimPrefix(strings.TrimPrefix(p.Value, "mailto:"), "MAILTO:")
}

type series struct {
	master    *ical.Event
	overrides map[int64]ical.Event // by RECURRENCE-ID (unix)
	orphans   []ical.Event         // overrides whose master is not in this object
}

func groupEvents(cal *ical.Calendar, local *time.Location) ([]string, map[string]*series) {
	var order []string
	byUID := map[string]*series{}
	for _, ev := range cal.Events() {
		uid := text(ev, ical.PropUID)
		s := byUID[uid]
		if s == nil {
			s = &series{overrides: map[int64]ical.Event{}}
			byUID[uid] = s
			order = append(order, uid)
		}
		if rid := ev.Props.Get(ical.PropRecurrenceID); rid != nil {
			if t, err := rid.DateTime(local); err == nil {
				s.overrides[t.Unix()] = ev
				continue
			}
		}
		if s.master == nil {
			e := ev
			s.master = &e
		}
	}
	for _, s := range byUID {
		if s.master == nil {
			for _, ov := range s.overrides {
				s.orphans = append(s.orphans, ov)
			}
			s.overrides = map[int64]ical.Event{}
		}
	}
	return order, byUID
}

func instance(ev ical.Event, start, end time.Time, allDay, recurring bool) Instance {
	return Instance{
		UID: text(ev, ical.PropUID), Summary: text(ev, ical.PropSummary),
		Location: text(ev, ical.PropLocation), Description: text(ev, ical.PropDescription),
		Status: strings.ToUpper(text(ev, ical.PropStatus)), Organizer: organizer(ev),
		Start: start, End: end, AllDay: allDay, Recurring: recurring,
	}
}

func overlaps(start, end, from, to time.Time) bool {
	if !start.Before(to) {
		return false
	}
	return end.After(from) || (end.Equal(start) && !start.Before(from))
}

// Expand returns the instances of all events in cal that overlap [from, to),
// sorted by start. Cancelled events and occurrences are left out; occurrences
// replaced by an override (RECURRENCE-ID) appear with the override's data.
func Expand(cal *ical.Calendar, from, to time.Time, local *time.Location) []Instance {
	normalizeZones(cal)
	order, byUID := groupEvents(cal, local)
	var out []Instance
	addSingle := func(ev ical.Event, recurring bool) {
		if cancelled(ev) {
			return
		}
		sp, err := eventSpan(ev, local)
		if err == nil && overlaps(sp.start, sp.end, from, to) {
			out = append(out, instance(ev, sp.start, sp.end, sp.allDay, recurring))
		}
	}
	for _, uid := range order {
		s := byUID[uid]
		for _, ov := range s.orphans {
			addSingle(ov, true)
		}
		if s.master == nil {
			continue
		}
		m := *s.master
		if cancelled(m) {
			continue
		}
		sp, err := eventSpan(m, local)
		if err != nil {
			continue
		}
		rs, err := m.RecurrenceSet(local)
		if err != nil || rs == nil {
			addSingle(m, false)
			continue
		}
		n, steps := 0, 0
		it := rs.Iterator()
		for occ, ok := it(); ok && n < maxInstances && steps < maxSteps; occ, ok = it() {
			steps++
			if !occ.Before(to) {
				break
			}
			if _, replaced := s.overrides[occ.Unix()]; replaced {
				continue
			}
			st, en := sp.at(occ)
			if overlaps(st, en, from, to) {
				out = append(out, instance(m, st, en, sp.allDay, true))
				n++
			}
		}
		for _, ov := range s.overrides {
			addSingle(ov, true)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].Start.Equal(out[j].Start) {
			return out[i].Start.Before(out[j].Start)
		}
		return out[i].AllDay && !out[j].AllDay
	})
	return out
}

// Info holds the indexed columns of a calendar object.
type Info struct {
	UID, Summary string
	FirstStart   int64
	LastEnd      *int64 // nil: recurs forever
	HasEvents    bool
}

// ObjectInfo computes the time bounds of all instances in cal (the store
// filters on them before Expand). Objects without events (to-dos, journals)
// get HasEvents=false and an empty range.
func ObjectInfo(cal *ical.Calendar, local *time.Location) Info {
	normalizeZones(cal)
	order, byUID := groupEvents(cal, local)
	var info Info
	first, last := int64(0), int64(0)
	forever := false
	note := func(st, en time.Time) {
		if !info.HasEvents || st.Unix() < first {
			first = st.Unix()
		}
		if !info.HasEvents || en.Unix() > last {
			last = en.Unix()
		}
		info.HasEvents = true
	}
	for _, uid := range order {
		s := byUID[uid]
		evs := slices.Clone(s.orphans)
		for _, ov := range s.overrides {
			evs = append(evs, ov)
		}
		if s.master != nil {
			m := *s.master
			if info.UID == "" {
				info.UID, info.Summary = uid, text(m, ical.PropSummary)
			}
			if sp, err := eventSpan(m, local); err == nil {
				note(sp.start, sp.end)
				if opt, err := m.Props.RecurrenceRule(); err == nil && opt != nil {
					switch {
					case !opt.Until.IsZero():
						_, en := sp.at(opt.Until)
						note(sp.start, en)
					case opt.Count > 0 && opt.Count <= 10000:
						if rs, err := m.RecurrenceSet(local); err == nil && rs != nil {
							if all := rs.All(); len(all) > 0 {
								_, en := sp.at(all[len(all)-1])
								note(sp.start, en)
							}
						}
					default:
						forever = true
					}
				}
				for _, rd := range m.Props[ical.PropRecurrenceDates] {
					if t, err := rd.DateTime(local); err == nil {
						st, en := sp.at(t)
						note(st, en)
					}
				}
			}
		}
		for _, ev := range evs {
			if info.UID == "" {
				info.UID, info.Summary = uid, text(ev, ical.PropSummary)
			}
			if sp, err := eventSpan(ev, local); err == nil {
				note(sp.start, sp.end)
			}
		}
	}
	info.FirstStart = first
	if !forever {
		info.LastEnd = &last
	}
	return info
}

// ExpandStored is Expand for an object as kept in the store.
func ExpandStored(data string, from, to time.Time, local *time.Location) ([]Instance, error) {
	cal, err := decodeICal(data)
	if err != nil {
		return nil, err
	}
	return Expand(cal, from, to, local), nil
}

// StoredInfo is ObjectInfo for iCalendar text.
func StoredInfo(data string, local *time.Location) (Info, error) {
	cal, err := decodeICal(data)
	if err != nil {
		return Info{}, err
	}
	return ObjectInfo(cal, local), nil
}
