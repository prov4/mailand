package rpc

import (
	"errors"
	"fmt"
	"net"
	"os"
	"syscall"
	"time"
)

// ListenUnix binds a Unix socket at path with mode 0600. A stale socket left
// by a dead daemon is removed; a live listener or a non-socket file is an error.
func ListenUnix(path string) (net.Listener, error) {
	fi, err := os.Lstat(path)
	switch {
	case err == nil:
		if fi.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("%s exists and is not a socket", path)
		}
		if c, derr := net.DialTimeout("unix", path, time.Second); derr == nil {
			c.Close()
			return nil, fmt.Errorf("another mailand-daemon is already listening on %s", path)
		}
		if err := os.Remove(path); err != nil {
			return nil, fmt.Errorf("remove stale socket: %w", err)
		}
	case !errors.Is(err, os.ErrNotExist):
		return nil, err
	}

	// umask is process-wide; restrict it so the socket is never world-accessible.
	old := syscall.Umask(0o177)
	l, err := net.Listen("unix", path)
	syscall.Umask(old)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		l.Close()
		return nil, err
	}
	return l, nil
}
