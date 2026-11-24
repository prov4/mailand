package dav

import (
	"bytes"
	"strings"

	"github.com/emersion/go-ical"
	"github.com/emersion/go-vcard"
)

// encodeICal serialises a parsed calendar without go-ical's encoder checks:
// that encoder rejects real-world data (no DTSTAMP, duplicate properties), and
// the store must keep whatever the server sent. Lines are folded at 75 octets.
func encodeICal(cal *ical.Calendar) string {
	var b strings.Builder
	writeComp(&b, cal.Component)
	return b.String()
}

func writeComp(b *strings.Builder, c *ical.Component) {
	writeLine(b, "BEGIN:"+c.Name)
	for name, props := range c.Props {
		for _, p := range props {
			writeLine(b, propLine(name, p.Params, p.Value))
		}
	}
	for _, child := range c.Children {
		writeComp(b, child)
	}
	writeLine(b, "END:"+c.Name)
}

func propLine(name string, params ical.Params, value string) string {
	var b strings.Builder
	b.WriteString(name)
	for k, vs := range params {
		b.WriteString(";" + k + "=")
		for i, v := range vs {
			if i > 0 {
				b.WriteByte(',')
			}
			if strings.ContainsAny(v, ";:,") {
				b.WriteString(`"` + strings.ReplaceAll(v, `"`, "'") + `"`)
			} else {
				b.WriteString(v)
			}
		}
	}
	b.WriteByte(':')
	b.WriteString(value) // go-ical keeps values in their escaped text form
	return b.String()
}

func writeLine(b *strings.Builder, line string) {
	for len(line) > 75 {
		cut := 75
		for cut > 1 && (line[cut]&0xC0) == 0x80 { // never split a UTF-8 sequence
			cut--
		}
		b.WriteString(line[:cut] + "\r\n ")
		line = line[cut:]
	}
	b.WriteString(line + "\r\n")
}

// decodeICal parses stored iCalendar text.
func decodeICal(data string) (*ical.Calendar, error) {
	return ical.NewDecoder(strings.NewReader(data)).Decode()
}

// encodeVCard serialises a card, adding VERSION if the server left it out
// (go-vcard refuses to encode without one).
func encodeVCard(card vcard.Card) (string, error) {
	if card.Get(vcard.FieldVersion) == nil {
		card = cloneCard(card)
		card.SetValue(vcard.FieldVersion, "3.0")
	}
	var buf bytes.Buffer
	if err := vcard.NewEncoder(&buf).Encode(card); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func cloneCard(c vcard.Card) vcard.Card {
	out := make(vcard.Card, len(c))
	for k, v := range c {
		out[k] = v
	}
	return out
}

// ContactFields extracts the indexed columns of a card.
type ContactFields struct {
	UID, FullName, Organization string
	Emails, Phones              []string
}

// CardFields reads name, emails, phones and organization from a vCard. A card
// without FN gets a name from N, the organization or the first email.
func CardFields(card vcard.Card) ContactFields {
	f := ContactFields{UID: strings.TrimSpace(card.Value(vcard.FieldUID))}
	f.FullName = strings.TrimSpace(card.PreferredValue(vcard.FieldFormattedName))
	if org := card.PreferredValue(vcard.FieldOrganization); org != "" {
		f.Organization = strings.TrimSpace(strings.Split(org, ";")[0])
	}
	for _, e := range card[vcard.FieldEmail] {
		if v := strings.TrimSpace(e.Value); v != "" {
			f.Emails = append(f.Emails, v)
		}
	}
	for _, p := range card[vcard.FieldTelephone] {
		if v := strings.TrimSpace(strings.TrimPrefix(p.Value, "tel:")); v != "" {
			f.Phones = append(f.Phones, v)
		}
	}
	if f.FullName == "" {
		if n := card.Name(); n != nil {
			f.FullName = strings.TrimSpace(strings.Join(strings.Fields(n.GivenName+" "+n.AdditionalName+" "+n.FamilyName), " "))
		}
	}
	if f.FullName == "" {
		f.FullName = f.Organization
	}
	if f.FullName == "" && len(f.Emails) > 0 {
		f.FullName = f.Emails[0]
	}
	return f
}
