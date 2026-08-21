package api

import (
	"fmt"
	"net/http"
	"testing"

	"meshdepot/internal/publicid"
)

// insertPlatformDesign creates a design that came from a platform, which is the
// only kind the library sync can hand back.
func (testHarness *harness) insertPlatformDesign(ownerID int, name, platform, sourceID, sourceURL string) int {
	testHarness.t.Helper()
	result, failure := testHarness.database.Exec(
		`INSERT INTO designs (user_id, public_id, name, source_platform, source_id, source_url)
		 VALUES (?, ?, ?, ?, ?, ?)`, ownerID, publicid.New(), name, platform, sourceID, sourceURL)
	if failure != nil {
		testHarness.t.Fatalf("insert design: %v", failure)
	}
	id, _ := result.LastInsertId()
	return int(id)
}

func (testHarness *harness) exclusionCount() int {
	testHarness.t.Helper()
	return testHarness.count("SELECT COUNT(*) FROM sync_exclusions WHERE user_id = ?", testHarness.userID)
}

func designPath(publicDesignID string) string {
	return fmt.Sprintf("/api/v1/designs/%s", publicDesignID)
}

// Deleting with the box ticked has to survive the next library sync, so the
// origin of the design is noted before the row goes.
func TestDesignsDestroyRecordsTheSyncExclusion(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertPlatformDesign(testHarness.userID, "Cube", "printables", "12345",
		"https://www.printables.com/model/12345-cube")

	answer := testHarness.asUser(http.MethodDelete, designPath(testHarness.designPID(designID))+"?exclude_from_sync=1", nil)

	if answer.status != http.StatusOK {
		t.Fatalf("the delete answered %d: %s", answer.status, answer.rawBody)
	}
	if count := testHarness.exclusionCount(); count != 1 {
		t.Fatalf("%d exclusions were noted", count)
	}
	platform := testHarness.scalar("SELECT source_platform FROM sync_exclusions WHERE user_id = ?", testHarness.userID)
	sourceID := testHarness.scalar("SELECT source_id FROM sync_exclusions WHERE user_id = ?", testHarness.userID)
	if platform != "printables" || sourceID != "12345" {
		t.Fatalf("the exclusion notes %s/%s", platform, sourceID)
	}
}

// Without the parameter nothing is noted: deleting a design is not by itself a
// decision about the sync.
func TestDesignsDestroyWithoutTheFlagNotesNothing(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertPlatformDesign(testHarness.userID, "Cube", "printables", "12345",
		"https://www.printables.com/model/12345-cube")

	testHarness.asUser(http.MethodDelete, designPath(testHarness.designPID(designID)), nil)

	if count := testHarness.exclusionCount(); count != 0 {
		t.Fatalf("%d exclusions were noted", count)
	}
}

// A design the user uploaded themselves has no platform origin, so there is
// nothing a sync could bring back and nothing to note.
func TestDesignsDestroyNotesNothingForAManualDesign(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Selbstgebaut")

	testHarness.asUser(http.MethodDelete, designPath(testHarness.designPID(designID))+"?exclude_from_sync=1", nil)

	if count := testHarness.exclusionCount(); count != 0 {
		t.Fatalf("%d exclusions were noted", count)
	}
}

// Someone else's design is refused, and the refusal must not leave a note
// behind either.
func TestDesignsDestroyNotesNothingForAForeignDesign(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertPlatformDesign(testHarness.adminID, "Fremd", "printables", "999",
		"https://www.printables.com/model/999-fremd")

	answer := testHarness.asUser(http.MethodDelete, designPath(testHarness.designPID(designID))+"?exclude_from_sync=1", nil)

	if answer.status == http.StatusOK {
		t.Fatalf("the foreign design was deleted: %s", answer.rawBody)
	}
	if count := testHarness.count("SELECT COUNT(*) FROM sync_exclusions", nil...); count != 0 {
		t.Fatalf("%d exclusions were noted", count)
	}
}
