// Package queuestate holds the per-platform pause/block state of the download
// queue in app_settings. It lets the download worker skip a platform whose
// queue is suspended, and lets the API show the admin (and every user) why a
// platform is not downloading right now.
//
// Two independent reasons suspend a platform:
//
//   - a manual pause set by an admin (queue_paused_<platform> = "1"), and
//   - an automatic block after too many anti-bot/rate-limit hits in a row
//     (queue_block_until_<platform> holds an expiry timestamp).
//
// A platform is skipped while either applies. The automatic block clears itself
// simply by its timestamp passing - no scheduler is involved, the worker just
// stops skipping once "until" is in the past.
//
// The package depends only on database/sql (plus the local dbutil helper) so
// both the worker and the api package can import it without an import cycle.
package queuestate

import (
	"database/sql"
	"time"

	"meshdepot/internal/dbutil"
)

// Platforms are the download platforms a pause/block can apply to. It mirrors
// the platforms that own a download cooldown (see settingsSchema in the api
// package) - "manual" designs never hit a platform and cannot be blocked.
var Platforms = []string{"thingiverse", "printables", "makerworld", "thangs", "cults3d", "myminifactory"}

// storedLayout is the timestamp format written to app_settings: RFC3339 in UTC,
// which JavaScript's Date parses directly. parseTime also accepts the
// "YYYY-MM-DD HH:MM:SS" that CURRENT_TIMESTAMP produces, so older or
// hand-edited values still read.
const storedLayout = time.RFC3339

func pausedKey(platform string) string { return "queue_paused_" + platform }
func untilKey(platform string) string  { return "queue_block_until_" + platform }
func reasonKey(platform string) string { return "queue_block_reason_" + platform }
func sinceKey(platform string) string  { return "queue_block_since_" + platform }

// State is the current pause/block state of a single platform, shaped for the
// JSON the frontend consumes.
type State struct {
	Platform string `json:"platform"`
	// Paused is the manual admin switch.
	Paused bool `json:"paused"`
	// Blocked is true while an automatic block is still in effect.
	Blocked bool `json:"blocked"`
	// Until is the RFC3339 moment the automatic block lifts (empty when not
	// blocked).
	Until string `json:"until,omitempty"`
	// Reason is the error key that triggered the automatic block.
	Reason string `json:"reason,omitempty"`
	// Since is the RFC3339 moment the automatic block started.
	Since string `json:"since,omitempty"`
}

// IsPlatform reports whether name is a known download platform.
func IsPlatform(name string) bool {
	for _, platform := range Platforms {
		if platform == name {
			return true
		}
	}
	return false
}

func readSetting(database *sql.DB, key string) string {
	var value string
	database.QueryRow("SELECT value FROM app_settings WHERE key=?", key).Scan(&value)
	return value
}

func writeSetting(database *sql.DB, key, value string) {
	dbutil.ExecLogged(database,
		"INSERT INTO app_settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value",
		key, value)
}

func parseTime(value string) (time.Time, bool) {
	if value == "" {
		return time.Time{}, false
	}
	if parsed, failure := time.Parse(storedLayout, value); failure == nil {
		return parsed, true
	}
	if parsed, failure := time.Parse("2006-01-02 15:04:05", value); failure == nil {
		return parsed, true
	}
	return time.Time{}, false
}

// Suspended reports whether the worker must skip this platform at instant now,
// for either reason (manual pause or an active automatic block).
func Suspended(database *sql.DB, platform string, now time.Time) bool {
	if readSetting(database, pausedKey(platform)) == "1" {
		return true
	}
	if until, ok := parseTime(readSetting(database, untilKey(platform))); ok && now.Before(until) {
		return true
	}
	return false
}

// Block auto-pauses a platform until the given moment, recording the triggering
// reason and the start time for display.
func Block(database *sql.DB, platform string, until time.Time, reason string, now time.Time) {
	writeSetting(database, untilKey(platform), until.UTC().Format(storedLayout))
	writeSetting(database, reasonKey(platform), reason)
	writeSetting(database, sinceKey(platform), now.UTC().Format(storedLayout))
}

// SetPaused sets or clears the manual pause switch of a platform.
func SetPaused(database *sql.DB, platform string, paused bool) {
	value := "0"
	if paused {
		value = "1"
	}
	writeSetting(database, pausedKey(platform), value)
}

// Resume clears both the manual pause and any automatic block of a platform -
// the single "let it run again now" action for the admin.
func Resume(database *sql.DB, platform string) {
	writeSetting(database, pausedKey(platform), "0")
	writeSetting(database, untilKey(platform), "")
	writeSetting(database, reasonKey(platform), "")
	writeSetting(database, sinceKey(platform), "")
}

// Get returns the state of a single platform as of instant now. Blocked and its
// detail fields reflect whether the stored block is still in the future.
func Get(database *sql.DB, platform string, now time.Time) State {
	state := State{Platform: platform, Paused: readSetting(database, pausedKey(platform)) == "1"}
	if until, ok := parseTime(readSetting(database, untilKey(platform))); ok && now.Before(until) {
		state.Blocked = true
		state.Until = until.UTC().Format(storedLayout)
		state.Reason = readSetting(database, reasonKey(platform))
		if since, ok := parseTime(readSetting(database, sinceKey(platform))); ok {
			state.Since = since.UTC().Format(storedLayout)
		}
	}
	return state
}

// Active returns only the platforms that are currently paused or blocked - what
// the header alert needs (an empty slice means "nothing to warn about").
func Active(database *sql.DB, now time.Time) []State {
	active := []State{}
	for _, platform := range Platforms {
		if state := Get(database, platform, now); state.Paused || state.Blocked {
			active = append(active, state)
		}
	}
	return active
}

// All returns the state of every platform - what the admin settings page shows.
func All(database *sql.DB, now time.Time) []State {
	all := make([]State, 0, len(Platforms))
	for _, platform := range Platforms {
		all = append(all, Get(database, platform, now))
	}
	return all
}
