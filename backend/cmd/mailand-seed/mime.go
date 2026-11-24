package main

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"mime"
	"mime/quotedprintable"
	netmail "net/mail"
	"strings"
	"time"
)

// part is one MIME entity: raw header lines plus an already-encoded body.
type part struct {
	headers []string
	body    []byte
}

func crlf(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\n", "\r\n")
}

func textPart(subtype, s string) part {
	var b bytes.Buffer
	w := quotedprintable.NewWriter(&b)
	_, _ = w.Write([]byte(crlf(s)))
	_ = w.Close()
	return part{
		headers: []string{
			"Content-Type: text/" + subtype + "; charset=utf-8",
			"Content-Transfer-Encoding: quoted-printable",
		},
		body: b.Bytes(),
	}
}

type attachment struct {
	filename  string
	mimeType  string
	data      []byte
	contentID string // without angle brackets
	inline    bool
}

func binPart(a attachment) part {
	disp := "attachment"
	if a.inline {
		disp = "inline"
	}
	h := []string{
		"Content-Type: " + mime.FormatMediaType(a.mimeType, map[string]string{"name": a.filename}),
		"Content-Transfer-Encoding: base64",
		"Content-Disposition: " + mime.FormatMediaType(disp, map[string]string{"filename": a.filename}),
	}
	if a.contentID != "" {
		h = append(h, "Content-ID: <"+a.contentID+">")
	}
	enc := base64.StdEncoding.EncodeToString(a.data)
	var b strings.Builder
	for len(enc) > 76 {
		b.WriteString(enc[:76] + "\r\n")
		enc = enc[76:]
	}
	b.WriteString(enc)
	return part{headers: h, body: []byte(b.String())}
}

func multipart(subtype, boundary string, parts ...part) part {
	var b bytes.Buffer
	for _, p := range parts {
		b.WriteString("--" + boundary + "\r\n")
		b.WriteString(strings.Join(p.headers, "\r\n") + "\r\n\r\n")
		b.Write(p.body)
		b.WriteString("\r\n")
	}
	b.WriteString("--" + boundary + "--\r\n")
	return part{
		headers: []string{fmt.Sprintf("Content-Type: multipart/%s; boundary=%q", subtype, boundary)},
		body:    b.Bytes(),
	}
}

type addr struct{ Name, Address string }

func (a addr) String() string {
	return (&netmail.Address{Name: a.Name, Address: a.Address}).String()
}

func addrList(as []addr) string {
	s := make([]string, len(as))
	for i, a := range as {
		s[i] = a.String()
	}
	return strings.Join(s, ", ")
}

// emlSpec describes one RFC 5322 message to render.
type emlSpec struct {
	id         string // Message-ID without angle brackets
	date       time.Time
	from       addr
	to, cc     []addr
	subject    string
	text, html string
	inlines    []attachment // related to the HTML part
	attach     []attachment
	inReplyTo  string
	references []string
}

func buildEML(s emlSpec, n int) []byte {
	var body part
	switch {
	case s.html == "":
		body = textPart("plain", s.text)
	default:
		htmlPart := textPart("html", s.html)
		if len(s.inlines) > 0 {
			rel := []part{htmlPart}
			for _, a := range s.inlines {
				a.inline = true
				rel = append(rel, binPart(a))
			}
			htmlPart = multipart("related", fmt.Sprintf("rel%d", n), rel...)
		}
		if s.text == "" {
			body = htmlPart
		} else {
			body = multipart("alternative", fmt.Sprintf("alt%d", n), textPart("plain", s.text), htmlPart)
		}
	}
	if len(s.attach) > 0 {
		ps := []part{body}
		for _, a := range s.attach {
			ps = append(ps, binPart(a))
		}
		body = multipart("mixed", fmt.Sprintf("mix%d", n), ps...)
	}

	var b bytes.Buffer
	h := func(k, v string) { b.WriteString(k + ": " + v + "\r\n") }
	h("Date", s.date.Format(time.RFC1123Z))
	h("From", s.from.String())
	if len(s.to) > 0 {
		h("To", addrList(s.to))
	}
	if len(s.cc) > 0 {
		h("Cc", addrList(s.cc))
	}
	h("Subject", mime.QEncoding.Encode("utf-8", s.subject))
	h("Message-ID", "<"+s.id+">")
	if s.inReplyTo != "" {
		h("In-Reply-To", "<"+s.inReplyTo+">")
	}
	if len(s.references) > 0 {
		refs := make([]string, len(s.references))
		for i, r := range s.references {
			refs[i] = "<" + r + ">"
		}
		h("References", strings.Join(refs, " "))
	}
	h("MIME-Version", "1.0")
	for _, l := range body.headers {
		b.WriteString(l + "\r\n")
	}
	b.WriteString("\r\n")
	b.Write(body.body)
	return b.Bytes()
}

// makePNG renders a small gradient logo.
func makePNG(size int, c1, c2 color.RGBA) []byte {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			t := float64(x+y) / float64(2*size-2)
			mix := func(a, b uint8) uint8 { return uint8(float64(a)*(1-t) + float64(b)*t) }
			img.Set(x, y, color.RGBA{mix(c1.R, c2.R), mix(c1.G, c2.G), mix(c1.B, c2.B), 255})
		}
	}
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}

// fakePDF is not a renderable document but starts/ends like one.
func fakePDF(title string, size int) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "%%PDF-1.4\n%% %s\n1 0 obj << /Type /Catalog >> endobj\n", title)
	for b.Len() < size {
		b.WriteString("% padding padding padding padding padding padding\n")
	}
	b.WriteString("%%EOF\n")
	return b.Bytes()
}

// fakeXLSX has the ZIP magic only.
func fakeXLSX(size int) []byte {
	b := []byte{'P', 'K', 3, 4}
	for len(b) < size {
		b = append(b, byte(len(b)*7))
	}
	return b
}
