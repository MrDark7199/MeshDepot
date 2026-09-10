// Package queuestate holds the per-platform pause and block state of the
// download queue in app_settings, so the worker can skip a suspended platform
// and the API can say why it is not downloading.
//
// Two independent reasons suspend a platform: a manual admin pause
// (queue_paused_<platform>), and an automatic block after too many anti-bot hits
// (queue_block_until_<platform>). The automatic one clears itself simply by its
// timestamp passing - no scheduler is involved.
//
// It depends only on database/sql, so worker and api can both import it.
package queuestate

import (
	"database/sql"
	"errors"
	"meshdepot/internal/logx"
	"time"

	"meshdepot/internal/dbutil"
)

// Platforms mirrors the platforms that own a download cooldown; "manual" designs
// never hit a platform and cannot be blocked.
var Platforms = []string{"thingiverse", "printables", "makerworld", "thangs", "cults3d", "myminifactory"}

// storedLayout is RFC3339 in UTC, which JavaScript's Date parses directly.
// parseTime also accepts CURRENT_TIMESTAMP's format, so older values still read.
const storedLayout = time.RFC3339

func pausedKey(platform string) string { return "queue_paused_" + platform }
func untilKey(platform string) string  { return "queue_block_until_" + platform }
func reasonKey(platform string) string { return "queue_block_reason_" + platform }
func sinceKey(platform string) string  { return "queue_block_since_" + platform }

// State is one platform's state, shaped for the JSON the frontend consumes.
type State struct {
	Platform string `json:"platform"`
	Paused   bool   `json:"paused"`
	Blocked  bool   `json:"blocked"`
	// Until is when the automatic block lifts, empty when not blocked.
	Until  string `json:"until,omitempty"`
	Reason string `json:"reason,omitempty"`
	Since  string `json:"since,omitempty"`
}

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
	if failure := database.QueryRow("SELECT value FROM app_settings WHERE key=?", key).Scan(&value); failure != nil && !errors.Is(failure, sql.ErrNoRows) {
		// Reads as "not suspended", so a paused platform would run for this tick.
		// Left that way on purpose - halting every download over one busy moment
		// is worse - but it no longer happens quietly.
		logx.Errorf("[queuestate] %s could not be read: %v", key, failure)
	}
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

// Suspended reports whether the worker must skip this platform, for either
// reason.
func Suspended(database *sql.DB, platform string, now time.Time) bool {
	if readSetting(database, pausedKey(platform)) == "1" {
		return true
	}
	if until, ok := parseTime(readSetting(database, untilKey(platform))); ok && now.Before(until) {
		return true
	}
	return false
}

// Block auto-pauses a platform, recording the triggering reason and start time.
func Block(database *sql.DB, platform string, until time.Time, reason string, now time.Time) {
	writeSetting(database, untilKey(platform), until.UTC().Format(storedLayout))
	writeSetting(database, reasonKey(platform), reason)
	writeSetting(database, sinceKey(platform), now.UTC().Format(storedLayout))
}

func SetPaused(database *sql.DB, platform string, paused bool) {
	value := "0"
	if paused {
		value = "1"
	}
	writeSetting(database, pausedKey(platform), value)
}

// Resume clears both the manual pause and any automatic block - the single "let
// it run again now" action.
func Resume(database *sql.DB, platform string) {
	writeSetting(database, pausedKey(platform), "0")
	writeSetting(database, untilKey(platform), "")
	writeSetting(database, reasonKey(platform), "")
	writeSetting(database, sinceKey(platform), "")
}

// Get reflects whether the stored block is still in the future.
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

// Active returns only the platforms currently paused or blocked, for the header
// alert.
func Active(database *sql.DB, now time.Time) []State {
	active := []State{}
	for _, platform := range Platforms {
		if state := Get(database, platform, now); state.Paused || state.Blocked {
			active = append(active, state)
		}
	}
	return active
}

// All returns every platform's state, for the admin settings page.
func All(database *sql.DB, now time.Time) []State {
	all := make([]State, 0, len(Platforms))
	for _, platform := range Platforms {
		all = append(all, Get(database, platform, now))
	}
	return all
}
