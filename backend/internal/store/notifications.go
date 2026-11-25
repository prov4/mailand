package store

import (
	"context"
	"database/sql"
	"errors"
)

// MaxMessageSeqByAccount returns the highest message seq for every account that
// has at least one stored message.
func (s *Store) MaxMessageSeqByAccount(ctx context.Context) (map[string]int64, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT account_id, MAX(seq) FROM messages GROUP BY account_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var acct string
		var seq int64
		if err := rows.Scan(&acct, &seq); err != nil {
			return nil, err
		}
		out[acct] = seq
	}
	return out, rows.Err()
}

// AccountMaxMessageSeq returns the highest stored message seq for accountID (0 if none).
func (s *Store) AccountMaxMessageSeq(ctx context.Context, accountID string) (int64, error) {
	var seq int64
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq), 0) FROM messages WHERE account_id=?`, accountID).Scan(&seq)
	return seq, err
}

// NewInboxUnread returns unread messages in accountID's INBOX (matched by
// special_use='\Inbox' or case-insensitive path 'INBOX') with seq > afterSeq
// and timestamp_unix >= minTimestamp, ordered newest first. It also returns the
// total count of matching messages (useful when capped by limit).
func (s *Store) NewInboxUnread(ctx context.Context, accountID string, afterSeq, minTimestamp int64, limit int) ([]MessageHeader, int, error) {
	if limit <= 0 {
		limit = 20
	}
	const where = `
FROM messages m
JOIN message_folders mf ON mf.message_seq = m.seq
JOIN folders f ON f.id = mf.folder_id
WHERE m.account_id = ?
  AND m.seq > ?
  AND m.is_read = 0
  AND m.timestamp_unix >= ?
  AND (f.special_use = '\Inbox' OR UPPER(f.path) = 'INBOX')`

	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(DISTINCT m.seq) `+where, accountID, afterSeq, minTimestamp).Scan(&total); err != nil {
		return nil, 0, err
	}
	if total == 0 {
		return nil, 0, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT `+headerCols+` `+where+` ORDER BY m.timestamp_unix DESC, m.seq DESC LIMIT ?`,
		accountID, afterSeq, minTimestamp, limit)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []MessageHeader
	for rows.Next() {
		h, err := scanHeader(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, h)
	}
	return out, total, rows.Err()
}

// WasEventNotified reports whether a reminder was already sent for the given
// calendar event occurrence.
func (s *Store) WasEventNotified(ctx context.Context, calendarID, uid string, startUnix int64) (bool, error) {
	var one int
	err := s.db.QueryRowContext(ctx,
		`SELECT 1 FROM notified_events WHERE calendar_id=? AND uid=? AND start_unix=?`,
		calendarID, uid, startUnix).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// MarkEventNotified records that a reminder was sent for the given occurrence.
func (s *Store) MarkEventNotified(ctx context.Context, calendarID, uid string, startUnix, notifiedAt int64) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO notified_events (calendar_id, uid, start_unix, notified_at) VALUES (?,?,?,?)
ON CONFLICT(calendar_id, uid, start_unix) DO UPDATE SET notified_at=excluded.notified_at`,
		calendarID, uid, startUnix, notifiedAt)
	return err
}

// PruneNotifiedEvents removes reminder records whose occurrence started before olderThanUnix.
func (s *Store) PruneNotifiedEvents(ctx context.Context, olderThanUnix int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM notified_events WHERE start_unix < ?`, olderThanUnix)
	return err
}
