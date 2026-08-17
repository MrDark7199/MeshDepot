package auth

import (
	"sync"
	"time"

	"meshdepot/internal/safego"
)

const (
	rateLimitMax    = 10               // Max. failed attempts per window.
	rateLimitWindow = 15 * time.Minute // Window size (equals 900 s).
)

// rateEntry counts failed attempts until the window expires.
type rateEntry struct {
	count   int
	expires time.Time
}

// rateLimiter throttles failed logins per IP (replaces the Redis counters).
type rateLimiter struct {
	mutex sync.Mutex
	data  map[string]rateEntry
}

// newRateLimiter creates a rate limiter and starts its sweeper.
func newRateLimiter() *rateLimiter {
	limiter := &rateLimiter{data: make(map[string]rateEntry)}
	safego.Go("rate-limiter-sweeper", limiter.sweep)
	return limiter
}

// sweep drops expired entries. Without it the map only ever grows: entries are
// keyed by client IP and nothing deletes them except a successful login, so a
// stream of failed logins from changing addresses is an unbounded memory leak.
// Same approach as ttlStore.sweep in this package.
func (limiter *rateLimiter) sweep() {
	ticker := time.NewTicker(rateLimitWindow)
	defer ticker.Stop()
	for range ticker.C {
		now := time.Now()
		limiter.mutex.Lock()
		for ip, entry := range limiter.data {
			if now.After(entry.expires) {
				delete(limiter.data, ip)
			}
		}
		limiter.mutex.Unlock()
	}
}

// Exceeded reports whether the IP has reached the limit.
func (limiter *rateLimiter) Exceeded(ip string) bool {
	limiter.mutex.Lock()
	defer limiter.mutex.Unlock()
	entry, ok := limiter.data[ip]
	if !ok || time.Now().After(entry.expires) {
		return false
	}
	return entry.count >= rateLimitMax
}

// Record increments the IP's failed-attempt counter and sets the expiry window.
func (limiter *rateLimiter) Record(ip string) {
	limiter.mutex.Lock()
	defer limiter.mutex.Unlock()
	entry, ok := limiter.data[ip]
	if !ok || time.Now().After(entry.expires) {
		limiter.data[ip] = rateEntry{count: 1, expires: time.Now().Add(rateLimitWindow)}
		return
	}
	entry.count++
	limiter.data[ip] = entry
}

// Clear resets an IP's counter after a successful login.
func (limiter *rateLimiter) Clear(ip string) {
	limiter.mutex.Lock()
	delete(limiter.data, ip)
	limiter.mutex.Unlock()
}
