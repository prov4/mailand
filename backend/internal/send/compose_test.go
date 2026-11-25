package send

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"net/mail"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-message"

	mailpkg "mailand/backend/internal/mail"
	"mailand/backend/internal/store"
)

func addr(s string) *mail.Address { a, _ := mail.ParseAddress(s); return a }

func parseRaw(t *testing.T, raw []byte) (*message.Entity, *mailpkg.Parsed) {
	t.Helper()
	e, err := message.Read(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	p, err := mailpkg.Parse(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	return e, p
}

func TestComposeHeadersAndBody(t *testing.T) {
	date := time.Date(2026, 3, 4, 10, 0, 0, 0, time.UTC)
	body := "Pozdrav,\nčšž ćđ — dugačka linija " + strings.Repeat("riječ ", 40) + "\n\nLp"
	raw, id, err := Compose(Input{
		From: mail.Address{Name: "Šime Žvić", Address: "me@example.com"},
		To:   []*mail.Address{addr("Ana Čović <ana@example.com>")}, Cc: []*mail.Address{addr("bob@example.com")},
		Subject: "Ponuda: čaj i žele", Body: body, Date: date,
		InReplyTo: "orig@x", References: []string{"root@x", "orig@x"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(strings.SplitN(id, "@", 2)[1], "example.com") {
		t.Errorf("message id %q", id)
	}
	e, p := parseRaw(t, raw)
	h := e.Header
	for k, want := range map[string]string{
		"Message-Id": "<" + id + ">", "In-Reply-To": "<orig@x>", "References": "<root@x> <orig@x>", "Mime-Version": "1.0",
	} {
		if got := h.Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	if h.Get("Bcc") != "" {
		t.Error("Bcc header present")
	}
	if !strings.Contains(string(raw), "=?utf-8?") && !strings.Contains(string(raw), "=?UTF-8?") {
		t.Error("expected RFC 2047 encoding in raw headers")
	}
	if strings.Contains(string(raw[:bytes.Index(raw, []byte("\r\n\r\n"))]), "č") {
		t.Error("raw non-ASCII in headers")
	}
	if p.Subject != "Ponuda: čaj i žele" || p.FromName != "Šime Žvić" || p.FromAddress != "me@example.com" {
		t.Errorf("parsed %+v", p)
	}
	if len(p.To) != 1 || p.To[0].Name != "Ana Čović" || len(p.Cc) != 1 {
		t.Errorf("to/cc %+v %+v", p.To, p.Cc)
	}
	if !p.HasDate || !p.Date.Equal(date) {
		t.Errorf("date %v", p.Date)
	}
	if p.MessageID != id || p.InReplyTo != "orig@x" || len(p.References) != 2 {
		t.Errorf("ids %+v", p)
	}
	if !strings.Contains(h.Get("Content-Type"), "utf-8") || h.Get("Content-Transfer-Encoding") != "quoted-printable" {
		t.Errorf("content headers %q %q", h.Get("Content-Type"), h.Get("Content-Transfer-Encoding"))
	}
	if p.TextBody != body {
		t.Errorf("body round trip:\n%q\n%q", p.TextBody, body)
	}
	for _, l := range strings.Split(string(raw), "\r\n") {
		if len(l) > 998 {
			t.Errorf("line too long: %d", len(l))
		}
	}
}

func TestComposeRejectsInjection(t *testing.T) {
	base := Input{From: mail.Address{Name: "Me", Address: "me@example.com"}, To: []*mail.Address{addr("a@example.com")}}
	for name, mod := range map[string]func(*Input){
		"subject": func(i *Input) { i.Subject = "hi\r\nBcc: evil@example.com" },
		"name":    func(i *Input) { i.From.Name = "Me\nBcc: x@y" },
		"replyto": func(i *Input) { i.InReplyTo = "a@b>\r\nX: y" },
		"refs":    func(i *Input) { i.References = []string{"a@b\r\nX: y"} },
	} {
		in := base
		mod(&in)
		if _, _, err := Compose(in); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestValidate(t *testing.T) {
	ok := Request{AccountID: "a", To: []string{"x@example.com"}}
	if err := Validate(ok); err != nil {
		t.Fatal(err)
	}
	bad := map[string]Request{
		"no account":   {To: []string{"x@example.com"}},
		"no rcpt":      {AccountID: "a"},
		"bad addr":     {AccountID: "a", To: []string{"not an address"}},
		"bad bcc":      {AccountID: "a", To: []string{"x@example.com"}, Bcc: []string{"@@"}},
		"crlf addr":    {AccountID: "a", To: []string{"x@example.com\r\nBcc: y@example.com"}},
		"crlf subject": {AccountID: "a", To: []string{"x@example.com"}, Subject: "a\nb"},
		"two in one":   {AccountID: "a", To: []string{"x@example.com, y@example.com"}},
	}
	for n, r := range bad {
		if err := Validate(r); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", n, err)
		}
	}
}

// --- store-backed Build ---

const origEML = "From: Alice <alice@example.com>\r\nTo: me@example.com\r\nSubject: Invoice\r\nMessage-ID: <orig@x>\r\n" +
	"References: <root@x> <mid@x>\r\nMIME-Version: 1.0\r\n" +
	"Content-Type: multipart/mixed; boundary=\"MIX\"\r\n\r\n--MIX\r\nContent-Type: text/plain\r\n\r\nSee attached.\r\n" +
	"--MIX\r\nContent-Type: application/pdf; name=\"inv.pdf\"\r\nContent-Disposition: attachment; filename=\"inv.pdf\"\r\n" +
	"Content-Transfer-Encoding: base64\r\n\r\nJVBERi0xLjQ=\r\n" +
	"--MIX\r\nContent-Type: image/png\r\nContent-ID: <logo@x>\r\nContent-Disposition: inline; filename=\"logo.png\"\r\n" +
	"Content-Transfer-Encoding: base64\r\n\r\niVBORw==\r\n--MIX--\r\n"

type fixture struct {
	st             *store.Store
	acct           store.Account
	inbox          string
	withBlob, noBl string // message ids
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	bg := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	acct := store.Account{ID: "acc", Name: "Me Myself", Email: "me@example.com", IMAPHost: "h", IMAPPort: 993, SMTPHost: "h", SMTPPort: 465}
	if err := st.UpsertAccount(bg, acct); err != nil {
		t.Fatal(err)
	}
	f, err := st.UpsertFolder(bg, store.Folder{AccountID: "acc", Name: "INBOX", Path: "INBOX", Kind: "folder", IsSyncSource: true})
	if err != nil {
		t.Fatal(err)
	}
	blob := filepath.Join(t.TempDir(), "orig.eml")
	if err := os.WriteFile(blob, []byte(origEML), 0o600); err != nil {
		t.Fatal(err)
	}
	put := func(uid uint32, mid, blobPath string) string {
		_, err := st.UpsertMessage(bg, store.Message{
			MessageHeader: store.MessageHeader{AccountID: "acc", FolderID: f.ID, UID: uid, Subject: "Invoice",
				FromName: "Alice", FromAddress: "alice@example.com", TimestampUnix: 1},
			MessageIDHeader: mid, TextBody: "x", RawBlobPath: blobPath,
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		hs, _, _ := st.ListMessages(bg, f.ID, 10, 0)
		for _, h := range hs {
			if h.UID == uid {
				return h.ID
			}
		}
		t.Fatal("not found")
		return ""
	}
	return &fixture{st: st, acct: acct, inbox: f.ID, withBlob: put(1, "orig@x", blob), noBl: put(2, "noblob@x", "")}
}

func TestBuildReply(t *testing.T) {
	fx := newFixture(t)
	bg := context.Background()
	b, err := Build(bg, fx.st, fx.acct, Request{AccountID: "acc", To: []string{"alice@example.com"},
		Bcc: []string{"secret@example.com"}, Subject: "Re: Invoice", TextBody: "ok", ReplyToID: fx.withBlob, InReplyTo: "ignored@x"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	_, p := parseRaw(t, b.Raw)
	if p.InReplyTo != "orig@x" {
		t.Errorf("in-reply-to %q", p.InReplyTo)
	}
	if got := strings.Join(p.References, ","); got != "root@x,mid@x,orig@x" {
		t.Errorf("references %q", got)
	}
	if strings.Contains(string(b.Raw), "secret@example.com") {
		t.Error("Bcc leaked into message")
	}
	if got := strings.Join(b.Recipients, ","); got != "alice@example.com,secret@example.com" {
		t.Errorf("envelope %q", got)
	}
	if p.HasAttachments {
		t.Error("reply must not attach")
	}

	// No blob: only the stored message id.
	b, err = Build(bg, fx.st, fx.acct, Request{AccountID: "acc", To: []string{"a@example.com"}, ReplyToID: fx.noBl}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	_, p = parseRaw(t, b.Raw)
	if p.InReplyTo != "noblob@x" || len(p.References) != 1 || p.References[0] != "noblob@x" {
		t.Errorf("no-blob reply %+v", p)
	}

	// Raw in_reply_to fallback and unknown reply id.
	b, err = Build(bg, fx.st, fx.acct, Request{AccountID: "acc", To: []string{"a@example.com"}, InReplyTo: "<raw@x>"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, p = parseRaw(t, b.Raw); p.InReplyTo != "raw@x" {
		t.Errorf("raw fallback %q", p.InReplyTo)
	}
	if _, err = Build(bg, fx.st, fx.acct, Request{AccountID: "acc", To: []string{"a@example.com"}, ReplyToID: "nope"}, time.Now()); !errors.Is(err, ErrInvalid) {
		t.Errorf("unknown reply id: %v", err)
	}
}

func TestBuildForward(t *testing.T) {
	fx := newFixture(t)
	bg := context.Background()
	b, err := Build(bg, fx.st, fx.acct, Request{AccountID: "acc", To: []string{"c@example.com"}, Subject: "Fwd: Invoice",
		TextBody: "see below", ForwardOfID: fx.withBlob}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	_, p := parseRaw(t, b.Raw)
	if len(p.Attachments) != 1 || p.Attachments[0].Filename != "inv.pdf" || p.Attachments[0].MIMEType != "application/pdf" || p.Attachments[0].Inline {
		t.Fatalf("attachments %+v", p.Attachments)
	}
	if p.TextBody != "see below" {
		t.Errorf("body %q", p.TextBody)
	}
	data, _, err := mailpkg.ExtractPart(bytes.NewReader(b.Raw), p.Attachments[0].PartPath)
	if err != nil || string(data) != "%PDF-1.4" {
		t.Errorf("attachment data %q %v", data, err)
	}
	if !strings.Contains(string(b.Raw), base64.StdEncoding.EncodeToString([]byte("%PDF-1.4"))) {
		t.Error("attachment not base64 encoded")
	}
	if _, err = Build(bg, fx.st, fx.acct, Request{AccountID: "acc", To: []string{"c@example.com"}, ForwardOfID: fx.noBl}, time.Now()); !errors.Is(err, ErrPrecondition) {
		t.Errorf("forward without blob: %v", err)
	}
}

func TestBuildUserAttachments(t *testing.T) {
	fx := newFixture(t)
	bg := context.Background()
	b, err := Build(bg, fx.st, fx.acct, Request{
		AccountID: "acc",
		To:        []string{"c@example.com"},
		Subject:   "Report",
		TextBody:  "attached",
		Attachments: []Attachment{
			{Filename: "notes.txt", MIMEType: "text/plain", Data: []byte("hello world")},
			{Filename: "raw.bin", MIMEType: "bad mime type", Data: []byte{1, 2, 3}},
		},
	}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	_, p := parseRaw(t, b.Raw)
	if len(p.Attachments) != 2 {
		t.Fatalf("got %d attachments, want 2: %+v", len(p.Attachments), p.Attachments)
	}
	if p.Attachments[0].Filename != "notes.txt" || p.Attachments[0].MIMEType != "text/plain" {
		t.Errorf("att 0: %+v", p.Attachments[0])
	}
	if p.Attachments[1].Filename != "raw.bin" || p.Attachments[1].MIMEType != "application/octet-stream" {
		t.Errorf("att 1: %+v", p.Attachments[1])
	}
	data, _, err := mailpkg.ExtractPart(bytes.NewReader(b.Raw), p.Attachments[0].PartPath)
	if err != nil || string(data) != "hello world" {
		t.Errorf("att 0 data: %q %v", data, err)
	}

	// Oversized attachment rejected.
	big := make([]byte, maxAttachmentTotal+1)
	if _, err = Build(bg, fx.st, fx.acct, Request{
		AccountID:   "acc",
		To:          []string{"c@example.com"},
		Attachments: []Attachment{{Filename: "huge.bin", Data: big}},
	}, time.Now()); !errors.Is(err, ErrInvalid) {
		t.Errorf("oversized attachment: got %v, want ErrInvalid", err)
	}
}
