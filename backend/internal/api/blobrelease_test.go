package api

import (
	"fmt"
	"net/http"
	"os"
	"testing"

	"meshdepot/internal/coerce"
)

// blobOf resolves the blob path behind an entry of a version.
func (testHarness *harness) blobOf(versionID int) string {
	testHarness.t.Helper()
	hash := testHarness.scalar("SELECT blob_hash FROM design_file_entries WHERE design_file_id = ? LIMIT 1", versionID)
	if hash == "" {
		testHarness.t.Fatalf("version %d has no blob hash", versionID)
	}
	return testHarness.userLayout(testHarness.userID).Blob(hash)
}

// A version keeps the content of the others: the blob only goes with the last
// link to it, which is what the link count is for.
func TestFilesDestroyReleasesTheBlobOnlyWithTheLastVersion(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Zwei Versionen")
	first := coerce.Int(testHarness.uploadVersion(designID, "1.0", "", map[string][]byte{"wuerfel.stl": stlBytes}).data(t)["id"])
	second := coerce.Int(testHarness.uploadVersion(designID, "2.0", "", map[string][]byte{"wuerfel.stl": stlBytes}).data(t)["id"])
	blob := testHarness.blobOf(first)

	answer := testHarness.asUser(http.MethodDelete, fmt.Sprintf("/api/v1/designs/%s/files/%d", testHarness.designPID(designID), second), nil)
	if answer.status != http.StatusOK {
		t.Fatalf("deleting 2.0 answered %d: %s", answer.status, answer.rawBody)
	}
	if _, failure := os.Stat(blob); failure != nil {
		t.Fatalf("the blob went although version 1.0 still links it: %v", failure)
	}

	answer = testHarness.asUser(http.MethodDelete, fmt.Sprintf("/api/v1/designs/%s/files/%d", testHarness.designPID(designID), first), nil)
	if answer.status != http.StatusOK {
		t.Fatalf("deleting 1.0 answered %d: %s", answer.status, answer.rawBody)
	}
	if _, failure := os.Stat(blob); !os.IsNotExist(failure) {
		t.Fatalf("the last version is gone but the blob stayed: %v", failure)
	}
}

func TestDesignsDestroyReleasesTheBlobs(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Zum Loeschen")
	versionID := coerce.Int(testHarness.uploadVersion(designID, "1.0", "", map[string][]byte{"wuerfel.stl": stlBytes}).data(t)["id"])
	blob := testHarness.blobOf(versionID)
	designDir := testHarness.userLayout(testHarness.userID).Design(designID)

	answer := testHarness.asUser(http.MethodDelete, fmt.Sprintf("/api/v1/designs/%s", testHarness.designPID(designID)), nil)
	if answer.status != http.StatusOK {
		t.Fatalf("the deletion answered %d: %s", answer.status, answer.rawBody)
	}
	if _, failure := os.Stat(designDir); !os.IsNotExist(failure) {
		t.Fatalf("the design directory survived: %v", failure)
	}
	if _, failure := os.Stat(blob); !os.IsNotExist(failure) {
		t.Fatalf("the blob survived the deletion: %v", failure)
	}
}

// Content shared with a second design must not go with the first: the two
// designs link one blob, and only the second delete may remove it.
func TestDesignsDestroyKeepsContentAnotherDesignStillUses(t *testing.T) {
	testHarness := newHarness(t)
	firstDesign := testHarness.insertDesign(testHarness.userID, "Erstes")
	secondDesign := testHarness.insertDesign(testHarness.userID, "Zweites")
	firstVersion := coerce.Int(testHarness.uploadVersion(firstDesign, "1.0", "", map[string][]byte{"wuerfel.stl": stlBytes}).data(t)["id"])
	testHarness.uploadVersion(secondDesign, "1.0", "", map[string][]byte{"wuerfel.stl": stlBytes})
	blob := testHarness.blobOf(firstVersion)

	if answer := testHarness.asUser(http.MethodDelete, fmt.Sprintf("/api/v1/designs/%s", testHarness.designPID(firstDesign)), nil); answer.status != http.StatusOK {
		t.Fatalf("the deletion answered %d: %s", answer.status, answer.rawBody)
	}
	if _, failure := os.Stat(blob); failure != nil {
		t.Fatalf("the blob went although the second design still links it: %v", failure)
	}

	if answer := testHarness.asUser(http.MethodDelete, fmt.Sprintf("/api/v1/designs/%s", testHarness.designPID(secondDesign)), nil); answer.status != http.StatusOK {
		t.Fatalf("the second deletion answered %d: %s", answer.status, answer.rawBody)
	}
	if _, failure := os.Stat(blob); !os.IsNotExist(failure) {
		t.Fatalf("both designs are gone but the blob stayed: %v", failure)
	}
}

// Deleting a single file used to leave it in the version directory: the row was
// the only thing that went, and the hard link kept the content alive.
func TestFilesDeleteEntryRemovesTheFileAndItsBlob(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Zwei Dateien")
	created := testHarness.uploadVersion(designID, "1.0", "", map[string][]byte{
		"wuerfel.stl": stlBytes,
		"notiz.txt":   []byte("keep me"),
	})
	versionID := coerce.Int(created.data(t)["id"])
	entryID := testHarness.scalarInt(
		"SELECT id FROM design_file_entries WHERE design_file_id = ? AND filename = 'notiz.txt'", versionID)
	published := testHarness.storedFile("SELECT path FROM design_file_entries WHERE id = ?", entryID)
	blob := testHarness.userLayout(testHarness.userID).Blob(
		testHarness.scalar("SELECT blob_hash FROM design_file_entries WHERE id = ?", entryID))

	answer := testHarness.asUser(http.MethodDelete,
		fmt.Sprintf("/api/v1/designs/%s/files/%d/entry/%d", testHarness.designPID(designID), versionID, entryID), nil)
	if answer.status != http.StatusOK {
		t.Fatalf("the deletion answered %d: %s", answer.status, answer.rawBody)
	}
	if _, failure := os.Stat(published); !os.IsNotExist(failure) {
		t.Fatalf("the published file survived the entry: %v", failure)
	}
	if _, failure := os.Stat(blob); !os.IsNotExist(failure) {
		t.Fatalf("the blob survived the entry: %v", failure)
	}
	// The other file of the same version is untouched.
	remaining := testHarness.storedFile(
		"SELECT path FROM design_file_entries WHERE design_file_id = ? AND filename = 'wuerfel.stl'", versionID)
	if _, failure := os.Stat(remaining); failure != nil {
		t.Fatalf("the remaining file went too: %v", failure)
	}
}
