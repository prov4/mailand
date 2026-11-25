package notify

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"mailand/backend/internal/store"
)

const (
	// mailMaxAge prevents ancient unread messages (e.g. after a UIDVALIDITY
	// resync) from triggering notifications.
	mailMaxAge = 24 * time.Hour
	// mailPreviewLimit is the max messages fetched to build a batch notification.
	mailPreviewLimit = 5
	// mailSnippetMax bounds the snippet shown in a single-message notification.
	mailSnippetMax = 120
)

// MailNotifier watches sync completion events and notifies on newly arrived
// unread INBOX messages.
type MailNotifier struct {
	st     *store.Store
	sender Sender
	log    *slog.Logger
	now    func() time.Time

	mu          sync.Mutex
	initialized map[string]bool  // account has completed at least one baseline sync
	lastSeq     map[string]int64 // highest seq already observed for the account
}

// NewMailNotifier creates a MailNotifier. Call Init before handling sync events.
func NewMailNotifier(st *store.Store, sender Sender, log *slog.Logger) *MailNotifier {
	if log == nil {
		log = slog.Default()
	}
	return &MailNotifier{
		st:          st,
		sender:      sender,
		log:         log,
		now:         time.Now,
		initialized: map[string]bool{},
		lastSeq:     map[string]int64{},
	}
}

// Init snapshots the current highest message seq per account so already-stored
// messages are never notified on startup.
func (m *MailNotifier) Init(ctx context.Context) error {
	seqs, err := m.st.MaxMessageSeqByAccount(ctx)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for acct, seq := range seqs {
		m.initialized[acct] = true
		m.lastSeq[acct] = seq
	}
	return nil
}

// OnSyncFinished should be called when a mail sync pass finishes for accountID.
// Passes with detail == "backlog" are ignored so initial mailbox downloads
// never flood the user with notifications.
func (m *MailNotifier) OnSyncFinished(ctx context.Context, accountID, detail string) {
	if accountID == "" || detail == "backlog" {
		return
	}

	m.mu.Lock()
	inited := m.initialized[accountID]
	afterSeq := m.lastSeq[accountID]
	m.mu.Unlock()

	maxSeq, err := m.st.AccountMaxMessageSeq(ctx, accountID)
	if err != nil {
		m.log.Warn("mail notify: max seq", "account", accountID, "err", err)
		return
	}

	// First completed sync for an account that had no messages at startup:
	// establish baseline without notifying on historical mail.
	if !inited {
		m.mu.Lock()
		m.initialized[accountID] = true
		if maxSeq > m.lastSeq[accountID] {
			m.lastSeq[accountID] = maxSeq
		}
		m.mu.Unlock()
		return
	}

	if maxSeq <= afterSeq {
		return
	}

	cutoff := m.now().Add(-mailMaxAge).Unix()
	msgs, total, err := m.st.NewInboxUnread(ctx, accountID, afterSeq, cutoff, mailPreviewLimit)
	if err != nil {
		m.log.Warn("mail notify: query unread", "account", accountID, "err", err)
		return
	}

	m.mu.Lock()
	if maxSeq > m.lastSeq[accountID] {
		m.lastSeq[accountID] = maxSeq
	}
	m.mu.Unlock()

	if total == 0 || len(msgs) == 0 {
		return
	}

	n := formatMailNotification(msgs, total)
	if err := m.sender.Send(ctx, n); err != nil {
		m.log.Warn("mail notify: send failed", "account", accountID, "err", err)
	}
}

func formatMailNotification(msgs []store.MessageHeader, total int) Notification {
	if total == 1 {
		msg := msgs[0]
		sender := strings.TrimSpace(msg.FromName)
		if sender == "" {
			sender = strings.TrimSpace(msg.FromAddress)
		}
		if sender == "" {
			sender = "New email"
		}
		subj := strings.TrimSpace(msg.Subject)
		if subj == "" {
			subj = "(no subject)"
		}
		body := subj
		if sn := strings.TrimSpace(msg.Snippet); sn != "" && sn != subj {
			body += "\n" + truncateRunes(sn, mailSnippetMax)
		}
		return Notification{
			Summary:  sender,
			Body:     body,
			Category: "email.arrived",
			Urgency:  UrgencyNormal,
			Timeout:  -1,
		}
	}

	const maxLines = 3
	var lines []string
	for i := 0; i < len(msgs) && i < maxLines; i++ {
		msg := msgs[i]
		who := strings.TrimSpace(msg.FromName)
		if who == "" {
			who = strings.TrimSpace(msg.FromAddress)
		}
		subj := strings.TrimSpace(msg.Subject)
		if subj == "" {
			subj = "(no subject)"
		}
		if who != "" {
			lines = append(lines, fmt.Sprintf("%s: %s", who, subj))
		} else {
			lines = append(lines, subj)
		}
	}
	if rem := total - len(lines); rem > 0 {
		lines = append(lines, fmt.Sprintf("+%d more", rem))
	}
	return Notification{
		Summary:  fmt.Sprintf("%d new emails", total),
		Body:     strings.Join(lines, "\n"),
		Category: "email.arrived",
		Urgency:  UrgencyNormal,
		Timeout:  -1,
	}
}

func truncateRunes(s string, maxLen int) string {
	r := []rune(s)
	if len(r) <= maxLen {
		return s
	}
	return strings.TrimSpace(string(r[:maxLen-1])) + "…"
}
