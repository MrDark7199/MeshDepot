package api

import (
	"path/filepath"
	"testing"
	"time"

	"meshdepot/internal/coerce"
	"meshdepot/internal/db"
	"meshdepot/internal/queuestate"
	"meshdepot/internal/worker"
)

// A waiting job carries what the display needs to explain itself: how many
// attempts it gets, and whether its platform is resting.
func TestAnnotateQueueRows(t *testing.T) {
	database, failure := db.Open(filepath.Join(t.TempDir(), "queue.db"))
	if failure != nil {
		t.Fatal(failure)
	}
	defer database.Close()
	if failure := db.InitSchema(database); failure != nil {
		t.Fatal(failure)
	}

	queuestate.Block(database, "makerworld", time.Now().Add(time.Hour), "captcha", time.Now())

	rows := []map[string]any{
		{"platform": "makerworld"},
		{"platform": "printables"},
		{"platform": ""},
	}
	annotateQueueRows(database, rows)

	if got := coerce.Int(rows[0]["max_retries"]); got != worker.MaxRetries {
		t.Errorf("max_retries = %d, want %d", got, worker.MaxRetries)
	}
	if blocked, _ := rows[0]["queue_blocked"].(bool); !blocked {
		t.Error("the blocked platform is not reported as blocked")
	}
	if blocked, _ := rows[1]["queue_blocked"].(bool); blocked {
		t.Error("an untouched platform is reported as blocked")
	}
	if _, present := rows[2]["max_retries"]; present {
		t.Error("a row without a platform was annotated")
	}
}
