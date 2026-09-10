// Package notify creates in-app notifications and, when the user asked for it,
// sends the same thing by e-mail. The two channels are independent:
// notification_prefs holds one column per type for the bell and a second,
// "<type>_email", for the mail.
package notify

import (
	"database/sql"
	"meshdepot/internal/logx"

	"meshdepot/internal/maildigest"
)

var allowed = map[string]bool{
	"sync_update":     true,
	"download_done":   true,
	"download_failed": true,
	"design_shared":   true,
	"storage_80":      true,
	"user_storage_80": true,
}

// User never fails hard: a notification is a side effect, and losing one must not
// take down the work that raised it.
func User(db *sql.DB, userID int, notificationType, title, body string, designID *int) {
	UserWithReference(db, userID, notificationType, title, body, "", designID)
}

// UserWithReference notes what the entry is about - a download's source URL, for
// instance - so the digest can check at send time whether it still holds.
func UserWithReference(db *sql.DB, userID int, notificationType, title, body, reference string, designID *int) {
	if !allowed[notificationType] {
		return
	}
	// Both switches in one read. With no row the bell is on and mail off: an upgrade
	// must not start mailing people who never asked. notificationType comes from the
	// whitelist, so interpolating it into the column names is safe.
	var inApp, byMail sql.NullInt64
	found := db.QueryRow(
		"SELECT "+notificationType+", "+notificationType+"_email FROM notification_prefs WHERE user_id = ? LIMIT 1",
		userID).Scan(&inApp, &byMail) == nil

	wantsInApp := !found || !inApp.Valid || inApp.Int64 != 0
	wantsMail := found && byMail.Valid && byMail.Int64 != 0

	if wantsInApp {
		var designIDArg any
		if designID != nil {
			designIDArg = *designID
		}
		if _, failure := db.Exec(
			"INSERT INTO notifications (user_id, type, title, body, design_id) VALUES (?, ?, ?, ?, ?)",
			userID, notificationType, title, body, designIDArg,
		); failure != nil {
			// Still not fatal - the work that raised this must not fail over a
			// notification - but a bell that stays empty should not be silent
			// about why.
			logx.Errorf("[notify] %s for user %d could not be stored: %v", notificationType, userID, failure)
		}
	}
	if wantsMail {
		// Queued rather than sent: maildigest collects a batch into one message per
		// member and drops what has resolved by the time it goes out.
		maildigest.Queue(db, userID, notificationType, title, body, reference)
	}
}
