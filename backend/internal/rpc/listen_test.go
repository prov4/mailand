package rpc

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sockPath(t *testing.T) string {
	// Keep it short: sun_path is limited to ~108 bytes.
	return filepath.Join(t.TempDir(), "m.sock")
}

func TestListenUnixMode0600(t *testing.T) {
	p := sockPath(t)
	l, err := ListenUnix(p)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Fatalf("mode = %o, want 600", perm)
	}
}

func TestListenUnixRemovesStaleSocket(t *testing.T) {
	p := sockPath(t)
	l, err := net.Listen("unix", p)
	if err != nil {
		t.Fatal(err)
	}
	// Close without unlinking to leave a stale socket file behind.
	l.(*net.UnixListener).SetUnlinkOnClose(false)
	l.Close()
	if _, err := os.Lstat(p); err != nil {
		t.Fatalf("expected stale socket: %v", err)
	}

	l2, err := ListenUnix(p)
	if err != nil {
		t.Fatalf("ListenUnix over stale socket: %v", err)
	}
	l2.Close()
}

func TestListenUnixRefusesLiveListener(t *testing.T) {
	p := sockPath(t)
	l, err := ListenUnix(p)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	_, err = ListenUnix(p)
	if err == nil || !strings.Contains(err.Error(), "already listening") {
		t.Fatalf("err = %v, want 'already listening'", err)
	}
	if _, serr := os.Lstat(p); serr != nil {
		t.Fatalf("live socket was removed: %v", serr)
	}
}

func TestListenUnixRefusesRegularFile(t *testing.T) {
	p := sockPath(t)
	if err := os.WriteFile(p, []byte("keep me"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ListenUnix(p); err == nil {
		t.Fatal("expected error for regular file")
	}
	if b, err := os.ReadFile(p); err != nil || string(b) != "keep me" {
		t.Fatalf("regular file was modified: %q, %v", b, err)
	}
}
