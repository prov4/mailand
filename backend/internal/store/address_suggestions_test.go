package store

import (
	"testing"
)

func TestSearchMessageAddresses(t *testing.T) {
	s, f := setup(t, "INBOX")

	add := func(uid uint32, fromName, fromAddr, toJSON, ccJSON string, ts int64) {
		m := msg(f["INBOX"], uid, "Subj", "body", ts)
		m.FromName = fromName
		m.FromAddress = fromAddr
		m.ToJSON = toJSON
		m.CcJSON = ccJSON
		_, err := s.UpsertMessage(ctx, m, nil)
		must(t, err)
	}

	add(1, "Marko Horvat", "marko@example.com", `[{"Name":"Robert Kovač","Address":"robert@example.com"}]`, `[]`, 100)
	add(2, "Marko Horvat", "marko@example.com", `[]`, `[{"Name":"Ana","Address":"ana.markovic@example.com"}]`, 200)
	add(3, "", "marko@example.com", `[]`, `[]`, 300) // newer message with empty display name should keep previous name

	got, err := s.SearchMessageAddresses(ctx, "marko", 10, nil)
	must(t, err)
	if len(got) != 2 {
		t.Fatalf("got %d results, want 2: %+v", len(got), got)
	}
	// Prefix match "Marko Horvat" should come before substring match "ana.markovic@example.com".
	if got[0].Address != "marko@example.com" || got[0].Name != "Marko Horvat" {
		t.Errorf("first = %+v", got[0])
	}
	if got[1].Address != "ana.markovic@example.com" {
		t.Errorf("second = %+v", got[1])
	}

	// Recipients in to_json are also found.
	got, err = s.SearchMessageAddresses(ctx, "robert", 10, nil)
	must(t, err)
	if len(got) != 1 || got[0].Address != "robert@example.com" || got[0].Name != "Robert Kovač" {
		t.Fatalf("robert = %+v", got)
	}

	// Exclude list filters out addresses already in CardDAV contacts.
	got, err = s.SearchMessageAddresses(ctx, "marko", 10, map[string]bool{"marko@example.com": true})
	must(t, err)
	if len(got) != 1 || got[0].Address != "ana.markovic@example.com" {
		t.Fatalf("excluded = %+v", got)
	}
}
