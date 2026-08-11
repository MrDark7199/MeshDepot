package health

import (
	"sync"
	"testing"
	"time"
)

// clocked returns a registry whose clock the test moves by hand - the whole
// point of the package is what happens after minutes of silence, and no test
// should wait for them.
func clocked() (*Registry, func(time.Duration)) {
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	var mutex sync.Mutex
	registry := New()
	registry.Now = func() time.Time {
		mutex.Lock()
		defer mutex.Unlock()
		return now
	}
	return registry, func(step time.Duration) {
		mutex.Lock()
		defer mutex.Unlock()
		now = now.Add(step)
	}
}

func TestUnregisteredLoopIsUnknown(t *testing.T) {
	registry := New()

	if _, present := registry.Get(DownloadWorker); present {
		t.Fatal("a loop that was never started reports a status")
	}
}

// A nil registry is what every component sees in the unit tests; it must stay
// silent instead of panicking.
func TestNilRegistryTolerated(t *testing.T) {
	var registry *Registry

	registry.Register(DownloadWorker, time.Second)
	registry.Beat(DownloadWorker)
	registry.Working(DownloadWorker)()
	if _, present := registry.Get(DownloadWorker); present {
		t.Fatal("the nil registry answered with a status")
	}
	if _, present := registry.Worst(DownloadWorker); present {
		t.Fatal("the nil registry answered a Worst query")
	}
}

// Registering counts as the first heartbeat: a loop that has just started must
// not be dead before its first tick.
func TestRegisterCountsAsHeartbeat(t *testing.T) {
	registry, _ := clocked()

	registry.Register(SyncWorker, 5*time.Second)

	status, present := registry.Get(SyncWorker)
	if !present || status.Stale || status.Busy {
		t.Fatalf("a freshly registered loop reports %+v", status)
	}
	if status.Interval != 5*time.Second {
		t.Fatalf("the interval was not kept: %s", status.Interval)
	}
}

func TestLoopGoesStaleAndRecovers(t *testing.T) {
	registry, advance := clocked()
	registry.Register(SyncWorker, 5*time.Second)

	// Below the tolerance floor nothing may happen - the fast loops tick every
	// few seconds and a loaded container delays them.
	advance(20 * time.Second)
	if status, _ := registry.Get(SyncWorker); status.Stale {
		t.Fatalf("20 s of silence already count as dead: %+v", status)
	}

	advance(time.Minute)
	status, _ := registry.Get(SyncWorker)
	if !status.Stale {
		t.Fatalf("80 s of silence do not count as dead: %+v", status)
	}
	if status.Age != 80*time.Second {
		t.Fatalf("the reported age is %s", status.Age)
	}

	registry.Beat(SyncWorker)
	if status, _ := registry.Get(SyncWorker); status.Stale {
		t.Fatalf("the loop stays dead after a tick: %+v", status)
	}
}

// The download worker blocks in a job for up to ten minutes; without this its
// tile would go red on every larger download.
func TestBusyLoopIsNotStale(t *testing.T) {
	registry, advance := clocked()
	registry.Register(DownloadWorker, 2*time.Second)

	endJob := registry.Working(DownloadWorker)
	advance(9 * time.Minute)

	status, _ := registry.Get(DownloadWorker)
	if !status.Busy || status.Stale {
		t.Fatalf("a running job reports %+v", status)
	}
	if status.Age != 9*time.Minute {
		t.Fatalf("the busy phase is %s long", status.Age)
	}

	// The end of the job is a sign of life, so the loop must not be stale from
	// the silence during it.
	endJob()
	if status, _ := registry.Get(DownloadWorker); status.Busy || status.Stale {
		t.Fatalf("after the job the loop reports %+v", status)
	}
}

// Releasing twice must not end a second, still running job.
func TestWorkingReleaseIsIdempotent(t *testing.T) {
	registry, _ := clocked()
	registry.Register(DownloadWorker, 2*time.Second)

	first := registry.Working(DownloadWorker)
	second := registry.Working(DownloadWorker)
	first()
	first()

	if status, _ := registry.Get(DownloadWorker); !status.Busy {
		t.Fatalf("the second job was ended along with the first: %+v", status)
	}
	second()
	if status, _ := registry.Get(DownloadWorker); status.Busy {
		t.Fatalf("the loop stays busy after the last release: %+v", status)
	}
}

func TestWorstPicksTheAlarmingLoop(t *testing.T) {
	registry, advance := clocked()
	registry.Register(SchedulerForce, 30*time.Second)
	registry.Register(SchedulerAuto, 10*time.Minute)

	// Both healthy: the longer silence is reported, and the slow loop is allowed
	// to have it.
	advance(90 * time.Second)
	registry.Beat(SchedulerForce)
	status, present := registry.Worst(SchedulerForce, SchedulerAuto)
	if !present || status.Stale {
		t.Fatalf("two healthy loops report %+v", status)
	}
	if status.Age != 90*time.Second {
		t.Fatalf("not the longest silence was reported: %s", status.Age)
	}

	// One dead loop decides, even though the other keeps ticking.
	advance(31 * time.Minute)
	registry.Beat(SchedulerForce)
	if status, _ := registry.Worst(SchedulerForce, SchedulerAuto); !status.Stale {
		t.Fatalf("a dead auto-sync loop is not reported: %+v", status)
	}
}

func TestWorstNeedsEveryLoop(t *testing.T) {
	registry, _ := clocked()
	registry.Register(SchedulerForce, 30*time.Second)

	if _, present := registry.Worst(SchedulerForce, SchedulerAuto); present {
		t.Fatal("a missing loop was answered with a status")
	}
}

func TestToleranceFollowsTheInterval(t *testing.T) {
	if tolerance := Tolerance(time.Second); tolerance != minTolerance {
		t.Fatalf("a fast loop gets the tolerance %s", tolerance)
	}
	if tolerance := Tolerance(10 * time.Minute); tolerance != 30*time.Minute {
		t.Fatalf("a slow loop gets the tolerance %s", tolerance)
	}
}
