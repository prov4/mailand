package rpc

import (
	"context"
	"fmt"
	"sort"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"mailand/backend/internal/dav"
	"mailand/backend/internal/rpc/pb"
)

const (
	maxEventRange       = 400 * 24 * time.Hour
	maxEventsPerQuery   = 20000
	defaultContactLimit = 1000
	maxContactLimit     = 5000
)

// eventZone is the zone all-day events and floating times are read in: the
// daemon runs in the user's session, so this is the user's local zone.
var eventZone = time.Local

// ListCalendars returns every synced calendar of every account.
func (s *Server) ListCalendars(ctx context.Context, _ *pb.Empty) (*pb.CalendarList, error) {
	cals, err := s.st.ListCalendars(ctx, "")
	if err != nil {
		return nil, s.mapErr("ListCalendars", err, "calendar")
	}
	out := &pb.CalendarList{}
	for _, c := range cals {
		out.Calendars = append(out.Calendars, &pb.Calendar{Id: c.ID, AccountId: c.AccountID, Name: c.Name, Color: c.Color, ReadOnly: c.ReadOnly})
	}
	return out, nil
}

// ListEvents expands the events overlapping [start, end) into occurrences.
func (s *Server) ListEvents(ctx context.Context, req *pb.ListEventsRequest) (*pb.EventList, error) {
	if req.EndUnix <= req.StartUnix {
		return nil, status.Error(codes.InvalidArgument, "end_unix must be after start_unix")
	}
	from, to := time.Unix(req.StartUnix, 0), time.Unix(req.EndUnix, 0)
	if to.Sub(from) > maxEventRange {
		return nil, status.Error(codes.InvalidArgument, "range is longer than 400 days")
	}
	var ids []string
	if len(req.CalendarIds) > 0 {
		ids = req.CalendarIds
	}
	objs, err := s.st.CalendarObjectsInRange(ctx, ids, req.StartUnix, req.EndUnix)
	if err != nil {
		return nil, s.mapErr("ListEvents", err, "calendar")
	}
	out := &pb.EventList{}
	for _, o := range objs {
		inst, err := dav.ExpandStored(o.Data, from, to, eventZone)
		if err != nil {
			s.log.Warn("unreadable calendar object", "calendar", o.CalendarID, "path", o.Path, "err", err)
			continue
		}
		for _, in := range inst {
			out.Events = append(out.Events, &pb.CalendarEvent{
				Id: fmt.Sprintf("%d-%d", o.Seq, in.Start.Unix()), CalendarId: o.CalendarID, Uid: in.UID,
				Summary: in.Summary, Location: in.Location, Description: in.Description,
				StartUnix: in.Start.Unix(), EndUnix: in.End.Unix(), AllDay: in.AllDay, Recurring: in.Recurring,
				Organizer: in.Organizer, Status: in.Status,
			})
			if len(out.Events) >= maxEventsPerQuery {
				return nil, status.Error(codes.ResourceExhausted, "too many events in range; ask for a shorter range")
			}
		}
	}
	sortEvents(out.Events)
	return out, nil
}

func sortEvents(evs []*pb.CalendarEvent) {
	sort.SliceStable(evs, func(i, j int) bool {
		a, b := evs[i], evs[j]
		if a.StartUnix != b.StartUnix {
			return a.StartUnix < b.StartUnix
		}
		if a.AllDay != b.AllDay {
			return a.AllDay
		}
		return a.Summary < b.Summary
	})
}

// ListContacts searches synced contacts by name, email or organization.
func (s *Server) ListContacts(ctx context.Context, req *pb.ListContactsRequest) (*pb.ContactList, error) {
	limit := int(req.Limit)
	if limit == 0 {
		limit = defaultContactLimit
	}
	cs, err := s.st.SearchContacts(ctx, req.Query, req.EmailOnly, min(limit, maxContactLimit))
	if err != nil {
		return nil, s.mapErr("ListContacts", err, "contact")
	}
	out := &pb.ContactList{}
	for _, c := range cs {
		out.Contacts = append(out.Contacts, &pb.Contact{
			Id: fmt.Sprint(c.Seq), AccountId: c.AccountID, AddressBookId: c.AddressBookID, FullName: c.FullName,
			Emails: c.Emails, Phones: c.Phones, Organization: c.Organization,
		})
	}
	return out, nil
}
