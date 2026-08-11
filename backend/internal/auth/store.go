// Package auth bundles authentication: sessions, login, TOTP 2FA and login rate
// limiting - all in-process, without an external store.
package auth

import (
	"sync"
	"time"

	"meshdepot/internal/safego"
)

type ttlEntry struct {
	value   string
	expires time.Time
}

// ttlStore is a thread-safe key-value store with expiry (replacement for Redis
// SETEX/GET/DEL). Expired entries are removed lazily and by a sweeper.
type ttlStore struct {
	mutex sync.Mutex
	data  map[string]ttlEntry
}

// newTTLStore creates a store and starts a background sweeper.
func newTTLStore() *ttlStore {
	store := &ttlStore{data: make(map[string]ttlEntry)}
	safego.Go("ttl-store-sweeper", store.sweep)
	return store
}

func (store *ttlStore) Set(key, value string, lifetime time.Duration) {
	store.mutex.Lock()
	store.data[key] = ttlEntry{value: value, expires: time.Now().Add(lifetime)}
	store.mutex.Unlock()
}

// Get returns the value if present and not expired.
func (store *ttlStore) Get(key string) (string, bool) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	entry, ok := store.data[key]
	if !ok || time.Now().After(entry.expires) {
		if ok {
			delete(store.data, key)
		}
		return "", false
	}
	return entry.value, true
}

func (store *ttlStore) Del(key string) {
	store.mutex.Lock()
	delete(store.data, key)
	store.mutex.Unlock()
}

// sweep periodically removes expired entries.
func (store *ttlStore) sweep() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		now := time.Now()
		store.mutex.Lock()
		for key, entry := range store.data {
			if now.After(entry.expires) {
				delete(store.data, key)
			}
		}
		store.mutex.Unlock()
	}
}
