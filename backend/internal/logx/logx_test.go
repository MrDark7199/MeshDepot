package logx

import (
	"bytes"
	"log"
	"strings"
	"testing"
)

// capture collects what the standard logger writes while fn runs.
func capture(t *testing.T, fn func()) string {
	t.Helper()
	var buffer bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&buffer)
	t.Cleanup(func() { log.SetOutput(previous) })
	fn()
	return buffer.String()
}

func TestParseAcceptsTheDocumentedNames(t *testing.T) {
	for name, want := range map[string]Level{
		"none": LevelNone, "error": LevelError, "warning": LevelWarning,
		"info": LevelInfo, "all": LevelAll, "  ERROR ": LevelError,
	} {
		got, ok := Parse(name)
		if !ok || got != want {
			t.Fatalf("Parse(%q) = %v, %v; want %v, true", name, got, ok, want)
		}
	}
	if _, ok := Parse("verbose"); ok {
		t.Fatal("an unknown name must not parse")
	}
}

func TestEachLevelWritesOnlyWhatItCovers(t *testing.T) {
	cases := []struct {
		level string
		want  []string
		gone  []string
	}{
		{"none", nil, []string{"error", "warning", "info", "debug"}},
		{"error", []string{"error"}, []string{"warning", "info", "debug"}},
		{"warning", []string{"error", "warning"}, []string{"info", "debug"}},
		{"info", []string{"error", "warning", "info"}, []string{"debug"}},
		{"all", []string{"error", "warning", "info", "debug"}, nil},
	}
	for _, testCase := range cases {
		output := capture(t, func() {
			Configure(testCase.level)
			Errorf("error")
			Warnf("warning")
			Infof("info")
			Debugf("debug")
		})
		for _, wanted := range testCase.want {
			if !strings.Contains(output, wanted) {
				t.Errorf("LOG_LEVEL=%s: %q missing from the output", testCase.level, wanted)
			}
		}
		for _, unwanted := range testCase.gone {
			if strings.Contains(output, unwanted) {
				t.Errorf("LOG_LEVEL=%s: %q should have been filtered out", testCase.level, unwanted)
			}
		}
	}
	Configure("all")
}

// An unusable value must not silence the log - that would hide the very errors
// the operator would need to notice the typo.
func TestAnUnknownLevelKeepsEverythingAndSaysSo(t *testing.T) {
	output := capture(t, func() {
		Configure("quiet-please")
		Errorf("error")
		Debugf("debug")
	})
	if !strings.Contains(output, "LOG_LEVEL") || !strings.Contains(output, "quiet-please") {
		t.Errorf("the fallback has to name the setting; got %q", output)
	}
	if !strings.Contains(output, "error") || !strings.Contains(output, "debug") {
		t.Errorf("everything should still be written; got %q", output)
	}
	Configure("all")
}
