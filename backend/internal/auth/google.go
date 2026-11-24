package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"html"
	"net"
	"net/http"
	"os/exec"
	"time"

	"golang.org/x/oauth2"

	"mailand/backend/internal/config"
)

// googleEndpoint mirrors golang.org/x/oauth2/google.Endpoint; that package
// pulls in the GCP metadata module, which we do not need.
var googleEndpoint = oauth2.Endpoint{
	AuthURL:   "https://accounts.google.com/o/oauth2/v2/auth",
	TokenURL:  "https://oauth2.googleapis.com/token",
	AuthStyle: oauth2.AuthStyleInParams,
}

// Scopes requested in a single consent (STARTUP.md 6E).
var Scopes = []string{
	"https://mail.google.com/",
	"https://www.googleapis.com/auth/calendar",
	"https://www.googleapis.com/auth/carddav",
}

// LoginTimeout bounds how long Login waits for the browser redirect.
const LoginTimeout = 5 * time.Minute

// ErrReauthRequired means the stored refresh token is missing, revoked or
// expired and the user must run `mailand-daemon login <id>` again.
var ErrReauthRequired = errors.New("reauthentication required")

var errStateMismatch = errors.New("oauth state mismatch")

// Option customises Login/TokenSource (used by tests to fake Google).
type Option func(*options)

type options struct{ endpoint oauth2.Endpoint }

// WithEndpoint overrides Google's OAuth2 endpoint.
func WithEndpoint(e oauth2.Endpoint) Option { return func(o *options) { o.endpoint = e } }

func newOptions(opts []Option) options {
	o := options{endpoint: googleEndpoint}
	for _, f := range opts {
		f(&o)
	}
	return o
}

func oauthConfig(cfg *config.Config, o options, redirect string) (*oauth2.Config, error) {
	if cfg == nil || cfg.Google == nil || cfg.Google.ClientID == "" {
		return nil, errors.New("[google] client_id is not configured")
	}
	return &oauth2.Config{
		ClientID:     cfg.Google.ClientID,
		ClientSecret: cfg.Google.ClientSecret,
		Endpoint:     o.endpoint,
		RedirectURL:  redirect,
		Scopes:       Scopes,
	}, nil
}

// OpenBrowser opens url with xdg-open.
func OpenBrowser(url string) error {
	return exec.Command("xdg-open", url).Start()
}

type callbackResult struct {
	code string
	err  error
}

// Login runs the installed-app loopback flow with PKCE and stores the refresh
// token in the keyring. openBrowser is called with the consent URL; nil means
// OpenBrowser.
func Login(ctx context.Context, cfg *config.Config, acct *config.Account, openBrowser func(url string) error, opts ...Option) error {
	if acct.Auth != config.AuthOAuth2Google {
		return fmt.Errorf("account %q does not use oauth2_google", acct.ID)
	}
	if openBrowser == nil {
		openBrowser = OpenBrowser
	}
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("listen for oauth callback: %w", err)
	}
	oc, err := oauthConfig(cfg, newOptions(opts), fmt.Sprintf("http://%s/callback", lis.Addr()))
	if err != nil {
		lis.Close()
		return err
	}

	state, err := randomState()
	if err != nil {
		lis.Close()
		return err
	}
	verifier := oauth2.GenerateVerifier()

	results := make(chan callbackResult, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		res := parseCallback(r, state)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if errors.Is(res.err, errStateMismatch) {
			// Not our redirect (stray or forged local request): reject it but
			// keep waiting for the real one instead of aborting the login.
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, "<h3>Invalid login callback</h3>")
			return
		}
		if res.err != nil {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, "<h3>Login failed</h3><p>%s</p>", html.EscapeString(res.err.Error()))
		} else {
			fmt.Fprint(w, "<h3>mailand: login complete</h3><p>You can close this tab.</p>")
		}
		select {
		case results <- res:
		default:
		}
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go srv.Serve(lis)
	defer srv.Close()

	authURL := oc.AuthCodeURL(state,
		oauth2.AccessTypeOffline,
		oauth2.SetAuthURLParam("prompt", "consent"),
		oauth2.SetAuthURLParam("login_hint", acct.Email),
		oauth2.S256ChallengeOption(verifier),
	)
	if err := openBrowser(authURL); err != nil {
		return fmt.Errorf("open browser: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, LoginTimeout)
	defer cancel()
	var res callbackResult
	select {
	case res = <-results:
	case <-ctx.Done():
		return fmt.Errorf("waiting for browser login: %w", ctx.Err())
	}
	if res.err != nil {
		return res.err
	}

	tok, err := oc.Exchange(ctx, res.code, oauth2.VerifierOption(verifier))
	if err != nil {
		return fmt.Errorf("exchange authorization code: %w", err)
	}
	if tok.RefreshToken == "" {
		return errors.New("google returned no refresh token; revoke mailand's access at https://myaccount.google.com/permissions and log in again")
	}
	if err := SetRefreshToken(acct.ID, tok.RefreshToken); err != nil {
		return fmt.Errorf("store refresh token in keyring: %w", err)
	}
	return nil
}

func parseCallback(r *http.Request, state string) callbackResult {
	q := r.URL.Query()
	if subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(state)) != 1 {
		return callbackResult{err: errStateMismatch}
	}
	if e := q.Get("error"); e != "" {
		msg := "authorization denied: " + e
		if d := q.Get("error_description"); d != "" {
			msg += " (" + d + ")"
		}
		return callbackResult{err: errors.New(msg)}
	}
	code := q.Get("code")
	if code == "" {
		return callbackResult{err: errors.New("callback had no authorization code")}
	}
	return callbackResult{code: code}
}

func randomState() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// TokenSource returns a cached token source backed by the stored refresh
// token. Errors from it wrap ErrReauthRequired when the user must log in again.
func TokenSource(ctx context.Context, cfg *config.Config, acct *config.Account, opts ...Option) (oauth2.TokenSource, error) {
	oc, err := oauthConfig(cfg, newOptions(opts), "")
	if err != nil {
		return nil, err
	}
	rt, err := GetRefreshToken(acct.ID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, fmt.Errorf("account %q: no refresh token stored: %w", acct.ID, ErrReauthRequired)
		}
		return nil, fmt.Errorf("read refresh token: %w", err)
	}
	src := &mappedSource{id: acct.ID, src: oc.TokenSource(ctx, &oauth2.Token{RefreshToken: rt})}
	return oauth2.ReuseTokenSource(nil, src), nil
}

type mappedSource struct {
	id  string
	src oauth2.TokenSource
}

func (m *mappedSource) Token() (*oauth2.Token, error) {
	tok, err := m.src.Token()
	var re *oauth2.RetrieveError
	if errors.As(err, &re) && re.ErrorCode == "invalid_grant" {
		return nil, fmt.Errorf("account %q: refresh token rejected: %w", m.id, ErrReauthRequired)
	}
	return tok, err
}
