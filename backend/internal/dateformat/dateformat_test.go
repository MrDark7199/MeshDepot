package dateformat

import "testing"

// The point of the package: the same instant, written the way the account asked
// for it. A mistake here shows every member a date in someone else's notation.
func TestTimestampFollowsThePattern(t *testing.T) {
	const stored = "2026-11-25 12:34:56"
	for pattern, expected := range map[string]string{
		"DD.MM.YYYY":  "25.11.2026 12:34",
		"DD/MM/YYYY":  "25/11/2026 12:34",
		"MM/DD/YYYY":  "11/25/2026 12:34",
		"YYYY-MM-DD":  "2026-11-25 12:34",
		"D MMM YYYY":  "25 Nov 2026 12:34",
		"MMM D, YYYY": "Nov 25, 2026 12:34",
	} {
		if got := Timestamp(stored, pattern); got != expected {
			t.Errorf("pattern %q: got %q, want %q", pattern, got, expected)
		}
	}
}

// No notation chosen, or a value that is not a timestamp: the stored text is
// passed through rather than mangled or lost.
func TestTimestampPassesThroughWhatItCannotFormat(t *testing.T) {
	if got := Timestamp("2026-11-25 12:34:56", ""); got != "2026-11-25 12:34:56" {
		t.Errorf("no pattern should keep the value, got %q", got)
	}
	if got := Timestamp("whenever", "DD.MM.YYYY"); got != "whenever" {
		t.Errorf("unparsable input should be kept, got %q", got)
	}
}

// Valid guards what may be stored; every offered pattern must pass and an
// invented one must not.
func TestValid(t *testing.T) {
	if !Valid("") || !Valid("YYYY-MM-DD") {
		t.Fatal("the default and a known pattern must be valid")
	}
	if Valid("DD-MM-YY") {
		t.Fatal("an unknown pattern must be refused")
	}
}
