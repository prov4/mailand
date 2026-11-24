// Package config resolves runtime paths and settings for mailand-daemon.
package config

import (
	"fmt"
	"os"
	"path/filepath"
)

// SocketPath returns the Unix socket path the daemon listens on:
// $MAILAND_SOCKET, else $XDG_RUNTIME_DIR/mailand.sock, else /tmp/mailand-<uid>.sock.
func SocketPath() string {
	if p := os.Getenv("MAILAND_SOCKET"); p != "" {
		return p
	}
	if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" {
		return filepath.Join(dir, "mailand.sock")
	}
	return fmt.Sprintf("/tmp/mailand-%d.sock", os.Getuid())
}

// ConfigPath returns the config file path: $MAILAND_CONFIG, else
// $XDG_CONFIG_HOME/mailand/config.toml (default ~/.config/...).
func ConfigPath() string {
	if p := os.Getenv("MAILAND_CONFIG"); p != "" {
		return p
	}
	return filepath.Join(xdgDir("XDG_CONFIG_HOME", ".config"), "mailand", "config.toml")
}

// DataDir returns $XDG_DATA_HOME/mailand (default ~/.local/share/mailand).
func DataDir() string {
	return filepath.Join(xdgDir("XDG_DATA_HOME", ".local/share"), "mailand")
}

// DBPath returns the SQLite database path.
func DBPath() string { return filepath.Join(DataDir(), "mailand.db") }

// BlobDir returns the directory for message body blobs.
func BlobDir() string { return filepath.Join(DataDir(), "blobs") }

func xdgDir(env, fallback string) string {
	if d := os.Getenv(env); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, fallback)
}
