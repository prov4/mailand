package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// Calendar is a CalDAV calendar collection.
type Calendar struct {
	ID, AccountID, Path, Name, Color string
	ReadOnly                         bool
}

// CalendarObject is one iCalendar resource (an event, possibly recurring, with
// its overrides). FirstStart/LastEnd bound all of its instances; LastEnd nil
// means the event recurs forever.
type CalendarObject struct {
	Seq          int64
	CalendarID   string
	Path, ETag   string
	UID, Summary string
	FirstStart   int64
	LastEnd      *int64
	Data         string
}

// AddressBook is a CardDAV address book collection.
type AddressBook struct {
	ID, AccountID, Path, Name string
	ReadOnly                  bool
}

// Contact is one vCard. Emails and Phones keep the card's order.
type Contact struct {
	Seq           int64
	AddressBookID string
	AccountID     string // filled by reads
	Path, ETag    string
	UID           string
	FullName      string
	Emails        []string
	Phones        []string
	Organization  string
	Data          string
}

// CollectionID builds the stable id of a calendar or address book.
func CollectionID(accountID, path string) string { return accountID + ":" + path }

// UpsertCalendar inserts or updates a calendar by (account, path) and returns its id.
func (s *Store) UpsertCalendar(ctx context.Context, c Calendar) (string, error) {
	if c.ID == "" {
		c.ID = CollectionID(c.AccountID, c.Path)
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO calendars (id, account_id, path, name, color, read_only) VALUES (?,?,?,?,?,?)
ON CONFLICT(account_id, path) DO UPDATE SET name=excluded.name, color=excluded.color, read_only=excluded.read_only`,
		c.ID, c.AccountID, c.Path, c.Name, c.Color, b2i(c.ReadOnly))
	return c.ID, err
}

// ListCalendars returns the calendars of accountID ("" = all accounts).
func (s *Store) ListCalendars(ctx context.Context, accountID string) ([]Calendar, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, account_id, path, name, color, read_only FROM calendars
WHERE ?='' OR account_id=? ORDER BY account_id, name COLLATE NOCASE, id`, accountID, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Calendar
	for rows.Next() {
		var c Calendar
		if err := rows.Scan(&c.ID, &c.AccountID, &c.Path, &c.Name, &c.Color, &c.ReadOnly); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// DeleteCalendarsExcept removes accountID's calendars whose id is not in keep
// (gone on the server), with their objects.
func (s *Store) DeleteCalendarsExcept(ctx context.Context, accountID string, keep []string) error {
	return s.deleteCollectionsExcept(ctx, "calendars", accountID, keep)
}

// DeleteAddressBooksExcept is DeleteCalendarsExcept for address books.
func (s *Store) DeleteAddressBooksExcept(ctx context.Context, accountID string, keep []string) error {
	return s.deleteCollectionsExcept(ctx, "address_books", accountID, keep)
}

func (s *Store) deleteCollectionsExcept(ctx context.Context, table, accountID string, keep []string) error {
	q := `DELETE FROM ` + table + ` WHERE account_id=?`
	args := []any{accountID}
	if len(keep) > 0 {
		q += ` AND id NOT IN (` + placeholders(len(keep)) + `)`
		for _, k := range keep {
			args = append(args, k)
		}
	}
	_, err := s.db.ExecContext(ctx, q, args...)
	return err
}

// CalendarETags maps resource path to ETag for the calendar's stored objects.
func (s *Store) CalendarETags(ctx context.Context, calendarID string) (map[string]string, error) {
	return s.etags(ctx, `SELECT path, etag FROM calendar_objects WHERE calendar_id=?`, calendarID)
}

// ContactETags maps resource path to ETag for the address book's stored cards.
func (s *Store) ContactETags(ctx context.Context, addressBookID string) (map[string]string, error) {
	return s.etags(ctx, `SELECT path, etag FROM contacts WHERE address_book_id=?`, addressBookID)
}

func (s *Store) etags(ctx context.Context, q, id string) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, q, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var p, e string
		if err := rows.Scan(&p, &e); err != nil {
			return nil, err
		}
		out[p] = e
	}
	return out, rows.Err()
}

// UpsertCalendarObjects stores objects (keyed by calendar and path) in one transaction.
func (s *Store) UpsertCalendarObjects(ctx context.Context, objs []CalendarObject) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		for _, o := range objs {
			var last any
			if o.LastEnd != nil {
				last = *o.LastEnd
			}
			if _, err := tx.ExecContext(ctx, `
INSERT INTO calendar_objects (calendar_id, path, etag, uid, summary, first_start, last_end, data) VALUES (?,?,?,?,?,?,?,?)
ON CONFLICT(calendar_id, path) DO UPDATE SET etag=excluded.etag, uid=excluded.uid, summary=excluded.summary,
  first_start=excluded.first_start, last_end=excluded.last_end, data=excluded.data`,
				o.CalendarID, o.Path, o.ETag, o.UID, o.Summary, o.FirstStart, last, o.Data); err != nil {
				return err
			}
		}
		return nil
	})
}

// DeleteCalendarObjects removes the given resource paths from a calendar.
func (s *Store) DeleteCalendarObjects(ctx context.Context, calendarID string, paths []string) error {
	return s.deleteByPaths(ctx, "calendar_objects", "calendar_id", calendarID, paths)
}

// DeleteContacts removes the given resource paths from an address book.
func (s *Store) DeleteContacts(ctx context.Context, addressBookID string, paths []string) error {
	return s.deleteByPaths(ctx, "contacts", "address_book_id", addressBookID, paths)
}

func (s *Store) deleteByPaths(ctx context.Context, table, col, id string, paths []string) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		for i := 0; i < len(paths); i += 500 {
			chunk := paths[i:min(i+500, len(paths))]
			args := []any{id}
			for _, p := range chunk {
				args = append(args, p)
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE `+col+`=? AND path IN (`+placeholders(len(chunk))+`)`, args...); err != nil {
				return err
			}
		}
		return nil
	})
}

// CalendarObjectsInRange returns objects of the given calendars (nil = all)
// that may have an instance overlapping [from, to).
func (s *Store) CalendarObjectsInRange(ctx context.Context, calendarIDs []string, from, to int64) ([]CalendarObject, error) {
	q := `SELECT seq, calendar_id, path, etag, uid, summary, first_start, last_end, data FROM calendar_objects
WHERE first_start < ? AND (last_end IS NULL OR last_end > ?)`
	args := []any{to, from}
	if calendarIDs != nil {
		if len(calendarIDs) == 0 {
			return nil, nil
		}
		q += ` AND calendar_id IN (` + placeholders(len(calendarIDs)) + `)`
		for _, id := range calendarIDs {
			args = append(args, id)
		}
	}
	rows, err := s.db.QueryContext(ctx, q+` ORDER BY first_start`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CalendarObject
	for rows.Next() {
		var o CalendarObject
		var last sql.NullInt64
		if err := rows.Scan(&o.Seq, &o.CalendarID, &o.Path, &o.ETag, &o.UID, &o.Summary, &o.FirstStart, &last, &o.Data); err != nil {
			return nil, err
		}
		if last.Valid {
			o.LastEnd = &last.Int64
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// UpsertAddressBook inserts or updates an address book by (account, path) and returns its id.
func (s *Store) UpsertAddressBook(ctx context.Context, a AddressBook) (string, error) {
	if a.ID == "" {
		a.ID = CollectionID(a.AccountID, a.Path)
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO address_books (id, account_id, path, name, read_only) VALUES (?,?,?,?,?)
ON CONFLICT(account_id, path) DO UPDATE SET name=excluded.name, read_only=excluded.read_only`,
		a.ID, a.AccountID, a.Path, a.Name, b2i(a.ReadOnly))
	return a.ID, err
}

// ListAddressBooks returns the address books of accountID ("" = all accounts).
func (s *Store) ListAddressBooks(ctx context.Context, accountID string) ([]AddressBook, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, account_id, path, name, read_only FROM address_books
WHERE ?='' OR account_id=? ORDER BY account_id, name COLLATE NOCASE, id`, accountID, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AddressBook
	for rows.Next() {
		var a AddressBook
		if err := rows.Scan(&a.ID, &a.AccountID, &a.Path, &a.Name, &a.ReadOnly); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// UpsertContacts stores cards (keyed by address book and path) in one transaction.
func (s *Store) UpsertContacts(ctx context.Context, cs []Contact) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		for _, c := range cs {
			if _, err := tx.ExecContext(ctx, `
INSERT INTO contacts (address_book_id, path, etag, uid, full_name, emails, phones, organization, data) VALUES (?,?,?,?,?,?,?,?,?)
ON CONFLICT(address_book_id, path) DO UPDATE SET etag=excluded.etag, uid=excluded.uid, full_name=excluded.full_name,
  emails=excluded.emails, phones=excluded.phones, organization=excluded.organization, data=excluded.data`,
				c.AddressBookID, c.Path, c.ETag, c.UID, c.FullName, strings.Join(c.Emails, "\n"),
				strings.Join(c.Phones, "\n"), c.Organization, c.Data); err != nil {
				return err
			}
		}
		return nil
	})
}

// SearchContacts returns contacts whose name, email or organization contains
// query (case-insensitive; "" = all), people with an email address first when
// emailOnly is false, ordered by name. limit <= 0 means 1000.
func (s *Store) SearchContacts(ctx context.Context, query string, emailOnly bool, limit int) ([]Contact, error) {
	if limit <= 0 {
		limit = 1000
	}
	like := "%" + escapeLike(strings.ToLower(query)) + "%"
	q := `SELECT c.seq, c.address_book_id, b.account_id, c.path, c.etag, c.uid, c.full_name, c.emails, c.phones, c.organization, c.data
FROM contacts c JOIN address_books b ON b.id = c.address_book_id
WHERE (lower(c.full_name) LIKE ? ESCAPE '\' OR lower(c.emails) LIKE ? ESCAPE '\' OR lower(c.organization) LIKE ? ESCAPE '\')`
	if emailOnly {
		q += ` AND c.emails <> ''`
	}
	// Name matches (prefix first) rank above email/organization matches.
	q += ` ORDER BY (lower(c.full_name) LIKE ? ESCAPE '\') DESC, (lower(c.full_name) LIKE ? ESCAPE '\') DESC,
  CASE WHEN c.full_name = '' THEN 1 ELSE 0 END, c.full_name COLLATE NOCASE, c.emails LIMIT ?`
	prefix := escapeLike(strings.ToLower(query)) + "%"
	rows, err := s.db.QueryContext(ctx, q, like, like, like, prefix, like, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Contact
	for rows.Next() {
		var c Contact
		var emails, phones string
		if err := rows.Scan(&c.Seq, &c.AddressBookID, &c.AccountID, &c.Path, &c.ETag, &c.UID, &c.FullName,
			&emails, &phones, &c.Organization, &c.Data); err != nil {
			return nil, err
		}
		c.Emails, c.Phones = splitLines(emails), splitLines(phones)
		out = append(out, c)
	}
	return out, rows.Err()
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

// GetCalendarObject returns the object with seq and its calendar.
func (s *Store) GetCalendarObject(ctx context.Context, seq int64) (CalendarObject, Calendar, error) {
	var o CalendarObject
	var c Calendar
	var last sql.NullInt64
	err := s.db.QueryRowContext(ctx, `
SELECT o.seq, o.calendar_id, o.path, o.etag, o.uid, o.summary, o.first_start, o.last_end, o.data,
       c.id, c.account_id, c.path, c.name, c.color, c.read_only
FROM calendar_objects o JOIN calendars c ON c.id = o.calendar_id WHERE o.seq = ?`, seq).Scan(
		&o.Seq, &o.CalendarID, &o.Path, &o.ETag, &o.UID, &o.Summary, &o.FirstStart, &last, &o.Data,
		&c.ID, &c.AccountID, &c.Path, &c.Name, &c.Color, &c.ReadOnly)
	if errors.Is(err, sql.ErrNoRows) {
		return o, c, ErrNotFound
	}
	if last.Valid {
		o.LastEnd = &last.Int64
	}
	return o, c, err
}

// GetContact returns the contact with seq and its address book.
func (s *Store) GetContact(ctx context.Context, seq int64) (Contact, AddressBook, error) {
	var c Contact
	var b AddressBook
	var emails, phones string
	err := s.db.QueryRowContext(ctx, `
SELECT c.seq, c.address_book_id, c.path, c.etag, c.uid, c.full_name, c.emails, c.phones, c.organization, c.data,
       b.id, b.account_id, b.path, b.name, b.read_only
FROM contacts c JOIN address_books b ON b.id = c.address_book_id WHERE c.seq = ?`, seq).Scan(
		&c.Seq, &c.AddressBookID, &c.Path, &c.ETag, &c.UID, &c.FullName, &emails, &phones, &c.Organization, &c.Data,
		&b.ID, &b.AccountID, &b.Path, &b.Name, &b.ReadOnly)
	if errors.Is(err, sql.ErrNoRows) {
		return c, b, ErrNotFound
	}
	c.AccountID = b.AccountID
	c.Emails, c.Phones = splitLines(emails), splitLines(phones)
	return c, b, err
}

// ContactSeq returns the seq of the contact at (addressBookID, path).
func (s *Store) ContactSeq(ctx context.Context, addressBookID, path string) (int64, error) {
	var seq int64
	err := s.db.QueryRowContext(ctx, `SELECT seq FROM contacts WHERE address_book_id=? AND path=?`, addressBookID, path).Scan(&seq)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	return seq, err
}
