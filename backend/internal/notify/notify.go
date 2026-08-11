// Package notify creates in-app notifications, provided the type is enabled in
// the user's notification_prefs.
package notify

import "database/sql"

// allowed lists the permitted notification types (= column names in
// notification_prefs).
var allowed = map[string]bool{
	"sync_update":     true,
	"download_done":   true,
	"download_failed": true,
	"design_shared":   true,
	"storage_80":      true,
}

// User creates a notification if the type is allowed and not disabled in the
// preferences. It never fails hard.
func User(db *sql.DB, userID int, notificationType, title, body string, designID *int) {
	if !allowed[notificationType] {
		return
	}
	// Check the preference - default = enabled when no row exists.
	// notificationType comes from the whitelist, so the column interpolation is safe.
	var preference sql.NullInt64
	failure := db.QueryRow("SELECT "+notificationType+" FROM notification_prefs WHERE user_id = ? LIMIT 1", userID).Scan(&preference)
	if failure == nil && preference.Valid && preference.Int64 == 0 {
		return
	}
	var designIDArg any
	if designID != nil {
		designIDArg = *designID
	}
	_, _ = db.Exec(
		"INSERT INTO notifications (user_id, type, title, body, design_id) VALUES (?, ?, ?, ?, ?)",
		userID, notificationType, title, body, designIDArg,
	)
}
