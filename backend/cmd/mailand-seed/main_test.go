package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mailand/backend/internal/config"
	"mailand/backend/internal/mail"
	"mailand/backend/internal/store"
)

func TestSeed(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	ctx := context.Background()
	var out bytes.Buffer
	// Noon, so "today" and "yesterday" mail does not depend on when the test runs.
	t0 := time.Now()
	now := time.Date(t0.Year(), t0.Month(), t0.Day(), 12, 0, 0, 0, t0.Location())
	if err := run(ctx, false, now, &out); err != nil {
		t.Fatal(err)
	}

	st, err := store.Open(config.DBPath())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	accs, _ := st.ListAccounts(ctx)
	if len(accs) != 2 {
		t.Fatalf("accounts = %d", len(accs))
	}
	cals, _ := st.ListCalendars(ctx, "")
	if len(cals) != 3 {
		t.Fatalf("calendars = %d", len(cals))
	}
	weekStart := now.AddDate(0, 0, -7).Unix()
	objs, err := st.CalendarObjectsInRange(ctx, nil, weekStart, now.AddDate(0, 0, 14).Unix())
	if err != nil || len(objs) < 8 {
		t.Fatalf("events around now: %d %v", len(objs), err)
	}
	if cs, _ := st.SearchContacts(ctx, "", false, 0); len(cs) != 17 {
		t.Fatalf("contacts = %d", len(cs))
	}
	var total, unread, today, yesterday, big, inline int
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	for _, a := range accs {
		fs, _ := st.ListFolders(ctx, a.ID)
		for _, f := range fs {
			if !f.IsSyncSource {
				continue
			}
			hs, _, err := st.ListMessages(ctx, f.ID, 500, 0)
			if err != nil {
				t.Fatal(err)
			}
			for _, h := range hs {
				total++
				if !h.IsRead {
					unread++
				}
				ts := time.Unix(h.TimestampUnix, 0)
				if ts.After(midnight) {
					today++
				} else if ts.After(midnight.AddDate(0, 0, -1)) {
					yesterday++
				}
				m, err := st.GetMessage(ctx, h.ID)
				if err != nil {
					t.Fatal(err)
				}
				if len(m.HTMLBody) > 3<<20 {
					big++
				}
				if strings.Contains(m.HTMLBody, `src="cid:logo`) {
					inline++
				}
				// Stored fields must agree with the blob.
				fi, err := os.Stat(m.RawBlobPath)
				if err != nil || fi.Mode().Perm() != 0o600 {
					t.Fatalf("blob %s: %v %v", m.RawBlobPath, fi, err)
				}
				if total%8 != 0 && len(m.HTMLBody) < 1<<20 {
					continue // full re-parse is slow under -race; sample
				}
				raw, _ := os.ReadFile(m.RawBlobPath)
				p, _ := mail.Parse(bytes.NewReader(raw))
				if p.Subject != m.Subject || p.HTMLBody != m.HTMLBody || p.TextBody != m.TextBody {
					t.Fatalf("message %s differs from its blob", m.ID)
				}
			}
		}
	}
	if total < 380 || total > 450 {
		t.Errorf("messages = %d", total)
	}
	if r := float64(unread) / float64(total); r < 0.2 || r > 0.4 {
		t.Errorf("unread ratio %.2f", r)
	}
	if today < 3 || yesterday < 3 {
		t.Errorf("today=%d yesterday=%d", today, yesterday)
	}
	if big != 2 || inline < 2 {
		t.Errorf("big=%d inline=%d", big, inline)
	}
	if fi, err := os.Stat(filepath.Dir(config.DBPath())); err != nil || fi.Mode().Perm() != 0o700 {
		t.Errorf("data dir perms: %v %v", fi, err)
	}
	for _, want := range []string{"gmail", "work", "[Gmail]/All Mail", config.DBPath()} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("summary lacks %q", want)
		}
	}

	// Refuses without -force; -force recreates.
	if err := run(ctx, false, now, &out); err == nil || !strings.Contains(err.Error(), "-force") {
		t.Errorf("second run err = %v", err)
	}
	if err := run(ctx, true, now, &out); err != nil {
		t.Fatalf("force: %v", err)
	}
	st2, _ := store.Open(config.DBPath())
	defer st2.Close()
	if a, _ := st2.ListAccounts(ctx); len(a) != 2 {
		t.Errorf("after force accounts = %d", len(a))
	}
}
