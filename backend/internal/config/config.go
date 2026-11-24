package config

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

const (
	ProviderGmail = "gmail"
	ProviderIMAP  = "imap"

	AuthOAuth2Google = "oauth2_google"
	AuthPassword     = "password"

	TLSImplicit = "implicit"
	TLSStartTLS = "starttls"
)

// Config is the parsed config.toml.
type Config struct {
	Google   *Google   `toml:"google"`
	Accounts []Account `toml:"account"`
}

// Google holds the user's own Desktop-app OAuth client. Google does not treat
// desktop client secrets as confidential (STARTUP.md 6E), so it may live here.
type Google struct {
	ClientID     string `toml:"client_id"`
	ClientSecret string `toml:"client_secret"`
}

// Account is one [[account]] table. Secrets never live here; see package auth.
type Account struct {
	ID       string `toml:"id"`
	Name     string `toml:"name"`
	Email    string `toml:"email"`
	Provider string `toml:"provider"`
	Auth     string `toml:"auth"`

	IMAPHost string `toml:"imap_host"`
	IMAPPort int    `toml:"imap_port"`
	IMAPTLS  string `toml:"imap_tls"`
	SMTPHost string `toml:"smtp_host"`
	SMTPPort int    `toml:"smtp_port"`
	SMTPTLS  string `toml:"smtp_tls"`

	CalDAVURL  string `toml:"caldav_url"`
	CardDAVURL string `toml:"carddav_url"`

	PollIntervalStr string        `toml:"poll_interval"`
	PollInterval    time.Duration `toml:"-"`
}

// DefaultPollInterval applies when poll_interval is unset.
const DefaultPollInterval = 5 * time.Minute

var idRe = regexp.MustCompile(`^[a-z0-9_-]+$`)

// Account returns the account with the given id.
func (c *Config) Account(id string) (*Account, bool) {
	for i := range c.Accounts {
		if c.Accounts[i].ID == id {
			return &c.Accounts[i], true
		}
	}
	return nil, false
}

// Load reads and validates the config file. If the file does not exist the
// returned error satisfies errors.Is(err, fs.ErrNotExist).
func Load(path string) (*Config, error) {
	var raw struct {
		Google   *Google          `toml:"google"`
		Accounts []map[string]any `toml:"account"`
	}
	if _, err := toml.DecodeFile(path, &raw); err != nil {
		return nil, wrapLoadErr(path, err)
	}
	// Reject secret-like keys before the strict decode so the message is specific.
	for i, a := range raw.Accounts {
		for k := range a {
			if isSecretKey(k) {
				return nil, fmt.Errorf("%s: account #%d: key %q not allowed: secrets (passwords, tokens) live in the system keyring, not the config file; use `mailand-daemon set-password` or `login`", path, i+1, k)
			}
		}
	}

	var cfg Config
	md, err := toml.DecodeFile(path, &cfg)
	if err != nil {
		return nil, wrapLoadErr(path, err)
	}
	if un := md.Undecoded(); len(un) > 0 {
		keys := make([]string, len(un))
		for i, k := range un {
			keys[i] = k.String()
		}
		return nil, fmt.Errorf("%s: unknown config keys: %s", path, strings.Join(keys, ", "))
	}
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &cfg, nil
}

func wrapLoadErr(path string, err error) error {
	var pe toml.ParseError
	if errors.As(err, &pe) {
		return fmt.Errorf("%s: %s", path, pe.Error())
	}
	return err // *PathError from os.Open keeps fs.ErrNotExist
}

func isSecretKey(k string) bool {
	k = strings.ToLower(k)
	for _, s := range []string{"password", "passwd", "token", "secret"} {
		if strings.Contains(k, s) {
			return true
		}
	}
	return false
}

func (c *Config) validate() error {
	seen := map[string]bool{}
	needGoogle := false
	for i := range c.Accounts {
		a := &c.Accounts[i]
		where := fmt.Sprintf("account #%d", i+1)
		if a.ID != "" {
			where = fmt.Sprintf("account %q", a.ID)
		}
		if a.ID == "" {
			return fmt.Errorf("%s: id is required", where)
		}
		if !idRe.MatchString(a.ID) {
			return fmt.Errorf("%s: id must match [a-z0-9_-]+", where)
		}
		if seen[a.ID] {
			return fmt.Errorf("%s: duplicate id", where)
		}
		seen[a.ID] = true
		if a.Email == "" || !strings.Contains(a.Email, "@") {
			return fmt.Errorf("%s: a valid email is required", where)
		}
		if a.Name == "" {
			a.Name = a.Email
		}
		switch a.Provider {
		case ProviderGmail, ProviderIMAP:
		case "":
			return fmt.Errorf("%s: provider is required (gmail or imap)", where)
		default:
			return fmt.Errorf("%s: unknown provider %q (want gmail or imap)", where, a.Provider)
		}
		if a.Auth == "" {
			if a.Provider == ProviderGmail {
				a.Auth = AuthOAuth2Google
			} else {
				a.Auth = AuthPassword
			}
		}
		switch a.Auth {
		case AuthOAuth2Google:
			needGoogle = true
		case AuthPassword:
		default:
			return fmt.Errorf("%s: unknown auth %q (want oauth2_google or password)", where, a.Auth)
		}
		if a.Auth == AuthOAuth2Google && a.Provider != ProviderGmail {
			return fmt.Errorf("%s: auth oauth2_google requires provider gmail", where)
		}
		if err := a.applyServerDefaults(); err != nil {
			return fmt.Errorf("%s: %w", where, err)
		}
		a.PollInterval = DefaultPollInterval
		if a.PollIntervalStr != "" {
			d, err := time.ParseDuration(a.PollIntervalStr)
			if err != nil || d <= 0 {
				return fmt.Errorf("%s: poll_interval %q is not a positive duration like \"5m\"", where, a.PollIntervalStr)
			}
			a.PollInterval = d
		}
	}
	if needGoogle && (c.Google == nil || c.Google.ClientID == "") {
		return errors.New("an account uses oauth2_google but [google] client_id is not set")
	}
	return nil
}

func (a *Account) applyServerDefaults() error {
	if a.Provider == ProviderGmail {
		def(&a.IMAPHost, "imap.gmail.com")
		def(&a.SMTPHost, "smtp.gmail.com")
		defInt(&a.IMAPPort, 993)
		defInt(&a.SMTPPort, 465)
	}
	if a.IMAPHost == "" || a.SMTPHost == "" {
		return errors.New("imap_host and smtp_host are required for provider imap")
	}
	def(&a.IMAPTLS, TLSImplicit)
	def(&a.SMTPTLS, TLSImplicit)
	defInt(&a.IMAPPort, map[bool]int{true: 993, false: 143}[a.IMAPTLS == TLSImplicit])
	defInt(&a.SMTPPort, map[bool]int{true: 465, false: 587}[a.SMTPTLS == TLSImplicit])
	for k, v := range map[string]string{"imap_tls": a.IMAPTLS, "smtp_tls": a.SMTPTLS} {
		if v != TLSImplicit && v != TLSStartTLS {
			return fmt.Errorf("%s %q must be implicit or starttls", k, v)
		}
	}
	return nil
}

func def(s *string, v string) {
	if *s == "" {
		*s = v
	}
}

func defInt(n *int, v int) {
	if *n == 0 {
		*n = v
	}
}

// ExampleConfig is a commented sample config.toml.
const ExampleConfig = `# mailand config. Secrets (passwords, refresh tokens) are NOT stored here;
# they live in the system keyring (see ` + "`mailand-daemon login|set-password`" + `).

# Your own Google Cloud OAuth client of type "Desktop app".
[google]
client_id = "1234567890-abc.apps.googleusercontent.com"
client_secret = "GOCSPX-..."

[[account]]
id = "gmail"                  # stable id: [a-z0-9-_]+, unique
name = "Personal Gmail"
email = "someone@gmail.com"
provider = "gmail"            # "gmail" | "imap"
# auth = "oauth2_google"      # default for gmail; "password" otherwise
# poll_interval = "5m"        # secondary-folder polling

# [[account]]
# id = "work"
# name = "Work"
# email = "me@example.com"
# provider = "imap"
# imap_host = "imap.example.com"
# imap_port = 993
# imap_tls = "implicit"       # or "starttls"
# smtp_host = "smtp.example.com"
# smtp_port = 587
# smtp_tls = "starttls"
# caldav_url = ""             # Milestone 5
# carddav_url = ""
`
