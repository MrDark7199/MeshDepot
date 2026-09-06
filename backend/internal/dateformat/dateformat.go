// Package dateformat writes dates in the notation an account has chosen.
//
// The choice is made in the interface and applies everywhere a date is shown,
// which includes the notification e-mails - a member who writes dates as
// 25/11/2026 should not get a mail that says 2026-11-25 12:34:56.
//
// The patterns are the same strings the browser uses (see DATE_FORMATS in
// frontend/src/utils/datetime.ts); this package only has to render them.
package dateformat

import (
	"database/sql"
	"strings"
	"time"
)

// Layouts maps a stored pattern to its Go layout. The empty pattern is the
// default and is absent on purpose: it means "no notation was chosen", and
// callers decide for themselves what to fall back to.
var Layouts = map[string]string{
	"DD.MM.YYYY":  "02.01.2006",
	"DD/MM/YYYY":  "02/01/2006",
	"MM/DD/YYYY":  "01/02/2006",
	"YYYY-MM-DD":  "2006-01-02",
	"D MMM YYYY":  "2 Jan 2006",
	"MMM D, YYYY": "Jan 2, 2006",
}

// Valid reports whether a pattern may be stored. "" is valid and means the
// account follows its display language.
func Valid(pattern string) bool {
	if pattern == "" {
		return true
	}
	_, known := Layouts[pattern]
	return known
}

// Of reads the pattern an account has chosen, "" when it has none.
func Of(database *sql.DB, userID int) string {
	var pattern string
	database.QueryRow("SELECT COALESCE(date_format, '') FROM users WHERE id = ?", userID).Scan(&pattern)
	if !Valid(pattern) {
		return ""
	}
	return pattern
}

// Timestamp rewrites a stored SQL timestamp ("2026-11-25 12:34:56", UTC) in the
// given notation, with the time of day appended.
//
// Anything it cannot parse, and the empty pattern, are returned unchanged: the
// raw value is at least correct, and a timestamp that arrives in an unexpected
// shape is not worth losing over its punctuation.
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
