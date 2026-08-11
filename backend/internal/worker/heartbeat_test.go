package worker

import (
	"testing"
	"time"

	"meshdepot/internal/health"
)

// waitFor polls until the condition holds; the loops run in their own
// goroutines, so the moment a state changes is not observable directly.
func waitFor(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for: %s", what)
}

// The health endpoint may only report the worker as running if the loop is
// really there, so Start has to announce it.
func TestDownloadWorkerRegistersItsHeartbeat(t *testing.T) {
	heartbeats := health.New()
	downloadWorker := NewDownloadWorker(newTestDB(t), func(Job) (int, error) { return 0, nil }, func(string) int { return 0 })
	downloadWorker.Heartbeat = heartbeats
	stop := make(chan struct{})

	downloadWorker.Start(stop)
	defer func() { close(stop); downloadWorker.Wait(30 * time.Second) }()

	status, present := heartbeats.Get(health.DownloadWorker)
	if !present {
		t.Fatal("the started worker has no heartbeat")
	}
	if status.Stale || status.Busy {
		t.Fatalf("a worker that has just started reports %+v", status)
	}
}

// A running download blocks the loop for up to ten minutes. Without the busy
// mark the health endpoint would read that silence as a dead loop.
func TestDownloadWorkerIsBusyWhileAJobRuns(t *testing.T) {
	database := newTestDB(t)
	enqueue(t, database, "printables")
	heartbeats := health.New()
	inJob := make(chan struct{})
	release := make(chan struct{})

	downloadWorker := NewDownloadWorker(database, func(Job) (int, error) {
		close(inJob)
		<-release
		return 42, nil
	}, func(string) int { return 0 })
	downloadWorker.Heartbeat = heartbeats
	stop := make(chan struct{})
	downloadWorker.Start(stop)
	defer func() { close(stop); downloadWorker.Wait(30 * time.Second) }()

	select {
	case <-inJob:
	case <-time.After(30 * time.Second):
		t.Fatal("the loop did not pick up the job")
	}
	if status, _ := heartbeats.Get(health.DownloadWorker); !status.Busy {
		t.Fatalf("the worker does not report the running job: %+v", status)
	}

	close(release)
	waitFor(t, "the end of the busy phase", func() bool {
		status, _ := heartbeats.Get(health.DownloadWorker)
		return !status.Busy && !status.Stale
	})
}

// Without a registry the worker must simply keep working - that is the state in
// every other test.
func TestDownloadWorkerWithoutHeartbeat(t *testing.T) {
	database := newTestDB(t)
	id := enqueue(t, database, "printables")
	downloadWorker := NewDownloadWorker(database, func(Job) (int, error) { return 42, nil }, func(string) int { return 0 })

	downloadWorker.RunOnce()

	if status, _, _ := statusOf(t, database, id); status != "done" {
		t.Fatalf("the job ended as %s", status)
	}
}

func TestSyncWorkerRegistersItsHeartbeat(t *testing.T) {
	heartbeats := health.New()
	syncWorker := NewSyncWorker(newTestDB(t), func(SyncJob) error { return nil })
	syncWorker.Heartbeat = heartbeats
	stop := make(chan struct{})

	syncWorker.Start(stop)
	defer func() { close(stop); syncWorker.Wait(30 * time.Second) }()

	status, present := heartbeats.Get(health.SyncWorker)
	if !present || status.Stale {
		t.Fatalf("the started sync worker reports %+v (present=%v)", status, present)
	}
}

func TestSyncWorkerIsBusyWhileAJobRuns(t *testing.T) {
	database := newTestDB(t)
	enqueueSync(t, database, 1)
	heartbeats := health.New()
	inJob := make(chan struct{})
	release := make(chan struct{})

	syncWorker := NewSyncWorker(database, func(SyncJob) error {
		close(inJob)
		<-release
		return nil
	})
	syncWorker.Heartbeat = heartbeats
	stop := make(chan struct{})
	syncWorker.Start(stop)
	defer func() { close(stop); syncWorker.Wait(30 * time.Second) }()

	select {
	case <-inJob:
	case <-time.After(30 * time.Second):
		t.Fatal("the loop did not pick up the job")
	}
	if status, _ := heartbeats.Get(health.SyncWorker); !status.Busy {
		t.Fatalf("the sync worker does not report the running job: %+v", status)
	}

	close(release)
	waitFor(t, "the end of the busy phase", func() bool {
		status, _ := heartbeats.Get(health.SyncWorker)
		return !status.Busy && !status.Stale
	})
}
