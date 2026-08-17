package api

import (
	"testing"
	"time"
)

// A download token is meant for exactly one request, so the second attempt with
// the same token has to come back empty.
func TestDownloadTokenIsGoneAfterTheFirstUse(t *testing.T) {
	store := newDLTokenStore()
	store.Set("token-1", dlToken{designID: 7, fileVersionID: 8, entryID: 9}, time.Minute)

	entry, found := store.Take("token-1")

	if !found {
		t.Fatal("the token was not accepted")
	}
	if entry.designID != 7 || entry.fileVersionID != 8 || entry.entryID != 9 {
		t.Fatalf("the token references %+v", entry)
	}
	if _, found := store.Take("token-1"); found {
		t.Fatal("the token was accepted a second time")
	}
}

func TestDownloadTokenIsUnknownWithoutBeingStored(t *testing.T) {
	store := newDLTokenStore()

	if _, found := store.Take("token-1"); found {
		t.Fatal("an unknown token was accepted")
	}
}

// An expired token is rejected and dropped rather than kept around until the
// sweeper comes by.
func TestDownloadTokenExpires(t *testing.T) {
	store := newDLTokenStore()
	store.Set("token-1", dlToken{designID: 7}, -time.Second)

	if _, found := store.Take("token-1"); found {
		t.Fatal("an expired token was accepted")
	}
	store.mutex.Lock()
	remaining := len(store.tokens)
	store.mutex.Unlock()
	if remaining != 0 {
		t.Fatalf("%d expired tokens are still stored", remaining)
	}
}

// Storing the same token twice replaces the reference instead of keeping the
// old one alive.
func TestDownloadTokenIsReplacedOnASecondSet(t *testing.T) {
	store := newDLTokenStore()
	store.Set("token-1", dlToken{designID: 7}, time.Minute)
	store.Set("token-1", dlToken{designID: 42}, time.Minute)

	entry, found := store.Take("token-1")

	if !found || entry.designID != 42 {
		t.Fatalf("the token references %+v (found: %v)", entry, found)
	}
}
