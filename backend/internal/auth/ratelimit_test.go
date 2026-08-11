package auth

import (
	"testing"
	"time"
)

func TestRateLimiterAllowsUntilLimit(t *testing.T) {
	limiter := newRateLimiter()
	address := "198.51.100.7"

	for attempt := 1; attempt < rateLimitMax; attempt++ {
		limiter.Record(address)
		if limiter.Exceeded(address) {
			t.Fatalf("blocked after %d of %d allowed attempts", attempt, rateLimitMax)
		}
	}
	limiter.Record(address)
	if !limiter.Exceeded(address) {
		t.Fatalf("still allowed after %d attempts", rateLimitMax)
	}
}

func TestRateLimiterIsPerAddress(t *testing.T) {
	limiter := newRateLimiter()
	for attempt := 0; attempt < rateLimitMax; attempt++ {
		limiter.Record("198.51.100.7")
	}
	if !limiter.Exceeded("198.51.100.7") {
		t.Fatal("the recorded address is not blocked")
	}
	if limiter.Exceeded("198.51.100.8") {
		t.Fatal("a different address was blocked along with it")
	}
}

func TestRateLimiterClearResetsCounter(t *testing.T) {
	limiter := newRateLimiter()
	address := "198.51.100.7"
	for attempt := 0; attempt < rateLimitMax; attempt++ {
		limiter.Record(address)
	}
	limiter.Clear(address)
	if limiter.Exceeded(address) {
		t.Fatal("the counter survived Clear")
	}
}

// An expired window must start counting from zero again instead of keeping the
// old total.
func TestRateLimiterWindowExpiry(t *testing.T) {
	limiter := newRateLimiter()
	address := "198.51.100.7"

	limiter.mutex.Lock()
	limiter.data[address] = rateEntry{count: rateLimitMax, expires: time.Now().Add(-time.Second)}
	limiter.mutex.Unlock()

	if limiter.Exceeded(address) {
		t.Fatal("an expired window still blocks")
	}
	limiter.Record(address)

	limiter.mutex.Lock()
	entry := limiter.data[address]
	limiter.mutex.Unlock()
	if entry.count != 1 {
		t.Fatalf("expected the counter to restart at 1, got %d", entry.count)
	}
}

func TestRateLimiterUnknownAddressIsNotBlocked(t *testing.T) {
	limiter := newRateLimiter()
	if limiter.Exceeded("203.0.113.1") {
		t.Fatal("an address that was never recorded is blocked")
	}
	limiter.Clear("203.0.113.1")
}
