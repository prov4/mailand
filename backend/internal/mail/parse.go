// Package mail parses RFC 5322 / MIME messages into the fields mailand stores.
// It is pure parsing code with no network or storage dependencies.
package mail

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"mime"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/emersion/go-message"
	"github.com/emersion/go-message/charset" // also registers non-UTF-8 charsets
	gomail "github.com/emersion/go-message/mail"
)

const (
	maxBodyBytes  = 10 << 20 // per text/plain and text/html
	maxPartBytes  = 256 << 20
	maxHeaderSize = 1 << 20
	maxDepth      = 20
	maxParts      = 2000
	snippetRunes  = 200
)

// Address is a decoded mailbox.
type Address struct {
	Name    string
	Address string
}

// Attachment describes a non-body MIME part. Content is not retained.
type Attachment struct {
	PartPath  string // IMAP-style part number, e.g. "1.2"
	Filename  string
	MIMEType  string
	Size      int64  // decoded bytes
	ContentID string // without angle brackets
	Inline    bool   // referenced via cid: rather than a user-visible attachment
}

// Parsed is the result of Parse. Fields are filled on a best-effort basis.
type Parsed struct {
	Subject     string
	FromName    string
	FromAddress string
	To, Cc      []Address
	Date        time.Time
	HasDate     bool
	MessageID   string
	InReplyTo   string
	References  []string

	TextBody     string
	HTMLBody     string
	TextFromHTML bool // TextBody was derived from HTMLBody

	Attachments    []Attachment
	HasAttachments bool // any non-inline attachment
	Snippet        string
}

// Parse reads a full message. It never panics; on malformed input it returns
// whatever could be extracted, with a nil error unless nothing was readable.
func Parse(r io.Reader) (p *Parsed, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			if p == nil {
				p = &Parsed{}
			}
			err = fmt.Errorf("mail: internal parse failure: %v", rec)
		}
	}()

	root, err := readRoot(r)
	if err != nil {
		return &Parsed{}, err
	}
	p = &Parsed{}
	parseHeaders(p, root.Header)

	w := &walker{p: p}
	res := w.walk(root, "", 0)
	p.TextBody = strings.TrimSpace(res.text)
	p.HTMLBody = strings.TrimSpace(res.html)
	if p.TextBody == "" && p.HTMLBody != "" {
		p.TextBody = HTMLToText(p.HTMLBody)
		p.TextFromHTML = p.TextBody != ""
	}
	for _, a := range p.Attachments {
		if !a.Inline {
			p.HasAttachments = true
		}
	}
	p.Snippet = makeSnippet(p.TextBody)
	return p, nil
}

// readRoot reads the top-level header leniently (dropping lines that are not
// valid header fields) and returns the entity.
func readRoot(r io.Reader) (*message.Entity, error) {
	br := bufio.NewReader(r)
	var hdr bytes.Buffer
	for hdr.Len() < maxHeaderSize {
		line, err := br.ReadString('\n')
		trimmed := strings.TrimRight(line, "\r\n")
		if trimmed == "" && line != "" {
			hdr.WriteString("\r\n")
			break
		}
		if trimmed != "" && validHeaderLine(trimmed, hdr.Len() > 0) {
			hdr.WriteString(trimmed + "\r\n")
		}
		if err != nil {
			hdr.WriteString("\r\n")
			break
		}
	}
	e, err := message.Read(io.MultiReader(&hdr, br))
	if e == nil {
		return nil, err
	}
	return e, nil // unknown charset/encoding: entity still readable
}

func validHeaderLine(l string, haveFields bool) bool {
	if l[0] == ' ' || l[0] == '\t' {
		return haveFields
	}
	i := strings.IndexByte(l, ':')
	if i <= 0 {
		return false
	}
	return !strings.ContainsAny(l[:i], " \t")
}

var (
	msgIDRe = regexp.MustCompile(`<[^<>\s]+>`)
	emailRe = regexp.MustCompile(`[^\s<>"',;]+@[^\s<>"',;]+`)
)

func parseHeaders(p *Parsed, h message.Header) {
	gh := gomail.Header{Header: h}
	if s, err := h.Text("Subject"); err == nil {
		p.Subject = strings.TrimSpace(s)
	} else {
		p.Subject = strings.TrimSpace(h.Get("Subject"))
	}
	p.Subject = strings.ToValidUTF8(p.Subject, "�")

	if from := addrList(gh, "From"); len(from) > 0 {
		p.FromName, p.FromAddress = from[0].Name, from[0].Address
	}
	p.To = addrList(gh, "To")
	p.Cc = addrList(gh, "Cc")

	if d, err := gh.Date(); err == nil && !d.IsZero() {
		p.Date, p.HasDate = d, true
	}
	if ids := msgIDRe.FindAllString(h.Get("Message-ID"), 1); len(ids) > 0 {
		p.MessageID = strings.Trim(ids[0], "<>")
	}
	if ids := msgIDRe.FindAllString(h.Get("In-Reply-To"), 1); len(ids) > 0 {
		p.InReplyTo = strings.Trim(ids[0], "<>")
	}
	for _, id := range msgIDRe.FindAllString(h.Get("References"), -1) {
		p.References = append(p.References, strings.Trim(id, "<>"))
	}
}

func addrList(h gomail.Header, key string) []Address {
	list, err := h.AddressList(key)
	var out []Address
	for _, a := range list {
		if a != nil && a.Address != "" {
			out = append(out, Address{Name: strings.ToValidUTF8(a.Name, "�"), Address: a.Address})
		}
	}
	if len(out) == 0 && err != nil {
		for _, m := range emailRe.FindAllString(h.Get(key), -1) {
			out = append(out, Address{Address: m})
		}
	}
	return out
}

type textResult struct{ text, html string }

type walker struct {
	p     *Parsed
	parts int
}

// children invokes fn for each child of a multipart entity with its 1-based
// number. Errors in the multipart stream end iteration but keep prior parts.
func children(e *message.Entity, fn func(n int, c *message.Entity) bool) {
	mr := e.MultipartReader()
	if mr == nil {
		return
	}
	for n := 1; ; n++ {
		c, err := mr.NextPart()
		if c == nil {
			_ = err
			return
		}
		if !fn(n, c) {
			return
		}
	}
}

func joinPath(parent string, n int) string {
	if parent == "" {
		return fmt.Sprint(n)
	}
	return fmt.Sprintf("%s.%d", parent, n)
}

func mediaType(h message.Header) (string, map[string]string) {
	t, params, _ := h.ContentType()
	t = strings.ToLower(strings.TrimSpace(t))
	if t == "" || !strings.Contains(t, "/") {
		t = "text/plain"
	}
	return t, params
}

func disposition(h message.Header) (string, map[string]string) {
	d, params, _ := h.ContentDisposition()
	return strings.ToLower(d), params
}

func isMultipart(e *message.Entity) bool {
	t, _ := mediaType(e.Header)
	return strings.HasPrefix(t, "multipart/") && e.MultipartReader() != nil
}

func (w *walker) walk(e *message.Entity, path string, depth int) textResult {
	w.parts++
	if depth > maxDepth || w.parts > maxParts {
		return textResult{}
	}
	t, _ := mediaType(e.Header)
	if isMultipart(e) {
		return w.walkMultipart(e, path, t, depth)
	}
	leafPath := path
	if leafPath == "" {
		leafPath = "1"
	}
	disp, dparams := disposition(e.Header)
	filename := partFilename(e.Header, dparams)
	if (t == "text/plain" || t == "text/html") && disp != "attachment" {
		s := readText(e.Body)
		if t == "text/html" {
			return textResult{html: s}
		}
		return textResult{text: s}
	}
	w.addAttachment(e, leafPath, t, disp, filename)
	return textResult{}
}

func (w *walker) walkMultipart(e *message.Entity, path, t string, depth int) textResult {
	var res textResult
	alt := t == "multipart/alternative"
	children(e, func(n int, c *message.Entity) bool {
		r := w.walk(c, joinPath(path, n), depth+1)
		if alt {
			// later alternatives are richer; keep the last non-empty of each kind
			if r.text != "" {
				res.text = r.text
			}
			if r.html != "" {
				res.html = r.html
			}
		} else {
			res.text = joinBody(res.text, r.text)
			res.html = joinBody(res.html, r.html)
		}
		return w.parts <= maxParts
	})
	return res
}

func joinBody(a, b string) string {
	a, b = strings.TrimSpace(a), strings.TrimSpace(b)
	switch {
	case a == "":
		return b
	case b == "":
		return a
	}
	if len(a)+len(b) > maxBodyBytes {
		return a
	}
	return a + "\n\n" + b
}

func (w *walker) addAttachment(e *message.Entity, path, t, disp, filename string) {
	n, _ := io.Copy(io.Discard, io.LimitReader(e.Body, maxPartBytes))
	cid := strings.Trim(strings.TrimSpace(e.Header.Get("Content-ID")), "<>")
	w.p.Attachments = append(w.p.Attachments, Attachment{
		PartPath:  path,
		Filename:  filename,
		MIMEType:  t,
		Size:      n,
		ContentID: cid,
		Inline:    cid != "" && disp != "attachment",
	})
}

// partFilename prefers Content-Disposition filename, then Content-Type name.
// The header parser decodes RFC 2231 and RFC 2047 forms.
func partFilename(h message.Header, dparams map[string]string) string {
	name := dparams["filename"]
	if name == "" {
		_, params := mediaType(h)
		name = params["name"]
	}
	if name != "" {
		dec := mime.WordDecoder{CharsetReader: charset.Reader}
		if out, err := dec.DecodeHeader(name); err == nil {
			name = out
		}
	}
	return strings.ToValidUTF8(name, "�")
}

func readText(r io.Reader) string {
	b, _ := io.ReadAll(io.LimitReader(r, maxBodyBytes)) // keep partial data on error
	s := string(b)
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "�")
	}
	return strings.ReplaceAll(s, "\r\n", "\n")
}

// ErrNoPart is returned by ExtractPart when the part path does not exist.
var ErrNoPart = errors.New("mail: part not found")

// ExtractPart returns the decoded content of the leaf part at partPath
// (numbering as in Attachment.PartPath).
func ExtractPart(r io.Reader, partPath string) ([]byte, Attachment, error) {
	root, err := readRoot(r)
	if err != nil {
		return nil, Attachment{}, err
	}
	e, path := findPart(root, "", partPath, 0)
	if e == nil {
		return nil, Attachment{}, ErrNoPart
	}
	t, _ := mediaType(e.Header)
	disp, dparams := disposition(e.Header)
	meta := Attachment{
		PartPath:  path,
		Filename:  partFilename(e.Header, dparams),
		MIMEType:  t,
		ContentID: strings.Trim(strings.TrimSpace(e.Header.Get("Content-ID")), "<>"),
	}
	meta.Inline = meta.ContentID != "" && disp != "attachment"
	data, err := io.ReadAll(io.LimitReader(e.Body, maxPartBytes))
	meta.Size = int64(len(data))
	if err != nil && len(data) == 0 {
		return nil, meta, err
	}
	return data, meta, nil
}

func findPart(e *message.Entity, path, target string, depth int) (*message.Entity, string) {
	if depth > maxDepth {
		return nil, ""
	}
	if !isMultipart(e) {
		p := path
		if p == "" {
			p = "1"
		}
		if p == target {
			return e, p
		}
		return nil, ""
	}
	var found *message.Entity
	var fpath string
	children(e, func(n int, c *message.Entity) bool {
		cp := joinPath(path, n)
		if cp != target && !strings.HasPrefix(target, cp+".") {
			return true // skip without reading
		}
		found, fpath = findPart(c, cp, target, depth+1)
		return found == nil
	})
	return found, fpath
}

var spaceRe = regexp.MustCompile(`\s+`)

func makeSnippet(text string) string {
	var kept []string
	for _, line := range strings.Split(text, "\n") {
		if line == "-- " || line == "--" {
			break
		}
		if strings.HasPrefix(strings.TrimSpace(line), ">") {
			continue
		}
		kept = append(kept, line)
	}
	s := strings.TrimSpace(spaceRe.ReplaceAllString(strings.Join(kept, " "), " "))
	if s == "" {
		s = strings.TrimSpace(spaceRe.ReplaceAllString(text, " "))
	}
	if utf8.RuneCountInString(s) > snippetRunes {
		s = string([]rune(s)[:snippetRunes])
	}
	return s
}
