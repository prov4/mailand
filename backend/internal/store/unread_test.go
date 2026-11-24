package store

import (
	"fmt"
	"math/rand"
	"testing"
)

// Incremental unread counts must always equal a full recount, whatever mix of
// upserts (new and existing, Gmail-keyed and not), label changes, flag
// updates and deletes happens.
func TestUnreadCountsStayExact(t *testing.T) {
	s, f := setup(t, "All", "Inbox", "Work", "Spam")
	folders := []string{f["All"], f["Inbox"], f["Work"], f["Spam"]}
	rng := rand.New(rand.NewSource(1))
	var seqs []int64
	var ids []string
	membership := func() []string {
		out := []string{f["All"]}
		for _, id := range folders[1:] {
			if rng.Intn(3) == 0 {
				out = append(out, id)
			}
		}
		if rng.Intn(5) == 0 {
			out = append(out, f["All"]) // duplicate entries are tolerated
		}
		return out
	}
	check := func(step int, op string) {
		t.Helper()
		for _, id := range folders {
			fl, err := s.GetFolder(ctx, id)
			must(t, err)
			var want int
			must(t, s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM message_folders mf JOIN messages m ON m.seq=mf.message_seq
WHERE mf.folder_id=? AND m.is_read=0`, id).Scan(&want))
			if fl.UnreadCount != want {
				t.Fatalf("step %d (%s): folder %s unread=%d, want %d", step, op, fl.Path, fl.UnreadCount, want)
			}
		}
	}
	for step := 0; step < 1500; step++ {
		var op string
		switch r := rng.Intn(10); {
		case r < 4 || len(seqs) == 0:
			op = "upsert"
			uid := uint32(rng.Intn(80) + 1)
			m := Message{MessageHeader: MessageHeader{AccountID: "a1", FolderID: f["All"], UID: uid,
				TimestampUnix: int64(uid), IsRead: rng.Intn(2) == 0}, TextBody: "b"}
			if rng.Intn(2) == 0 {
				g := int64(uid)
				m.GmMsgID = &g
			}
			seq, err := s.UpsertMessage(ctx, m, membership())
			if err != nil {
				continue // e.g. gm_msgid/uid collision between keying modes; not what we test
			}
			seqs = append(seqs, seq)
			msg, err := s.db.QueryContext(ctx, `SELECT id FROM messages WHERE seq=?`, seq)
			must(t, err)
			for msg.Next() {
				var id string
				must(t, msg.Scan(&id))
				ids = append(ids, id)
			}
			msg.Close()
		case r < 6:
			op = "labels"
			err := s.SetMessageFolders(ctx, seqs[rng.Intn(len(seqs))], membership())
			if err != nil && err != ErrNotFound {
				t.Fatal(err)
			}
		case r < 8:
			op = "updateflags"
			must(t, s.UpdateFlags(ctx, f["All"], uint32(rng.Intn(80)+1), rng.Intn(2) == 0, rng.Intn(2) == 0))
		case r < 9:
			op = "setflags"
			rd := rng.Intn(2) == 0
			var pick []string
			for i := 0; i < 3 && len(ids) > 0; i++ {
				pick = append(pick, ids[rng.Intn(len(ids))])
			}
			must(t, s.SetFlags(ctx, append(pick, pick...), &rd, nil)) // duplicates on purpose
		default:
			op = "delete"
			must(t, s.DeleteByUIDs(ctx, f["All"], []uint32{uint32(rng.Intn(80) + 1)}))
		}
		check(step, fmt.Sprint(op))
	}
}
