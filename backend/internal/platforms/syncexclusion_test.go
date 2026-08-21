package platforms

import (
	"database/sql"
	"path/filepath"
	"testing"

	"meshdepot/internal/db"
)

// exclusionTestDB is an empty library with one user, ready for queue tests.
func exclusionTestDB(t *testing.T) *sql.DB {
	t.Helper()
	database, failure := db.Open(filepath.Join(t.TempDir(), "exclusions.db"))
	if failure != nil {
		t.Fatal(failure)
	}
	if failure := db.InitSchema(database); failure != nil {
		t.Fatal(failure)
	}
	return database
}

func queuedCount(t *testing.T, database *sql.DB, userID int) int {
	t.Helper()
	var count int
	if failure := database.QueryRow("SELECT COUNT(*) FROM download_queue WHERE user_id = ?", userID).Scan(&count); failure != nil {
		t.Fatalf("counting the queue: %v", failure)
	}
	return count
}

// The whole point of the exclusion: a design the user deleted on purpose must
// not come back with the next library sync.
func TestQueueIfNewSkipsAnExcludedDesign(t *testing.T) {
	database := exclusionTestDB(t)
	designURL := "https://www.printables.com/model/12345-cube"
	if _, failure := database.Exec(
		`INSERT INTO sync_exclusions (user_id, source_platform, source_id, source_url) VALUES (?, ?, ?, ?)`,
		1, "printables", "12345", designURL); failure != nil {
		t.Fatalf("noting the exclusion: %v", failure)
	}

	if queueID := queueIfNew(database, designURL, "printables", 1); queueID != 0 {
		t.Fatalf("the design was queued as %d", queueID)
	}
	if count := queuedCount(t, database, 1); count != 0 {
		t.Fatalf("%d entries are in the queue", count)
	}
}

// The exclusion is written from the design row, where the id is known. The sync
// sees only a URL, so matching has to work from the id it extracts.
func TestQueueIfNewSkipsAnExclusionStoredWithoutTheURL(t *testing.T) {
	database := exclusionTestDB(t)
	if _, failure := database.Exec(
		`INSERT INTO sync_exclusions (user_id, source_platform, source_id) VALUES (?, ?, ?)`,
		1, "printables", "12345"); failure != nil {
		t.Fatalf("noting the exclusion: %v", failure)
	}

	if queueID := queueIfNew(database, "https://www.printables.com/model/12345-cube", "printables", 1); queueID != 0 {
		t.Fatalf("the design was queued as %d", queueID)
	}
}

// Another user's exclusion says nothing about this library.
func TestQueueIfNewIgnoresAForeignExclusion(t *testing.T) {
	database := exclusionTestDB(t)
	designURL := "https://www.printables.com/model/12345-cube"
	// The exclusion references its owner, so that user has to exist.
	if _, failure := database.Exec(
		`INSERT INTO users (id, hash, name) VALUES (2, 'x', 'second')`); failure != nil {
		t.Fatalf("creating the second user: %v", failure)
	}
	if _, failure := database.Exec(
		`INSERT INTO sync_exclusions (user_id, source_platform, source_id, source_url) VALUES (?, ?, ?, ?)`,
		2, "printables", "12345", designURL); failure != nil {
		t.Fatalf("noting the exclusion: %v", failure)
	}

	if queueID := queueIfNew(database, designURL, "printables", 1); queueID == 0 {
		t.Fatal("the design was skipped although the exclusion belongs to another user")
	}
}

func TestQueueIfNewQueuesADesignThatWasNeverExcluded(t *testing.T) {
	database := exclusionTestDB(t)

	if queueID := queueIfNew(database, "https://www.printables.com/model/12345-cube", "printables", 1); queueID == 0 {
		t.Fatal("the design was not queued")
	}
	if count := queuedCount(t, database, 1); count != 1 {
		t.Fatalf("%d entries are in the queue", count)
	}
}

// Importing the design by hand is the way back; afterwards the sync may bring
// it again.
func TestLiftSyncExclusionUnblocksTheDesign(t *testing.T) {
	database := exclusionTestDB(t)
	designURL := "https://www.printables.com/model/12345-cube"
	if _, failure := database.Exec(
		`INSERT INTO sync_exclusions (user_id, source_platform, source_id, source_url) VALUES (?, ?, ?, ?)`,
		1, "printables", "12345", designURL); failure != nil {
		t.Fatalf("noting the exclusion: %v", failure)
	}

	LiftSyncExclusion(database, 1, "printables", "", designURL)

	if IsExcludedFromSync(database, 1, "printables", "12345", designURL) {
		t.Fatal("the design is still excluded")
	}
	if queueID := queueIfNew(database, designURL, "printables", 1); queueID == 0 {
		t.Fatal("the design was not queued after the exclusion was lifted")
	}
}
