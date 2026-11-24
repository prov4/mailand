package auth

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zalando/go-keyring"
	"golang.org/x/oauth2"

	"mailand/backend/internal/config"
)

func TestKeyring(t *testing.T) {
	keyring.MockInit()
	if _, err := GetPassword("a"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	SetPassword("a", "pw")
	SetRefreshToken("a", "rt")
	if v, _ := GetPassword("a"); v != "pw" {
		t.Error(v)
	}
	if v, _ := GetRefreshToken("a"); v != "rt" {
		t.Error(v)
	}
	if err := Delete("a"); err != nil {
		t.Fatal(err)
	}
	if _, err := GetRefreshToken("a"); !errors.Is(err, ErrNotFound) {
		t.Error(err)
	}
	if err := Delete("a"); err != nil {
		t.Error("delete of missing should be nil:", err)
	}
}

func TestXOAuth2(t *testing.T) {
	mech, ir, err := NewXOAuth2Client("u@gmail.com", "tok").Start()
	if err != nil || mech != "XOAUTH2" || string(ir) != "user=u@gmail.com\x01auth=Bearer tok\x01\x01" {
		t.Fatalf("%q %q %v", mech, ir, err)
	}
	resp, err := NewXOAuth2Client("u", "t").Next([]byte(`{"status":"400"}`))
	if err != nil || resp == nil || len(resp) != 0 {
		t.Fatalf("%v %v", resp, err)
	}
}

func TestSASLClient(t *testing.T) {
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "abc"})
	c, err := SASLClient(context.Background(), ts, "e@x")
	if err != nil {
		t.Fatal(err)
	}
	if _, ir, _ := c.Start(); !strings.Contains(string(ir), "Bearer abc") {
		t.Errorf("%q", ir)
	}
}

type fakeGoogle struct {
	*httptest.Server
	mu      sync.Mutex
	form    url.Values
	respond func(w http.ResponseWriter)
}

func newFakeGoogle(t *testing.T) *fakeGoogle {
	f := &fakeGoogle{respond: func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"access_token":"at","refresh_token":"rt-1","token_type":"Bearer","expires_in":3600}`)
	}}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		f.mu.Lock()
		f.form = r.PostForm
		f.mu.Unlock()
		f.respond(w)
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeGoogle) opt() Option {
	return WithEndpoint(oauth2.Endpoint{AuthURL: f.URL + "/auth", TokenURL: f.URL + "/token", AuthStyle: oauth2.AuthStyleInParams})
}

var (
	testCfg  = &config.Config{Google: &config.Google{ClientID: "cid", ClientSecret: "csec"}}
	testAcct = &config.Account{ID: "gmail", Email: "me@gmail.com", Auth: config.AuthOAuth2Google}
)

// browser simulates the user: it checks the consent URL and hits the redirect.
func browser(t *testing.T, mutate func(q url.Values), got *url.Values) func(string) error {
	return func(u string) error {
		pu, _ := url.Parse(u)
		q := pu.Query()
		if got != nil {
			*got = q
		}
		cb, _ := url.Parse(q.Get("redirect_uri"))
		if cb.Hostname() != "127.0.0.1" || cb.Path != "/callback" {
			t.Errorf("redirect_uri %s", cb)
		}
		rq := url.Values{"code": {"thecode"}, "state": {q.Get("state")}}
		if mutate != nil {
			mutate(rq)
		}
		cb.RawQuery = rq.Encode()
		resp, err := http.Get(cb.String())
		if err != nil {
			return err
		}
		resp.Body.Close()
		return nil
	}
}

func TestLoginSuccess(t *testing.T) {
	keyring.MockInit()
	f := newFakeGoogle(t)
	var q url.Values
	if err := Login(context.Background(), testCfg, testAcct, browser(t, nil, &q), f.opt()); err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]string{
		"code_challenge_method": "S256", "access_type": "offline", "prompt": "consent",
		"login_hint": "me@gmail.com", "client_id": "cid", "response_type": "code",
	} {
		if q.Get(k) != v {
			t.Errorf("%s = %q, want %q", k, q.Get(k), v)
		}
	}
	for _, s := range Scopes {
		if !strings.Contains(q.Get("scope"), s) {
			t.Errorf("missing scope %s", s)
		}
	}
	if q.Get("code_challenge") == "" || q.Get("state") == "" {
		t.Error("missing challenge/state")
	}
	if f.form.Get("code_verifier") == "" || f.form.Get("code") != "thecode" {
		t.Errorf("token request: %v", f.form)
	}
	if tok, _ := GetRefreshToken("gmail"); tok != "rt-1" {
		t.Errorf("stored %q", tok)
	}
}

func TestLoginRejections(t *testing.T) {
	keyring.MockInit()
	f := newFakeGoogle(t)
	ctx := context.Background()

	// A forged callback (wrong state) is rejected with 400 but must not abort
	// the login: the genuine redirect that follows still completes it.
	real := browser(t, nil, nil)
	err := Login(ctx, testCfg, testAcct, func(u string) error {
		pu, _ := url.Parse(u)
		cb, _ := url.Parse(pu.Query().Get("redirect_uri"))
		cb.RawQuery = url.Values{"code": {"evil"}, "state": {"evil"}}.Encode()
		resp, err := http.Get(cb.String())
		if err != nil {
			return err
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("forged callback status = %d, want 400", resp.StatusCode)
		}
		return real(u)
	}, f.opt())
	if err != nil {
		t.Errorf("login after forged callback: %v", err)
	}
	keyring.MockInit() // the cases below must not store a token
	err = Login(ctx, testCfg, testAcct, browser(t, func(q url.Values) {
		q.Del("code")
		q.Set("error", "access_denied")
	}, nil), f.opt())
	if err == nil || !strings.Contains(err.Error(), "access_denied") {
		t.Errorf("error param: %v", err)
	}
	f.respond = func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"access_token":"at","token_type":"Bearer","expires_in":3600}`)
	}
	err = Login(ctx, testCfg, testAcct, browser(t, nil, nil), f.opt())
	if err == nil || !strings.Contains(err.Error(), "no refresh token") {
		t.Errorf("no refresh: %v", err)
	}
	if _, err := GetRefreshToken("gmail"); !errors.Is(err, ErrNotFound) {
		t.Error("token stored despite failure")
	}
}

func TestLoginCancel(t *testing.T) {
	keyring.MockInit()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err := Login(ctx, testCfg, testAcct, func(string) error { return nil })
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
}

func TestTokenSource(t *testing.T) {
	keyring.MockInit()
	f := newFakeGoogle(t)
	ctx := context.Background()

	if _, err := TokenSource(ctx, testCfg, testAcct, f.opt()); !errors.Is(err, ErrReauthRequired) {
		t.Fatalf("no token: %v", err)
	}

	SetRefreshToken("gmail", "rt-1")
	f.respond = func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"access_token":"fresh","token_type":"Bearer","expires_in":3600}`)
	}
	ts, err := TokenSource(ctx, testCfg, testAcct, f.opt())
	if err != nil {
		t.Fatal(err)
	}
	tok, err := ts.Token()
	if err != nil || tok.AccessToken != "fresh" || f.form.Get("refresh_token") != "rt-1" || f.form.Get("grant_type") != "refresh_token" {
		t.Fatalf("%v %v %v", tok, err, f.form)
	}

	f.respond = func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"error":"invalid_grant","error_description":"Token has been expired or revoked."}`)
	}
	ts, _ = TokenSource(ctx, testCfg, testAcct, f.opt())
	if _, err := ts.Token(); !errors.Is(err, ErrReauthRequired) {
		t.Fatalf("invalid_grant: %v", err)
	}
}
