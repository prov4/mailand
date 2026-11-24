CREATE TABLE IF NOT EXISTS accounts (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    email TEXT NOT NULL,
    imap_host TEXT NOT NULL,
    imap_port INTEGER NOT NULL,
    smtp_host TEXT NOT NULL,
    smtp_port INTEGER NOT NULL,
    auth_type TEXT NOT NULL DEFAULT 'password',  -- 'password' | 'oauth2_google'
    caldav_url TEXT,
    carddav_url TEXT
);

CREATE TABLE IF NOT EXISTS folders (
    id TEXT PRIMARY KEY,
    account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    path TEXT NOT NULL,                 -- IMAP mailbox path, or Gmail label name
    kind TEXT NOT NULL DEFAULT 'folder', -- 'folder' | 'gmail_label'
    special_use TEXT,                    -- '\Inbox', '\Sent', '\Drafts', '\Trash', '\Junk', '\All', ...
    is_sync_source INTEGER NOT NULL DEFAULT 1, -- 1 = UIDs are fetched from this mailbox (Gmail: All Mail, Spam, Trash only)
    uid_validity INTEGER DEFAULT 0,
    last_synced_uid INTEGER DEFAULT 0,
    highest_modseq INTEGER DEFAULT 0,   -- CONDSTORE: detect flag/label changes cheaply
    unread_count INTEGER DEFAULT 0,
    UNIQUE(account_id, path)
);

CREATE TABLE IF NOT EXISTS messages (
    seq INTEGER PRIMARY KEY,            -- stable rowid alias; FTS5 external content key (VACUUM-safe)
    id TEXT NOT NULL UNIQUE,
    account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    folder_id TEXT NOT NULL REFERENCES folders(id) ON DELETE CASCADE, -- source mailbox the UID belongs to
    uid INTEGER NOT NULL,
    gm_msgid INTEGER,                   -- Gmail X-GM-MSGID (stable across labels/moves); NULL for non-Gmail
    gm_thrid INTEGER,                   -- Gmail X-GM-THRID (conversation id); NULL for non-Gmail
    message_id_header TEXT,
    subject TEXT NOT NULL DEFAULT '',
    from_name TEXT NOT NULL DEFAULT '',
    from_address TEXT NOT NULL DEFAULT '',
    to_json TEXT NOT NULL DEFAULT '[]',
    cc_json TEXT NOT NULL DEFAULT '[]',
    timestamp_unix INTEGER NOT NULL,
    is_read INTEGER NOT NULL DEFAULT 0,
    is_flagged INTEGER NOT NULL DEFAULT 0,
    has_attachments INTEGER NOT NULL DEFAULT 0,
    snippet TEXT NOT NULL DEFAULT '',
    text_body TEXT NOT NULL DEFAULT '',
    html_body TEXT NOT NULL DEFAULT '',
    raw_blob_path TEXT,
    UNIQUE(folder_id, uid),
    UNIQUE(account_id, gm_msgid)
);

-- Which folders/labels a message appears in. Non-Gmail: exactly one row (its folder).
-- Gmail: one row per label (from X-GM-LABELS), so each email is stored once but shows in every label view.
CREATE TABLE IF NOT EXISTS message_folders (
    message_seq INTEGER NOT NULL REFERENCES messages(seq) ON DELETE CASCADE,
    folder_id TEXT NOT NULL REFERENCES folders(id) ON DELETE CASCADE,
    timestamp_unix INTEGER NOT NULL,    -- denormalized from messages for fast paginated listing
    PRIMARY KEY (message_seq, folder_id)
);

CREATE INDEX IF NOT EXISTS idx_message_folders_list ON message_folders(folder_id, timestamp_unix DESC);
CREATE INDEX IF NOT EXISTS idx_messages_thread ON messages(account_id, gm_thrid);

-- Full-Text Search: external-content FTS5 index over messages (bodies stored once).
-- unicode61 + remove_diacritics 2: word matching in any language, accent-insensitive ("cafe" finds "café").
-- prefix '2 3': fast search-as-you-type prefix queries ("fak*" finds "faktura").
CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts USING fts5(
    subject,
    from_name,
    from_address,
    text_body,
    content='messages',
    content_rowid='seq',
    tokenize='unicode61 remove_diacritics 2',
    prefix='2 3'
);

CREATE TRIGGER IF NOT EXISTS messages_fts_ai AFTER INSERT ON messages BEGIN
    INSERT INTO messages_fts(rowid, subject, from_name, from_address, text_body)
    VALUES (new.seq, new.subject, new.from_name, new.from_address, new.text_body);
END;

CREATE TRIGGER IF NOT EXISTS messages_fts_ad AFTER DELETE ON messages BEGIN
    INSERT INTO messages_fts(messages_fts, rowid, subject, from_name, from_address, text_body)
    VALUES ('delete', old.seq, old.subject, old.from_name, old.from_address, old.text_body);
END;

CREATE TRIGGER IF NOT EXISTS messages_fts_au
AFTER UPDATE OF subject, from_name, from_address, text_body ON messages BEGIN
    INSERT INTO messages_fts(messages_fts, rowid, subject, from_name, from_address, text_body)
    VALUES ('delete', old.seq, old.subject, old.from_name, old.from_address, old.text_body);
    INSERT INTO messages_fts(rowid, subject, from_name, from_address, text_body)
    VALUES (new.seq, new.subject, new.from_name, new.from_address, new.text_body);
END;
