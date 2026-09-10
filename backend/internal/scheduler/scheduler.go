// Package scheduler watches the library_sync_force flag, triggers periodic
// library syncs and enqueues due designs into the sync_queue.
package scheduler

import (
	"database/sql"
	"strconv"
	"time"

	"meshdepot/internal/dbutil"

	"meshdepot/internal/health"
	"meshdepot/internal/safego"
)

const forceFlagInterval = 30 * time.Second

type Scheduler struct {
	DB               *sql.DB
	RunLibrarySync   func()
	AutoSyncInterval time.Duration
	// SendMailDigest is injected, so the scheduler does not depend on the mail stack.
	SendMailDigest     func()
	MailDigestInterval time.Duration
	// Heartbeat is wired by main.go; nil elsewhere, which the registry tolerates.
	Heartbeat *health.Registry
}

func New(db *sql.DB, runLibrarySync func()) *Scheduler {
	return &Scheduler{DB: db, RunLibrarySync: runLibrarySync, AutoSyncInterval: 10 * time.Minute}
}

func (scheduler *Scheduler) Start(stop <-chan struct{}) {
	scheduler.Heartbeat.Register(health.SchedulerForce, forceFlagInterval)
	scheduler.Heartbeat.Register(health.SchedulerAuto, scheduler.AutoSyncInterval)
	if scheduler.SendMailDigest != nil && scheduler.MailDigestInterval > 0 {
		scheduler.Heartbeat.Register(health.SchedulerMailDigest, scheduler.MailDigestInterval)
	}
	safego.Go("scheduler.force-flag", func() {
		scheduler.loop(stop, health.SchedulerForce, forceFlagInterval, func() {
			if scheduler.consumeForceFlag() && scheduler.RunLibrarySync != nil {
				// A library sync runs for minutes and blocks this loop, so it is reported busy
				// rather than dead.
				endBusy := scheduler.Heartbeat.Working(health.SchedulerForce)
				defer endBusy()
				scheduler.RunLibrarySync()
			}
		})
	})
	safego.Go("scheduler.auto-sync", func() {
		scheduler.loop(stop, health.SchedulerAuto, scheduler.AutoSyncInterval, func() { scheduler.autoSyncEnqueue() })
	})
	// Only when mail is wired at all: a deployment without it should not carry a
	// loop that wakes up to find nothing to do.
	if scheduler.SendMailDigest != nil && scheduler.MailDigestInterval > 0 {
		safego.Go("scheduler.mail-digest", func() {
			scheduler.loop(stop, health.SchedulerMailDigest, scheduler.MailDigestInterval, scheduler.SendMailDigest)
		})
	}
}

// loop calls task at the given interval and reports every tick to the registry.
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
// was requested.
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

// DesignUpdateMinDays is the shortest interval between two update checks of one
// design. Every check is a full re-download, so a shorter interval multiplies the
// requests a platform sees - which is what gets an account blocked.
const DesignUpdateMinDays = 7

// autoSyncBatch keeps a library idle for weeks from filling the queue in one
// pass.
const autoSyncBatch = 20

// designUpdateSettings: missing rows count as enabled with the built-in minimum,
// which is what the schema seeds.
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

// autoSyncEnqueue takes designs whose last check is older than their owner's
// interval and that are not already queued. The interval is per design, not per
// run. Members who switched updates off are skipped, and nobody checks more often
// than the server-wide floor.
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
