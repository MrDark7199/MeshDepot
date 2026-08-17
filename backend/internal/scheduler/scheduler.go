// Package scheduler replaces docker/cronjob/entrypoint.sh: it watches the
// library_sync_force flag, triggers periodic library syncs and enqueues due
// designs into the sync_queue (auto-sync).
package scheduler

import (
	"database/sql"
	"strconv"
	"time"

	"meshdepot/internal/dbutil"

	"meshdepot/internal/health"
	"meshdepot/internal/safego"
)

// forceFlagInterval is how often the library_sync_force flag is polled.
const forceFlagInterval = 30 * time.Second

// Scheduler bundles the periodic background loops.
type Scheduler struct {
	DB *sql.DB
	// RunLibrarySync triggers the library sync of all accounts (injected).
	RunLibrarySync   func()
	AutoSyncInterval time.Duration
	// Heartbeat records the liveness of both loops for the health endpoint.
	// Wired by main.go; nil elsewhere, which the registry tolerates.
	Heartbeat *health.Registry
}

// New creates a scheduler with the default interval (10 min).
func New(db *sql.DB, runLibrarySync func()) *Scheduler {
	return &Scheduler{DB: db, RunLibrarySync: runLibrarySync, AutoSyncInterval: 10 * time.Minute}
}

// Start starts the loops until stop is closed.
func (scheduler *Scheduler) Start(stop <-chan struct{}) {
	scheduler.Heartbeat.Register(health.SchedulerForce, forceFlagInterval)
	scheduler.Heartbeat.Register(health.SchedulerAuto, scheduler.AutoSyncInterval)
	safego.Go("scheduler.force-flag", func() {
		scheduler.loop(stop, health.SchedulerForce, forceFlagInterval, func() {
			if scheduler.consumeForceFlag() && scheduler.RunLibrarySync != nil {
				// A library sync runs for minutes and blocks this loop for that
				// long, so it is reported as busy rather than as a dead loop.
				endBusy := scheduler.Heartbeat.Working(health.SchedulerForce)
				defer endBusy()
				scheduler.RunLibrarySync()
			}
		})
	})
	safego.Go("scheduler.auto-sync", func() {
		scheduler.loop(stop, health.SchedulerAuto, scheduler.AutoSyncInterval, func() { scheduler.autoSyncEnqueue() })
	})
}

// loop calls task at the given interval until stop is closed and reports every
// tick to the heartbeat registry under the given loop name.
func (scheduler *Scheduler) loop(stop <-chan struct{}, name string, interval time.Duration, task func()) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			scheduler.Heartbeat.Beat(name)
			// Per tick: one bad iteration must not stop the whole schedule.
			safego.Run("scheduler.tick", task)
		}
	}
}

// consumeForceFlag returns true and resets the flag when a forced library sync
// was requested (via /admin/library-sync/run).
func (scheduler *Scheduler) consumeForceFlag() bool {
	var value string
	if failure := scheduler.DB.QueryRow("SELECT value FROM app_settings WHERE key='library_sync_force'").Scan(&value); failure != nil {
		return false
	}
	if value != "1" {
		return false
	}
	dbutil.ExecLogged(scheduler.DB, "UPDATE app_settings SET value='0' WHERE key='library_sync_force'")
	return true
}

// DesignUpdateMinDays is the shortest interval between two update checks of the
// same design. Every check is a full re-download from the source platform, so a
// shorter interval multiplies the requests a platform sees per library - which
// is what gets an account blocked.
const DesignUpdateMinDays = 7

// autoSyncBatch bounds how many designs one tick enqueues, so a library that has
// been idle for weeks does not fill the queue in a single pass.
const autoSyncBatch = 20

// designUpdateSettings reads the server-wide switch and interval floor. Missing
// rows count as enabled with the built-in minimum, which is what the schema
// seeds.
func (scheduler *Scheduler) designUpdateSettings() (enabled bool, minDays int) {
	enabled, minDays = true, DesignUpdateMinDays
	var value string
	if scheduler.DB.QueryRow("SELECT value FROM app_settings WHERE key='design_update_enabled'").Scan(&value) == nil && value == "0" {
		enabled = false
	}
	if scheduler.DB.QueryRow("SELECT value FROM app_settings WHERE key='design_update_min_days'").Scan(&value) == nil {
		if days, failure := strconv.Atoi(value); failure == nil && days > minDays {
			minDays = days
		}
	}
	return enabled, minDays
}

// autoSyncEnqueue enqueues designs whose last update check is older than the
// interval their owner configured, and that are not already queued.
//
// The interval is per design, not per run: a design synced on the 1st comes up
// on the 8th, one synced on the 2nd on the 9th. Members who switched the update
// off (sync_min_age_days IS NULL) are skipped entirely, and nobody can check
// more often than the server-wide floor.
func (scheduler *Scheduler) autoSyncEnqueue() int {
	enabled, minDays := scheduler.designUpdateSettings()
	if !enabled {
		return 0
	}
	rows, failure := scheduler.DB.Query(`
		SELECT d.id, d.user_id FROM designs d
		LEFT JOIN notification_prefs np ON np.user_id = d.user_id
		WHERE d.source_url IS NOT NULL AND d.source_url != ''
		  AND (np.user_id IS NULL OR np.sync_min_age_days IS NOT NULL)
		  AND (d.last_synced_at IS NULL
		       OR d.last_synced_at < datetime('now', '-' || MAX(COALESCE(np.sync_min_age_days, 0), ?) || ' days'))
		  AND NOT EXISTS (SELECT 1 FROM sync_queue sq WHERE sq.design_id = d.id AND sq.status IN ('pending','running'))
		ORDER BY d.last_synced_at IS NOT NULL, d.last_synced_at ASC
		LIMIT ?`, minDays, autoSyncBatch)
	if failure != nil {
		return 0
	}
	type syncJob struct{ designID, userID int }
	var jobs []syncJob
	for rows.Next() {
		var job syncJob
		if rows.Scan(&job.designID, &job.userID) == nil {
			jobs = append(jobs, job)
		}
	}
	rows.Close()
	queued := 0
	for _, job := range jobs {
		if _, failure := scheduler.DB.Exec("INSERT INTO sync_queue (design_id, user_id, status) VALUES (?, ?, 'pending')", job.designID, job.userID); failure == nil {
			queued++
		}
	}
	return queued
}
