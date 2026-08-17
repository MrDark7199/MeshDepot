package auth

import (
	"strings"
	"testing"
)

func TestGenerateTotpSecretFormat(t *testing.T) {
	first := generateTotpSecret()
	if len(first) != 20 {
		t.Fatalf("expected a 20 character secret, got %d", len(first))
	}
	for _, character := range first {
		if !strings.ContainsRune(base32Alphabet, character) {
			t.Fatalf("character %q is outside the base32 alphabet", character)
		}
	}
	if first == generateTotpSecret() {
		t.Fatal("two generated secrets are identical")
	}
}

func TestBase32DecodeKnownVector(t *testing.T) {
	// RFC 4648: "MZXW6YTB" decodes to "fooba".
	if decoded := string(base32Decode("MZXW6YTB")); decoded != "fooba" {
		t.Fatalf("expected fooba, got %q", decoded)
	}
}

func TestBase32DecodeIgnoresSpacesAndCase(t *testing.T) {
	reference := base32Decode("MZXW6YTB")
	for _, variant := range []string{"mzxw6ytb", "MZXW 6YTB", "mzxw 6ytb"} {
		if string(base32Decode(variant)) != string(reference) {
			t.Fatalf("variant %q decoded differently", variant)
		}
	}
}

func TestBase32DecodeSkipsInvalidCharacters(t *testing.T) {
	if string(base32Decode("MZXW-6YTB!")) != "fooba" {
		t.Fatal("invalid characters changed the decoded result")
	}
	if len(base32Decode("!!!")) != 0 {
		t.Fatal("a string without valid characters produced output")
	}
}

func TestTotpCodeIsSixDigitsAndOffsetDependent(t *testing.T) {
	secret := generateTotpSecret()
	current := totpCode(secret, 0)
	if len(current) != 6 {
		t.Fatalf("expected 6 digits, got %q", current)
	}
	for _, digit := range current {
		if digit < '0' || digit > '9' {
			t.Fatalf("code %q contains a non-digit", current)
		}
	}
	if totpCode(secret, 0) != current {
		t.Fatal("the same window produced two different codes")
	}
	distant := totpCode(secret, 1000)
	if distant == current {
		t.Fatal("a window 1000 periods away produced the same code")
	}
}

func TestTotpCodeDiffersPerSecret(t *testing.T) {
	if totpCode(generateTotpSecret(), 0) == totpCode(generateTotpSecret(), 0) {
		t.Fatal("two different secrets produced the same code")
	}
}

func TestVerifyTotpAcceptsDriftWindow(t *testing.T) {
	service, _ := newTestAuth(t)
	secret := generateTotpSecret()

	for _, offset := range []int{-1, 0, 1} {
		if !service.verifyTotp(secret, totpCode(secret, offset), 1) {
			t.Fatalf("the code for offset %d was rejected", offset)
		}
	}
}

func TestVerifyTotpRejectsOutsideDriftWindow(t *testing.T) {
	service, _ := newTestAuth(t)
	secret := generateTotpSecret()

	for _, offset := range []int{-2, 2, 50} {
		if service.verifyTotp(secret, totpCode(secret, offset), 1) {
			t.Fatalf("the code for offset %d was accepted", offset)
		}
	}
	if service.verifyTotp(secret, "000000", 1) && totpCode(secret, 0) != "000000" {
		t.Fatal("a code that belongs to no window was accepted")
	}
}

// A captured code must not be usable a second time within its validity window.
func TestVerifyTotpBlocksReplay(t *testing.T) {
	service, _ := newTestAuth(t)
	secret := generateTotpSecret()
	code := totpCode(secret, 0)

	if !service.verifyTotp(secret, code, 1) {
		t.Fatal("the first use of the code was rejected")
	}
	if service.verifyTotp(secret, code, 1) {
		t.Fatal("the code was accepted a second time")
	}
}

// The replay block is per user, so two accounts sharing a code by coincidence do
// not lock each other out.
func TestVerifyTotpReplayBlockIsPerUser(t *testing.T) {
	service, _ := newTestAuth(t)
	secret := generateTotpSecret()
	code := totpCode(secret, 0)

	if !service.verifyTotp(secret, code, 1) {
		t.Fatal("the first use was rejected")
	}
	if !service.verifyTotp(secret, code, 2) {
		t.Fatal("the same code was blocked for a different user")
	}
}
