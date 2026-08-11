package auth

import (
	"crypto/rand"
	"encoding/hex"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

// dummyHash is what a login for a non-existent account is checked against.
// Without it the request returns immediately, while a login for a real account
// spends ~50-100 ms in bcrypt - a difference that is measurable over the network
// and turns the login form into a user-enumeration oracle.
//
// The hash is derived from random bytes at startup, so no input can match it.
var dummyHash = generateDummyHash()

// generateDummyHash builds a bcrypt hash of a random string at the same cost the
// real hashes use, so the comparison takes the same time.
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

// VerifyDummyPassword performs the same work as VerifyPassword against a hash
// nothing can match. Call it on the "user not found" path so both outcomes cost
// the same. The result is meaningless and therefore not returned.
func VerifyDummyPassword(password string) {
	_ = VerifyPassword(dummyHash, password)
}

// VerifyPassword checks a password against a bcrypt hash. PHP's password_hash
// produces the `$2y$` prefix; Go's bcrypt expects `$2a$`/`$2b$`, so `$2y$` is
// transparently rewritten to `$2b$` (binary compatible).
func VerifyPassword(hash, password string) bool {
	if strings.HasPrefix(hash, "$2y$") {
		hash = "$2b$" + hash[4:]
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// HashPassword produces a bcrypt hash (compatible with PHP's password_verify).
func HashPassword(password string) (string, error) {
	hash, failure := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if failure != nil {
		return "", failure
	}
	return string(hash), nil
}
