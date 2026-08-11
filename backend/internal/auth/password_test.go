package auth

import "testing"

func TestHashPasswordRoundTrip(t *testing.T) {
	hash, failure := HashPassword("correct horse battery staple")
	if failure != nil {
		t.Fatalf("hash password: %v", failure)
	}
	if !VerifyPassword(hash, "correct horse battery staple") {
		t.Fatal("the freshly created hash does not verify against its own password")
	}
	if VerifyPassword(hash, "correct horse battery stapl") {
		t.Fatal("a wrong password was accepted")
	}
}

// The PHP backend wrote $2y$ hashes; Go's bcrypt only knows $2a$/$2b$. Existing
// accounts must keep working after the port.
func TestVerifyPasswordAcceptsPHPPrefix(t *testing.T) {
	hash, failure := HashPassword("legacy-secret")
	if failure != nil {
		t.Fatalf("hash password: %v", failure)
	}
	phpStyleHash := "$2y$" + hash[4:]
	if !VerifyPassword(phpStyleHash, "legacy-secret") {
		t.Fatal("a $2y$ hash from the PHP backend was rejected")
	}
	if VerifyPassword(phpStyleHash, "wrong") {
		t.Fatal("a $2y$ hash accepted the wrong password")
	}
}

func TestVerifyPasswordRejectsMalformedHash(t *testing.T) {
	for _, hash := range []string{"", "not-a-bcrypt-hash", "$2y$", "$2b$99$short"} {
		if VerifyPassword(hash, "anything") {
			t.Fatalf("malformed hash %q was accepted", hash)
		}
	}
}

// VerifyDummyPassword exists so the "unknown user" path costs the same as a real
// check. It must never report success and never panic.
func TestVerifyDummyPasswordNeverMatches(t *testing.T) {
	VerifyDummyPassword("")
	VerifyDummyPassword("anything at all")
	if VerifyPassword(dummyHash, "anything at all") {
		t.Fatal("the dummy hash matched a password")
	}
	if dummyHash == "" {
		t.Fatal("the dummy hash is empty, so the timing equalisation does no work")
	}
}
