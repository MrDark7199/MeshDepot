// Package maildigest collects pending notification e-mails and sends them as
// one message per member.
//
// Without it a batch behaves badly: forty queued downloads of which thirty-four
// fail produce thirty-four separate e-mails, and the reader stops at the third
// and looks for the off switch. One message per member per interval keeps the
// same information and costs one notification.
//
// The delay buys something the immediate send cannot have: the message is
// assembled when it goes out, not when the event happened, so anything that has
// resolved in the meantime is left out. A download that was re-queued and
// succeeded no longer appears. What remains is still worth saying so, which is
// why the message says it may be a few minutes old.
package maildigest

import (
	"database/sql"
	"fmt"
	"log"
	"strings"
	"time"

	"meshdepot/internal/dateformat"
	"meshdepot/internal/mail"
	"meshdepot/internal/quota"
)

// Interval is how often pending mail is collected and sent.
const Interval = 10 * time.Minute

// Queue records one notification for sending. It is a no-op when the member has
// no address, so callers do not have to check first.
func Queue(database *sql.DB, userID int, notificationType, title, body, reference string) {
	var address string
	database.QueryRow("SELECT COALESCE(email, '') FROM users WHERE id = ?", userID).Scan(&address)
	if strings.TrimSpace(address) == "" {
		return
	}
	_, _ = database.Exec(
		"INSERT INTO notification_mail_queue (user_id, type, title, body, reference) VALUES (?, ?, ?, ?, ?)",
		userID, notificationType, title, body, reference)
}

type pending struct {
	id                                  int
	notificationType, title, body, when string
	reference                           string
}

// Send collects everything pending and sends one message per member.
func Send(database *sql.DB, decryptor mail.Decryptor) {
	config, ready := mail.Load(database, decryptor)
	if !ready {
		// Mail is off or incomplete. The rows stay pending rather than being
		// dropped: switching it on should not lose what happened in between, and
		// the staleness check keeps the eventual message honest.
		return
	}

	rows, failure := database.Query(`
		SELECT DISTINCT user_id FROM notification_mail_queue WHERE sent_at IS NULL`)
	if failure != nil {
		return
	}
	var recipients []int
	for rows.Next() {
		var userID int
		if rows.Scan(&userID) == nil {
			recipients = append(recipients, userID)
		}
	}
	rows.Close()

	for _, userID := range recipients {
		sendFor(database, config, userID)
	}
}

func sendFor(database *sql.DB, config mail.Config, userID int) {
	var address string
	database.QueryRow("SELECT COALESCE(email, '') FROM users WHERE id = ?", userID).Scan(&address)

	rows, failure := database.Query(`
		SELECT id, type, title, COALESCE(body, ''), COALESCE(reference, ''), created_at
		FROM notification_mail_queue WHERE user_id = ? AND sent_at IS NULL ORDER BY id`, userID)
	if failure != nil {
		return
	}
	var items []pending
	var allIDs []int
	for rows.Next() {
		var item pending
		if rows.Scan(&item.id, &item.notificationType, &item.title, &item.body, &item.reference, &item.when) == nil {
			allIDs = append(allIDs, item.id)
			items = append(items, item)
		}
	}
	rows.Close()
	if len(items) == 0 {
		return
	}

	// Marked before the send, and for everything collected - including what the
	// staleness check drops. A failed send must not leave rows behind that pile
	// up and go out multiplied on the next run; the event is already in the
	// member's bell either way.
	markSent(database, allIDs)

	var current []pending
	for _, item := range items {
		if stillRelevant(database, userID, item) {
			current = append(current, item)
		}
	}
	if len(current) == 0 || strings.TrimSpace(address) == "" {
		return
	}

	subject, body := compose(current, dateformat.Of(database, userID))
	if failure := mail.Send(config, address, subject, body); failure != nil {
		log.Printf("[maildigest] sending to user %d failed: %v", userID, failure)
	}
}

// stillRelevant reports whether an entry is worth sending now.
//
// Only the cases that can genuinely resolve are checked. A finished download
// stays true forever, and a failure is only recorded once the job has stopped
// retrying - so the two that can go stale are a failure the member re-queued
// successfully, and a storage warning they have since acted on.
func stillRelevant(database *sql.DB, userID int, item pending) bool {
	switch item.notificationType {
	case "download_failed":
		if item.reference == "" {
			return true
		}
		var done int
		database.QueryRow(`
			SELECT COUNT(*) FROM download_queue
			WHERE user_id = ? AND source_url = ? AND status = 'done'`, userID, item.reference).Scan(&done)
		return done == 0
	case "user_storage_80":
		return quota.Of(database, userID).NearlyFull()
	default:
		return true
	}
}

func markSent(database *sql.DB, ids []int) {
	for _, id := range ids {
		_, _ = database.Exec("UPDATE notification_mail_queue SET sent_at = CURRENT_TIMESTAMP WHERE id = ?", id)
	}
}

// compose builds subject and body.
//
// A single entry keeps its own title as the subject, the way an immediate send
// would read. Several get a counted subject instead: "Download failed" repeated
// for a batch of thirty-four says nothing about what arrived.
func compose(items []pending, pattern string) (subject, body string) {
	if len(items) == 1 {
		subject = items[0].title
	} else {
		subject = fmt.Sprintf("MeshDepot: %d notifications", len(items))
	}

	var builder strings.Builder
	for _, item := range items {
		builder.WriteString("• ")
		builder.WriteString(item.title)
		if item.when != "" {
			// In the account's own notation, the same as everywhere in the interface.
			builder.WriteString("  (" + dateformat.Timestamp(item.when, pattern) + ")")
		}
		builder.WriteString("\n")
		if trimmed := strings.TrimSpace(item.body); trimmed != "" {
			for _, line := range strings.Split(trimmed, "\n") {
				builder.WriteString("    " + line + "\n")
			}
		}
		builder.WriteString("\n")
	}
	// Said plainly, because the delay is the one thing about this message that
	// could otherwise mislead: it is a summary of the last few minutes, not a
	// report of this second.
	builder.WriteString("---\n")
	builder.WriteString(fmt.Sprintf(
		"This is a summary and is sent at most every %d minutes, so some of it may already be dealt with.\n"+
			"Entries that had resolved by the time this was sent were left out. The bell in MeshDepot always shows the current state.",
		int(Interval.Minutes())))
	return subject, builder.String()
}
