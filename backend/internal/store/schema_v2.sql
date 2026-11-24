-- Milestone 5: calendars (CalDAV) and contacts (CardDAV).
-- Objects are stored raw (iCalendar / vCard) plus a few indexed columns; the
-- daemon parses and expands recurring events at query time.

CREATE TABLE calendars (
    id TEXT PRIMARY KEY,                -- "<account_id>:<collection path>"
    account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    path TEXT NOT NULL,                 -- CalDAV collection path on the server
    name TEXT NOT NULL,
    color TEXT NOT NULL DEFAULT '',     -- "#rrggbb" or "" (UI picks one)
    read_only INTEGER NOT NULL DEFAULT 0,
    UNIQUE(account_id, path)
);

CREATE TABLE calendar_objects (
    seq INTEGER PRIMARY KEY,
    calendar_id TEXT NOT NULL REFERENCES calendars(id) ON DELETE CASCADE,
    path TEXT NOT NULL,                 -- resource path (.ics) on the server
    etag TEXT NOT NULL DEFAULT '',
    uid TEXT NOT NULL DEFAULT '',
    summary TEXT NOT NULL DEFAULT '',
    first_start INTEGER NOT NULL,       -- earliest instance start (unix)
    last_end INTEGER,                   -- latest instance end (unix); NULL = recurs forever
    data TEXT NOT NULL,                 -- the iCalendar object
    UNIQUE(calendar_id, path)
);
CREATE INDEX idx_calendar_objects_range ON calendar_objects(calendar_id, first_start);

CREATE TABLE address_books (
    id TEXT PRIMARY KEY,                -- "<account_id>:<collection path>"
    account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    path TEXT NOT NULL,
    name TEXT NOT NULL,
    read_only INTEGER NOT NULL DEFAULT 0,
    UNIQUE(account_id, path)
);

CREATE TABLE contacts (
    seq INTEGER PRIMARY KEY,
    address_book_id TEXT NOT NULL REFERENCES address_books(id) ON DELETE CASCADE,
    path TEXT NOT NULL,                 -- resource path (.vcf) on the server
    etag TEXT NOT NULL DEFAULT '',
    uid TEXT NOT NULL DEFAULT '',
    full_name TEXT NOT NULL DEFAULT '',
    emails TEXT NOT NULL DEFAULT '',    -- newline-separated, as written in the card
    phones TEXT NOT NULL DEFAULT '',    -- newline-separated
    organization TEXT NOT NULL DEFAULT '',
    data TEXT NOT NULL,                 -- the vCard
    UNIQUE(address_book_id, path)
);
CREATE INDEX idx_contacts_name ON contacts(full_name COLLATE NOCASE);
