package scheduler

import (
	"database/sql"
	"sync/atomic"
	"testing"
	"time"

	"meshdepot/internal/health"
	"meshdepot/internal/publicid"
)

// insertSyncableDesign creates a design the auto-sync would pick up: it has a
// source URL and has never been checked.
func insertSyncableDesign(t *testing.T, database *sql.DB, name string) int {
	t.Helper()
	result, failure := database.Exec(
		"INSERT INTO designs (user_id, public_id, name, source_url, source_platform) VALUES (1, ?, ?, ?, 'thingiverse')",
		publicid.New(), name, "https://example.org/thing/"+name,
	)
	if failure != nil {
		t.Fatalf("insert design: %v", failure)
	}
	id, _ := result.LastInsertId()
	return int(id)
}

func TestNewUsesTheDefaultInterval(t *testing.T) {
	database := newDB(t)
	scheduler := New(database, nil)

	if scheduler.AutoSyncInterval != 10*time.Minute {
		t.Fatalf("unexpected interval %s", scheduler.AutoSyncInterval)
	}
	if scheduler.DB != database {
		t.Fatal("the database was not handed over")
	}
}

// Without the row there is nothing to consume - a fresh installation must not
// trigger a library sync on every tick.
func TestConsumeForceFlagWithoutTheSetting(t *testing.T) {
	database := newDB(t)
	database.Exec("DELETE FROM app_settings WHERE key='library_sync_force'")

	if New(database, nil).consumeForceFlag() {
		t.Fatal("a missing setting was read as a force request")
	}
}

func TestConsumeForceFlagOnBrokenDatabase(t *testing.T) {
	database := newDB(t)
	database.Close()

	if New(database, nil).consumeForceFlag() {
		t.Fatal("a closed database was read as a force request")
	}
}

// Any value other than "1" is off; the flag must not be truthy by accident.
func TestConsumeForceFlagIgnoresOtherValues(t *testing.T) {
	database := newDB(t)
	scheduler := New(database, nil)

	for _, value := range []string{"0", "", "true", "yes", "2"} {
		database.Exec("UPDATE app_settings SET value=? WHERE key='library_sync_force'", value)
		if scheduler.consumeForceFlag() {
			t.Fatalf("the value %q triggered a library sync", value)
		}
	}
}

// A library idle for weeks must not fill the queue in one pass.
func TestAutoSyncEnqueueStopsAtTheBatchLimit(t *testing.T) {
	database := newDB(t)
	scheduler := New(database, nil)
	for index := 0; index < autoSyncBatch+5; index++ {
		insertSyncableDesign(t, database, string(rune('a'+index)))
	}

	if enqueued := scheduler.autoSyncEnqueue(); enqueued != autoSyncBatch {
		t.Fatalf("expected %d enqueued, got %d", autoSyncBatch, enqueued)
	}

	var queued int
	database.QueryRow("SELECT COUNT(*) FROM sync_queue").Scan(&queued)
	if queued != autoSyncBatch {
		t.Fatalf("sync_queue holds %d rows", queued)
	}
}

// A design checked within autoSyncAge is not due yet.
func TestAutoSyncEnqueueSkipsRecentlySyncedDesigns(t *testing.T) {
	database := newDB(t)
	scheduler := New(database, nil)
	recent := insertSyncableDesign(t, database, "recent")
	overdue := insertSyncableDesign(t, database, "overdue")
	database.Exec("UPDATE designs SET last_synced_at = datetime('now', '-1 day') WHERE id = ?", recent)
	database.Exec("UPDATE designs SET last_synced_at = datetime('now', '-30 days') WHERE id = ?", overdue)

	if enqueued := scheduler.autoSyncEnqueue(); enqueued != 1 {
		t.Fatalf("expected 1 enqueued, got %d", enqueued)
	}

	var designID int
	database.QueryRow("SELECT design_id FROM sync_queue").Scan(&designID)
	if designID != overdue {
		t.Fatalf("the wrong design was enqueued: %d", designID)
	}
}

// A design that is already running must not be queued a second time.
func TestAutoSyncEnqueueSkipsRunningJobs(t *testing.T) {
	database := newDB(t)
	scheduler := New(database, nil)
	designID := insertSyncableDesign(t, database, "running")
	database.Exec("INSERT INTO sync_queue (design_id, user_id, status) VALUES (?, 1, 'running')", designID)

	if enqueued := scheduler.autoSyncEnqueue(); enqueued != 0 {
		t.Fatalf("a running design was enqueued again: %d", enqueued)
	}
}

// A finished job does not block the next round.
func TestAutoSyncEnqueueRequeuesAfterAFinishedJob(t *testing.T) {
	database := newDB(t)
	scheduler := New(database, nil)
	designID := insertSyncableDesign(t, database, "done")
	database.Exec("INSERT INTO sync_queue (design_id, user_id, status) VALUES (?, 1, 'done')", designID)

	if enqueued := scheduler.autoSyncEnqueue(); enqueued != 1 {
		t.Fatalf("expected 1 enqueued, got %d", enqueued)
	}
}

// Designs that were never checked come first, otherwise a new import waits
// behind everything that was ever synced.
func TestAutoSyncEnqueuePrefersNeverSyncedDesigns(t *testing.T) {
	database := newDB(t)
	scheduler := New(database, nil)
	synced := insertSyncableDesign(t, database, "synced")
	database.Exec("UPDATE designs SET last_synced_at = datetime('now', '-30 days') WHERE id = ?", synced)
	neverSynced := insertSyncableDesign(t, database, "new")

	scheduler.autoSyncEnqueue()

	var firstDesignID int
	database.QueryRow("SELECT design_id FROM sync_queue ORDER BY id ASC LIMIT 1").Scan(&firstDesignID)
	if firstDesignID != neverSynced {
		t.Fatalf("the never-synced design is not first: %d", firstDesignID)
	}
}

func TestAutoSyncEnqueueOnBrokenDatabase(t *testing.T) {
	database := newDB(t)
	database.Close()

	if enqueued := New(database, nil).autoSyncEnqueue(); enqueued != 0 {
		t.Fatalf("a closed database reported %d enqueued designs", enqueued)
	}
}

// Both loops must announce themselves, otherwise the health endpoint reports a
// running scheduler as not started.
func TestStartRegistersBothLoops(t *testing.T) {
	scheduler := New(newDB(t), nil)
	scheduler.AutoSyncInterval = 5 * time.Millisecond
	heartbeats := health.New()
	scheduler.Heartbeat = heartbeats
	stop := make(chan struct{})
	defer close(stop)

	scheduler.Start(stop)

	for _, name := range []string{health.SchedulerForce, health.SchedulerAuto} {
		if _, present := heartbeats.Get(name); !present {
			t.Fatalf("the loop %q did not register", name)
		}
	}
}

// Registering alone would keep a dead loop looking healthy forever; every tick
// has to renew the heartbeat.
func TestLoopBeatsOnEveryTick(t *testing.T) {
	scheduler := New(newDB(t), nil)
	heartbeats := health.New()
	scheduler.Heartbeat = heartbeats
	heartbeats.Register(health.SchedulerAuto, time.Second)
	stop := make(chan struct{})
	defer close(stop)

	go scheduler.loop(stop, health.SchedulerAuto, 5*time.Millisecond, func() {})

	// Long enough that the registration alone would be old by now: only a tick
	// can make the heartbeat young again.
	time.Sleep(300 * time.Millisecond)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if status, _ := heartbeats.Get(health.SchedulerAuto); status.Age < 100*time.Millisecond {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("the heartbeat was not renewed by the ticks")
}

func TestLoopRunsTheTaskUntilStop(t *testing.T) {
	scheduler := New(newDB(t), nil)
	ticks := make(chan struct{}, 8)
	stop := make(chan struct{})
	finished := make(chan struct{})

	go func() {
		scheduler.loop(stop, health.SchedulerAuto, 5*time.Millisecond, func() { ticks <- struct{}{} })
		close(finished)
	}()

	for round := 0; round < 3; round++ {
		select {
		case <-ticks:
		case <-time.After(5 * time.Second):
			t.Fatalf("the loop stopped after %d ticks", round)
		}
	}

	close(stop)
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("the loop did not leave after stop")
	}
}

// A panicking task costs one tick, not the schedule.
func TestLoopSurvivesAPanickingTask(t *testing.T) {
	scheduler := New(newDB(t), nil)
	var calls atomic.Int32
	stop := make(chan struct{})
	defer close(stop)

	go scheduler.loop(stop, health.SchedulerAuto, 5*time.Millisecond, func() {
		calls.Add(1)
		panic("bad tick")
	})

	deadline := time.Now().Add(5 * time.Second)
	for calls.Load() < 3 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if calls.Load() < 3 {
		t.Fatalf("the loop stopped after %d panicking ticks", calls.Load())
	}
}

// Start runs both loops; the force flag has the shorter interval, so it is the
// one that has to answer within the test budget.
func TestStartConsumesTheForceFlag(t *testing.T) {
	database := newDB(t)
	triggered := make(chan struct{}, 1)
	scheduler := New(database, func() { triggered <- struct{}{} })
	scheduler.AutoSyncInterval = 10 * time.Millisecond
	database.Exec("UPDATE app_settings SET value='1' WHERE key='library_sync_force'")

	stop := make(chan struct{})
	defer close(stop)
	scheduler.Start(stop)

	select {
	case <-triggered:
	case <-time.After(60 * time.Second):
		t.Fatal("the force flag was not consumed")
	}

	var value string
	database.QueryRow("SELECT value FROM app_settings WHERE key='library_sync_force'").Scan(&value)
	if value != "0" {
		t.Fatalf("the flag was not reset: %q", value)
	}
}

// Without a library sync the force loop must not fall over.
func TestStartWithoutALibrarySync(t *testing.T) {
	database := newDB(t)
	scheduler := New(database, nil)
	scheduler.AutoSyncInterval = 5 * time.Millisecond
	insertSyncableDesign(t, database, "auto")

	stop := make(chan struct{})
	defer close(stop)
	scheduler.Start(stop)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var queued int
		database.QueryRow("SELECT COUNT(*) FROM sync_queue").Scan(&queued)
		if queued == 1 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("the auto-sync loop did not enqueue the design")
}
