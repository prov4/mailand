package dav

import (
	"errors"
	"strings"
	"time"

	"github.com/emersion/go-ical"
)

// Attendee is one ATTENDEE of an event.
type Attendee struct {
	Name, Email string
	Status      string // PARTSTAT: NEEDS-ACTION, ACCEPTED, TENTATIVE, DECLINED, DELEGATED
	Role        string // REQ-PARTICIPANT, OPT-PARTICIPANT, CHAIR, NON-PARTICIPANT
}

// Responses an attendee can give (iCalendar PARTSTAT values).
const (
	Accepted    = "ACCEPTED"
	Tentative   = "TENTATIVE"
	Declined    = "DECLINED"
	NeedsAction = "NEEDS-ACTION"
)

var (
	// ErrNotInvited: the account is not an attendee (or is the organizer).
	ErrNotInvited = errors.New("you are not invited to this event")
	// ErrConflict: the event changed on the server since it was synced.
	ErrConflict = errors.New("the event changed on the server")
	// ErrBadResponse: not one of Accepted, Tentative, Declined.
	ErrBadResponse = errors.New("response must be ACCEPTED, TENTATIVE or DECLINED")
)

// addrOf returns the lower-cased email of a CAL-ADDRESS ("mailto:x@y").
func addrOf(p *ical.Prop) string {
	v := strings.TrimSpace(p.Value)
	if len(v) >= 7 && strings.EqualFold(v[:7], "mailto:") {
		v = v[7:]
	}
	if e := p.Params.Get("EMAIL"); e != "" && !strings.Contains(v, "@") {
		v = e
	}
	return strings.ToLower(v)
}

func organizerEmail(ev ical.Event) string {
	if p := ev.Props.Get(ical.PropOrganizer); p != nil {
		return addrOf(p)
	}
	return ""
}

func attendees(ev ical.Event) []Attendee {
	var out []Attendee
	for i := range ev.Props[ical.PropAttendee] {
		p := &ev.Props[ical.PropAttendee][i]
		st := strings.ToUpper(p.Params.Get(ical.ParamParticipationStatus))
		if st == "" {
			st = NeedsAction
		}
		role := strings.ToUpper(p.Params.Get(ical.ParamRole))
		if role == "" {
			role = "REQ-PARTICIPANT"
		}
		out = append(out, Attendee{Name: p.Params.Get(ical.ParamCommonName), Email: addrOf(p), Status: st, Role: role})
	}
	return out
}

// MyStatus returns email's PARTSTAT in the attendee list ("" if absent) and
// whether email may respond (an attendee who is not the organizer).
func MyStatus(in Instance, email string) (status string, canRespond bool) {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return "", false
	}
	for _, a := range in.Attendees {
		if a.Email == email {
			return a.Status, in.OrganizerEmail != email
		}
	}
	return "", false
}

// SetResponse sets email's PARTSTAT in every VEVENT of cal (master and
// overrides), drops its RSVP request and refreshes DTSTAMP. It fails with
// ErrNotInvited if email is not an attendee anywhere or is the organizer.
func SetResponse(cal *ical.Calendar, email, status string, now time.Time) error {
	switch status {
	case Accepted, Tentative, Declined:
	default:
		return ErrBadResponse
	}
	email = strings.ToLower(strings.TrimSpace(email))
	found := false
	for _, ev := range cal.Events() {
		if organizerEmail(ev) == email {
			return ErrNotInvited
		}
		touched := false
		for i := range ev.Props[ical.PropAttendee] {
			p := &ev.Props[ical.PropAttendee][i]
			if addrOf(p) != email {
				continue
			}
			p.Params.Set(ical.ParamParticipationStatus, status)
			p.Params.Del(ical.ParamRSVP)
			touched = true
		}
		if touched {
			found = true
			ev.Props.SetDateTime(ical.PropDateTimeStamp, now.UTC())
		}
	}
	if !found {
		return ErrNotInvited
	}
	return nil
}

// RespondData applies SetResponse to stored iCalendar text and returns the new text.
func RespondData(data, email, status string, now time.Time) (string, error) {
	cal, err := decodeICal(data)
	if err != nil {
		return "", err
	}
	if err := SetResponse(cal, email, status, now); err != nil {
		return "", err
	}
	return encodeICal(cal), nil
}
