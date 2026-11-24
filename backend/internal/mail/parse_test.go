package mail

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func load(t testing.TB, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func parse(t *testing.T, name string) *Parsed {
	t.Helper()
	p, err := Parse(bytes.NewReader(load(t, name)))
	if err != nil {
		t.Fatalf("Parse(%s): %v", name, err)
	}
	return p
}

func eq[T comparable](t *testing.T, what string, got, want T) {
	t.Helper()
	if got != want {
		t.Errorf("%s = %#v, want %#v", what, got, want)
	}
}

func TestPlain(t *testing.T) {
	p := parse(t, "plain.eml")
	eq(t, "Subject", p.Subject, "Sastanak u petak")
	eq(t, "FromName", p.FromName, "Ana Horvat")
	eq(t, "FromAddress", p.FromAddress, "ana@example.com")
	eq(t, "len To", len(p.To), 2)
	eq(t, "To[1]", p.To[1], Address{Address: "marko@example.org"})
	eq(t, "Cc", p.Cc[0], Address{Name: "Kovač, Petra", Address: "petra@example.net"})
	eq(t, "HasDate", p.HasDate, true)
	if !p.Date.Equal(time.Date(2026, 3, 3, 9, 15, 0, 0, time.UTC)) {
		t.Errorf("Date = %v", p.Date)
	}
	eq(t, "MessageID", p.MessageID, "abc123@example.com")
	eq(t, "InReplyTo", p.InReplyTo, "prev1@example.org")
	eq(t, "References", strings.Join(p.References, ","), "root0@example.org,prev1@example.org")
	eq(t, "Snippet", p.Snippet, "Bok Josipe, vidimo se u petak u 10h. Ana")
	eq(t, "TextFromHTML", p.TextFromHTML, false)
	eq(t, "HasAttachments", p.HasAttachments, false)
}

func TestAlternative(t *testing.T) {
	p := parse(t, "alternative.eml")
	eq(t, "TextBody", p.TextBody, "Plain version of the message.")
	if !strings.Contains(p.HTMLBody, "<b>version</b>") {
		t.Errorf("HTMLBody = %q", p.HTMLBody)
	}
	eq(t, "TextFromHTML", p.TextFromHTML, false)
}

func TestNewsletterHTMLOnly(t *testing.T) {
	p := parse(t, "newsletter.eml")
	eq(t, "TextFromHTML", p.TextFromHTML, true)
	for _, bad := range []string{"secret_tracking", "color:red", "alert", "preheader", "Deals", "pixel", "<"} {
		if strings.Contains(p.TextBody, bad) {
			t.Errorf("TextBody contains %q: %q", bad, p.TextBody)
		}
	}
	for _, want := range []string{"Big Sale", "Save 50% on “widgets” & gadgets.", "Buy now (https://shop.example/buy)"} {
		if !strings.Contains(p.TextBody, want) {
			t.Errorf("TextBody missing %q: %q", want, p.TextBody)
		}
	}
	if p.HTMLBody == "" {
		t.Error("HTMLBody empty")
	}
}

func TestMixedPDF(t *testing.T) {
	p := parse(t, "mixed_pdf.eml")
	eq(t, "Subject", p.Subject, "Račun 2026-03")
	eq(t, "FromName", p.FromName, "Računovodstvo")
	eq(t, "TextBody", p.TextBody, "U privitku je račun.")
	eq(t, "HasAttachments", p.HasAttachments, true)
	eq(t, "len Attachments", len(p.Attachments), 1)
	a := p.Attachments[0]
	eq(t, "PartPath", a.PartPath, "2")
	eq(t, "Filename", a.Filename, "račun_čšž.pdf")
	eq(t, "MIMEType", a.MIMEType, "application/pdf")
	eq(t, "Size", a.Size, int64(len("%PDF-1.4 fake pdf content ")*10))
	eq(t, "Inline", a.Inline, false)

	data, meta, err := ExtractPart(bytes.NewReader(load(t, "mixed_pdf.eml")), "2")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(data, []byte("%PDF-1.4")) || int64(len(data)) != a.Size {
		t.Errorf("ExtractPart data = %d bytes", len(data))
	}
	eq(t, "meta.Filename", meta.Filename, a.Filename)
	if _, _, err := ExtractPart(bytes.NewReader(load(t, "mixed_pdf.eml")), "9"); err == nil {
		t.Error("expected error for missing part")
	}
}

func TestRelatedInline(t *testing.T) {
	p := parse(t, "related_inline.eml")
	eq(t, "HasAttachments", p.HasAttachments, false)
	eq(t, "len Attachments", len(p.Attachments), 1)
	a := p.Attachments[0]
	eq(t, "PartPath", a.PartPath, "2")
	eq(t, "ContentID", a.ContentID, "logo@x")
	eq(t, "Inline", a.Inline, true)
	eq(t, "TextBody", p.TextBody, "See the logo.")
	if !strings.Contains(p.HTMLBody, "cid:logo@x") {
		t.Errorf("HTMLBody = %q", p.HTMLBody)
	}
	_, meta, err := ExtractPart(bytes.NewReader(load(t, "related_inline.eml")), "2")
	if err != nil || meta.ContentID != "logo@x" || meta.Size != 16 {
		t.Errorf("ExtractPart meta=%+v err=%v", meta, err)
	}
}

func TestCharsets(t *testing.T) {
	const want = "Čačak, ćevapi, žaba, šuma, đak. ČĆŽŠĐ."
	eq(t, "latin2 body", parse(t, "latin2.eml").TextBody, want)
	eq(t, "latin2 subject", parse(t, "latin2.eml").Subject, "čačak")
	eq(t, "cp1250 body", parse(t, "cp1250.eml").TextBody, want)
}

func TestEncodings(t *testing.T) {
	eq(t, "qp", parse(t, "qp.eml").TextBody, "Čačak, ćevapi, žaba, šuma, đak.")
	eq(t, "base64", parse(t, "base64.eml").TextBody, "Zdravo svijete, Čačak i đak.")
}

func TestRFC2047(t *testing.T) {
	p := parse(t, "rfc2047.eml")
	eq(t, "Subject", p.Subject, "Račun za ožujak")
	eq(t, "FromName", p.FromName, "Ana Čović")
	eq(t, "To name", p.To[0].Name, "Šime Šimić")
	eq(t, "HasDate", p.HasDate, true)
}

func TestMissingDateAndContentType(t *testing.T) {
	p := parse(t, "no_ctype.eml")
	eq(t, "HasDate", p.HasDate, false)
	eq(t, "TextBody", p.TextBody, "Just a body, no content type.")
}

func TestUnknownCharset(t *testing.T) {
	p := parse(t, "badcharset.eml")
	if !strings.HasPrefix(p.TextBody, "Hello bad charset caf") {
		t.Errorf("TextBody = %q", p.TextBody)
	}
}

func TestMalformed(t *testing.T) {
	p := parse(t, "malformed.eml")
	eq(t, "Subject", p.Subject, "Broken")
	eq(t, "TextBody", p.TextBody, "First part without end boundary.")
	eq(t, "len Attachments", len(p.Attachments), 1)
	eq(t, "Filename", p.Attachments[0].Filename, "x.bin")
}

func TestGarbage(t *testing.T) {
	for _, in := range []string{
		"", "\n", "\n\n\n", "garbage", ":::\n\nbody",
		"Content-Type: multipart/mixed\n\nno boundary",
		"Content-Type: multipart/mixed; boundary=\n\n--\n",
		"Content-Type: multipart/alternative; boundary=x\n\n--x\n\n--x\nContent-Type: text/html\n\n<p>hi",
		"Subject: =?bogus?Q?abc?=\nFrom: <<>>\nTo: a@b, ,,\n\nx",
	} {
		if _, err := Parse(strings.NewReader(in)); err != nil {
			t.Logf("Parse(%q) error: %v", in, err)
		}
	}
}

func TestMultipleInlineTextParts(t *testing.T) {
	in := "Content-Type: multipart/mixed; boundary=b\n\n--b\nContent-Type: text/plain\n\nOne\n--b\nContent-Type: text/plain\nContent-Disposition: attachment; filename=n.txt\n\nHidden\n--b\nContent-Type: text/plain\n\nTwo\n--b--\n"
	p, _ := Parse(strings.NewReader(in))
	eq(t, "TextBody", p.TextBody, "One\n\nTwo")
	eq(t, "HasAttachments", p.HasAttachments, true)
	eq(t, "PartPath", p.Attachments[0].PartPath, "2")
}

func TestBodyCap(t *testing.T) {
	in := "Content-Type: text/plain\n\n" + strings.Repeat("a", maxBodyBytes+1000)
	p, _ := Parse(strings.NewReader(in))
	if len(p.TextBody) > maxBodyBytes {
		t.Errorf("TextBody not capped: %d", len(p.TextBody))
	}
}

func TestSnippet(t *testing.T) {
	long := strings.Repeat("čšž ", 200)
	if n := len([]rune(makeSnippet(long))); n != snippetRunes {
		t.Errorf("snippet runes = %d", n)
	}
	eq(t, "quote only", makeSnippet("> quoted only"), "> quoted only")
	eq(t, "sig", makeSnippet("Hi\n\nthere\n-- \nSig"), "Hi there")
}

func TestHTMLToText(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"entities", "<p>Tom &amp; Jerry &lt;3 &eacute;&nbsp;x &#269;</p>", "Tom & Jerry <3 é x č"},
		{"scripts", "<html><head><title>T</title><style>p{}</style></head><body>a<script>evil()</script>b<!-- c --></body></html>", "ab"},
		{"blocks", "<div>one</div><div>two</div>line<br>next<h1>H</h1>", "one\ntwo\nline\nnext\n\nH"},
		{"lists", "<ul><li>a<ul><li>b</li></ul></li><li>c</li></ul>", "- a\n- b\n- c"},
		{"whitespace", "<p>  a \t\n  b  </p>\n\n\n\n<p>c</p><p></p><p></p><p>d</p>", "a b\n\nc\n\nd"},
		{"link differs", `<a href="https://x.example/p">click</a>`, "click (https://x.example/p)"},
		{"link same", `<a href="https://x.example">https://x.example</a>`, "https://x.example"},
		{"mailto", `<a href="mailto:a@b.c">mail me</a>`, "mail me"},
		{"hidden", `<div style="display: none">pre</div><p>body</p>`, "body"},
		{"table", "<table><tr><td>a</td><td>b</td></tr><tr><td>c</td><td>d</td></tr></table>", "a b\nc d"},
		{"pre", "<pre>a\n  b</pre>", "a\nb"},
		{"fragment", "just text", "just text"},
		{"unclosed", "<p>one<p>two <b>bold", "one\n\ntwo bold"},
		{"empty", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := HTMLToText(c.in); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestHTMLToTextDeepNesting(t *testing.T) {
	HTMLToText(strings.Repeat("<div>", 100000) + "x")
}

func FuzzParse(f *testing.F) {
	ents, _ := filepath.Glob("testdata/*.eml")
	for _, n := range ents {
		b, _ := os.ReadFile(n)
		f.Add(b)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		p, _ := Parse(bytes.NewReader(data))
		for _, a := range p.Attachments {
			_, _, _ = ExtractPart(bytes.NewReader(data), a.PartPath)
		}
	})
}
