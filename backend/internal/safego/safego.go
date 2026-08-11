// Package safego starts background goroutines that survive a panic.
//
// The app runs as a single process: HTTP API, download and sync workers, the
// scheduler and the Tor supervisor all share it. net/http recovers panics in its
// own handlers, but a panic in any goroutine started outside a handler tears down
// the entire process - and with it every running download. The downloader paths
// type-assert their way through foreign JSON (`entry["name"].(string)`,
// `collection["models"].([]any)`), so one unexpected platform response is enough.
//
// Go turns a panic into a full log+exit, which is the right default for a program
// that cannot know whether its state is still sound. Here we do know: a worker job
// is self-contained, so losing that one job is preferable to losing the container.
package safego

import (
	"log"
	"runtime/debug"
)

// Go runs fn in a new goroutine and recovers a panic instead of letting it kill
// the process. name identifies the goroutine in the log line.
func Go(name string, fn func()) {
	go Run(name, fn)
}

// Run calls fn in the current goroutine and recovers a panic. Use it inside an
// existing loop (where every iteration should survive) or when the caller starts
// the goroutine itself. Returns true if fn completed without panicking.
func Run(name string, fn func()) (ok bool) {
	defer func() {
		if recovered := recover(); recovered != nil {
			ok = false
			log.Printf("[panic] %s: %v\n%s", name, recovered, debug.Stack())
		}
	}()
	fn()
	return true
}
