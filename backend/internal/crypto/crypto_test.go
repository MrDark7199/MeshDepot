package crypto

import (
	"encoding/base64"
	"strings"
	"testing"
)

// phpReference was produced in the meshdepot_php container with the exact
// init.php algorithm: appKey="test-key-123", userId=7,
// plaintext="hello-secret-äöü". It is also the legacy-format fixture: values
// already in the database use exactly this layout.
const (
	phpAppKey    = "test-key-123"
	phpUserID    = 7
	phpPlaintext = "hello-secret-äöü"
	phpReference = "TMbrzs2Fyvkfehev0e0IW9feGeN7JlkKZheUH/jTuB8Og8XhY/h8qNclko0HbRaE"
)

// TestDecryptPHPValue proves that a value written before the GCM switch (the
// PHP-compatible CBC format) is still readable.
func TestDecryptPHPValue(t *testing.T) {
	helper := New(phpAppKey)
	got, ok := helper.Decrypt(phpReference, phpUserID)
	if !ok {
		t.Fatalf("Decrypt reported ok=false for the PHP reference value")
	}
	if got != phpPlaintext {
		t.Fatalf("Decrypt = %q, expected %q", got, phpPlaintext)
	}
}

// TestRoundTrip verifies Encrypt→Decrypt within Go.
func TestRoundTrip(t *testing.T) {
	helper := New("another-master-key")
	for _, plaintext := range []string{"", "x", "secret password 123", "äöü-üml@ut"} {
		encrypted, failure := helper.Encrypt(plaintext, 42)
		if failure != nil {
			t.Fatalf("Encrypt(%q): %v", plaintext, failure)
		}
		got, ok := helper.Decrypt(encrypted, 42)
		if !ok || got != plaintext {
			t.Fatalf("RoundTrip(%q) = %q ok=%v", plaintext, got, ok)
		}
	}
}

// TestWrongUserFails ensures another user cannot recover the plaintext. (A wrong
// key can coincidentally yield valid PKCS7 padding in the legacy format, so this
// does not assert ok==false but rather "never returns the plaintext".)
func TestWrongUserFails(t *testing.T) {
	helper := New(phpAppKey)
	if got, ok := helper.Decrypt(phpReference, 8); ok && got == phpPlaintext {
		t.Fatalf("Decrypt with the wrong userId returned the plaintext")
	}
	encrypted, failure := helper.Encrypt(phpPlaintext, phpUserID)
	if failure != nil {
		t.Fatalf("Encrypt: %v", failure)
	}
	if _, ok := helper.Decrypt(encrypted, 8); ok {
		t.Fatalf("GCM value opened with the wrong userId")
	}
}

// TestEncryptUsesAuthenticatedFormat pins the storage format: everything
// written now must carry the GCM marker, otherwise old CBC values would keep
// being produced unnoticed.
func TestEncryptUsesAuthenticatedFormat(t *testing.T) {
	helper := New("another-master-key")
	encrypted, failure := helper.Encrypt("secret", 42)
	if failure != nil {
		t.Fatalf("Encrypt: %v", failure)
	}
	raw, failure := base64.StdEncoding.DecodeString(encrypted)
	if failure != nil {
		t.Fatalf("Encrypt did not return base64: %v", failure)
	}
	if !strings.HasPrefix(string(raw), gcmMagic) {
		t.Fatalf("Encrypt produced a value without the %q marker", gcmMagic)
	}
}

// TestTamperedCiphertextFails is the point of the GCM switch: in CBC a flipped
// bit silently changed the plaintext, now it makes the value unreadable.
func TestTamperedCiphertextFails(t *testing.T) {
	helper := New("another-master-key")
	encrypted, failure := helper.Encrypt("secret password 123", 42)
	if failure != nil {
		t.Fatalf("Encrypt: %v", failure)
	}
	raw, _ := base64.StdEncoding.DecodeString(encrypted)
	for _, position := range []int{len(gcmMagic), len(gcmMagic) + 4, len(raw) - 1} {
		tampered := make([]byte, len(raw))
		copy(tampered, raw)
		tampered[position] ^= 0x01
		if _, ok := helper.Decrypt(base64.StdEncoding.EncodeToString(tampered), 42); ok {
			t.Fatalf("Decrypt accepted a ciphertext modified at byte %d", position)
		}
	}
}

// TestRoundTripEveryUser guards the nonce/AAD wiring across users: a value must
// only open for the user it was written for, and each call must produce a fresh
// ciphertext.
func TestRoundTripEveryUser(t *testing.T) {
	helper := New("master")
	first, _ := helper.Encrypt("same-plaintext", 1)
	second, _ := helper.Encrypt("same-plaintext", 1)
	if first == second {
		t.Fatalf("two encryptions of the same plaintext are identical - nonce not random")
	}
	if _, ok := helper.Decrypt(first, 2); ok {
		t.Fatalf("user 2 could open user 1's value")
	}
}
