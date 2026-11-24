package dav

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/mail"
	"slices"
	"strings"

	"github.com/emersion/go-vcard"

	"mailand/backend/internal/store"
)

// ContactInput is what the UI edits. Everything else in an existing card
// (photo, addresses, birthday, notes, ...) is kept as it is.
type ContactInput struct {
	FullName, Organization string
	Emails, Phones         []string
}

var (
	// ErrInvalidContact: no name and no email, or a malformed address.
	ErrInvalidContact = errors.New("invalid contact")
	// ErrNoAddressBook: the account has no writable address book.
	ErrNoAddressBook = errors.New("this account has no writable address book")
	// ErrReadOnly: the contact's address book is read-only.
	ErrReadOnly = errors.New("this address book is read-only")
)

func clean(list []string) []string {
	var out []string
	for _, v := range list {
		if v = strings.TrimSpace(v); v != "" && !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	return out
}

// Normalize trims the input and validates it.
func (in ContactInput) Normalize() (ContactInput, error) {
	in.FullName = strings.TrimSpace(in.FullName)
	in.Organization = strings.TrimSpace(in.Organization)
	in.Emails, in.Phones = clean(in.Emails), clean(in.Phones)
	if in.FullName == "" && len(in.Emails) == 0 && in.Organization == "" {
		return in, fmt.Errorf("%w: give a name, an email or an organization", ErrInvalidContact)
	}
	for _, e := range in.Emails {
		if a, err := mail.ParseAddress(e); err != nil || a.Address != e || strings.ContainsAny(e, "\r\n") {
			return in, fmt.Errorf("%w: %q is not an email address", ErrInvalidContact, e)
		}
	}
	for _, p := range append([]string{in.FullName, in.Organization}, in.Phones...) {
		if strings.ContainsAny(p, "\r\n") {
			return in, fmt.Errorf("%w: line breaks are not allowed", ErrInvalidContact)
		}
	}
	return in, nil
}

func newUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	h := hex.EncodeToString(b[:])
	return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

// splitName guesses N from a display name: the last word is the family name.
func splitName(full string) *vcard.Name {
	parts := strings.Fields(full)
	if len(parts) < 2 {
		return &vcard.Name{GivenName: full}
	}
	return &vcard.Name{GivenName: strings.Join(parts[:len(parts)-1], " "), FamilyName: parts[len(parts)-1]}
}

// replaceValues sets field k to values, keeping the parameters (TYPE, PREF)
// of values that were already there and in their new order.
func replaceValues(card vcard.Card, k string, values []string, newParams vcard.Params) {
	old := card[k]
	var out []*vcard.Field
	for _, v := range values {
		var f *vcard.Field
		for _, o := range old {
			if strings.EqualFold(strings.TrimPrefix(o.Value, "tel:"), v) {
				f = o
				break
			}
		}
		if f == nil {
			f = &vcard.Field{Value: v, Params: newParams}
		}
		out = append(out, f)
	}
	if len(out) == 0 {
		delete(card, k)
	} else {
		card[k] = out
	}
}

// BuildCard applies in to existingData (a stored vCard; "" for a new contact)
// and returns the vCard text and its UID.
func BuildCard(existingData string, in ContactInput) (data, uid string, err error) {
	if in, err = in.Normalize(); err != nil {
		return "", "", err
	}
	card := vcard.Card{}
	if existingData != "" {
		if card, err = vcard.NewDecoder(strings.NewReader(existingData)).Decode(); err != nil {
			return "", "", err
		}
	}
	if card.Get(vcard.FieldVersion) == nil {
		card.SetValue(vcard.FieldVersion, "3.0") // what Google expects; also fine elsewhere
	}
	if uid = strings.TrimSpace(card.Value(vcard.FieldUID)); uid == "" {
		uid = newUID()
		card.SetValue(vcard.FieldUID, uid)
	}
	name := in.FullName
	if name == "" {
		name = in.Organization
	}
	if name == "" {
		name = in.Emails[0]
	}
	if strings.TrimSpace(card.PreferredValue(vcard.FieldFormattedName)) != name || card.Name() == nil {
		card.SetValue(vcard.FieldFormattedName, name)
		card.SetName(splitName(in.FullName))
	}
	if in.Organization == "" {
		delete(card, vcard.FieldOrganization)
	} else if org := card.PreferredValue(vcard.FieldOrganization); strings.TrimSpace(strings.Split(org, ";")[0]) != in.Organization {
		card.SetValue(vcard.FieldOrganization, in.Organization)
	}
	replaceValues(card, vcard.FieldEmail, in.Emails, vcard.Params{vcard.ParamType: {"INTERNET"}})
	replaceValues(card, vcard.FieldTelephone, in.Phones, nil)
	data, err = encodeVCard(card)
	return data, uid, err
}

// ContactRow builds the store row for a card.
func ContactRow(bookID, path, etag, data string) (store.Contact, error) {
	card, err := vcard.NewDecoder(strings.NewReader(data)).Decode()
	if err != nil {
		return store.Contact{}, err
	}
	f := CardFields(card)
	return store.Contact{AddressBookID: bookID, Path: path, ETag: etag, UID: f.UID, FullName: f.FullName,
		Emails: f.Emails, Phones: f.Phones, Organization: f.Organization, Data: data}, nil
}

// SaveContact creates (seq 0, in accountID's first writable address book) or
// updates a contact on the server and in the store; it returns the seq.
func (m *Manager) SaveContact(ctx context.Context, seq int64, accountID string, in ContactInput) (int64, error) {
	var (
		book    store.AddressBook
		oldData string
		path    string
		etag    string
	)
	if seq != 0 {
		c, b, err := m.st.GetContact(ctx, seq)
		if err != nil {
			return 0, err
		}
		book, oldData, path, etag = b, c.Data, c.Path, c.ETag
	} else {
		b, err := m.writableBook(ctx, accountID)
		if err != nil {
			return 0, err
		}
		book = b
	}
	if book.ReadOnly {
		return 0, ErrReadOnly
	}
	data, uid, err := BuildCard(oldData, in)
	if err != nil {
		return 0, err
	}
	if path == "" {
		path = strings.TrimSuffix(book.Path, "/") + "/" + uid + ".vcf"
	}
	acct, ok := m.cfg.Account(book.AccountID)
	if !ok || acct.ContactsURL() == "" {
		return 0, ErrNoAddressBook
	}
	client, err := m.respondClient(ctx, acct)
	if err != nil {
		return 0, err
	}
	newETag, err := putObject(ctx, client, acct.ContactsURL(), path, "text/vcard; charset=utf-8", data, etag, seq == 0)
	if errors.Is(err, ErrConflict) {
		m.TriggerSync(acct.ID)
	}
	if err != nil {
		return 0, err
	}
	row, err := ContactRow(book.ID, path, newETag, data)
	if err != nil {
		return 0, err
	}
	if err := m.st.UpsertContacts(ctx, []store.Contact{row}); err != nil {
		return 0, err
	}
	m.emit(Event{Kind: ContactsUpdated, AccountID: acct.ID})
	return m.st.ContactSeq(ctx, book.ID, path)
}

// DeleteContact removes a contact on the server and in the store.
func (m *Manager) DeleteContact(ctx context.Context, seq int64) error {
	c, book, err := m.st.GetContact(ctx, seq)
	if err != nil {
		return err
	}
	if book.ReadOnly {
		return ErrReadOnly
	}
	acct, ok := m.cfg.Account(book.AccountID)
	if !ok || acct.ContactsURL() == "" {
		return ErrNoAddressBook
	}
	client, err := m.respondClient(ctx, acct)
	if err != nil {
		return err
	}
	if err := deleteObject(ctx, client, acct.ContactsURL(), c.Path, c.ETag); err != nil {
		if errors.Is(err, ErrConflict) {
			m.TriggerSync(acct.ID)
		}
		return err
	}
	if err := m.st.DeleteContacts(ctx, book.ID, []string{c.Path}); err != nil {
		return err
	}
	m.emit(Event{Kind: ContactsUpdated, AccountID: acct.ID})
	return nil
}

func (m *Manager) writableBook(ctx context.Context, accountID string) (store.AddressBook, error) {
	books, err := m.st.ListAddressBooks(ctx, accountID)
	if err != nil {
		return store.AddressBook{}, err
	}
	for _, b := range books {
		if !b.ReadOnly {
			return b, nil
		}
	}
	return store.AddressBook{}, ErrNoAddressBook
}
