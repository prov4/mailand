package store

import "context"

// FlagState is a message's flags as currently stored.
type FlagState struct{ IsRead, IsFlagged, IsAnswered bool }

// FlagStates returns the stored flags of every message whose source mailbox is
// folderID, keyed by UID. The IMAP sync uses it to apply only real changes.
func (s *Store) FlagStates(ctx context.Context, folderID string) (map[uint32]FlagState, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT uid, is_read, is_flagged, is_answered FROM messages WHERE folder_id=?`, folderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[uint32]FlagState{}
	for rows.Next() {
		var uid uint32
		var rd, fl, ans int
		if err := rows.Scan(&uid, &rd, &fl, &ans); err != nil {
			return nil, err
		}
		out[uid] = FlagState{rd != 0, fl != 0, ans != 0}
	}
	return out, rows.Err()
}

// MessageRef identifies a stored message by its Message-ID header.
type MessageRef struct {
	Seq             int64
	FolderID        string // source mailbox
	MessageIDHeader string // as stored (parser output, without angle brackets)
}

// MessageRefs lists every message of an account that has a Message-ID header.
func (s *Store) MessageRefs(ctx context.Context, accountID string) ([]MessageRef, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT seq, folder_id, message_id_header FROM messages
WHERE account_id=? AND message_id_header IS NOT NULL AND message_id_header <> ''`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MessageRef
	for rows.Next() {
		var r MessageRef
		if err := rows.Scan(&r.Seq, &r.FolderID, &r.MessageIDHeader); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Memberships returns message_folders as seq -> folder IDs for one account.
func (s *Store) Memberships(ctx context.Context, accountID string) (map[int64][]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT mf.message_seq, mf.folder_id FROM message_folders mf
JOIN messages m ON m.seq = mf.message_seq WHERE m.account_id=?`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64][]string{}
	for rows.Next() {
		var seq int64
		var f string
		if err := rows.Scan(&seq, &f); err != nil {
			return nil, err
		}
		out[seq] = append(out[seq], f)
	}
	return out, rows.Err()
}
