package auth

import (
	"sync"
	"testing"
	"time"
)

func TestTTLStoreSetGetDel(t *testing.T) {
	store := newTTLStore()

	if _, found := store.Get("missing"); found {
		t.Fatal("an unset key reported as found")
	}

	store.Set("token", "42", time.Minute)
	value, found := store.Get("token")
	if !found || value != "42" {
		t.Fatalf("expected 42/true, got %q/%v", value, found)
	}

	store.Del("token")
	if _, found := store.Get("token"); found {
		t.Fatal("a deleted key is still readable")
	}
}

func TestTTLStoreExpiresAndDropsEntry(t *testing.T) {
	store := newTTLStore()
	store.Set("short", "value", -time.Second)

	if _, found := store.Get("short"); found {
		t.Fatal("an expired key was returned")
	}
	store.mutex.Lock()
	_, stillStored := store.data["short"]
	store.mutex.Unlock()
	if stillStored {
		t.Fatal("the expired entry was not dropped on read")
	}
}

func TestTTLStoreOverwriteExtendsLifetime(t *testing.T) {
	store := newTTLStore()
	store.Set("key", "first", -time.Second)
	store.Set("key", "second", time.Minute)

	value, found := store.Get("key")
	if !found || value != "second" {
		t.Fatalf("expected second/true, got %q/%v", value, found)
	}
}

func TestTTLStoreIsSafeForConcurrentUse(t *testing.T) {
	store := newTTLStore()
	var waitGroup sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		waitGroup.Add(1)
		go func(worker int) {
			defer waitGroup.Done()
			key := "worker" + itoa(worker)
			for round := 0; round < 200; round++ {
				store.Set(key, "value", time.Minute)
				store.Get(key)
				store.Del(key)
			}
		}(worker)
	}
	waitGroup.Wait()
}
