package auth

import (
	"crypto/rand"
	"encoding/hex"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

// dummyHash is what a login for a non-existent account is checked against.
// Without it that request returns immediately while a real one spends ~50-100 ms
// in bcrypt - measurable over the network, and an enumeration oracle. Derived
// from random bytes at startup, so no input can match it.
var dummyHash = generateDummyHash()

func generateDummyHash() string {
	secret := make([]byte, 32)
	if _, failure := rand.Read(secret); failure != nil {
		// Cannot happen in practice; fall back to a constant that still hashes.
		secret = []byte("meshdepot-dummy")
	}
	hash, failure := bcrypt.GenerateFromPassword([]byte(hex.EncodeToString(secret)), bcrypt.DefaultCost)
	if failure != nil {
		return ""
	}
	return string(hash)
}

// VerifyDummyPassword does the same work against a hash nothing can match, for
// the "user not found" path. The result is meaningless and not returned.
func VerifyDummyPassword(password string) {
	_ = VerifyPassword(dummyHash, password)
}

// VerifyPassword rewrites PHP's `$2y$` prefix to `$2b$`, which Go's bcrypt expects
// and which is binary compatible.
func VerifyPassword(hash, password string) bool {
	if strings.HasPrefix(hash, "$2y$") {
		hash = "$2b$" + hash[4:]
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

func HashPassword(password string) (string, error) {
	hash, failure := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if failure != nil {
		return "", failure
	}
	return string(hash), nil
}
