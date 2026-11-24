package auth

import (
	"context"

	"github.com/emersion/go-sasl"
	"golang.org/x/oauth2"
)

type xoauth2Client struct{ user, token string }

// NewXOAuth2Client returns a SASL XOAUTH2 client (Gmail's documented mechanism).
func NewXOAuth2Client(user, accessToken string) sasl.Client {
	return &xoauth2Client{user, accessToken}
}

func (c *xoauth2Client) Start() (string, []byte, error) {
	return "XOAUTH2", []byte("user=" + c.user + "\x01auth=Bearer " + c.token + "\x01\x01"), nil
}

// Next handles the server's JSON error challenge: an empty response makes the
// server finish with the real tagged failure.
func (c *xoauth2Client) Next(challenge []byte) ([]byte, error) {
	return []byte{}, nil
}

// SASLClient fetches a fresh access token from ts and returns an XOAUTH2 client.
func SASLClient(ctx context.Context, ts oauth2.TokenSource, email string) (sasl.Client, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	tok, err := ts.Token()
	if err != nil {
		return nil, err
	}
	return NewXOAuth2Client(email, tok.AccessToken), nil
}

// PlainClient returns a SASL PLAIN client for password accounts.
func PlainClient(email, password string) sasl.Client {
	return sasl.NewPlainClient("", email, password)
}
