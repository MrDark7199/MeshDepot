// Package health tracks the liveness of the background loops (download worker,
// sync worker, scheduler) so the admin health endpoint can report what they are
// really doing instead of a hard-coded "ok".
//
// Everything runs in one process, so a loop is in exactly one of three states:
// ticking, busy with a job, or gone. safego keeps a panicking tick from killing
// its loop, but a panic in the loop body itself ends the goroutine silently -
// the queue then stands still until the container is restarted, and nothing in
// the database shows it. A registered loop that stops beating is that case.
//
// A busy loop is deliberately not stale: a download may hold the worker for ten
// minutes, and the loop cannot tick while it does.
package health

import (
	"sync"
	"time"
)

// Loop names. They double as the keys of the health endpoint, so they follow
// its naming rather than the Go identifiers.
const (
	DownloadWorker = "download_worker"
	SyncWorker     = "sync_worker"
	SchedulerForce = "scheduler.force_flag"
	SchedulerAuto  = "scheduler.auto_sync"
)

// staleFactor is how many ticks a loop may miss before it counts as dead. Two
// would already trip on a single slow tick.
const staleFactor = 3

// minTolerance is the floor under the tolerance. The workers tick every 2–5 s;
// without it, the scheduling jitter of a loaded container would be enough to
// report a perfectly healthy loop as gone.
const minTolerance = 30 * time.Second

// Tolerance is how long a loop with the given tick interval may stay silent
// before it counts as stale.
func Tolerance(interval time.Duration) time.Duration {
	if tolerance := interval * staleFactor; tolerance > minTolerance {
		return tolerance
	}
	return minTolerance
}

// Status is the state of one loop.
type Status struct {
	// Interval is the tick interval the loop registered with.
	Interval time.Duration
	// Age is the time since the last tick - or since the current job started,
	// when Busy.
	Age time.Duration
	// Busy means the loop is inside a job and therefore cannot tick.
	Busy bool
	// Stale means the loop has missed too many ticks: it is no longer running.
	Stale bool
}

// loop is the bookkeeping of a single registered loop.
type loop struct {
	interval  time.Duration
	last      time.Time
	busySince time.Time
	// busyDepth counts open Working calls, so overlapping jobs (which the
	// current workers do not have, but a future pool would) end the busy phase
	// only when the last of them is done.
	busyDepth int
}

// Registry collects the heartbeats. All methods tolerate a nil receiver, so
// components that are not wired to one (every unit test) need no null checks.
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

// Register announces a loop with its tick interval. Registering counts as the
// first heartbeat so a loop that has just started is not reported dead before
// it can tick for the first time.
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

// Beat records a tick of an already registered loop.
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

// Working marks the loop as busy with a job and returns the function that ends
// the busy phase. Call it around work that blocks the loop's own ticker.
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
				// The job just ended counts as a sign of life: the loop is about
				// to reach its select again.
				entry.last = registry.clock()
			}
		})
	}
}

// Get returns the status of one loop. The second result is false when the loop
// was never registered - it was never started, or never wired to the registry.
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

// Worst reduces several loops to the one status worth reporting, for subsystems
// that consist of more than one loop. A missing loop wins over a stale one, a
// stale one over busy, and among equals the longest silence - the loop that is
// closest to being suspicious is the one to show.
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

// rank orders the states from harmless to alarming.
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
