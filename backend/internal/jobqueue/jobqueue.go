// Package jobqueue puts work into the one queue that fetches from platforms:
// a new design from a URL (an import) and the update check of a design already
// in the library (an update).
//
// One table for both, because both knock on the same door. A platform's
// cooldown, its anti-bot block and its one-job-at-a-time rule only mean
// something when every request to it passes through the same runner; with two
// queues an import and an update of the same platform ran side by side, and
// only one of them knew about the captcha.
//
// Enqueueing lives here rather than with each producer so the duplicate check
// is the same everywhere - five places used to write their own.
package jobqueue

import (
	"database/sql"
	"errors"

	"meshdepot/internal/dbutil"
)

const (
	KindImport = "import"
	KindUpdate = "update"
)

// ErrUnknown is returned when the duplicate check could not be answered. Nothing
// is queued then: "unknown" must not turn into a second job for the same thing.
var ErrUnknown = errors.New("job queue: duplicate check failed")

// EnqueueImport queues a new design from a URL, unless the same URL is already
// waiting or running for that user. Returns the job id, or 0 when an identical
// job was already there.
func EnqueueImport(database *sql.DB, userID int, sourceURL, platform string) (int, error) {
	found, known := dbutil.Exists(database,
		"SELECT id FROM job_queue WHERE kind = 'import' AND user_id = ? AND source_url = ? AND status IN ('pending','running') LIMIT 1",
		userID, sourceURL)
	if !known {
		return 0, ErrUnknown
	}
	if found {
		return 0, nil
	}
	result, failure := database.Exec(
		"INSERT INTO job_queue (kind, user_id, source_url, platform, status) VALUES ('import', ?, ?, ?, 'pending')",
		userID, sourceURL, platform)
	if failure != nil {
		return 0, failure
	}
	id, _ := result.LastInsertId()
	return int(id), nil
}

// EnqueueUpdate queues an update check for a design, unless one is already
// waiting or running. Automatic marks a check the scheduler asked for: anything
// a person asked for is claimed before it.
//
// The design's own source decides the platform, so the check queues behind the
// platform's other work and shares its cooldown and its block.
func EnqueueUpdate(database *sql.DB, userID, designID int, automatic bool) (bool, error) {
	found, known := dbutil.Exists(database,
		"SELECT id FROM job_queue WHERE kind = 'update' AND design_id = ? AND status IN ('pending','running') LIMIT 1",
		designID)
	if !known {
		return false, ErrUnknown
	}
	if found {
		return false, nil
	}
	automaticFlag := 0
	if automatic {
		automaticFlag = 1
	}
	result, failure := database.Exec(`
		INSERT INTO job_queue (kind, user_id, design_id, source_url, platform, automatic, status)
		SELECT 'update', ?, id, source_url, COALESCE(NULLIF(source_platform, ''), 'manual'), ?, 'pending'
		FROM designs WHERE id = ? AND user_id = ? AND COALESCE(source_url, '') != ''`,
		userID, automaticFlag, designID, userID)
	if failure != nil {
		return false, failure
	}
	affected, _ := result.RowsAffected()
	return affected > 0, nil
}
