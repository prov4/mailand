package rpc

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"mailand/backend/internal/auth"
	"mailand/backend/internal/dav"
	"mailand/backend/internal/rpc/pb"
	"mailand/backend/internal/store"
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
	// The account address of each calendar decides "my" attendance.
	cals, err := s.st.ListCalendars(ctx, "")
	if err != nil {
		return nil, s.mapErr("ListEvents", err, "calendar")
	}
	accs, err := s.st.ListAccounts(ctx)
	if err != nil {
		return nil, s.mapErr("ListEvents", err, "account")
	}
	email := map[string]string{}
	for _, a := range accs {
		email[a.ID] = strings.ToLower(a.Email)
	}
	type calInfo struct {
		email    string
		readOnly bool
	}
	calOf := map[string]calInfo{}
	for _, c := range cals {
		calOf[c.ID] = calInfo{email[c.AccountID], c.ReadOnly}
	}

	out := &pb.EventList{}
	for _, o := range objs {
		inst, err := dav.ExpandStored(o.Data, from, to, eventZone)
		if err != nil {
			s.log.Warn("unreadable calendar object", "calendar", o.CalendarID, "path", o.Path, "err", err)
			continue
		}
		ci := calOf[o.CalendarID]
		for _, in := range inst {
			mine, can := dav.MyStatus(in, ci.email)
			ev := &pb.CalendarEvent{
				Id: fmt.Sprintf("%d-%d", o.Seq, in.Start.Unix()), CalendarId: o.CalendarID, Uid: in.UID,
				Summary: in.Summary, Location: in.Location, Description: in.Description,
				StartUnix: in.Start.Unix(), EndUnix: in.End.Unix(), AllDay: in.AllDay, Recurring: in.Recurring,
				Organizer: in.Organizer, Status: in.Status, OrganizerEmail: in.OrganizerEmail,
				MyStatus: mine, CanRespond: can && !ci.readOnly,
			}
			for _, a := range in.Attendees {
				ev.Attendees = append(ev.Attendees, &pb.Attendee{Name: a.Name, Email: a.Email, Status: a.Status,
					Role: a.Role, IsMe: a.Email == ci.email})
			}
			out.Events = append(out.Events, ev)
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
	books, err := s.st.ListAddressBooks(ctx, "")
	if err != nil {
		return nil, s.mapErr("ListContacts", err, "contact")
	}
	ro := map[string]bool{}
	for _, b := range books {
		ro[b.ID] = b.ReadOnly
	}
	out := &pb.ContactList{}
	for _, c := range cs {
		out.Contacts = append(out.Contacts, contactPB(c, ro[c.AddressBookID]))
	}
	return out, nil
}

func contactPB(c store.Contact, readOnly bool) *pb.Contact {
	return &pb.Contact{
		Id: fmt.Sprint(c.Seq), AccountId: c.AccountID, AddressBookId: c.AddressBookID, FullName: c.FullName,
		Emails: c.Emails, Phones: c.Phones, Organization: c.Organization, ReadOnly: readOnly,
	}
}

// ContactEditor writes contacts (dav.Manager, or OfflineContacts in -offline).
type ContactEditor interface {
	SaveContact(ctx context.Context, seq int64, accountID string, in dav.ContactInput) (int64, error)
	DeleteContact(ctx context.Context, seq int64) error
}

// SetContactEditor installs the SaveContact/DeleteContact backend.
func (s *Server) SetContactEditor(e ContactEditor) {
	s.sendMu.Lock()
	s.contacts = e
	s.sendMu.Unlock()
}

func (s *Server) contactEditor() (ContactEditor, error) {
	s.sendMu.RLock()
	defer s.sendMu.RUnlock()
	if s.contacts == nil {
		return nil, status.Error(codes.FailedPrecondition, "contacts sync is not running")
	}
	return s.contacts, nil
}

func parseSeq(id string) (int64, error) {
	seq, err := strconv.ParseInt(id, 10, 64)
	if err != nil || seq <= 0 {
		return 0, status.Error(codes.InvalidArgument, "bad contact_id")
	}
	return seq, nil
}

// SaveContact creates or updates a contact.
func (s *Server) SaveContact(ctx context.Context, req *pb.SaveContactRequest) (*pb.Contact, error) {
	ed, err := s.contactEditor()
	if err != nil {
		return nil, err
	}
	var seq int64
	if req.ContactId != "" {
		if seq, err = parseSeq(req.ContactId); err != nil {
			return nil, err
		}
	} else if req.AccountId == "" {
		return nil, status.Error(codes.InvalidArgument, "account_id is required for a new contact")
	}
	in := dav.ContactInput{FullName: req.FullName, Organization: req.Organization, Emails: req.Emails, Phones: req.Phones}
	seq, err = ed.SaveContact(ctx, seq, req.AccountId, in)
	if err != nil {
		return nil, s.mapDavErr("SaveContact", err, "contact")
	}
	c, b, err := s.st.GetContact(ctx, seq)
	if err != nil {
		return nil, s.mapErr("SaveContact", err, "contact")
	}
	return contactPB(c, b.ReadOnly), nil
}

// DeleteContact removes a contact.
func (s *Server) DeleteContact(ctx context.Context, req *pb.ContactRequest) (*pb.Empty, error) {
	ed, err := s.contactEditor()
	if err != nil {
		return nil, err
	}
	seq, err := parseSeq(req.ContactId)
	if err != nil {
		return nil, err
	}
	if err := ed.DeleteContact(ctx, seq); err != nil {
		return nil, s.mapDavErr("DeleteContact", err, "contact")
	}
	return &pb.Empty{}, nil
}

// mapDavErr maps write errors from dav to gRPC codes with user-facing text.
func (s *Server) mapDavErr(op string, err error, what string) error {
	switch {
	case errors.Is(err, store.ErrNotFound):
		return status.Errorf(codes.NotFound, "%s not found", what)
	case errors.Is(err, dav.ErrInvalidContact), errors.Is(err, dav.ErrBadResponse):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, dav.ErrNotInvited), errors.Is(err, dav.ErrReadOnly), errors.Is(err, dav.ErrNoAddressBook):
		return status.Error(codes.FailedPrecondition, err.Error())
	case errors.Is(err, dav.ErrConflict):
		return status.Errorf(codes.Aborted, "the %s changed on the server; it is being refreshed, try again in a moment", what)
	case errors.Is(err, auth.ErrReauthRequired):
		return status.Error(codes.Unauthenticated, "reauth_required")
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return status.FromContextError(err).Err()
	}
	s.log.Warn("server write failed", "op", op, "err", err)
	return status.Error(codes.Unavailable, "could not update the server: "+shortErr(err))
}

// ListMessagesByAddress returns recent mail with a contact.
func (s *Server) ListMessagesByAddress(ctx context.Context, req *pb.AddressMessagesRequest) (*pb.MessageHeaderList, error) {
	limit := int(req.Limit)
	if limit <= 0 {
		limit = 20
	}
	hs, err := s.st.MessagesByAddress(ctx, req.Addresses, min(limit, 200))
	if err != nil {
		return nil, s.mapErr("ListMessagesByAddress", err, "message")
	}
	out := &pb.MessageHeaderList{TotalCount: uint32(len(hs))}
	for _, h := range hs {
		out.Messages = append(out.Messages, header(h))
	}
	return out, nil
}

// Responder answers invitations (dav.Manager, or OfflineResponder in -offline).
type Responder interface {
	Respond(ctx context.Context, objectSeq int64, status string) error
}

// SetResponder installs the RespondToEvent backend. Call before serving.
func (s *Server) SetResponder(r Responder) {
	s.sendMu.Lock()
	s.responder = r
	s.sendMu.Unlock()
}

// RespondToEvent accepts, tentatively accepts or declines an invitation.
func (s *Server) RespondToEvent(ctx context.Context, req *pb.RespondToEventRequest) (*pb.Empty, error) {
	switch req.Status {
	case dav.Accepted, dav.Tentative, dav.Declined:
	default:
		return nil, status.Error(codes.InvalidArgument, "status must be ACCEPTED, TENTATIVE or DECLINED")
	}
	seqStr, _, _ := strings.Cut(req.EventId, "-")
	seq, err := strconv.ParseInt(seqStr, 10, 64)
	if err != nil || seq <= 0 {
		return nil, status.Error(codes.InvalidArgument, "bad event_id")
	}
	s.sendMu.RLock()
	r := s.responder
	s.sendMu.RUnlock()
	if r == nil {
		return nil, status.Error(codes.FailedPrecondition, "calendar sync is not running")
	}
	if err := r.Respond(ctx, seq, req.Status); err != nil {
		return nil, s.mapDavErr("RespondToEvent", err, "event")
	}
	return &pb.Empty{}, nil
}

func shortErr(err error) string {
	m := err.Error()
	if len(m) > 160 {
		m = m[:160] + "…"
	}
	return m
}
