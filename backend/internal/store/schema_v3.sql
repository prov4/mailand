-- Desktop notification deduplication for upcoming calendar event reminders.
CREATE TABLE notified_events (
    calendar_id TEXT NOT NULL REFERENCES calendars(id) ON DELETE CASCADE,
    uid TEXT NOT NULL,
    start_unix INTEGER NOT NULL,
    notified_at INTEGER NOT NULL,
    PRIMARY KEY (calendar_id, uid, start_unix)
);
CREATE INDEX idx_notified_events_start ON notified_events(start_unix);
