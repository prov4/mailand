package mail

import (
	"reflect"
	"testing"

	"github.com/emersion/go-imap/v2"

	"mailand/backend/internal/store"
)

func TestClassify(t *testing.T) {
	l := func(name string, attrs ...imap.MailboxAttr) *imap.ListData {
		return &imap.ListData{Mailbox: name, Attrs: attrs}
	}
	cases := []struct {
		l      *imap.ListData
		gmail  bool
		sp     string
		kind   string
		source bool
	}{
		{l("INBOX"), true, `\Inbox`, "gmail_label", false},
		{l("[Gmail]/Alle Nachrichten", imap.MailboxAttrAll), true, `\All`, "folder", true},
		{l("[Gmail]/Spam", imap.MailboxAttrJunk), true, `\Junk`, "folder", true},
		{l("[Gmail]/Trash", imap.MailboxAttrTrash), true, `\Trash`, "folder", true},
		{l("[Gmail]/Sent Mail", imap.MailboxAttrSent), true, `\Sent`, "gmail_label", false},
		{l("Work"), true, "", "gmail_label", false},
		{l("inbox"), false, `\Inbox`, "folder", true},
		{l("Sent", imap.MailboxAttrSent), false, `\Sent`, "folder", true},
	}
	for _, c := range cases {
		sp, kind, src := classify(c.l, c.gmail)
		if sp != c.sp || kind != c.kind || src != c.source {
			t.Errorf("%s gmail=%v: got %q %q %v", c.l.Mailbox, c.gmail, sp, kind, src)
		}
	}
}

func TestNormMsgID(t *testing.T) {
	if normMsgID(" <Ab@X.com> ") != "ab@x.com" || normMsgID("ab@x.com") != "ab@x.com" {
		t.Error("normalisation")
	}
}

func TestApplyLabels(t *testing.T) {
	const all, inbox, work, starred = "g:All", "g:INBOX", "g:Work", "g:Starred"
	refs := []store.MessageRef{
		{Seq: 1, FolderID: all, MessageIDHeader: "a@x"},
		{Seq: 2, FolderID: all, MessageIDHeader: "B@x"},
		{Seq: 3, FolderID: all, MessageIDHeader: "c@x"},
		{Seq: 4, FolderID: "g:Trash", MessageIDHeader: "d@x"},
	}
	current := map[int64][]string{
		1: {all},
		2: {all, work},  // Work is not re-read this pass: must be kept
		3: {all, inbox}, // INBOX re-read and no longer contains c: label dropped
		4: {"g:Trash"},
	}
	fetched := map[string][]string{
		inbox:   {"<A@x>", "<b@x>", "<ghost@x>"}, // ghost has no stored message
		starred: {"<a@x>"},
	}
	got, unmapped := applyLabels(refs, current, fetched)
	want := map[int64][]string{
		1: {all, inbox, starred},
		2: {all, inbox, work},
		3: {all},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("changes:\n got %v\nwant %v", got, want)
	}
	if unmapped != 1 {
		t.Errorf("unmapped %d", unmapped)
	}

	// Idempotent: applying the result again changes nothing.
	for seq, fs := range got {
		current[seq] = fs
	}
	if again, _ := applyLabels(refs, current, fetched); len(again) != 0 {
		t.Errorf("not idempotent: %v", again)
	}
}

func TestApplyLabelsDuplicateMessageIDAndRepairsMissingSource(t *testing.T) {
	refs := []store.MessageRef{
		{Seq: 1, FolderID: "g:All", MessageIDHeader: "dup@x"},
		{Seq: 2, FolderID: "g:All", MessageIDHeader: "dup@x"},
	}
	// Seq 2 lost its membership rows entirely: the source folder is restored.
	got, _ := applyLabels(refs, map[int64][]string{1: {"g:All"}}, map[string][]string{"g:INBOX": {"<dup@x>"}})
	want := map[int64][]string{1: {"g:All", "g:INBOX"}, 2: {"g:All", "g:INBOX"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

// End-to-end label sync against the memory server, driving the Gmail steps by
// hand (the memory server has no SPECIAL-USE or X-GM extensions).
func TestSyncLabelsEndToEnd(t *testing.T) {
	e := newEnv(t)
	for _, n := range []string{"All", "Work"} {
		if err := e.user.Create(n, nil); err != nil {
			t.Fatal(err)
		}
	}
	m1, m2 := rawMsg("One", "1@x", "a"), rawMsg("Two", "2@x", "b")
	e.appendMsg("All", m1)
	e.appendMsg("All", m2)
	e.appendMsg("INBOX", m1)
	e.appendMsg("Work", m1)
	e.appendMsg("Work", m2)

	e.m = NewManager(e.cfg, e.st, e.blobDir, nil, nil)
	e.m.plain = true
	e.m.password = func(string) (string, error) { return testPass, nil }
	a := &e.cfg.Accounts[0]
	if err := e.st.UpsertAccount(bg, store.Account{ID: a.ID, Name: "n", Email: a.Email, IMAPHost: a.IMAPHost, IMAPPort: a.IMAPPort, SMTPHost: "s", SMTPPort: 1}); err != nil {
		t.Fatal(err)
	}
	w := newWorker(e.m, a)
	c, err := w.connect(bg, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.close()

	up := func(path, kind string, src bool) store.Folder {
		f, err := e.st.UpsertFolder(bg, store.Folder{ID: folderID("acc", path), AccountID: "acc", Name: path, Path: path, Kind: kind, IsSyncSource: src})
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	all := up("All", "folder", true)
	folders := []store.Folder{all, up("INBOX", "gmail_label", false), up("Work", "gmail_label", false)}
	if n, err := w.syncSource(bg, c, all); err != nil || n != 2 {
		t.Fatalf("source sync: %d %v", n, err)
	}
	if err := w.syncLabels(bg, c, folders, false); err != nil {
		t.Fatal(err)
	}

	membership := func(subject string) []string {
		for _, h := range e.msgs("acc:All") {
			if h.Subject == subject {
				m, _ := e.st.GetMessage(bg, h.ID)
				return m.FolderIDs
			}
		}
		return nil
	}
	if got, want := membership("One"), []string{"acc:All", "acc:INBOX", "acc:Work"}; !reflect.DeepEqual(got, want) {
		t.Errorf("One: %v", got)
	}
	if got, want := membership("Two"), []string{"acc:All", "acc:Work"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Two: %v", got)
	}
	if n := len(e.msgs("acc:Work")); n != 2 {
		t.Errorf("Work view has %d messages", n)
	}
	// Each email is still stored exactly once.
	var total int
	for _, f := range []string{"acc:All"} {
		total += len(e.msgs(f))
	}
	if total != 2 {
		t.Errorf("messages stored: %d", total)
	}
	if fi, _ := e.st.GetFolder(bg, "acc:INBOX"); fi.UIDValidity == 0 {
		t.Error("label folder state not recorded")
	}

	// Unread counts follow memberships: both messages are unread.
	if fi, _ := e.st.GetFolder(bg, "acc:Work"); fi.UnreadCount != 2 {
		t.Errorf("Work unread %d", fi.UnreadCount)
	}

	// Removing a label on the server is picked up (no CONDSTORE here: always re-read).
	c2 := e.client()
	c2.Select("Work", nil).Wait()
	c2.Store(imap.UIDSetNum(1), &imap.StoreFlags{Op: imap.StoreFlagsAdd, Silent: true, Flags: []imap.Flag{imap.FlagDeleted}}, nil).Close()
	c2.Expunge().Close()
	if err := w.syncLabels(bg, c, folders, false); err != nil {
		t.Fatal(err)
	}
	if got, want := membership("One"), []string{"acc:All", "acc:INBOX"}; !reflect.DeepEqual(got, want) {
		t.Errorf("One after label removal: %v", got)
	}
}
