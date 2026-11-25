// Package notify sends desktop notifications over the session D-Bus
// (org.freedesktop.Notifications) for newly arrived INBOX mail and upcoming
// calendar events.
package notify

import (
	"context"
	"fmt"
	"sync"

	"github.com/godbus/dbus/v5"
)

const (
	dbusDest = "org.freedesktop.Notifications"
	dbusPath = dbus.ObjectPath("/org/freedesktop/Notifications")
	dbusCall = "org.freedesktop.Notifications.Notify"

	appName      = "Mailand"
	appIcon      = "mailand"
	desktopEntry = "mailand-ui"
)

// Urgency levels defined by the Freedesktop Desktop Notifications Specification.
const (
	UrgencyLow      byte = 0
	UrgencyNormal   byte = 1
	UrgencyCritical byte = 2
)

// Notification is a single desktop notification to display.
type Notification struct {
	Summary  string
	Body     string
	Category string // e.g. "email.arrived", "x-mailand.calendar"
	Urgency  byte
	Timeout  int32 // milliseconds; -1 lets the notification daemon decide
}

// Sender delivers desktop notifications. Replaced in tests.
type Sender interface {
	Send(ctx context.Context, n Notification) error
}

// DBusSender delivers notifications via org.freedesktop.Notifications on the
// user's session D-Bus. It connects lazily and reconnects if the bus drops.
type DBusSender struct {
	mu   sync.Mutex
	conn *dbus.Conn
}

// NewDBusSender creates a DBusSender.
func NewDBusSender() *DBusSender { return &DBusSender{} }

// Send calls org.freedesktop.Notifications.Notify.
func (d *DBusSender) Send(ctx context.Context, n Notification) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if err := d.sendOnce(ctx, n); err != nil {
		// Retry once with a fresh connection in case the session bus restarted.
		d.resetLocked()
		return d.sendOnce(ctx, n)
	}
	return nil
}

func (d *DBusSender) sendOnce(ctx context.Context, n Notification) error {
	if d.conn == nil {
		c, err := dbus.ConnectSessionBus()
		if err != nil {
			return fmt.Errorf("connect session dbus: %w", err)
		}
		d.conn = c
	}
	hints := map[string]dbus.Variant{
		"urgency":       dbus.MakeVariant(n.Urgency),
		"desktop-entry": dbus.MakeVariant(desktopEntry),
	}
	if n.Category != "" {
		hints["category"] = dbus.MakeVariant(n.Category)
	}
	timeout := n.Timeout
	if timeout == 0 {
		timeout = -1
	}
	obj := d.conn.Object(dbusDest, dbusPath)
	call := obj.CallWithContext(ctx, dbusCall, 0,
		appName,
		uint32(0), // replaces_id
		appIcon,
		n.Summary,
		n.Body,
		[]string{}, // actions
		hints,
		timeout,
	)
	var id uint32
	if err := call.Store(&id); err != nil {
		return fmt.Errorf("dbus notify: %w", err)
	}
	return nil
}

func (d *DBusSender) resetLocked() {
	if d.conn != nil {
		_ = d.conn.Close()
		d.conn = nil
	}
}

// Close closes the underlying D-Bus connection, if open.
func (d *DBusSender) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.conn == nil {
		return nil
	}
	err := d.conn.Close()
	d.conn = nil
	return err
}
