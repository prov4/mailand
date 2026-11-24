// Package send composes outgoing RFC 5322 messages and delivers them over SMTP.
package send

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"net/mail"
	"os"
	"strings"
	"time"

	"github.com/emersion/go-message"
	gomail "github.com/emersion/go-message/mail"

	mailpkg "mailand/backend/internal/mail"
	"mailand/backend/internal/store"
)

// Error categories; the rpc layer maps them to gRPC codes with errors.Is.
var (
	ErrInvalid           = errors.New("invalid message")         // bad request: addresses, unknown account/message
	ErrRecipientRejected = errors.New("recipient rejected")      // SMTP server refused a recipient
	ErrAuth              = errors.New("authentication failed")   // may also wrap auth.ErrReauthRequired
	ErrUnavailable       = errors.New("mail server unavailable") // network / server-side trouble
	ErrPrecondition      = errors.New("failed precondition")     // e.g. forwarded message has no stored raw blob
)

const (
	maxAttachmentTotal = 25 << 20
	maxReferencesLen   = 800
)

// Request is a send request (mirrors pb.SendMessageRequest; plain text only).
type Request struct {
	AccountID   string
	To, Cc, Bcc []string
	Subject     string
	TextBody    string
	InReplyTo   string // raw Message-ID fallback
	ReplyToID   string // mailand message id; takes precedence over InReplyTo
	ForwardOfID string // mailand message id whose attachments are attached
}

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, a...))
}

func hasCRLF(s string) bool { return strings.ContainsAny(s, "\r\n\x00") }

// Validate checks the request without touching the store: header injection,
// recipient syntax and non-empty recipient list.
func Validate(r Request) error {
	if strings.TrimSpace(r.AccountID) == "" {
		return invalid("account_id is required")
	}
	for name, v := range map[string]string{"subject": r.Subject, "in_reply_to": r.InReplyTo,
		"reply_to_id": r.ReplyToID, "forward_of_id": r.ForwardOfID, "account_id": r.AccountID} {
		if hasCRLF(v) {
			return invalid("%s must not contain line breaks", name)
		}
	}
	if len(r.To)+len(r.Cc)+len(r.Bcc) == 0 {
		return invalid("at least one recipient is required")
	}
	for name, l := range map[string][]string{"to": r.To, "cc": r.Cc, "bcc": r.Bcc} {
		if _, err := parseAddrs(name, l); err != nil {
			return err
		}
	}
	return nil
}

func parseAddrs(field string, in []string) ([]*mail.Address, error) {
	out := make([]*mail.Address, 0, len(in))
	for _, s := range in {
		if hasCRLF(s) {
			return nil, invalid("%s: address must not contain line breaks", field)
		}
		a, err := mail.ParseAddress(strings.TrimSpace(s))
		if err != nil {
			return nil, invalid("%s: bad address %q", field, s)
		}
		out = append(out, a)
	}
	return out, nil
}

// Attachment is a file attached to a composed message.
type Attachment struct {
	Filename string
	MIMEType string
	Data     []byte
}

// Input is a fully resolved message for Compose.
type Input struct {
	From        mail.Address
	To, Cc      []*mail.Address // Bcc is deliberately absent: it never appears in headers
	Subject     string
	Body        string
	Date        time.Time
	MessageID   string   // without angle brackets; generated when empty
	InReplyTo   string   // without angle brackets
	References  []string // without angle brackets
	Attachments []Attachment
}

// NewMessageID returns a fresh Message-ID (without angle brackets) at domain.
func NewMessageID(domain string) string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("%s.%d@%s", hex.EncodeToString(b[:]), time.Now().UnixNano(), domain)
}

func domainOf(email string) string {
	if i := strings.LastIndexByte(email, '@'); i >= 0 && i+1 < len(email) {
		return email[i+1:]
	}
	return "localhost"
}

func toGo(as []*mail.Address) []*gomail.Address {
	out := make([]*gomail.Address, len(as))
	for i, a := range as {
		out[i] = &gomail.Address{Name: a.Name, Address: a.Address}
	}
	return out
}

// Compose renders the message with CRLF line endings. It returns the bytes and
// the Message-ID (without angle brackets).
func Compose(in Input) ([]byte, string, error) {
	for _, s := range []string{in.Subject, in.From.Name, in.From.Address, in.MessageID, in.InReplyTo} {
		if hasCRLF(s) {
			return nil, "", invalid("header values must not contain line breaks")
		}
	}
	for _, r := range in.References {
		if hasCRLF(r) || strings.ContainsAny(r, " \t<>") {
			return nil, "", invalid("bad reference id")
		}
	}
	id := in.MessageID
	if id == "" {
		id = NewMessageID(domainOf(in.From.Address))
	}
	date := in.Date
	if date.IsZero() {
		date = time.Now()
	}

	var h gomail.Header
	h.SetAddressList("From", toGo([]*mail.Address{&in.From}))
	if len(in.To) > 0 {
		h.SetAddressList("To", toGo(in.To))
	}
	if len(in.Cc) > 0 {
		h.SetAddressList("Cc", toGo(in.Cc))
	}
	h.SetSubject(in.Subject)
	h.SetDate(date)
	h.Set("Message-ID", "<"+id+">")
	if in.InReplyTo != "" {
		h.Set("In-Reply-To", "<"+in.InReplyTo+">")
	}
	if refs := fitReferences(in.References); len(refs) > 0 {
		h.Set("References", "<"+strings.Join(refs, "> <")+">")
	}
	h.Set("MIME-Version", "1.0")

	body := strings.ReplaceAll(strings.ReplaceAll(in.Body, "\r\n", "\n"), "\r", "\n")

	var buf bytes.Buffer
	if len(in.Attachments) == 0 {
		h.Set("Content-Type", "text/plain; charset=utf-8")
		h.Set("Content-Transfer-Encoding", "quoted-printable")
		w, err := message.CreateWriter(&buf, h.Header)
		if err != nil {
			return nil, "", err
		}
		if _, err := w.Write([]byte(body)); err != nil {
			return nil, "", err
		}
		if err := w.Close(); err != nil {
			return nil, "", err
		}
		return buf.Bytes(), id, nil
	}

	h.Set("Content-Type", "multipart/mixed")
	mw, err := message.CreateWriter(&buf, h.Header)
	if err != nil {
		return nil, "", err
	}
	var th message.Header
	th.Set("Content-Type", "text/plain; charset=utf-8")
	th.Set("Content-Transfer-Encoding", "quoted-printable")
	pw, err := mw.CreatePart(th)
	if err != nil {
		return nil, "", err
	}
	if _, err := pw.Write([]byte(body)); err != nil {
		return nil, "", err
	}
	if err := pw.Close(); err != nil {
		return nil, "", err
	}
	for _, a := range in.Attachments {
		name := strings.Map(func(r rune) rune {
			if r == '\r' || r == '\n' || r == 0 {
				return '_'
			}
			return r
		}, a.Filename)
		if name == "" {
			name = "attachment"
		}
		typ := a.MIMEType
		if typ == "" {
			typ = "application/octet-stream"
		}
		var ah message.Header
		ah.SetContentType(typ, map[string]string{"name": name})
		ah.SetContentDisposition("attachment", map[string]string{"filename": name})
		ah.Set("Content-Transfer-Encoding", "base64")
		aw, err := mw.CreatePart(ah)
		if err != nil {
			return nil, "", err
		}
		if _, err := aw.Write(a.Data); err != nil {
			return nil, "", err
		}
		if err := aw.Close(); err != nil {
			return nil, "", err
		}
	}
	if err := mw.Close(); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), id, nil
}

// fitReferences keeps the header under the line-length limit by dropping
// ids from the middle (keeps the thread root and the most recent ones).
func fitReferences(refs []string) []string {
	size := func(r []string) int {
		n := 0
		for _, s := range r {
			n += len(s) + 3
		}
		return n
	}
	for len(refs) > 2 && size(refs) > maxReferencesLen {
		refs = append(refs[:1:1], refs[2:]...)
	}
	return refs
}

// Built is the outcome of Build.
type Built struct {
	Raw        []byte
	MessageID  string   // without angle brackets
	Recipients []string // envelope: to + cc + bcc addresses
	To, Cc     []*mail.Address
}

// Build validates req, resolves reply/forward context from the store and
// composes the message for acct.
func Build(ctx context.Context, st *store.Store, acct store.Account, req Request, now time.Time) (*Built, error) {
	if err := Validate(req); err != nil {
		return nil, err
	}
	to, _ := parseAddrs("to", req.To)
	cc, _ := parseAddrs("cc", req.Cc)
	bcc, _ := parseAddrs("bcc", req.Bcc)

	in := Input{
		From:    mail.Address{Name: acct.Name, Address: acct.Email},
		To:      to,
		Cc:      cc,
		Subject: req.Subject,
		Body:    req.TextBody,
		Date:    now,
	}
	if hasCRLF(acct.Name) || hasCRLF(acct.Email) {
		return nil, invalid("account name/email must not contain line breaks")
	}

	switch {
	case req.ReplyToID != "":
		m, err := st.GetMessage(ctx, req.ReplyToID)
		if errors.Is(err, store.ErrNotFound) {
			return nil, invalid("reply_to_id: message not found")
		} else if err != nil {
			return nil, err
		}
		in.InReplyTo, in.References = replyHeaders(m)
	case req.InReplyTo != "":
		id := strings.Trim(strings.TrimSpace(req.InReplyTo), "<>")
		if id == "" || strings.ContainsAny(id, " \t<>") {
			return nil, invalid("in_reply_to: bad Message-ID")
		}
		in.InReplyTo, in.References = id, []string{id}
	}

	if req.ForwardOfID != "" {
		m, err := st.GetMessage(ctx, req.ForwardOfID)
		if errors.Is(err, store.ErrNotFound) {
			return nil, invalid("forward_of_id: message not found")
		} else if err != nil {
			return nil, err
		}
		atts, err := forwardAttachments(m)
		if err != nil {
			return nil, err
		}
		in.Attachments = atts
	}

	raw, id, err := Compose(in)
	if err != nil {
		return nil, err
	}
	b := &Built{Raw: raw, MessageID: id, To: to, Cc: cc}
	for _, l := range [][]*mail.Address{to, cc, bcc} {
		for _, a := range l {
			b.Recipients = append(b.Recipients, a.Address)
		}
	}
	return b, nil
}

// replyHeaders derives In-Reply-To and References from the stored message and,
// when available, its raw blob.
func replyHeaders(m store.Message) (inReplyTo string, refs []string) {
	id := strings.Trim(m.MessageIDHeader, "<> ")
	var parsed *mailpkg.Parsed
	if m.RawBlobPath != "" {
		if f, err := os.Open(m.RawBlobPath); err == nil {
			parsed, _ = mailpkg.Parse(f)
			f.Close()
		}
	}
	if parsed != nil {
		if id == "" {
			id = parsed.MessageID
		}
		refs = append(refs, parsed.References...)
		if len(refs) == 0 && parsed.InReplyTo != "" {
			refs = append(refs, parsed.InReplyTo)
		}
	}
	if id == "" {
		return "", nil
	}
	seen := map[string]bool{}
	out := refs[:0:0]
	for _, r := range append(refs, id) {
		if r != "" && !seen[r] && !strings.ContainsAny(r, " \t<>\r\n") {
			seen[r] = true
			out = append(out, r)
		}
	}
	return id, out
}

func forwardAttachments(m store.Message) ([]Attachment, error) {
	if m.RawBlobPath == "" {
		return nil, fmt.Errorf("%w: forwarded message has no stored raw content", ErrPrecondition)
	}
	f, err := os.Open(m.RawBlobPath)
	if err != nil {
		return nil, fmt.Errorf("%w: forwarded message has no stored raw content", ErrPrecondition)
	}
	p, err := mailpkg.Parse(f)
	f.Close()
	if err != nil || p == nil {
		return nil, fmt.Errorf("%w: forwarded message is unreadable", ErrPrecondition)
	}
	var out []Attachment
	total := 0
	for _, a := range p.Attachments {
		if a.Inline {
			continue
		}
		f, err := os.Open(m.RawBlobPath)
		if err != nil {
			return nil, fmt.Errorf("%w: forwarded message has no stored raw content", ErrPrecondition)
		}
		data, meta, err := mailpkg.ExtractPart(f, a.PartPath)
		f.Close()
		if err != nil {
			return nil, fmt.Errorf("extract attachment %s: %w", a.PartPath, err)
		}
		total += len(data)
		if total > maxAttachmentTotal {
			return nil, invalid("attachments larger than %d MiB", maxAttachmentTotal>>20)
		}
		typ := meta.MIMEType
		if _, _, err := mime.ParseMediaType(typ); err != nil {
			typ = "application/octet-stream"
		}
		out = append(out, Attachment{Filename: a.Filename, MIMEType: typ, Data: data})
	}
	return out, nil
}
