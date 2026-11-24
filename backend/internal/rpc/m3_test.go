package rpc

import (
	"bytes"
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"

	"mailand/backend/internal/rpc/pb"
	"mailand/backend/internal/store"
)

var pngBytes = []byte("\x89PNG\r\n\x1a\nfakepngdata")

func b64(b []byte) string {
	s := base64.StdEncoding.EncodeToString(b)
	var sb strings.Builder
	for len(s) > 76 {
		sb.WriteString(s[:76] + "\r\n")
		s = s[76:]
	}
	return sb.String() + s
}

func inlineEML(extra []byte) string {
	s := "From: a@example.com\r\nTo: b@example.com\r\nSubject: Inline\r\nMIME-Version: 1.0\r\n" +
		"Content-Type: multipart/mixed; boundary=\"MIX\"\r\n\r\n" +
		"--MIX\r\nContent-Type: multipart/related; boundary=\"REL\"\r\n\r\n" +
		"--REL\r\nContent-Type: text/html; charset=utf-8\r\n\r\n<p>hi <img src=\"cid:logo@x\"></p>\r\n" +
		"--REL\r\nContent-Type: image/png; name=\"logo.png\"\r\nContent-ID: <logo@x>\r\nContent-Disposition: inline; filename=\"logo.png\"\r\n" +
		"Content-Transfer-Encoding: base64\r\n\r\n" + b64(pngBytes) + "\r\n--REL--\r\n" +
		"--MIX\r\nContent-Type: application/pdf\r\nContent-Disposition: attachment; filename=\"doc.pdf\"\r\nContent-Transfer-Encoding: base64\r\n\r\n"
	if extra == nil {
		extra = []byte("%PDF-1.4")
	}
	return s + b64(extra) + "\r\n--MIX--\r\n"
}

// addMsg stores a message in e.inbox with the given blob content ("" = no blob).
func (e *env) addMsg(t *testing.T, name string, uid uint32, blobContent string, html string) string {
	t.Helper()
	blob := ""
	if blobContent != "" {
		blob = filepath.Join(t.TempDir(), name+".eml")
		if err := os.WriteFile(blob, []byte(blobContent), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	m := store.Message{
		MessageHeader: store.MessageHeader{AccountID: "gm", FolderID: e.inbox, UID: uid, Subject: name,
			TimestampUnix: time.Now().Unix(), HasAttachments: blob != ""},
		TextBody: "body", HTMLBody: html, RawBlobPath: blob,
	}
	if _, err := e.st.UpsertMessage(ctx(t), m, nil); err != nil {
		t.Fatal(err)
	}
	hs, _, _ := e.st.ListMessages(ctx(t), e.inbox, 500, 0)
	for _, h := range hs {
		if h.UID == uid {
			return h.ID
		}
	}
	t.Fatal("message not found")
	return ""
}

func TestFolderKindAndSpecialUse(t *testing.T) {
	e := setup(t, true)
	if _, err := e.st.UpsertFolder(ctx(t), store.Folder{AccountID: "gm", Name: "INBOX", Path: "INBOX", Kind: "gmail_label", SpecialUse: `\Inbox`}); err != nil {
		t.Fatal(err)
	}
	fl, err := e.c.ListFolders(ctx(t), &pb.AccountRequest{AccountId: "gm"})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range fl.Folders {
		switch f.Path {
		case "INBOX":
			if f.Kind != "gmail_label" || f.SpecialUse != `\Inbox` {
				t.Errorf("inbox = %v", f)
			}
		case "Projects":
			if f.Kind != "gmail_label" || f.SpecialUse != "" {
				t.Errorf("projects = %v", f)
			}
		}
	}
}

func TestInlineAttachmentsAndGetAttachment(t *testing.T) {
	e := setup(t, true)
	cx := ctx(t)
	id := e.addMsg(t, "inline", 200, inlineEML(nil), "<p>hi</p>")

	d, err := e.c.GetMessage(cx, &pb.MessageRequest{MessageId: id})
	if err != nil || len(d.Attachments) != 2 {
		t.Fatalf("detail: %v %v", d, err)
	}
	var in, pdf *pb.AttachmentInfo
	for _, a := range d.Attachments {
		if a.Inline {
			in = a
		} else {
			pdf = a
		}
	}
	if in == nil || pdf == nil || in.ContentId != "logo@x" || in.MimeType != "image/png" || pdf.ContentId != "" || pdf.Filename != "doc.pdf" {
		t.Fatalf("attachments: %v", d.Attachments)
	}

	got, err := e.c.GetAttachment(cx, &pb.AttachmentRequest{MessageId: id, AttachmentId: in.Id})
	if err != nil || !bytes.Equal(got.Data, pngBytes) || got.Info.ContentId != "logo@x" || !got.Info.Inline || got.Info.SizeBytes != uint64(len(pngBytes)) {
		t.Fatalf("inline: %v %v", got, err)
	}
	got, err = e.c.GetAttachment(cx, &pb.AttachmentRequest{MessageId: id, AttachmentId: pdf.Id})
	if err != nil || string(got.Data) != "%PDF-1.4" || got.Info.Filename != "doc.pdf" {
		t.Fatalf("pdf: %v %v", got, err)
	}

	_, err = e.c.GetAttachment(cx, &pb.AttachmentRequest{MessageId: id, AttachmentId: "9.9"})
	wantCode(t, err, codes.NotFound, "unknown part")
	_, err = e.c.GetAttachment(cx, &pb.AttachmentRequest{MessageId: "missing", AttachmentId: "1"})
	wantCode(t, err, codes.NotFound, "unknown message")
	_, err = e.c.GetAttachment(cx, &pb.AttachmentRequest{AttachmentId: "1"})
	wantCode(t, err, codes.InvalidArgument, "empty message id")
	_, err = e.c.GetAttachment(cx, &pb.AttachmentRequest{MessageId: id})
	wantCode(t, err, codes.InvalidArgument, "empty attachment id")

	noBlob := e.addMsg(t, "noblob", 201, "", "")
	_, err = e.c.GetAttachment(cx, &pb.AttachmentRequest{MessageId: noBlob, AttachmentId: "1"})
	wantCode(t, err, codes.FailedPrecondition, "no blob")
}

func TestGetAttachmentTooLarge(t *testing.T) {
	if testing.Short() {
		t.Skip("writes a 51 MiB attachment")
	}
	e := setup(t, true)
	id := e.addMsg(t, "huge", 202, inlineEML(make([]byte, MaxAttachmentBytes+1)), "")
	cx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	_, err := e.c.GetAttachment(cx, &pb.AttachmentRequest{MessageId: id, AttachmentId: "2"})
	wantCode(t, err, codes.ResourceExhausted, "too large")
}

func TestLargeMessageRoundTrip(t *testing.T) {
	e := setup(t, true)
	cx := ctx(t)
	html := "<p>" + strings.Repeat("šđčćž ", 1<<20) + "</p>" // > 6 MiB, over the 4 MiB default
	id := e.addMsg(t, "big", 203, inlineEML(bytes.Repeat([]byte("x"), 5<<20)), html)
	d, err := e.c.GetMessage(cx, &pb.MessageRequest{MessageId: id})
	if err != nil || len(d.HtmlBody) != len(html) {
		t.Fatalf("big body: %v len=%d want %d", err, len(d.GetHtmlBody()), len(html))
	}
	a, err := e.c.GetAttachment(cx, &pb.AttachmentRequest{MessageId: id, AttachmentId: "2"})
	if err != nil || len(a.Data) != 5<<20 {
		t.Fatalf("big attachment: %v", err)
	}
}

func TestOfflineSyncer(t *testing.T) {
	e := setup(t, false)
	off := NewOfflineSyncer(e.st, e.broker)
	e.srv.sync = off
	cx := ctx(t)

	ch, cancel := e.broker.Subscribe()
	defer cancel()
	next := func() *pb.DaemonEvent {
		select {
		case ev := <-ch:
			return ev
		case <-time.After(3 * time.Second):
			t.Fatal("no event")
			return nil
		}
	}

	id := e.msgs["multi"]
	if _, err := e.c.SetMessageFlags(cx, &pb.SetFlagsRequest{MessageIds: []string{id}, IsRead: ptr(true), IsFlagged: ptr(true)}); err != nil {
		t.Fatal(err)
	}
	m, err := e.st.GetMessage(cx, id)
	if err != nil || !m.IsRead || !m.IsFlagged {
		t.Fatalf("store not updated: %+v %v", m, err)
	}
	if ev := next(); ev.Type != pb.DaemonEvent_FLAGS_UPDATED || ev.AccountId != "gm" {
		t.Errorf("event = %v", ev)
	}

	if _, err := e.c.TriggerSync(cx, &pb.AccountRequest{AccountId: "wk"}); err != nil {
		t.Fatal(err)
	}
	if ev := next(); ev.Type != pb.DaemonEvent_SYNC_STARTED || ev.AccountId != "wk" {
		t.Errorf("event = %v", ev)
	}
	if ev := next(); ev.Type != pb.DaemonEvent_SYNC_FINISHED || ev.AccountId != "wk" {
		t.Errorf("event = %v", ev)
	}
	_, err = e.c.TriggerSync(cx, &pb.AccountRequest{AccountId: "nope"})
	wantCode(t, err, codes.NotFound, "unknown account")
	if err := off.TriggerSync("nope"); err == nil {
		t.Error("offline TriggerSync of unknown account should fail")
	}
}

func ptr[T any](v T) *T { return &v }
