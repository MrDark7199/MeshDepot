// Package maildigest collects pending notification e-mails into one message per
// member. Without it, forty queued downloads of which thirty-four fail produce
// thirty-four e-mails, and the reader stops at the third to look for the off
// switch.
//
// The delay also buys something an immediate send cannot have: the message is
// assembled when it goes out, so anything resolved in the meantime is left out -
// which is why it says it may be a few minutes old.
package maildigest

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"meshdepot/internal/dateformat"
	"meshdepot/internal/mail"
	"meshdepot/internal/quota"

	"meshdepot/internal/logx"
)

const Interval = 10 * time.Minute

// Queue is a no-op when the member has no address, so callers need not check.
func Queue(database *sql.DB, userID int, notificationType, title, body, reference string) {
	var address string
	database.QueryRow("SELECT COALESCE(email, '') FROM users WHERE id = ?", userID).Scan(&address)
	if strings.TrimSpace(address) == "" {
		return
	}
	if _, failure := database.Exec(
		"INSERT INTO notification_mail_queue (user_id, type, title, body, reference) VALUES (?, ?, ?, ?, ?)",
		userID, notificationType, title, body, reference); failure != nil {
		logx.Errorf("[maildigest] queueing %s for user %d failed: %v", notificationType, userID, failure)
	}
}

type pending struct {
	id                                  int
	notificationType, title, body, when string
	reference                           string
}

func Send(database *sql.DB, decryptor mail.Decryptor) {
	config, ready := mail.Load(database, decryptor)
	if !ready {
		// Mail is off or incomplete. The rows stay pending rather than being dropped:
		// switching it on should not lose what happened in between.
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

	// Marked before the send, and for everything collected: a failed send must not
	// leave rows that pile up and go out multiplied on the next run. The event is in
	// the member's bell either way.
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
		logx.Errorf("[maildigest] sending to user %d failed: %v", userID, failure)
	}
}

// stillRelevant checks only the cases that can genuinely resolve: a failure the
// member re-queued successfully, and a storage warning they have acted on.
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

// A row that stays unmarked is picked up again on the next run, so the member
// gets the same digest twice. Nothing here can prevent that; it can say it.
func markSent(database *sql.DB, ids []int) {
	for _, id := range ids {
		if _, failure := database.Exec("UPDATE notification_mail_queue SET sent_at = CURRENT_TIMESTAMP WHERE id = ?", id); failure != nil {
			logx.Errorf("[maildigest] entry %d could not be marked as sent - it will go out again: %v", id, failure)
		}
	}
}

// compose keeps a single entry's own title as the subject; several get a counted
// one, since "Download failed" repeated thirty-four times says nothing.
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
			// In the account's own notation, as everywhere in the interface.
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
	// Said plainly, because the delay is the one thing that could mislead: this is a
	// summary of the last few minutes.
	builder.WriteString("---\n")
	builder.WriteString(fmt.Sprintf(
		"This is a summary and is sent at most every %d minutes, so some of it may already be dealt with.\n"+
			"Entries that had resolved by the time this was sent were left out. The bell in MeshDepot always shows the current state.",
		int(Interval.Minutes())))
	return subject, builder.String()
}
