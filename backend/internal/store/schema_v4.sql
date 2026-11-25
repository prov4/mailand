-- Per-account email signature.
ALTER TABLE accounts ADD COLUMN signature TEXT NOT NULL DEFAULT '';
