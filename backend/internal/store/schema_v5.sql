-- Replied marker (\Answered flag) and rich/image signatures.
ALTER TABLE messages ADD COLUMN is_answered INTEGER NOT NULL DEFAULT 0;
ALTER TABLE accounts ADD COLUMN signature_html TEXT NOT NULL DEFAULT '';
ALTER TABLE accounts ADD COLUMN signature_image TEXT NOT NULL DEFAULT '';
