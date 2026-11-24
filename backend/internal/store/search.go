package store

import (
	"context"
	"strings"
	"unicode"
)

// Highlight markers wrapped around matches in SearchHit.Snippet.
const (
	HighlightStart = "\x02"
	HighlightEnd   = "\x03"
)

// SearchHit is one matching email (once, regardless of how many labels it has).
type SearchHit struct {
	MessageHeader
	// Snippet of text_body with matches wrapped in HighlightStart/HighlightEnd
	// (control chars, so they cannot clash with mail content). If the match was
	// only in subject/sender, the header's stored snippet is returned instead.
	MatchSnippet string
}

// buildFTSQuery turns user input into a safe FTS5 expression: each
// whitespace-separated word becomes a quoted phrase, the last gets a prefix
// '*'. Words without any letter or digit are dropped. Returns "" if nothing is left.
func buildFTSQuery(q string) string {
	var terms []string
	for _, w := range strings.Fields(q) {
		w = strings.ReplaceAll(w, `"`, " ")
		w = strings.TrimSpace(w)
		if strings.IndexFunc(w, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsNumber(r) }) < 0 {
			continue
		}
		terms = append(terms, `"`+w+`"`)
	}
	if len(terms) == 0 {
		return ""
	}
	terms[len(terms)-1] += "*"
	return strings.Join(terms, " ")
}

// Search runs a plain-words full-text query. accountID "" searches all accounts.
// Results are ranked by bm25 (subject 10, from_name 4, from_address 4, body 1)
// then newest first. limit <= 0 means 50. Empty/symbol-only queries return nil.
func (s *Store) Search(ctx context.Context, userQuery, accountID string, limit int) ([]SearchHit, error) {
	match := buildFTSQuery(userQuery)
	if match == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 50
	}
	query := `SELECT ` + headerCols + `, snippet(messages_fts, 3, ?, ?, '…', 16)
FROM messages_fts JOIN messages m ON m.seq = messages_fts.rowid
WHERE messages_fts MATCH ?`
	args := []any{HighlightStart, HighlightEnd, match}
	if accountID != "" {
		query += ` AND m.account_id = ?`
		args = append(args, accountID)
	}
	query += ` ORDER BY bm25(messages_fts, 10.0, 4.0, 4.0, 1.0), m.timestamp_unix DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SearchHit
	for rows.Next() {
		var hit SearchHit
		h, err := scanHeader(rows, &hit.MatchSnippet)
		if err != nil {
			return nil, err
		}
		hit.MessageHeader = h
		if !strings.Contains(hit.MatchSnippet, HighlightStart) {
			hit.MatchSnippet = h.Snippet
		}
		out = append(out, hit)
	}
	return out, rows.Err()
}

// RebuildSearchIndex rebuilds messages_fts from messages (recovery).
func (s *Store) RebuildSearchIndex(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO messages_fts(messages_fts) VALUES('rebuild')`)
	return err
}
