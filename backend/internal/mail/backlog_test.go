package mail

import (
	"fmt"
	"testing"
)

// A large initial sync is split into budget-sized passes that run back to back,
// report progress, and leave last_synced_uid alone until the
// backlog is gone.
func TestBacklogSyncInChunks(t *testing.T) {
	e := newEnv(t)
	for i := 1; i <= 10; i++ {
		e.appendMsg("INBOX", rawMsg(fmt.Sprint("s", i), fmt.Sprintf("%d@x", i), "b"))
	}
	e.tweak = func(m *Manager) { m.passBudget = 3 }
	e.start(nil)
	e.waitCount(inboxID, 10)
	waitFor(t, "final pass without backlog", func() bool {
		fin := e.eventsOf(EventSyncFinished)
		return len(fin) > 0 && fin[len(fin)-1].Detail == ""
	})

	backlog := 0
	for _, ev := range e.eventsOf(EventSyncFinished) {
		if ev.Detail == "backlog" {
			backlog++
		}
	}
	if backlog != 3 { // 3+3+3 with backlog, then 1
		t.Errorf("backlog passes = %d, want 3", backlog)
	}
	// After each chunk the remaining count is reported; the final pass clears
	// it with a plain SyncFinished instead of a 0.
	var got []string
	for _, ev := range e.eventsOf(EventSyncProgress) {
		got = append(got, ev.Detail)
	}
	if fmt.Sprint(got) != "[7 4 1]" {
		t.Errorf("progress = %v, want [7 4 1]", got)
	}
	f, err := e.st.GetFolder(bg, inboxID)
	if err != nil || f.LastSyncedUID != 10 || f.UnreadCount != 10 {
		t.Errorf("folder after backlog: %+v %v", f, err)
	}
}
