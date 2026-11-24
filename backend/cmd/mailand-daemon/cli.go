package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"golang.org/x/term"

	"mailand/backend/internal/auth"
	"mailand/backend/internal/config"
)

// runSubcommand handles `login` and `set-password`. It reports whether args
// named a subcommand; the exit code is meaningful only then.
func runSubcommand(args []string) (handled bool, code int) {
	if len(args) < 1 || (args[0] != "login" && args[0] != "set-password") {
		return false, 0
	}
	if len(args) != 2 {
		fmt.Fprintf(os.Stderr, "usage: mailand-daemon %s <account_id>\n", args[0])
		return true, 2
	}
	var err error
	switch args[0] {
	case "login":
		err = cmdLogin(args[1])
	case "set-password":
		err = cmdSetPassword(args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return true, 1
	}
	return true, 0
}

func loadAccount(id string) (*config.Config, *config.Account, error) {
	cfg, err := config.Load(config.ConfigPath())
	if err != nil {
		return nil, nil, err
	}
	acct, ok := cfg.Account(id)
	if !ok {
		return nil, nil, fmt.Errorf("no account %q in %s", id, config.ConfigPath())
	}
	return cfg, acct, nil
}

func cmdLogin(id string) error {
	cfg, acct, err := loadAccount(id)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	open := func(url string) error {
		fmt.Printf("Opening your browser to sign in. If it does not open, visit:\n\n%s\n\n", url)
		if err := auth.OpenBrowser(url); err != nil {
			fmt.Fprintln(os.Stderr, "could not run xdg-open:", err)
		}
		return nil
	}
	if err := auth.Login(ctx, cfg, acct, open); err != nil {
		return err
	}
	fmt.Printf("Logged in %s; refresh token stored in the keyring.\n", acct.Email)
	return nil
}

func cmdSetPassword(id string) error {
	_, acct, err := loadAccount(id)
	if err != nil {
		return err
	}
	if acct.Auth != config.AuthPassword {
		return fmt.Errorf("account %q uses %s, not password", id, acct.Auth)
	}
	pw, err := readSecret(fmt.Sprintf("Password for %s: ", acct.Email))
	if err != nil {
		return err
	}
	if pw == "" {
		return errors.New("empty password")
	}
	if err := auth.SetPassword(id, pw); err != nil {
		return err
	}
	fmt.Println("Password stored in the keyring.")
	return nil
}

func readSecret(prompt string) (string, error) {
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		fmt.Fprint(os.Stderr, prompt)
		b, err := term.ReadPassword(fd)
		fmt.Fprintln(os.Stderr)
		return string(b), err
	}
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && !(errors.Is(err, io.EOF) && line != "") {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}
