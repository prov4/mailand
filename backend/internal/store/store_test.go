package store

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

var ctx = context.Background()

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "sub", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func setup(t *testing.T, folders ...string) (*Store, map[string]string) {
	t.Helper()
	s := newStore(t)
	must(t, s.UpsertAccount(ctx, Account{ID: "a1", Name: "A", Email: "a@x", IMAPHost: "h", IMAPPort: 993, SMTPHost: "s", SMTPPort: 465}))
	ids := map[string]string{}
	for _, p := range folders {
		f, err := s.UpsertFolder(ctx, Folder{AccountID: "a1", Name: p, Path: p, IsSyncSource: true})
		must(t, err)
		ids[p] = f.ID
	}
	return s, ids
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func msg(folder string, uid uint32, subject, body string, ts int64) Message {
	return Message{MessageHeader: MessageHeader{AccountID: "a1", FolderID: folder, UID: uid, Subject: subject,
		FromName: "Bob", FromAddress: "bob@example.com", TimestampUnix: ts}, TextBody: body}
}

func ids(hs []MessageHeader) []string {
	var o []string
	for _, h := range hs {
		o = append(o, h.Subject)
	}
	return o
}

func TestOpenIdempotentAndPragmas(t *testing.T) {
	p := filepath.Join(t.TempDir(), "m.db")
	s, err := Open(p)
	must(t, err)
	s.Close()
	s, err = Open(p) // schema applied twice
	must(t, err)
	defer s.Close()
	var v, fk int
	must(t, s.db.QueryRow("PRAGMA user_version").Scan(&v))
	if v != 1 {
		t.Fatalf("user_version=%d", v)
	}
	// every pooled connection must have FKs on
	c1, _ := s.db.Conn(ctx)
	c2, _ := s.db.Conn(ctx)
	must(t, c1.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fk))
	if fk != 1 {
		t.Fatal("fk off on conn1")
	}
	must(t, c2.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fk))
	if fk != 1 {
		t.Fatal("fk off on conn2")
	}
	c1.Close()
	c2.Close()
	var jm string
	must(t, s.db.QueryRow("PRAGMA journal_mode").Scan(&jm))
	if jm != "wal" {
		t.Fatal(jm)
	}
}

func TestAccountsFolders(t *testing.T) {
	s, f := setup(t, "INBOX")
	got, err := s.GetAccount(ctx, "a1")
	must(t, err)
	if got.AuthType != "password" {
		t.Fatal(got.AuthType)
	}
	if _, err := s.GetAccount(ctx, "nope"); err != ErrNotFound {
		t.Fatal(err)
	}
	// Upsert keeps ID and sync state.
	must(t, s.UpdateFolderSyncState(ctx, f["INBOX"], 7, 42, 99))
	again, err := s.UpsertFolder(ctx, Folder{AccountID: "a1", Name: "Inbox", Path: "INBOX", SpecialUse: `\Inbox`})
	must(t, err)
	if again.ID != f["INBOX"] || again.Name != "Inbox" || again.SpecialUse != `\Inbox` {
		t.Fatalf("%+v", again)
	}
	if again.UIDValidity != 7 || again.LastSyncedUID != 42 || again.HighestModSeq != 99 {
		t.Fatalf("sync state lost: %+v", again)
	}
	fl, _ := s.ListFolders(ctx, "a1")
	if len(fl) != 1 {
		t.Fatal(len(fl))
	}
	must(t, s.DeleteFolder(ctx, f["INBOX"]))
	if _, err := s.GetFolder(ctx, f["INBOX"]); err != ErrNotFound {
		t.Fatal(err)
	}
}

func TestGmailUpsertNoDuplicateAndMove(t *testing.T) {
	s, f := setup(t, "All", "Trash", "Inbox", "Work", "Misc")
	gm := int64(1234)
	m := msg(f["All"], 10, "Hello", "body", 100)
	m.GmMsgID = &gm
	seq1, err := s.UpsertMessage(ctx, m, []string{f["Inbox"], f["Work"], f["Misc"]})
	must(t, err)
	// moved to Trash: new source folder + uid, same gm_msgid
	m.FolderID, m.UID = f["Trash"], 5
	seq2, err := s.UpsertMessage(ctx, m, []string{f["Trash"]})
	must(t, err)
	if seq1 != seq2 {
		t.Fatalf("duplicate: %d vs %d", seq1, seq2)
	}
	var n int
	must(t, s.db.QueryRow("SELECT COUNT(*) FROM messages").Scan(&n))
	if n != 1 {
		t.Fatal(n)
	}
	var hs []MessageHeader
	hs, total, _ := s.ListMessages(ctx, f["Inbox"], 10, 0)
	if total != 0 || len(hs) != 0 {
		t.Fatal("label rows not replaced")
	}
	hs, total, _ = s.ListMessages(ctx, f["Trash"], 10, 0)
	if total != 1 || hs[0].FolderID != f["Trash"] || hs[0].UID != 5 {
		t.Fatalf("%+v", hs)
	}
	if u, _ := s.MaxUID(ctx, f["All"]); u != 0 {
		t.Fatal(u)
	}
}

func TestLabelsAndSearchOnce(t *testing.T) {
	s, f := setup(t, "All", "Inbox", "Work", "Misc")
	gm := int64(1)
	m := msg(f["All"], 1, "Faktura", "x", 100)
	m.GmMsgID = &gm
	seq, err := s.UpsertMessage(ctx, m, []string{f["Inbox"], f["Work"], f["Misc"]})
	must(t, err)
	for _, k := range []string{"Inbox", "Work", "Misc"} {
		hs, total, err := s.ListMessages(ctx, f[k], 10, 0)
		must(t, err)
		if total != 1 || len(hs) != 1 {
			t.Fatalf("%s: %d", k, total)
		}
	}
	hits, err := s.Search(ctx, "faktura", "", 10)
	must(t, err)
	if len(hits) != 1 {
		t.Fatalf("hits=%d", len(hits))
	}
	must(t, s.SetMessageFolders(ctx, seq, []string{f["Work"]}))
	if _, total, _ := s.ListMessages(ctx, f["Inbox"], 10, 0); total != 0 {
		t.Fatal("inbox still has it")
	}
	got, err := s.GetMessage(ctx, hits[0].ID)
	must(t, err)
	if len(got.FolderIDs) != 1 || got.FolderIDs[0] != f["Work"] || *got.GmMsgID != 1 {
		t.Fatalf("%+v", got)
	}
}

func TestPaginationAndOrder(t *testing.T) {
	s, f := setup(t, "INBOX")
	for i := 1; i <= 25; i++ {
		_, err := s.UpsertMessage(ctx, msg(f["INBOX"], uint32(i), fmt.Sprintf("s%02d", i), "b", int64(i/2)), nil) // ties
		must(t, err)
	}
	seen := map[string]bool{}
	var prevTS int64 = 1 << 40
	for off := 0; off < 25; off += 10 {
		hs, total, err := s.ListMessages(ctx, f["INBOX"], 10, off)
		must(t, err)
		if total != 25 {
			t.Fatal(total)
		}
		for _, h := range hs {
			if seen[h.ID] || h.TimestampUnix > prevTS {
				t.Fatalf("dup or order: %+v", h)
			}
			seen[h.ID], prevTS = true, h.TimestampUnix
		}
	}
	if len(seen) != 25 {
		t.Fatal(len(seen))
	}
	hs, _, _ := s.ListMessages(ctx, f["INBOX"], 10, 0)
	if hs[0].TimestampUnix != 12 {
		t.Fatal(ids(hs))
	}
}

func TestExplainListMessages(t *testing.T) {
	s, f := setup(t, "INBOX")
	rows, err := s.db.Query(`EXPLAIN QUERY PLAN SELECT m.id FROM message_folders mf JOIN messages m ON m.seq = mf.message_seq
WHERE mf.folder_id = ? ORDER BY mf.timestamp_unix DESC, mf.rowid ASC LIMIT 10 OFFSET 0`, f["INBOX"])
	must(t, err)
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var a, b, c int
		var d string
		must(t, rows.Scan(&a, &b, &c, &d))
		plan = append(plan, d)
		t.Log(d)
	}
	j := strings.Join(plan, "|")
	if !strings.Contains(j, "idx_message_folders_list") || strings.Contains(j, "TEMP B-TREE") {
		t.Fatalf("plan: %s", j)
	}
}

func TestFTSTriggersAndRebuild(t *testing.T) {
	s, f := setup(t, "INBOX")
	m := msg(f["INBOX"], 1, "Subj", "alpha beta", 1)
	_, err := s.UpsertMessage(ctx, m, nil)
	must(t, err)
	count := func(q string) int {
		h, err := s.Search(ctx, q, "", 10)
		must(t, err)
		return len(h)
	}
	if count("alpha") != 1 {
		t.Fatal("insert")
	}
	m.TextBody = "gamma delta"
	_, err = s.UpsertMessage(ctx, m, nil) // update path
	must(t, err)
	if count("alpha") != 0 || count("gamma") != 1 {
		t.Fatal("update")
	}
	must(t, s.RebuildSearchIndex(ctx))
	if count("gamma") != 1 {
		t.Fatal("rebuild")
	}
	must(t, s.DeleteByUIDs(ctx, f["INBOX"], []uint32{1}))
	if count("gamma") != 0 {
		t.Fatal("delete")
	}
}

func TestSearchFeatures(t *testing.T) {
	s, f := setup(t, "INBOX")
	add := func(uid uint32, subj, body string, ts int64) {
		_, err := s.UpsertMessage(ctx, msg(f["INBOX"], uid, subj, body, ts), nil)
		must(t, err)
	}
	add(1, "Kafe", "idemo na café sutra", 1)
	add(2, "Žaba", "nešto", 2)
	add(3, "Nova faktura", "iznos", 3)
	add(4, "Other", "please see the invoice attached", 4)
	add(5, "Invoice March", "details", 3)
	first := func(q string) string {
		h, err := s.Search(ctx, q, "", 10)
		must(t, err)
		if len(h) == 0 {
			return ""
		}
		return h[0].Subject
	}
	if first("cafe") != "Kafe" {
		t.Fatal("accent cafe")
	}
	if first("zaba") != "Žaba" {
		t.Fatal("accent zaba")
	}
	if first("fak") != "Nova faktura" {
		t.Fatal("prefix")
	}
	if first("invoice") != "Invoice March" {
		t.Fatal("rank: subject should beat body-only (which is newer)")
	}
	if first("invoice marc") != "Invoice March" {
		t.Fatal("multi-term prefix")
	}
	h, _ := s.Search(ctx, "cafe", "", 10)
	if !strings.Contains(h[0].MatchSnippet, HighlightStart+"café"+HighlightEnd) {
		t.Fatalf("snippet %q", h[0].MatchSnippet)
	}
	if h, _ := s.Search(ctx, "faktura", "other", 10); len(h) != 0 {
		t.Fatal("account filter")
	}
	if h, _ := s.Search(ctx, "   ", "", 10); len(h) != 0 {
		t.Fatal("empty")
	}
	for _, q := range []string{`"`, `-`, `:`, `*`, `AND`, `(`, `foo"bar`, `a:b -c`, `NEAR(`, `OR OR`, `日本語`, `ж*`, `"unterminated`, `x ^y`, "\x00", `)))`, `title:foo`, `'`, `;DROP TABLE messages`} {
		if _, err := s.Search(ctx, q, "", 10); err != nil {
			t.Errorf("query %q errored: %v", q, err)
		}
	}
}

func TestUnreadFlagsAndUIDs(t *testing.T) {
	s, f := setup(t, "All", "Inbox")
	gm := int64(9)
	m := msg(f["All"], 3, "u", "b", 1)
	m.GmMsgID = &gm
	_, err := s.UpsertMessage(ctx, m, []string{f["Inbox"]})
	must(t, err)
	unread := func() int { fl, _ := s.GetFolder(ctx, f["Inbox"]); return fl.UnreadCount }
	if unread() != 1 {
		t.Fatal(unread())
	}
	must(t, s.UpdateFlags(ctx, f["All"], 3, true, true))
	if unread() != 0 {
		t.Fatal("after read")
	}
	hs, _, _ := s.ListMessages(ctx, f["Inbox"], 10, 0)
	if !hs[0].IsRead || !hs[0].IsFlagged {
		t.Fatal("flags")
	}
	f2 := false
	must(t, s.SetFlags(ctx, []string{hs[0].ID, "missing"}, &f2, nil))
	if unread() != 1 {
		t.Fatal("after SetFlags")
	}
	got, _ := s.GetMessage(ctx, hs[0].ID)
	if !got.IsFlagged {
		t.Fatal("nil flag must stay unchanged")
	}
	must(t, s.DeleteByUIDs(ctx, f["All"], []uint32{3}))
	if unread() != 0 {
		t.Fatal("after delete")
	}
	_, err = s.UpsertMessage(ctx, msg(f["All"], 8, "x", "", 1), nil)
	must(t, err)
	_, err = s.UpsertMessage(ctx, msg(f["All"], 4, "y", "", 1), nil)
	must(t, err)
	if u, _ := s.MaxUID(ctx, f["All"]); u != 8 {
		t.Fatal(u)
	}
	if us, _ := s.UIDs(ctx, f["All"]); len(us) != 2 || us[0] != 4 {
		t.Fatal(us)
	}
}

func TestCascadeOnAccountDelete(t *testing.T) {
	s, f := setup(t, "INBOX")
	_, err := s.UpsertMessage(ctx, msg(f["INBOX"], 1, "zzz", "zzz", 1), nil)
	must(t, err)
	must(t, s.DeleteAccount(ctx, "a1"))
	for _, tbl := range []string{"folders", "messages", "message_folders"} {
		var n int
		must(t, s.db.QueryRow("SELECT COUNT(*) FROM "+tbl).Scan(&n))
		if n != 0 {
			t.Fatalf("%s has %d rows", tbl, n)
		}
	}
	if h, _ := s.Search(ctx, "zzz", "", 10); len(h) != 0 {
		t.Fatal("fts not cleaned by cascade")
	}
}

func TestSyncStateRoundTrip(t *testing.T) {
	s, f := setup(t, "INBOX")
	must(t, s.UpdateFolderSyncState(ctx, f["INBOX"], 1, 2, 3))
	g, _ := s.GetFolder(ctx, f["INBOX"])
	if g.UIDValidity != 1 || g.LastSyncedUID != 2 || g.HighestModSeq != 3 {
		t.Fatalf("%+v", g)
	}
	if err := s.UpdateFolderSyncState(ctx, "nope", 1, 1, 1); err != ErrNotFound {
		t.Fatal(err)
	}
}
