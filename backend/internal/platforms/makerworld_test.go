package platforms

import (
	"regexp"
	"testing"
)

func TestMakerworldIDExtraction(t *testing.T) {
	cases := []struct{ url, model, profile string }{
		{"https://makerworld.com/en/models/123456", "123456", ""},
		{"https://makerworld.com/en/models/789-foo?profileId-456", "789", "456"},
		{"https://makerworld.com/models/42#profileId-7", "42", "7"},
		{"https://makerworld.com/en/u/someone", "", ""},
	}
	for _, testCase := range cases {
		model := ""
		if match := modelIDPattern.FindStringSubmatch(testCase.url); match != nil {
			model = match[1]
		}
		profile := ""
		if profileMatch := profileIDPattern.FindStringSubmatch(testCase.url); profileMatch != nil {
			profile = profileMatch[1]
		}
		if model != testCase.model || profile != testCase.profile {
			t.Errorf("%q -> model=%q profile=%q, want model=%q profile=%q", testCase.url, model, profile, testCase.model, testCase.profile)
		}
	}
}

func TestGenerateTotpFormat(t *testing.T) {
	code := generateTotp("JBSWY3DPEHPK3PXP")
	if !regexp.MustCompile(`^\d{6}$`).MatchString(code) {
		t.Errorf("TOTP code %q is not 6 digits", code)
	}
	// Stable within the same 30-second window.
	if again := generateTotp("JBSWY3DPEHPK3PXP"); again != code {
		t.Errorf("TOTP not stable within window: %q vs %q", code, again)
	}
	// Spaces in the secret are ignored (same result).
	if spaced := generateTotp("JBSW Y3DP EHPK 3PXP"); spaced != code {
		t.Errorf("TOTP with spaces differs: %q vs %q", spaced, code)
	}
}
