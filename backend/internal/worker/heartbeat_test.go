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
func TestQueueWorkerRegistersItsHeartbeat(t *testing.T) {
	heartbeats := health.New()
	queueWorker := NewQueueWorker(newTestDB(t), func(Job) (int, error) { return 0, nil }, func(string) int { return 0 })
	queueWorker.Heartbeat = heartbeats
	stop := make(chan struct{})

	queueWorker.Start(stop)
	defer func() { close(stop); queueWorker.Wait(30 * time.Second) }()

	status, present := heartbeats.Get(health.QueueWorker)
	if !present {
		t.Fatal("the started worker has no heartbeat")
	}
	if status.Stale || status.Busy {
		t.Fatalf("a worker that has just started reports %+v", status)
	}
}

// A running download blocks the loop for up to ten minutes. Without the busy
// mark the health endpoint would read that silence as a dead loop.
func TestQueueWorkerIsBusyWhileAJobRuns(t *testing.T) {
	database := newTestDB(t)
	enqueue(t, database, "printables")
	heartbeats := health.New()
	inJob := make(chan struct{})
	release := make(chan struct{})

	queueWorker := NewQueueWorker(database, func(Job) (int, error) {
		close(inJob)
		<-release
		return 42, nil
	}, func(string) int { return 0 })
	queueWorker.Heartbeat = heartbeats
	stop := make(chan struct{})
	queueWorker.Start(stop)
	defer func() { close(stop); queueWorker.Wait(30 * time.Second) }()

	select {
	case <-inJob:
	case <-time.After(30 * time.Second):
		t.Fatal("the loop did not pick up the job")
	}
	if status, _ := heartbeats.Get(health.QueueWorker); !status.Busy {
		t.Fatalf("the worker does not report the running job: %+v", status)
	}

	close(release)
	waitFor(t, "the end of the busy phase", func() bool {
		status, _ := heartbeats.Get(health.QueueWorker)
		return !status.Busy && !status.Stale
	})
}

// Issue #8: each platform has its own runner, so a slow download on one platform
// must not hold up another. Here thingiverse is stuck mid-download while
// printables still has to finish - which it cannot if a single loop processes
// one job at a time.
func TestPlatformsDownloadInParallel(t *testing.T) {
	database := newTestDB(t)
	enqueue(t, database, "thingiverse")
	enqueue(t, database, "printables")

	thingiverseInJob := make(chan struct{})
	releaseThingiverse := make(chan struct{})
	printablesDone := make(chan struct{})

	queueWorker := NewQueueWorker(database, func(job Job) (int, error) {
		switch job.Platform {
		case "thingiverse":
			close(thingiverseInJob)
			<-releaseThingiverse
			return 1, nil
		case "printables":
			close(printablesDone)
			return 2, nil
		}
		return 0, nil
	}, func(string) int { return 0 })
	queueWorker.Heartbeat = health.New()
	stop := make(chan struct{})
	queueWorker.Start(stop)
	defer func() { close(stop); close(releaseThingiverse); queueWorker.Wait(30 * time.Second) }()

	select {
	case <-thingiverseInJob:
	case <-time.After(30 * time.Second):
		t.Fatal("the thingiverse job never started")
	}
	select {
	case <-printablesDone:
	case <-time.After(30 * time.Second):
		t.Fatal("printables was blocked behind the stuck thingiverse download (issue #8)")
	}
}

// Without a registry the worker must simply keep working - that is the state in
// every other test.
func TestQueueWorkerWithoutHeartbeat(t *testing.T) {
	database := newTestDB(t)
	id := enqueue(t, database, "printables")
	queueWorker := NewQueueWorker(database, func(Job) (int, error) { return 42, nil }, func(string) int { return 0 })

	queueWorker.RunOnce()

	if status, _, _ := statusOf(t, database, id); status != "done" {
		t.Fatalf("the job ended as %s", status)
	}
}
