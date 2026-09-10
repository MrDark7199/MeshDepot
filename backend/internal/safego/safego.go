// Package safego starts background goroutines that survive a panic.
//
// The app is a single process: API, workers, scheduler and Tor supervisor share
// it. net/http recovers panics in its own handlers, but a panic in a goroutine
// started outside one tears down the process and every running download with it -
// and the downloaders type-assert their way through foreign JSON, so one
// unexpected platform response is enough.
//
// Go's full log-and-exit is right for a program that cannot know whether its
// state is sound. Here we do know: a worker job is self-contained.
package safego

import (
	"runtime/debug"

	"meshdepot/internal/logx"
)

// Go recovers a panic instead of letting it kill the process. name identifies the
// goroutine in the log line.
func Go(name string, fn func()) {
	go Run(name, fn)
}

// Run calls fn in the current goroutine and recovers a panic - for a loop where
// every iteration should survive. True when fn completed.
func Run(name string, fn func()) (ok bool) {
	defer func() {
		if recovered := recover(); recovered != nil {
			ok = false
			logx.Errorf("[panic] %s: %v\n%s", name, recovered, debug.Stack())
		}
	}()
	fn()
	return true
}
