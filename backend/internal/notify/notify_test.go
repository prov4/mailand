package notify

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"mailand/backend/internal/store"
)

type fakeSender struct {
	mu   sync.Mutex
	sent []Notification
}

func (f *fakeSender) Send(_ context.Context, n Notification) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, n)
	return nil
}

func (f *fakeSender) take() []Notification {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := append([]Notification(nil), f.sent...)
	f.sent = nil
	return out
}

func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestMailNotifierInitialAndNew(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	now := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)

	if err := st.UpsertAccount(ctx, store.Account{
		ID: "g", Name: "Gmail", Email: "me@gmail.com",
		IMAPHost: "imap.gmail.com", IMAPPort: 993, SMTPHost: "smtp.gmail.com", SMTPPort: 465,
	}); err != nil {
		t.Fatal(err)
	}
	allMail, err := st.UpsertFolder(ctx, store.Folder{
		ID: "g:[Gmail]/All Mail", AccountID: "g", Name: "All Mail", Path: "[Gmail]/All Mail",
		SpecialUse: `\All`, IsSyncSource: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	inbox, err := st.UpsertFolder(ctx, store.Folder{
		ID: "g:INBOX", AccountID: "g", Name: "INBOX", Path: "INBOX",
		Kind: "gmail_label", SpecialUse: `\Inbox`, IsSyncSource: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	sent, err := st.UpsertFolder(ctx, store.Folder{
		ID: "g:Sent", AccountID: "g", Name: "Sent", Path: "[Gmail]/Sent Mail",
		Kind: "gmail_label", SpecialUse: `\Sent`, IsSyncSource: false,
	})
	if err != nil {
		t.Fatal(err)
	}

	fs := &fakeSender{}
	mn := NewMailNotifier(st, fs, nil)
	mn.now = func() time.Time { return now }
	if err := mn.Init(ctx); err != nil {
		t.Fatal(err)
	}

	// Brand-new account: initial backlog and first completed pass must NOT notify.
	if _, err := st.UpsertMessage(ctx, store.Message{
		MessageHeader: store.MessageHeader{
			AccountID: "g", FolderID: allMail.ID, UID: 1,
			Subject: "Historical mail", FromName: "Old", FromAddress: "old@example.com",
			TimestampUnix: now.Add(-1 * time.Hour).Unix(), IsRead: false,
		},
	}, []string{allMail.ID, inbox.ID}); err != nil {
		t.Fatal(err)
	}
	mn.OnSyncFinished(ctx, "g", "backlog")
	mn.OnSyncFinished(ctx, "g", "")
	if got := fs.take(); len(got) != 0 {
		t.Fatalf("expected 0 notifications on initial sync, got %+v", got)
	}

	// New unread message in Sent (not INBOX) + already-read message in INBOX + old message in INBOX -> none should notify.
	for _, tc := range []struct {
		uid     uint32
		read    bool
		ts      int64
		folders []string
	}{
		{2, false, now.Unix(), []string{allMail.ID, sent.ID}},
		{3, true, now.Unix(), []string{allMail.ID, inbox.ID}},
		{4, false, now.Add(-48 * time.Hour).Unix(), []string{allMail.ID, inbox.ID}},
	} {
		if _, err := st.UpsertMessage(ctx, store.Message{
			MessageHeader: store.MessageHeader{
				AccountID: "g", FolderID: allMail.ID, UID: tc.uid,
				Subject: "Ignore", FromAddress: "x@example.com",
				TimestampUnix: tc.ts, IsRead: tc.read,
			},
		}, tc.folders); err != nil {
			t.Fatal(err)
		}
	}
	mn.OnSyncFinished(ctx, "g", "")
	if got := fs.take(); len(got) != 0 {
		t.Fatalf("expected 0 notifications for sent/read/old, got %+v", got)
	}

	// Single new unread INBOX message -> detailed notification.
	if _, err := st.UpsertMessage(ctx, store.Message{
		MessageHeader: store.MessageHeader{
			AccountID: "g", FolderID: allMail.ID, UID: 5,
			Subject: "Lunch?", FromName: "Alice", FromAddress: "alice@example.com",
			TimestampUnix: now.Unix(), IsRead: false, Snippet: "Want to grab tacos at noon?",
		},
	}, []string{allMail.ID, inbox.ID}); err != nil {
		t.Fatal(err)
	}
	mn.OnSyncFinished(ctx, "g", "")
	got := fs.take()
	if len(got) != 1 {
		t.Fatalf("expected 1 notification, got %d", len(got))
	}
	if got[0].Summary != "Alice" || !strings.Contains(got[0].Body, "Lunch?") || !strings.Contains(got[0].Body, "tacos") {
		t.Errorf("unexpected single notification: %+v", got[0])
	}

	// Repeat sync with no new mail -> no duplicate notification.
	mn.OnSyncFinished(ctx, "g", "")
	if got := fs.take(); len(got) != 0 {
		t.Fatalf("expected 0 duplicate notifications, got %+v", got)
	}

	// Batch of 4 new unread INBOX messages -> single grouped notification.
	for i := 0; i < 4; i++ {
		if _, err := st.UpsertMessage(ctx, store.Message{
			MessageHeader: store.MessageHeader{
				AccountID: "g", FolderID: allMail.ID, UID: uint32(10 + i),
				Subject: fmt.Sprintf("Msg %d", i), FromName: fmt.Sprintf("Sender %d", i),
				TimestampUnix: now.Add(time.Duration(i) * time.Second).Unix(), IsRead: false,
			},
		}, []string{allMail.ID, inbox.ID}); err != nil {
			t.Fatal(err)
		}
	}
	mn.OnSyncFinished(ctx, "g", "")
	got = fs.take()
	if len(got) != 1 {
		t.Fatalf("expected 1 grouped notification, got %d", len(got))
	}
	if got[0].Summary != "4 new emails" || !strings.Contains(got[0].Body, "+1 more") {
		t.Errorf("unexpected batch notification: %+v", got[0])
	}
}

func makeICS(uid, summary, dtstart, dtend, extra string) string {
	return "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nBEGIN:VEVENT\r\n" +
		"UID:" + uid + "\r\n" +
		"SUMMARY:" + summary + "\r\n" +
		"DTSTART:" + dtstart + "\r\n" +
		"DTEND:" + dtend + "\r\n" +
		extra +
		"END:VEVENT\r\nEND:VCALENDAR\r\n"
}

func TestCalendarNotifier(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	now := time.Date(2026, 11, 25, 14, 0, 0, 0, time.UTC)

	if err := st.UpsertAccount(ctx, store.Account{
		ID: "a", Name: "Work", Email: "me@example.com",
		IMAPHost: "imap.example.com", IMAPPort: 993, SMTPHost: "smtp.example.com", SMTPPort: 465,
	}); err != nil {
		t.Fatal(err)
	}
	calID, err := st.UpsertCalendar(ctx, store.Calendar{AccountID: "a", Path: "/cal/", Name: "Work"})
	if err != nil {
		t.Fatal(err)
	}

	soon := now.Add(8 * time.Minute)
	soonEnd := soon.Add(30 * time.Minute)
	later := now.Add(30 * time.Minute)
	laterEnd := later.Add(30 * time.Minute)
	fmtUTC := func(tm time.Time) string { return tm.UTC().Format("20060102T150405Z") }

	soonEndUnix := soonEnd.Unix()
	laterEndUnix := laterEnd.Unix()
	allDayStart := time.Date(2026, 11, 25, 0, 0, 0, 0, time.UTC).Unix()
	allDayEnd := time.Date(2026, 11, 26, 0, 0, 0, 0, time.UTC).Unix()

	objs := []store.CalendarObject{
		{
			CalendarID: calID, Path: "standup.ics", UID: "u-standup", Summary: "Team Standup",
			FirstStart: soon.Unix(), LastEnd: &soonEndUnix,
			Data: makeICS("u-standup", "Team Standup", fmtUTC(soon), fmtUTC(soonEnd), "LOCATION:Room 4B\r\n"),
		},
		{
			CalendarID: calID, Path: "later.ics", UID: "u-later", Summary: "Later Sync",
			FirstStart: later.Unix(), LastEnd: &laterEndUnix,
			Data: makeICS("u-later", "Later Sync", fmtUTC(later), fmtUTC(laterEnd), ""),
		},
		{
			CalendarID: calID, Path: "cancelled.ics", UID: "u-cancel", Summary: "Cancelled",
			FirstStart: soon.Unix(), LastEnd: &soonEndUnix,
			Data: makeICS("u-cancel", "Cancelled", fmtUTC(soon), fmtUTC(soonEnd), "STATUS:CANCELLED\r\n"),
		},
		{
			CalendarID: calID, Path: "declined.ics", UID: "u-declined", Summary: "Declined",
			FirstStart: soon.Unix(), LastEnd: &soonEndUnix,
			Data: makeICS("u-declined", "Declined", fmtUTC(soon), fmtUTC(soonEnd),
				"ORGANIZER:mailto:boss@example.com\r\nATTENDEE;PARTSTAT=DECLINED:mailto:me@example.com\r\n"),
		},
		{
			CalendarID: calID, Path: "allday.ics", UID: "u-allday", Summary: "Holiday",
			FirstStart: allDayStart, LastEnd: &allDayEnd,
			Data: "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nBEGIN:VEVENT\r\nUID:u-allday\r\nSUMMARY:Holiday\r\nDTSTART;VALUE=DATE:20261125\r\nDTEND;VALUE=DATE:20261126\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n",
		},
	}
	if err := st.UpsertCalendarObjects(ctx, objs); err != nil {
		t.Fatal(err)
	}

	fs := &fakeSender{}
	cn := NewCalendarNotifier(st, fs, 10*time.Minute, false, nil)
	cn.now = func() time.Time { return now }
	cn.local = time.UTC

	// First check: only "Team Standup" should fire.
	cn.Check(ctx)
	got := fs.take()
	if len(got) != 1 {
		t.Fatalf("expected 1 meeting notification, got %+v", got)
	}
	if got[0].Summary != "Team Standup" || !strings.Contains(got[0].Body, "In 8 min (14:08)") || !strings.Contains(got[0].Body, "Room 4B") {
		t.Errorf("unexpected event notification: %+v", got[0])
	}

	// Second check (even on a fresh CalendarNotifier, simulating restart): must not re-notify.
	cn2 := NewCalendarNotifier(st, fs, 10*time.Minute, false, nil)
	cn2.now = func() time.Time { return now.Add(1 * time.Minute) }
	cn2.local = time.UTC
	cn2.Check(ctx)
	if got := fs.take(); len(got) != 0 {
		t.Fatalf("expected 0 duplicate notifications after restart, got %+v", got)
	}

	// Advance clock to 14:22 -> "Later Sync" (14:30) is now within 10 min and should fire.
	cn2.now = func() time.Time { return now.Add(22 * time.Minute) }
	cn2.Check(ctx)
	got = fs.take()
	if len(got) != 1 || got[0].Summary != "Later Sync" {
		t.Fatalf("expected Later Sync notification, got %+v", got)
	}
}
