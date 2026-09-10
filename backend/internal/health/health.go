// Package health tracks the liveness of the background loops, so the admin health
// endpoint reports what they are really doing instead of a hard-coded "ok".
//
// A loop is ticking, busy with a job, or gone. safego keeps a panicking tick from
// killing its loop, but a panic in the loop body ends the goroutine silently: the
// queue stands still until the container is restarted and nothing in the database
// shows it. A registered loop that stops beating is that case.
//
// A busy loop is deliberately not stale - a download may hold the worker for ten
// minutes, and it cannot tick while it does.
package health

import (
	"sync"
	"time"
)

// Loop names, which double as the keys of the health endpoint.
const (
	DownloadWorker = "download_worker"
	SyncWorker     = "sync_worker"
	SchedulerForce = "scheduler.force_flag"
	SchedulerAuto  = "scheduler.auto_sync"
	// Collects pending notification e-mails into one message per member.
	SchedulerMailDigest = "scheduler.mail_digest"
)

// staleFactor is how many ticks a loop may miss before it counts as dead. Two
// would already trip on a single slow tick.
const staleFactor = 3

// minTolerance is the floor: the workers tick every 2-5 s, and the jitter of a
// loaded container would otherwise report a healthy loop as gone.
const minTolerance = 30 * time.Second

// Tolerance is how long a loop with this tick interval may stay silent.
func Tolerance(interval time.Duration) time.Duration {
	if tolerance := interval * staleFactor; tolerance > minTolerance {
		return tolerance
	}
	return minTolerance
}

type Status struct {
	Interval time.Duration
	// Age is the time since the last tick, or since the current job started.
	Age   time.Duration
	Busy  bool
	Stale bool
}

type loop struct {
	interval  time.Duration
	last      time.Time
	busySince time.Time
	// busyDepth counts open Working calls, so overlapping jobs end the busy phase
	// only when the last of them is done.
	busyDepth int
}

// Registry collects the heartbeats. Every method tolerates a nil receiver, so a
// component that is not wired to one needs no null checks.
type Registry struct {
	// Now is the clock, replaced in tests. nil means time.Now.
	Now func() time.Time

	mutex sync.Mutex
	loops map[string]*loop
}

func New() *Registry { return &Registry{loops: map[string]*loop{}} }

func (registry *Registry) clock() time.Time {
	if registry.Now != nil {
		return registry.Now()
	}
	return time.Now()
}

// Register counts as the first heartbeat, so a loop that has just started is not
// reported dead before it can tick.
func (registry *Registry) Register(name string, interval time.Duration) {
	if registry == nil {
		return
	}
	registry.mutex.Lock()
	defer registry.mutex.Unlock()
	if registry.loops == nil {
		registry.loops = map[string]*loop{}
	}
	registry.loops[name] = &loop{interval: interval, last: registry.clock()}
}

func (registry *Registry) Beat(name string) {
	if registry == nil {
		return
	}
	registry.mutex.Lock()
	defer registry.mutex.Unlock()
	if entry := registry.loops[name]; entry != nil {
		entry.last = registry.clock()
	}
}

// Working marks the loop busy and returns the function that ends the busy phase.
// Call it around work that blocks the loop's own ticker.
func (registry *Registry) Working(name string) func() {
	if registry == nil {
		return func() {}
	}
	registry.mutex.Lock()
	entry := registry.loops[name]
	if entry == nil {
		registry.mutex.Unlock()
		return func() {}
	}
	if entry.busyDepth == 0 {
		entry.busySince = registry.clock()
	}
	entry.busyDepth++
	registry.mutex.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			registry.mutex.Lock()
			defer registry.mutex.Unlock()
			entry.busyDepth--
			if entry.busyDepth <= 0 {
				entry.busyDepth = 0
				entry.busySince = time.Time{}
				// The job just ended is a sign of life: the loop is about to reach its select.
				entry.last = registry.clock()
			}
		})
	}
}

// Get returns false when the loop was never registered - never started, or never
// wired to the registry.
func (registry *Registry) Get(name string) (Status, bool) {
	if registry == nil {
		return Status{}, false
	}
	registry.mutex.Lock()
	defer registry.mutex.Unlock()
	entry := registry.loops[name]
	if entry == nil {
		return Status{}, false
	}
	now := registry.clock()
	if entry.busyDepth > 0 {
		return Status{Interval: entry.interval, Age: now.Sub(entry.busySince), Busy: true}, true
	}
	age := now.Sub(entry.last)
	return Status{Interval: entry.interval, Age: age, Stale: age > Tolerance(entry.interval)}, true
}

// Worst reduces several loops to the one status worth reporting. Missing beats
// stale, stale beats busy, and among equals the longest silence wins.
func (registry *Registry) Worst(names ...string) (Status, bool) {
	var worst Status
	for index, name := range names {
		status, present := registry.Get(name)
		if !present {
			return Status{}, false
		}
		if index == 0 || rank(status) > rank(worst) || (rank(status) == rank(worst) && status.Age > worst.Age) {
			worst = status
		}
	}
	return worst, len(names) > 0
}

func rank(status Status) int {
	switch {
	case status.Stale:
		return 2
	case status.Busy:
		return 1
	default:
		return 0
	}
}
