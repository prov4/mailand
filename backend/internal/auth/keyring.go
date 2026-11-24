// Package auth stores account secrets in the system keyring and implements
// Google OAuth2 login plus the SASL mechanisms used for IMAP/SMTP.
package auth

import (
	"errors"

	"github.com/zalando/go-keyring"
)

// Keyring layout: service "mailand"; user "<account_id>" holds the password
// (auth = "password"), user "<account_id>:refresh" holds the Google OAuth2
// refresh token. Account ids are [a-z0-9_-]+, so the ":" suffix cannot collide.
const keyringService = "mailand"

// ErrNotFound is returned when no secret is stored for the account.
var ErrNotFound = keyring.ErrNotFound

func passwordKey(id string) string { return id }
func refreshKey(id string) string  { return id + ":refresh" }

func SetPassword(id, pw string) error { return keyring.Set(keyringService, passwordKey(id), pw) }
func GetPassword(id string) (string, error) {
	return keyring.Get(keyringService, passwordKey(id))
}

func SetRefreshToken(id, tok string) error { return keyring.Set(keyringService, refreshKey(id), tok) }
func GetRefreshToken(id string) (string, error) {
	return keyring.Get(keyringService, refreshKey(id))
}

// Delete removes all secrets stored for the account; missing entries are ignored.
func Delete(id string) error {
	var errs []error
	for _, k := range []string{passwordKey(id), refreshKey(id)} {
		if err := keyring.Delete(keyringService, k); err != nil && !errors.Is(err, keyring.ErrNotFound) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
