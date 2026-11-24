// Package store is the SQLite persistence layer (schema in schema.sql).
//
// Conventions for callers:
//   - All IDs (account, folder, message) are opaque strings. Account and folder
//     IDs are chosen by the caller (UpsertFolder generates one if empty);
//     message IDs are generated here when Message.ID is empty and are stable
//     across upserts.
//   - Every method that touches more than one statement runs in one transaction.
//   - folders.unread_count is a stored column that is recomputed from
//     messages/message_folders inside the same transaction as any change that
//     can affect it, so it is always exact.
package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite" // registers driver "sqlite" (pure Go, FTS5 included)
)

//go:embed schema.sql
var schemaV1 string

// migrations[i] upgrades the database from user_version i to i+1.
// Append new entries for later milestones; never edit old ones.
var migrations = []string{schemaV1}

// ErrNotFound is returned by Get* methods when the row does not exist.
var ErrNotFound = errors.New("store: not found")

// Store wraps the SQLite database. It is safe for concurrent use.
type Store struct {
	db *sql.DB
}

// Open opens (creating if needed) the database at path and applies migrations.
//
// Pragmas are passed as DSN _pragma parameters, which modernc runs on every
// new pooled connection, so foreign_keys/busy_timeout hold for all of them.
// WAL lets readers run concurrently with one writer; busy_timeout makes a
// second writer wait instead of failing with SQLITE_BUSY.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("store: create dir: %w", err)
	}
	q := url.Values{}
	for _, p := range []string{
		"journal_mode(WAL)", "foreign_keys(ON)", "busy_timeout(5000)", "synchronous(NORMAL)",
	} {
		q.Add("_pragma", p)
	}
	db, err := sql.Open("sqlite", "file:"+path+"?"+q.Encode())
	if err != nil {
		return nil, err
	}
	s := &Store{db: db}
	if err := s.migrate(context.Background()); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: migrate: %w", err)
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate(ctx context.Context) error {
	var v int
	if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v); err != nil {
		return err
	}
	for ; v < len(migrations); v++ {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, migrations[v]); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: %w", v+1, err)
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", v+1)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

type execer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (s *Store) inTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// placeholders returns "?,?,?" for n items.
func placeholders(n int) string {
	s := make([]byte, 0, n*2)
	for i := 0; i < n; i++ {
		if i > 0 {
			s = append(s, ',')
		}
		s = append(s, '?')
	}
	return string(s)
}
