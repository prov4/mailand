package mail

import (
	"context"
	"errors"
	"time"

	"github.com/emersion/go-imap/v2/imapclient"

	"mailand/backend/internal/auth"
)

// poke wakes the session for an INBOX-triggered pass; never blocks.
func (w *worker) poke() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

// idleLoop keeps an IDLE on INBOX over a dedicated connection and reconnects
// (with a fresh token) with backoff whenever it drops. It exits with ctx.
func (w *worker) idleLoop(ctx context.Context) {
	backoff := minBackoff
	for ctx.Err() == nil {
		start := time.Now()
		err := w.idleOnce(ctx)
		if ctx.Err() != nil || errors.Is(err, auth.ErrReauthRequired) {
			return // the main session reports reauth
		}
		if time.Since(start) > time.Minute {
			backoff = minBackoff
		}
		w.log.Debug("idle connection ended", "err", err, "retry_in", backoff)
		t := time.NewTimer(backoff)
		select {
		case <-t.C:
		case <-ctx.Done():
			t.Stop()
			return
		}
		backoff = min(backoff*2, maxBackoff)
	}
}

func (w *worker) idleOnce(ctx context.Context) error {
	opts := &imapclient.Options{UnilateralDataHandler: &imapclient.UnilateralDataHandler{
		Mailbox: func(d *imapclient.UnilateralDataMailbox) {
			if d.NumMessages != nil {
				w.poke()
			}
		},
		Expunge: func(uint32) { w.poke() },
		Fetch: func(md *imapclient.FetchMessageData) { // flag change
			_, _ = md.Collect()
			w.poke()
		},
	}}
	c, err := w.connect(ctx, opts)
	if err != nil {
		return err
	}
	defer c.close()
	if _, err := c.selectRO("INBOX"); err != nil {
		return err
	}
	cmd, err := c.Idle()
	if err != nil {
		return err
	}
	select {
	case <-ctx.Done():
	case <-c.Closed():
	}
	_ = cmd.Close()
	return cmd.Wait()
}
