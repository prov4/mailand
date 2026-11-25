package store

import (
	"context"
	"database/sql"
	"errors"
)

// Account mirrors the accounts table. Secrets live in the keyring, not here.
type Account struct {
	ID, Name, Email       string
	IMAPHost              string
	IMAPPort              int
	SMTPHost              string
	SMTPPort              int
	AuthType              string // "password" | "oauth2_google"; defaults to "password"
	CalDAVURL, CardDAVURL string
	Signature             string
	SignatureHTML         string
	SignatureImage        string
}

const accountCols = `id, name, email, imap_host, imap_port, smtp_host, smtp_port, auth_type, COALESCE(caldav_url,''), COALESCE(carddav_url,''), COALESCE(signature,''), COALESCE(signature_html,''), COALESCE(signature_image,'')`

func scanAccount(sc interface{ Scan(...any) error }) (Account, error) {
	var a Account
	err := sc.Scan(&a.ID, &a.Name, &a.Email, &a.IMAPHost, &a.IMAPPort, &a.SMTPHost, &a.SMTPPort, &a.AuthType, &a.CalDAVURL, &a.CardDAVURL, &a.Signature, &a.SignatureHTML, &a.SignatureImage)
	return a, err
}

// UpsertAccount inserts or updates by ID (never REPLACE: that would cascade-delete mail).
func (s *Store) UpsertAccount(ctx context.Context, a Account) error {
	if a.AuthType == "" {
		a.AuthType = "password"
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO accounts (id, name, email, imap_host, imap_port, smtp_host, smtp_port, auth_type, caldav_url, carddav_url, signature, signature_html, signature_image) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET name=excluded.name, email=excluded.email,
  imap_host=excluded.imap_host, imap_port=excluded.imap_port,
  smtp_host=excluded.smtp_host, smtp_port=excluded.smtp_port,
  auth_type=excluded.auth_type, caldav_url=excluded.caldav_url, carddav_url=excluded.carddav_url,
  signature=excluded.signature, signature_html=excluded.signature_html, signature_image=excluded.signature_image`,
		a.ID, a.Name, a.Email, a.IMAPHost, a.IMAPPort, a.SMTPHost, a.SMTPPort, a.AuthType,
		nullStr(a.CalDAVURL), nullStr(a.CardDAVURL), a.Signature, a.SignatureHTML, a.SignatureImage)
	return err
}

// ListAccounts returns all accounts ordered by name then ID.
func (s *Store) ListAccounts(ctx context.Context) ([]Account, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+accountCols+` FROM accounts ORDER BY name, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Account
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// GetAccount returns ErrNotFound if the account does not exist.
func (s *Store) GetAccount(ctx context.Context, id string) (Account, error) {
	a, err := scanAccount(s.db.QueryRowContext(ctx, `SELECT `+accountCols+` FROM accounts WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return a, ErrNotFound
	}
	return a, err
}

// DeleteAccount removes the account and (by cascade) its folders and messages.
func (s *Store) DeleteAccount(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM accounts WHERE id=?`, id)
	return err
}
