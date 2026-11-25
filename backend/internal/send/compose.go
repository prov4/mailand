// Package send composes outgoing RFC 5322 messages and delivers them over SMTP.
package send

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"mime"
	"net/http"
	"net/mail"
	"os"
	"path/filepath"
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

// Request is a send request (mirrors pb.SendMessageRequest).
type Request struct {
	AccountID   string
	To, Cc, Bcc []string
	Subject     string
	TextBody    string
	HTMLBody    string
	InReplyTo   string // raw Message-ID fallback
	ReplyToID   string // mailand message id; takes precedence over InReplyTo
	ForwardOfID string // mailand message id whose attachments are attached
	Attachments []Attachment
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
	total := 0
	for _, a := range r.Attachments {
		total += len(a.Data)
		if total > maxAttachmentTotal {
			return invalid("attachments larger than %d MiB", maxAttachmentTotal>>20)
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

// InlineImage is an image embedded in an HTML body via Content-ID (cid:).
type InlineImage struct {
	ContentID string // without angle brackets
	Filename  string
	MIMEType  string
	Data      []byte
}

// Input is a fully resolved message for Compose.
type Input struct {
	From         mail.Address
	To, Cc       []*mail.Address // Bcc is deliberately absent: it never appears in headers
	Subject      string
	Body         string
	HTMLBody     string // optional; when non-empty, Compose emits multipart/alternative
	Date         time.Time
	MessageID    string   // without angle brackets; generated when empty
	InReplyTo    string   // without angle brackets
	References   []string // without angle brackets
	InlineImages []InlineImage
	Attachments  []Attachment
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
	hasHTML := in.HTMLBody != ""
	if len(in.Attachments) == 0 && !hasHTML {
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

	if len(in.Attachments) == 0 && hasHTML {
		h.Set("Content-Type", "multipart/alternative")
		aw, err := message.CreateWriter(&buf, h.Header)
		if err != nil {
			return nil, "", err
		}
		if err := writeAlternativeChildren(aw, body, in.HTMLBody, in.InlineImages); err != nil {
			return nil, "", err
		}
		if err := aw.Close(); err != nil {
			return nil, "", err
		}
		return buf.Bytes(), id, nil
	}

	h.Set("Content-Type", "multipart/mixed")
	mw, err := message.CreateWriter(&buf, h.Header)
	if err != nil {
		return nil, "", err
	}
	if hasHTML {
		var altH message.Header
		altH.Set("Content-Type", "multipart/alternative")
		aw, err := mw.CreatePart(altH)
		if err != nil {
			return nil, "", err
		}
		if err := writeAlternativeChildren(aw, body, in.HTMLBody, in.InlineImages); err != nil {
			return nil, "", err
		}
		if err := aw.Close(); err != nil {
			return nil, "", err
		}
	} else {
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
	}
	for _, a := range in.Attachments {
		name := sanitizeHeaderName(a.Filename, "attachment")
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

func sanitizeHeaderName(name, fallback string) string {
	name = strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' || r == 0 {
			return '_'
		}
		return r
	}, name)
	if name == "" {
		return fallback
	}
	return name
}

func writeAlternativeChildren(aw *message.Writer, plain, htmlBody string, inline []InlineImage) error {
	var th message.Header
	th.Set("Content-Type", "text/plain; charset=utf-8")
	th.Set("Content-Transfer-Encoding", "quoted-printable")
	pw, err := aw.CreatePart(th)
	if err != nil {
		return err
	}
	if _, err := pw.Write([]byte(plain)); err != nil {
		return err
	}
	if err := pw.Close(); err != nil {
		return err
	}

	if len(inline) == 0 {
		var hh message.Header
		hh.Set("Content-Type", "text/html; charset=utf-8")
		hh.Set("Content-Transfer-Encoding", "quoted-printable")
		hw, err := aw.CreatePart(hh)
		if err != nil {
			return err
		}
		if _, err := hw.Write([]byte(htmlBody)); err != nil {
			return err
		}
		return hw.Close()
	}

	var relH message.Header
	relH.Set("Content-Type", "multipart/related")
	rw, err := aw.CreatePart(relH)
	if err != nil {
		return err
	}
	var hh message.Header
	hh.Set("Content-Type", "text/html; charset=utf-8")
	hh.Set("Content-Transfer-Encoding", "quoted-printable")
	hw, err := rw.CreatePart(hh)
	if err != nil {
		return err
	}
	if _, err := hw.Write([]byte(htmlBody)); err != nil {
		return err
	}
	if err := hw.Close(); err != nil {
		return err
	}
	for _, img := range inline {
		name := sanitizeHeaderName(img.Filename, "image")
		typ := img.MIMEType
		if typ == "" {
			typ = "application/octet-stream"
		}
		var ih message.Header
		ih.SetContentType(typ, map[string]string{"name": name})
		ih.SetContentDisposition("inline", map[string]string{"filename": name})
		ih.Set("Content-ID", "<"+img.ContentID+">")
		ih.Set("Content-Transfer-Encoding", "base64")
		iw, err := rw.CreatePart(ih)
		if err != nil {
			return err
		}
		if _, err := iw.Write(img.Data); err != nil {
			return err
		}
		if err := iw.Close(); err != nil {
			return err
		}
	}
	return rw.Close()
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
	if len(req.Attachments) > 0 {
		total := 0
		for _, a := range in.Attachments {
			total += len(a.Data)
		}
		for _, a := range req.Attachments {
			total += len(a.Data)
			if total > maxAttachmentTotal {
				return nil, invalid("attachments larger than %d MiB", maxAttachmentTotal>>20)
			}
			typ := a.MIMEType
			if _, _, err := mime.ParseMediaType(typ); err != nil {
				typ = "application/octet-stream"
			}
			in.Attachments = append(in.Attachments, Attachment{
				Filename: a.Filename,
				MIMEType: typ,
				Data:     a.Data,
			})
		}
	}

	if req.HTMLBody != "" {
		in.HTMLBody = req.HTMLBody
		if acct.SignatureImage != "" {
			img, err := loadSignatureImage(acct.SignatureImage)
			if err != nil {
				return nil, err
			}
			in.InlineImages = append(in.InlineImages, img)
			if !strings.Contains(in.HTMLBody, "cid:"+sigImageCID) {
				imgTag := `<div style="margin-top:8px"><img src="cid:` + sigImageCID + `" alt="" style="max-width:240px;height:auto;display:block"></div>`
				if idx := strings.LastIndex(strings.ToLower(in.HTMLBody), "</body>"); idx >= 0 {
					in.HTMLBody = in.HTMLBody[:idx] + imgTag + in.HTMLBody[idx:]
				} else {
					in.HTMLBody += imgTag
				}
			}
		}
	} else if acct.SignatureHTML != "" || acct.SignatureImage != "" {
		if acct.SignatureImage != "" {
			img, err := loadSignatureImage(acct.SignatureImage)
			if err != nil {
				return nil, err
			}
			in.InlineImages = append(in.InlineImages, img)
		}
		in.HTMLBody = buildSignatureHTML(req.TextBody, acct.Signature, acct.SignatureHTML, len(in.InlineImages) > 0)
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

const sigImageCID = "signature-image@mailand"

func loadSignatureImage(rawPath string) (InlineImage, error) {
	p := strings.TrimSpace(rawPath)
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, p[2:])
		}
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return InlineImage{}, fmt.Errorf("%w: signature_image %q: %v", ErrPrecondition, rawPath, err)
	}
	if len(data) > maxAttachmentTotal {
		return InlineImage{}, invalid("signature_image larger than %d MiB", maxAttachmentTotal>>20)
	}
	typ := mime.TypeByExtension(strings.ToLower(filepath.Ext(p)))
	if i := strings.IndexByte(typ, ';'); i >= 0 {
		typ = strings.TrimSpace(typ[:i])
	}
	if typ == "" {
		typ = http.DetectContentType(data)
	}
	return InlineImage{
		ContentID: sigImageCID,
		Filename:  filepath.Base(p),
		MIMEType:  typ,
		Data:      data,
	}, nil
}

// buildSignatureHTML turns the plain-text body into clean HTML and embeds the
// HTML signature and/or inline signature image in the right place (right after
// the text signature, above any quoted reply).
func buildSignatureHTML(textBody, plainSig, sigHTML string, hasImage bool) string {
	norm := strings.ReplaceAll(strings.ReplaceAll(textBody, "\r\n", "\n"), "\r", "\n")
	imgTag := ""
	if hasImage {
		imgTag = `<div style="margin-top:8px"><img src="cid:` + sigImageCID + `" alt="" style="max-width:240px;height:auto;display:block"></div>`
	}
	toHTML := func(s string) string {
		return strings.ReplaceAll(html.EscapeString(s), "\n", "<br>\n")
	}

	richSig := strings.TrimSpace(sigHTML)
	if hasImage && !strings.Contains(richSig, "cid:"+sigImageCID) {
		if richSig != "" {
			richSig += "\n" + imgTag
		} else {
			richSig = imgTag
		}
	}

	// Build the exact plain-text signature block the composer inserted.
	plainSig = strings.TrimSpace(plainSig)
	var sigBlock string
	if plainSig != "" {
		if strings.HasPrefix(plainSig, "--") {
			sigBlock = "\n\n" + plainSig
		} else {
			sigBlock = "\n\n-- \n" + plainSig
		}
	}

	var bodyHTML string
	if sigBlock != "" {
		if idx := strings.Index(norm, sigBlock); idx >= 0 {
			before := norm[:idx]
			after := norm[idx+len(sigBlock):]
			if strings.TrimSpace(sigHTML) != "" {
				// Replace plain signature block with custom HTML signature (+ image).
				bodyHTML = toHTML(before) + `<div style="margin-top:12px">` + richSig + `</div>` + toHTML(after)
			} else {
				// Keep plain text signature and place the image directly beneath it.
				bodyHTML = toHTML(before+sigBlock) + imgTag + toHTML(after)
			}
		}
	}
	if bodyHTML == "" {
		// No plain signature marker found: place before forwarded/quoted block if present, else at end.
		splitAt := -1
		for _, marker := range []string{"\n\n---------- Forwarded message ----------", "\n\nOn "} {
			if i := strings.Index(norm, marker); i >= 0 && (splitAt < 0 || i < splitAt) {
				splitAt = i
			}
		}
		if splitAt >= 0 {
			bodyHTML = toHTML(norm[:splitAt]) + `<div style="margin-top:12px">` + richSig + `</div>` + toHTML(norm[splitAt:])
		} else {
			bodyHTML = toHTML(norm) + `<div style="margin-top:12px">` + richSig + `</div>`
		}
	}

	return `<!doctype html><html><body><div style="font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;font-size:14px;line-height:1.45">` +
		bodyHTML + `</div></body></html>`
}
