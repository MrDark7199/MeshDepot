package auth

import (
	"strings"
	"testing"
)

// Two keys must never collide, and neither may be guessable from the other.
func TestNewAPIKeyIsUniqueAndPrefixed(t *testing.T) {
	seen := map[string]bool{}
	for attempt := 0; attempt < 200; attempt++ {
		key, failure := NewAPIKey()
		if failure != nil {
			t.Fatalf("creating a key failed: %v", failure)
		}
		if !strings.HasPrefix(key, KeyPrefix) {
			t.Fatalf("a key must be recognisable, got %q", key)
		}
		if len(key) < len(KeyPrefix)+40 {
			t.Fatalf("a key that short is not 32 random bytes: %q", key)
		}
		if seen[key] {
			t.Fatal("the same key was handed out twice")
		}
		seen[key] = true
	}
}

// The stored value must not be the key itself - that is the entire point of
// storing a hash.
func TestHashAPIKeyDoesNotContainTheKey(t *testing.T) {
	key, _ := NewAPIKey()
	hash := HashAPIKey(key)
	if hash == key || strings.Contains(hash, strings.TrimPrefix(key, KeyPrefix)) {
		t.Fatal("the hash still carries the key")
	}
	if HashAPIKey(key) != hash {
		t.Fatal("hashing the same key twice gave two answers")
	}
	if HashAPIKey(key+"x") == hash {
		t.Fatal("a different key hashed to the same value")
	}
}

// The preview identifies a key in a list without being usable as one.
func TestAPIKeyPreviewIsShort(t *testing.T) {
	key, _ := NewAPIKey()
	preview := APIKeyPreview(key)
	if len(preview) >= len(key) {
		t.Fatalf("the preview is the whole key: %q", preview)
	}
	if !strings.HasPrefix(key, preview) {
		t.Fatalf("the preview %q is not the head of the key", preview)
	}
}
