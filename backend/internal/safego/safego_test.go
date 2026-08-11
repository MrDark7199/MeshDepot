package safego

import (
	"errors"
	"sync"
	"testing"
)

func TestRunReportsSuccess(t *testing.T) {
	executed := false
	if !Run("job", func() { executed = true }) {
		t.Fatal("Run reported a panic for a function that did not panic")
	}
	if !executed {
		t.Fatal("the function was not called")
	}
}

func TestRunRecoversPanicAndReportsFailure(t *testing.T) {
	if Run("job", func() { panic("deliberate") }) {
		t.Fatal("Run reported success although the function panicked")
	}
}

// The panic types that actually occur here are failed type assertions and nil
// dereferences on foreign JSON, not string panics.
func TestRunRecoversRuntimeAndErrorPanics(t *testing.T) {
	if Run("type assertion", func() {
		var decoded any = 42
		_ = decoded.(string)
	}) {
		t.Fatal("a failed type assertion was not recovered")
	}
	if Run("nil map", func() {
		var entries map[string]any
		entries["key"] = 1
	}) {
		t.Fatal("a write to a nil map was not recovered")
	}
	if Run("error value", func() { panic(errors.New("deliberate")) }) {
		t.Fatal("a panic with an error value was not recovered")
	}
}

func TestGoRunsInBackgroundAndSurvivesPanic(t *testing.T) {
	var waitGroup sync.WaitGroup
	waitGroup.Add(2)

	Go("healthy", func() { defer waitGroup.Done() })
	Go("panicking", func() {
		defer waitGroup.Done()
		panic("deliberate")
	})

	waitGroup.Wait()
}
