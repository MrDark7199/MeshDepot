package api

import (
	"sync"
	"time"

	"meshdepot/internal/safego"
)

// dlToken references a file entry for the token-based download.
type dlToken struct {
	designID, fileVersionID, entryID int
	expires                          time.Time
}

// dlTokenStore is an in-process one-time-token store (replacement for Redis
// dl_token:*), as used by slicer downloads without a session.
type dlTokenStore struct {
	mutex  sync.Mutex
	tokens map[string]dlToken
}

// newDLTokenStore creates the store and starts a sweeper.
func newDLTokenStore() *dlTokenStore {
	store := &dlTokenStore{tokens: make(map[string]dlToken)}
	safego.Go("dl-token-sweeper", func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			now := time.Now()
			store.mutex.Lock()
			for token, entry := range store.tokens {
				if now.After(entry.expires) {
					delete(store.tokens, token)
				}
			}
			store.mutex.Unlock()
		}
	})
	return store
}

func (store *dlTokenStore) Set(token string, entry dlToken, lifetime time.Duration) {
	entry.expires = time.Now().Add(lifetime)
	store.mutex.Lock()
	store.tokens[token] = entry
	store.mutex.Unlock()
}

// Take returns a token and deletes it (one-time use).
func (store *dlTokenStore) Take(token string) (dlToken, bool) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	entry, ok := store.tokens[token]
	if !ok || time.Now().After(entry.expires) {
		delete(store.tokens, token)
		return dlToken{}, false
	}
	delete(store.tokens, token)
	return entry, true
}
