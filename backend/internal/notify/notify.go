// Package notify creates in-app notifications and, when the user asked for it,
// sends the same thing by e-mail.
//
// The two channels are independent: a type can go to the bell, to the inbox, to
// both, or nowhere. notification_prefs holds one column per type for the bell
// and a second, "<type>_email", for the mail.
package notify

import (
	"database/sql"

	"meshdepot/internal/maildigest"
)

// allowed lists the permitted notification types (= column names in
// notification_prefs).
var allowed = map[string]bool{
	"sync_update":     true,
	"download_done":   true,
	"download_failed": true,
	"design_shared":   true,
	"storage_80":      true,
	"user_storage_80": true,
}

// User creates a notification if the type is allowed and not disabled in the
// preferences, and sends it by e-mail when that is switched on as well. It
// never fails hard: a notification is a side effect, and losing one must not
// take down the work that raised it.
func User(db *sql.DB, userID int, notificationType, title, body string, designID *int) {
	UserWithReference(db, userID, notificationType, title, body, "", designID)
}

// UserWithReference is User with a note of what the entry is about, so the
// digest can check at send time whether it still holds - the source URL of a
// download, for instance.
func UserWithReference(db *sql.DB, userID int, notificationType, title, body, reference string, designID *int) {
	if !allowed[notificationType] {
		return
	}
	// Both switches in one read. Defaults when no row exists: the bell is on,
	// mail is off - an upgrade must not start mailing people who never asked for
	// it. notificationType comes from the whitelist, so interpolating it into the
	// column names is safe.
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
		_, _ = db.Exec(
			"INSERT INTO notifications (user_id, type, title, body, design_id) VALUES (?, ?, ?, ?, ?)",
			userID, notificationType, title, body, designIDArg,
		)
	}
	if wantsMail {
		// Queued rather than sent: a batch of forty downloads would otherwise be
		// forty separate e-mails. maildigest collects them into one message per
		// member and drops what has resolved by the time it goes out.
		maildigest.Queue(db, userID, notificationType, title, body, reference)
	}
}
