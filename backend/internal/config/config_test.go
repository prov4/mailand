package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func write(t *testing.T, s string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

const google = "[google]\nclient_id=\"id\"\nclient_secret=\"s\"\n"

func TestValid(t *testing.T) {
	cfg, err := Load(write(t, google+`
[[account]]
id="gmail"
name="G"
email="a@gmail.com"
provider="gmail"
poll_interval="2m"

[[account]]
id="work_1"
email="b@example.com"
provider="imap"
imap_host="imap.example.com"
smtp_host="smtp.example.com"
smtp_tls="starttls"
`))
	if err != nil {
		t.Fatal(err)
	}
	g, _ := cfg.Account("gmail")
	if g.Auth != AuthOAuth2Google || g.IMAPHost != "imap.gmail.com" || g.IMAPPort != 993 ||
		g.SMTPHost != "smtp.gmail.com" || g.SMTPPort != 465 || g.PollInterval != 2*time.Minute {
		t.Errorf("gmail defaults: %+v", g)
	}
	w, _ := cfg.Account("work_1")
	if w.Auth != AuthPassword || w.IMAPPort != 993 || w.IMAPTLS != TLSImplicit ||
		w.SMTPPort != 587 || w.Name != "b@example.com" || w.PollInterval != DefaultPollInterval {
		t.Errorf("imap defaults: %+v", w)
	}
}

func TestExampleParses(t *testing.T) {
	if _, err := Load(write(t, ExampleConfig)); err != nil {
		t.Fatal(err)
	}
}

func TestErrors(t *testing.T) {
	acct := func(body string) string {
		return google + "[[account]]\nname=\"n\"\n" + body
	}
	gm := "id=\"a\"\nemail=\"a@gmail.com\"\nprovider=\"gmail\"\n"
	tests := []struct{ name, cfg, want string }{
		{"missing id", acct("email=\"a@b.c\"\nprovider=\"gmail\"\n"), "id is required"},
		{"bad id", acct("id=\"Bad Id\"\nemail=\"a@b.c\"\nprovider=\"gmail\"\n"), "[a-z0-9_-]+"},
		{"dup id", acct(gm) + "[[account]]\n" + gm, "duplicate id"},
		{"missing email", acct("id=\"a\"\nprovider=\"gmail\"\n"), "email"},
		{"missing provider", acct("id=\"a\"\nemail=\"a@b.c\"\n"), "provider is required"},
		{"unknown provider", acct("id=\"a\"\nemail=\"a@b.c\"\nprovider=\"x\"\n"), "unknown provider"},
		{"unknown auth", acct(gm + "auth=\"x\"\n"), "unknown auth"},
		{"oauth on imap", acct("id=\"a\"\nemail=\"a@b.c\"\nprovider=\"imap\"\nauth=\"oauth2_google\"\nimap_host=\"h\"\nsmtp_host=\"h\"\n"), "requires provider gmail"},
		{"imap no host", acct("id=\"a\"\nemail=\"a@b.c\"\nprovider=\"imap\"\n"), "imap_host"},
		{"bad tls", acct(gm + "imap_tls=\"x\"\n"), "implicit or starttls"},
		{"bad poll", acct(gm + "poll_interval=\"soon\"\n"), "poll_interval"},
		{"no google", "[[account]]\n" + gm, "client_id"},
		{"unknown key", acct(gm + "bogus=1\n"), "unknown config keys"},
		{"secret key", acct(gm + "password=\"hunter2\"\n"), "keyring"},
		{"token key", acct(gm + "refresh_token=\"x\"\n"), "keyring"},
		{"bad caldav", acct(gm + "caldav_url=\"dav.example.com\"\n"), "caldav_url"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(write(t, tt.cfg))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("got %v, want containing %q", err, tt.want)
			}
			if strings.Contains(err.Error(), "hunter2") {
				t.Error("error leaks secret value")
			}
		})
	}
}

func TestMissingFile(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "nope.toml"))
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("got %v", err)
	}
}

func TestPaths(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/c")
	t.Setenv("XDG_DATA_HOME", "/d")
	t.Setenv("MAILAND_CONFIG", "")
	if ConfigPath() != "/c/mailand/config.toml" || DBPath() != "/d/mailand/mailand.db" || BlobDir() != "/d/mailand/blobs" {
		t.Error(ConfigPath(), DBPath(), BlobDir())
	}
	t.Setenv("MAILAND_CONFIG", "/x.toml")
	if ConfigPath() != "/x.toml" {
		t.Error(ConfigPath())
	}
}

func TestDAVURLs(t *testing.T) {
	cfg, err := Load(write(t, google+`
[[account]]
id = "g"
email = "me+x@gmail.com"
provider = "gmail"

[[account]]
id = "g2"
email = "b@gmail.com"
provider = "gmail"
caldav_url = "off"
carddav_url = "https://dav.example.com/card/"

[[account]]
id = "w"
email = "w@example.com"
provider = "imap"
imap_host = "h"
smtp_host = "h"
`))
	if err != nil {
		t.Fatal(err)
	}
	g, g2, w := cfg.Accounts[0], cfg.Accounts[1], cfg.Accounts[2]
	if got := g.CalendarURL(); got != "https://apidata.googleusercontent.com/caldav/v2/me+x@gmail.com/user" {
		t.Errorf("gmail caldav: %s", got)
	}
	if got := g.ContactsURL(); got != "https://www.googleapis.com/carddav/v1/principals/me+x@gmail.com/lists/default/" {
		t.Errorf("gmail carddav: %s", got)
	}
	if g2.CalendarURL() != "" || g2.ContactsURL() != "https://dav.example.com/card/" {
		t.Errorf("off/override: %q %q", g2.CalendarURL(), g2.ContactsURL())
	}
	if w.CalendarURL() != "" || w.ContactsURL() != "" {
		t.Errorf("imap without DAV: %q %q", w.CalendarURL(), w.ContactsURL())
	}
}
