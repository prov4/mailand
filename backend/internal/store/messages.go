package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// MessageHeader is the list/search row: everything but bodies.
type MessageHeader struct {
	ID             string
	Seq            int64
	AccountID      string
	FolderID       string // source mailbox the UID belongs to (not the folder being listed)
	UID            uint32
	GmThrID        *int64
	Subject        string
	FromName       string
	FromAddress    string
	TimestampUnix  int64
	IsRead         bool
	IsFlagged      bool
	IsAnswered     bool
	HasAttachments bool
	Snippet        string
}

// Message is a full row. For UpsertMessage, Seq and FolderIDs are ignored.
type Message struct {
	MessageHeader
	GmMsgID         *int64 // Gmail X-GM-MSGID; nil for non-Gmail
	MessageIDHeader string
	ToJSON, CcJSON  string // JSON arrays; default "[]"
	TextBody        string // must be non-empty whenever the message has any body (HTML fallback)
	HTMLBody        string
	RawBlobPath     string
	FolderIDs       []string // memberships; filled by GetMessage
}

const headerCols = `m.id, m.seq, m.account_id, m.folder_id, m.uid, m.gm_thrid, m.subject, m.from_name, m.from_address,
  m.timestamp_unix, m.is_read, m.is_flagged, m.is_answered, m.has_attachments, m.snippet`

func scanHeader(sc interface{ Scan(...any) error }, extra ...any) (MessageHeader, error) {
	var h MessageHeader
	var thr sql.NullInt64
	var rd, fl, ans, att int
	dst := append([]any{&h.ID, &h.Seq, &h.AccountID, &h.FolderID, &h.UID, &thr, &h.Subject, &h.FromName,
		&h.FromAddress, &h.TimestampUnix, &rd, &fl, &ans, &att, &h.Snippet}, extra...)
	if err := sc.Scan(dst...); err != nil {
		return h, err
	}
	if thr.Valid {
		h.GmThrID = &thr.Int64
	}
	h.IsRead, h.IsFlagged, h.IsAnswered, h.HasAttachments = rd != 0, fl != 0, ans != 0, att != 0
	return h, nil
}

func nullInt(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}

// UpsertMessage stores a message and replaces its folder memberships, atomically.
//
// Identity: if m.GmMsgID != nil the row is keyed by (account_id, gm_msgid) and
// its source folder_id/uid are updated (Gmail move to Trash/Spam); otherwise by
// (folder_id, uid). On update the existing id and seq are kept. All other
// columns, including flags and bodies, are overwritten with m's values.
// folderIDs are the folders/labels the message appears in; nil means just the
// source folder. Returns the message's seq.
func (s *Store) UpsertMessage(ctx context.Context, m Message, folderIDs []string) (int64, error) {
	if m.ID == "" {
		m.ID = newID()
	}
	if m.ToJSON == "" {
		m.ToJSON = "[]"
	}
	if m.CcJSON == "" {
		m.CcJSON = "[]"
	}
	if folderIDs == nil {
		folderIDs = []string{m.FolderID}
	}
	conflict := "(folder_id, uid)"
	if m.GmMsgID != nil {
		conflict = "(account_id, gm_msgid)"
	}
	var seq int64
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		// Folders the message was in before, so their counts can be fixed.
		var old []string
		var prev int64
		prevRead := true // a new message had no unread contribution before
		var err error
		if m.GmMsgID != nil {
			err = tx.QueryRowContext(ctx, `SELECT seq, is_read FROM messages WHERE account_id=? AND gm_msgid=?`, m.AccountID, *m.GmMsgID).Scan(&prev, &prevRead)
		} else {
			err = tx.QueryRowContext(ctx, `SELECT seq, is_read FROM messages WHERE folder_id=? AND uid=?`, m.FolderID, m.UID).Scan(&prev, &prevRead)
		}
		if err == nil {
			if old, err = queryStrings(ctx, tx, `SELECT folder_id FROM message_folders WHERE message_seq=?`, prev); err != nil {
				return err
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}

		err = tx.QueryRowContext(ctx, `
INSERT INTO messages (id, account_id, folder_id, uid, gm_msgid, gm_thrid, message_id_header, subject,
  from_name, from_address, to_json, cc_json, timestamp_unix, is_read, is_flagged, is_answered, has_attachments,
  snippet, text_body, html_body, raw_blob_path)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT `+conflict+` DO UPDATE SET
  folder_id=excluded.folder_id, uid=excluded.uid, gm_msgid=excluded.gm_msgid, gm_thrid=excluded.gm_thrid,
  message_id_header=excluded.message_id_header, subject=excluded.subject, from_name=excluded.from_name,
  from_address=excluded.from_address, to_json=excluded.to_json, cc_json=excluded.cc_json,
  timestamp_unix=excluded.timestamp_unix, is_read=excluded.is_read, is_flagged=excluded.is_flagged,
  is_answered=excluded.is_answered, has_attachments=excluded.has_attachments, snippet=excluded.snippet,
  text_body=excluded.text_body, html_body=excluded.html_body, raw_blob_path=excluded.raw_blob_path
RETURNING seq`,
			m.ID, m.AccountID, m.FolderID, m.UID, nullInt(m.GmMsgID), nullInt(m.GmThrID), nullStr(m.MessageIDHeader),
			m.Subject, m.FromName, m.FromAddress, m.ToJSON, m.CcJSON, m.TimestampUnix, b2i(m.IsRead),
			b2i(m.IsFlagged), b2i(m.IsAnswered), b2i(m.HasAttachments), m.Snippet, m.TextBody, m.HTMLBody, nullStr(m.RawBlobPath)).Scan(&seq)
		if err != nil {
			return err
		}
		if err := replaceMemberships(ctx, tx, seq, m.TimestampUnix, folderIDs); err != nil {
			return err
		}
		if err := adjustUnread(ctx, tx, old, -unread(prevRead)); err != nil {
			return err
		}
		return adjustUnread(ctx, tx, folderIDs, unread(m.IsRead))
	})
	return seq, err
}

func replaceMemberships(ctx context.Context, tx *sql.Tx, seq, ts int64, folderIDs []string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM message_folders WHERE message_seq=?`, seq); err != nil {
		return err
	}
	for _, f := range folderIDs {
		if _, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO message_folders (message_seq, folder_id, timestamp_unix) VALUES (?,?,?)`, seq, f, ts); err != nil {
			return err
		}
	}
	return nil
}

// SetMessageFolders replaces a message's memberships (Gmail labels changed).
func (s *Store) SetMessageFolders(ctx context.Context, seq int64, folderIDs []string) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		var ts int64
		var isRead bool
		if err := tx.QueryRowContext(ctx, `SELECT timestamp_unix, is_read FROM messages WHERE seq=?`, seq).Scan(&ts, &isRead); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		old, err := queryStrings(ctx, tx, `SELECT folder_id FROM message_folders WHERE message_seq=?`, seq)
		if err != nil {
			return err
		}
		if err := replaceMemberships(ctx, tx, seq, ts, folderIDs); err != nil {
			return err
		}
		if err := adjustUnread(ctx, tx, old, -unread(isRead)); err != nil {
			return err
		}
		return adjustUnread(ctx, tx, folderIDs, unread(isRead))
	})
}

// UpdateFlags sets flags for the message with source (folderID, uid), e.g. from
// an IMAP FETCH FLAGS update. A missing message is not an error.
func (s *Store) UpdateFlags(ctx context.Context, folderID string, uid uint32, isRead, isFlagged, isAnswered bool) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		var seq int64
		var wasRead bool
		err := tx.QueryRowContext(ctx, `SELECT seq, is_read FROM messages WHERE folder_id=? AND uid=?`, folderID, uid).Scan(&seq, &wasRead)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE messages SET is_read=?, is_flagged=?, is_answered=? WHERE seq=?`,
			b2i(isRead), b2i(isFlagged), b2i(isAnswered), seq); err != nil {
			return err
		}
		return adjustForSeq(ctx, tx, seq, unread(isRead)-unread(wasRead))
	})
}

// SetAnswered marks the given message as replied (\Answered). Unknown ids are skipped.
func (s *Store) SetAnswered(ctx context.Context, messageID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE messages SET is_answered=1 WHERE id=?`, messageID)
	return err
}

// SetFlags updates flags by message id; nil leaves a flag unchanged. Unknown ids are skipped.
func (s *Store) SetFlags(ctx context.Context, messageIDs []string, isRead, isFlagged *bool) error {
	if isRead == nil && isFlagged == nil {
		return nil
	}
	return s.inTx(ctx, func(tx *sql.Tx) error {
		for _, id := range messageIDs {
			var seq int64
			var wasRead, nowRead bool
			err := tx.QueryRowContext(ctx, `SELECT seq, is_read FROM messages WHERE id=?`, id).Scan(&seq, &wasRead)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return err
			}
			if err := tx.QueryRowContext(ctx, `
UPDATE messages SET is_read = COALESCE(?, is_read), is_flagged = COALESCE(?, is_flagged)
WHERE seq=? RETURNING is_read`, optBool(isRead), optBool(isFlagged), seq).Scan(&nowRead); err != nil {
				return err
			}
			if err := adjustForSeq(ctx, tx, seq, unread(nowRead)-unread(wasRead)); err != nil {
				return err
			}
		}
		return nil
	})
}

func optBool(p *bool) any {
	if p == nil {
		return nil
	}
	return b2i(*p)
}

// adjustForSeq applies an unread-count delta to every folder the message is in.
func adjustForSeq(ctx context.Context, tx *sql.Tx, seq int64, delta int) error {
	if delta == 0 {
		return nil
	}
	_, err := tx.ExecContext(ctx, `
UPDATE folders SET unread_count = MAX(0, unread_count + ?)
WHERE id IN (SELECT folder_id FROM message_folders WHERE message_seq=?)`, delta, seq)
	return err
}

// DeleteByUIDs removes messages whose source is (folderID, uid in uids), i.e.
// expunged on the server. Memberships and FTS rows go with them.
func (s *Store) DeleteByUIDs(ctx context.Context, folderID string, uids []uint32) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		var fs []string
		for i := 0; i < len(uids); i += 500 {
			chunk := uids[i:min(i+500, len(uids))]
			args := []any{folderID}
			for _, u := range chunk {
				args = append(args, u)
			}
			in := " WHERE m.folder_id=? AND m.uid IN (" + placeholders(len(chunk)) + ")"
			got, err := queryStrings(ctx, tx, `SELECT DISTINCT mf.folder_id FROM message_folders mf JOIN messages m ON m.seq=mf.message_seq`+in, args...)
			if err != nil {
				return err
			}
			fs = append(fs, got...)
			if _, err := tx.ExecContext(ctx, `DELETE FROM messages WHERE folder_id=? AND uid IN (`+placeholders(len(chunk))+`)`, args...); err != nil {
				return err
			}
		}
		return recomputeUnread(ctx, tx, fs)
	})
}

// MaxUID returns the highest stored UID whose source is folderID, 0 if none.
func (s *Store) MaxUID(ctx context.Context, folderID string) (uint32, error) {
	var u uint32
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(uid),0) FROM messages WHERE folder_id=?`, folderID).Scan(&u)
	return u, err
}

// UIDs returns all stored UIDs with source folderID, ascending (expunge reconciliation).
func (s *Store) UIDs(ctx context.Context, folderID string) ([]uint32, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT uid FROM messages WHERE folder_id=? ORDER BY uid`, folderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []uint32
	for rows.Next() {
		var u uint32
		if err := rows.Scan(&u); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// GetMessage returns the full row by message id, with FolderIDs; ErrNotFound if missing.
func (s *Store) GetMessage(ctx context.Context, id string) (Message, error) {
	var m Message
	var gm sql.NullInt64
	var mid, raw sql.NullString
	h, err := scanHeader(s.db.QueryRowContext(ctx, `SELECT `+headerCols+`,
  m.gm_msgid, m.message_id_header, m.to_json, m.cc_json, m.text_body, m.html_body, m.raw_blob_path
FROM messages m WHERE m.id=?`, id),
		&gm, &mid, &m.ToJSON, &m.CcJSON, &m.TextBody, &m.HTMLBody, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return m, ErrNotFound
	}
	if err != nil {
		return m, err
	}
	m.MessageHeader = h
	if gm.Valid {
		m.GmMsgID = &gm.Int64
	}
	m.MessageIDHeader, m.RawBlobPath = mid.String, raw.String
	m.FolderIDs, err = queryStrings(ctx, s.db, `SELECT folder_id FROM message_folders WHERE message_seq=? ORDER BY folder_id`, h.Seq)
	return m, err
}

// ListMessages returns one page of a folder/label view, newest first, plus the
// total number of messages in it. It reads message_folders (idx_message_folders_list).
func (s *Store) ListMessages(ctx context.Context, folderID string, limit, offset int) ([]MessageHeader, int, error) {
	return s.ListMessagesFiltered(ctx, folderID, limit, offset, false)
}

// ListMessagesFiltered is ListMessages with an optional unread-only filter.
func (s *Store) ListMessagesFiltered(ctx context.Context, folderID string, limit, offset int, unreadOnly bool) ([]MessageHeader, int, error) {
	var total int
	var err error
	if unreadOnly {
		err = s.db.QueryRowContext(ctx, `SELECT COUNT(*)
FROM message_folders mf JOIN messages m ON m.seq = mf.message_seq
WHERE mf.folder_id = ? AND m.is_read = 0`, folderID).Scan(&total)
	} else {
		err = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM message_folders WHERE folder_id=?`, folderID).Scan(&total)
	}
	if err != nil {
		return nil, 0, err
	}
	q := `SELECT ` + headerCols + `
FROM message_folders mf JOIN messages m ON m.seq = mf.message_seq
WHERE mf.folder_id = ?`
	if unreadOnly {
		q += ` AND m.is_read = 0`
	}
	q += `
ORDER BY mf.timestamp_unix DESC, mf.rowid ASC
LIMIT ? OFFSET ?`
	rows, err := s.db.QueryContext(ctx, q, folderID, limit, offset)
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

// RemoveMessageFromFolder removes seq from folderID's membership and updates
// that folder's unread count atomically.
func (s *Store) RemoveMessageFromFolder(ctx context.Context, seq int64, folderID string) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM message_folders WHERE message_seq=? AND folder_id=?`, seq, folderID); err != nil {
			return err
		}
		return recomputeUnread(ctx, tx, []string{folderID})
	})
}

// MessagesByAddress returns the newest messages from, to or cc any of the
// addresses (case-insensitive), at most limit.
func (s *Store) MessagesByAddress(ctx context.Context, addresses []string, limit int) ([]MessageHeader, error) {
	var conds []string
	var args []any
	for _, a := range addresses {
		a = strings.ToLower(strings.TrimSpace(a))
		if a == "" {
			continue
		}
		like := `%"` + escapeLike(a) + `"%` // JSON string value: exact address
		conds = append(conds, `lower(m.from_address) = ?`, `lower(m.to_json) LIKE ? ESCAPE '\'`, `lower(m.cc_json) LIKE ? ESCAPE '\'`)
		args = append(args, a, like, like)
	}
	if len(conds) == 0 {
		return nil, nil
	}
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, `SELECT `+headerCols+` FROM messages m WHERE `+strings.Join(conds, " OR ")+
		` ORDER BY m.timestamp_unix DESC LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MessageHeader
	for rows.Next() {
		h, err := scanHeader(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}
