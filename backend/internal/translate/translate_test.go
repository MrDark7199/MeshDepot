package translate

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestLooksLikeHTML(t *testing.T) {
	yes := []string{"<p>hi</p>", "text <b>bold</b>", "<div class=x>"}
	no := []string{"plain text", "1 < 2 and 3 > 2", "no markup here", "a<3"}
	for _, sample := range yes {
		if !looksLikeHTML(sample) {
			t.Errorf("%q should look like HTML", sample)
		}
	}
	for _, sample := range no {
		if looksLikeHTML(sample) {
			t.Errorf("%q should NOT look like HTML", sample)
		}
	}
}

func TestSplitLongText(t *testing.T) {
	if got := splitLongText("short"); len(got) != 1 || got[0] != "short" {
		t.Fatalf("short text should stay unchanged: %v", got)
	}
	long := strings.Repeat("a", 3000) + "\n" + strings.Repeat("b", 3000)
	chunks := splitLongText(long)
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}
	if strings.Join(chunks, "") != long {
		t.Fatal("chunks must reconstruct the original text exactly")
	}
	for _, chunk := range chunks {
		if len(chunk) > batchCharLimit {
			t.Fatalf("chunk exceeds the limit: %d", len(chunk))
		}
	}
}

// Without a sentence end or a newline there is no good boundary, so the cut is
// hard - but it must never land inside a multi-byte rune.
func TestSplitLongTextCutsHardWithoutABoundary(t *testing.T) {
	text := strings.Repeat("ä", 3000)

	chunks := splitLongText(text)

	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}
	if strings.Join(chunks, "") != text {
		t.Fatal("chunks must reconstruct the original text exactly")
	}
	for index, chunk := range chunks {
		if len(chunk) > batchCharLimit {
			t.Fatalf("chunk %d exceeds the limit: %d", index, len(chunk))
		}
		if !utf8.ValidString(chunk) {
			t.Fatalf("chunk %d was cut inside a rune", index)
		}
	}
}

func TestTruncateRunes(t *testing.T) {
	if got := truncateRunes("hello", 10); got != "hello" {
		t.Errorf("short: %q", got)
	}
	if got := truncateRunes("héllo wörld", 5); got != "héllo" {
		t.Errorf("rune cut wrong: %q", got)
	}
	// Multi-byte runes must not be split.
	sample := strings.Repeat("ä", 300)
	got := truncateRunes(sample, 255)
	if len([]rune(got)) != 255 {
		t.Errorf("expected 255 runes, got %d", len([]rune(got)))
	}
}
