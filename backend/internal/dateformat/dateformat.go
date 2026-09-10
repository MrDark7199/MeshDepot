// Package dateformat writes dates in the notation an account has chosen, which
// includes the notification e-mails: a member who writes 25/11/2026 should not get
// a mail saying 2026-11-25 12:34:56.
//
// The patterns are the same strings the browser uses (DATE_FORMATS in
// frontend/src/utils/datetime.ts).
package dateformat

import (
	"database/sql"
	"strings"
	"time"
)

// Layouts maps a stored pattern to its Go layout. The empty pattern is absent on
// purpose: it means no notation was chosen, and callers pick their own fallback.
var Layouts = map[string]string{
	"DD.MM.YYYY":  "02.01.2006",
	"DD/MM/YYYY":  "02/01/2006",
	"MM/DD/YYYY":  "01/02/2006",
	"YYYY-MM-DD":  "2006-01-02",
	"D MMM YYYY":  "2 Jan 2006",
	"MMM D, YYYY": "Jan 2, 2006",
}

// Valid: "" is valid and means the account follows its display language.
func Valid(pattern string) bool {
	if pattern == "" {
		return true
	}
	_, known := Layouts[pattern]
	return known
}

func Of(database *sql.DB, userID int) string {
	var pattern string
	database.QueryRow("SELECT COALESCE(date_format, '') FROM users WHERE id = ?", userID).Scan(&pattern)
	if !Valid(pattern) {
		return ""
	}
	return pattern
}

// Timestamp rewrites a stored SQL timestamp (UTC) in the given notation, time of
// day appended. Anything unparsable, and the empty pattern, are returned
// unchanged - the raw value is at least correct.
func Timestamp(stored, pattern string) string {
	layout, known := Layouts[pattern]
	if !known {
		return stored
	}
	when, failure := time.Parse("2006-01-02 15:04:05", strings.TrimSpace(stored))
	if failure != nil {
		return stored
	}
	return when.Format(layout + " 15:04")
}
