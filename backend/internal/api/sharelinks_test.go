package api

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"meshdepot/internal/coerce"
	"meshdepot/internal/publicid"
)

// linkPath is the owner's endpoint for a design's links.
func linkPath(testHarness *harness, designID int) string {
	return fmt.Sprintf("/api/v1/designs/%s/links", testHarness.designPID(designID))
}

// createShareLink makes a link and returns its token.
func createShareLink(t *testing.T, testHarness *harness, designID, hours int) string {
	t.Helper()
	answer := testHarness.asUser(http.MethodPost, linkPath(testHarness, designID),
		map[string]any{"expires_in_hours": hours})
	if answer.status != http.StatusCreated {
		t.Fatalf("creating a link answered %d: %s", answer.status, answer.rawBody)
	}
	token := coerce.StringOr(answer.data(t)["token"], "")
	if !publicid.Valid(token) {
		t.Fatalf("the token is not an opaque id: %q", token)
	}
	return token
}

func TestShareLinkOpensTheDesignWithoutASession(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Oeffentlich")
	testHarness.setDesignFields(designID, map[string]any{"description": "Eine Beschreibung", "author": "Jemand"})
	token := createShareLink(t, testHarness, designID, 24)

	answer := testHarness.anonymous(http.MethodGet, "/api/v1/public/share/"+token, nil)

	if answer.status != http.StatusOK {
		t.Fatalf("the link answered %d: %s", answer.status, answer.rawBody)
	}
	shared := answer.data(t)
	if shared["name"] != "Oeffentlich" || shared["description"] != "Eine Beschreibung" {
		t.Fatalf("unexpected payload: %s", answer.rawBody)
	}
}

// The view is assembled field by field. Handing out the design row would carry
// the owner's notes and every column added later to a stranger.
func TestShareLinkCarriesNeitherNotesNorIdentifiers(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Mit Notizen")
	testHarness.setDesignFields(designID, map[string]any{"notes": "Nur fuer mich"})
	token := createShareLink(t, testHarness, designID, 0)

	answer := testHarness.anonymous(http.MethodGet, "/api/v1/public/share/"+token, nil)

	shared := answer.data(t)
	for _, forbidden := range []string{"notes", "id", "public_id", "user_id", "is_hidden"} {
		if _, present := shared[forbidden]; present {
			t.Errorf("the public view carries %q: %s", forbidden, answer.rawBody)
		}
	}
}

func TestShareLinkRefusesAnExpiredToken(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Abgelaufen")
	token := createShareLink(t, testHarness, designID, 24)
	if _, failure := testHarness.database.Exec(
		"UPDATE design_share_links SET expires_at = datetime('now', '-1 hour') WHERE token = ?", token); failure != nil {
		t.Fatalf("age the link: %v", failure)
	}

	answer := testHarness.anonymous(http.MethodGet, "/api/v1/public/share/"+token, nil)

	if answer.status != http.StatusNotFound {
		t.Fatalf("an expired link answered %d", answer.status)
	}
}

// A link without an expiry runs until it is deleted - that is what "unlimited"
// means, and it must not be mistaken for "already expired".
func TestShareLinkWithoutAnExpiryKeepsWorking(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Unbegrenzt")
	token := createShareLink(t, testHarness, designID, 0)

	if answer := testHarness.anonymous(http.MethodGet, "/api/v1/public/share/"+token, nil); answer.status != http.StatusOK {
		t.Fatalf("an unlimited link answered %d", answer.status)
	}
}

func TestShareLinkStopsWorkingOnceDeleted(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Widerrufen")
	token := createShareLink(t, testHarness, designID, 24)
	links := testHarness.asUser(http.MethodGet, linkPath(testHarness, designID), nil).list(t)
	linkID := coerce.Int(links[0].(map[string]any)["id"])

	revoke := testHarness.asUser(http.MethodDelete,
		fmt.Sprintf("%s/%d", linkPath(testHarness, designID), linkID), nil)

	if revoke.status != http.StatusOK {
		t.Fatalf("deleting the link answered %d", revoke.status)
	}
	if answer := testHarness.anonymous(http.MethodGet, "/api/v1/public/share/"+token, nil); answer.status != http.StatusNotFound {
		t.Fatalf("a deleted link still answered %d", answer.status)
	}
}

// An unknown token has to look exactly like an expired or deleted one -
// anything else confirms that a design is behind it.
func TestShareLinkAnswersEveryBadTokenTheSameWay(t *testing.T) {
	testHarness := newHarness(t)

	for _, token := range []string{publicid.New(), "nonsense", ""} {
		answer := testHarness.anonymous(http.MethodGet, "/api/v1/public/share/"+token, nil)
		if answer.status != http.StatusNotFound {
			t.Fatalf("the token %q answered %d", token, answer.status)
		}
	}
}

func TestShareLinkDownloadsAFileAndRefusesAForeignOne(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Mit Datei")
	testHarness.insertFileVersion(designID, 10, "teil.stl")
	entryID := testHarness.scalarInt("SELECT id FROM design_file_entries ORDER BY id DESC LIMIT 1")
	if failure := os.WriteFile(filepath.Join(testHarness.dataRoot, "teil.stl"), []byte("solid teil"), 0o644); failure != nil {
		t.Fatalf("write the shared file: %v", failure)
	}
	token := createShareLink(t, testHarness, designID, 24)

	// The listing names the file.
	shared := testHarness.anonymous(http.MethodGet, "/api/v1/public/share/"+token, nil).data(t)
	files := shared["files"].([]any)
	if len(files) != 1 || files[0].(map[string]any)["filename"] != "teil.stl" {
		t.Fatalf("the file list is %v", files)
	}

	download := testHarness.anonymous(http.MethodGet,
		fmt.Sprintf("/api/v1/public/share/%s/files/%d", token, entryID), nil)
	if download.status != http.StatusOK {
		t.Fatalf("the download answered %d: %s", download.status, download.rawBody)
	}
	if download.rawBody != "solid teil" {
		t.Fatalf("the download carries %q", download.rawBody)
	}

	// A file of another design is not reachable through this token, even though
	// the token itself is valid.
	foreignDesign := testHarness.insertDesign(testHarness.adminID, "Fremd")
	testHarness.insertFileVersion(foreignDesign, 10, "fremd.stl")
	foreignEntry := testHarness.scalarInt("SELECT id FROM design_file_entries ORDER BY id DESC LIMIT 1")
	foreign := testHarness.anonymous(http.MethodGet,
		fmt.Sprintf("/api/v1/public/share/%s/files/%d", token, foreignEntry), nil)
	if foreign.status != http.StatusNotFound {
		t.Fatalf("a foreign file answered %d", foreign.status)
	}
}

func TestShareLinksAreRefusedForAForeignDesign(t *testing.T) {
	testHarness := newHarness(t)
	foreign := testHarness.insertDesign(testHarness.adminID, "Fremd")

	answer := testHarness.asUser(http.MethodPost, linkPath(testHarness, foreign),
		map[string]any{"expires_in_hours": 24})

	if answer.status != http.StatusNotFound {
		t.Fatalf("a link for a foreign design answered %d", answer.status)
	}
	if testHarness.count("SELECT COUNT(*) FROM design_share_links") != 0 {
		t.Fatal("the link was created anyway")
	}
}

// The owner picks the lifetime, so an odd number of hours is fine. Only the
// nonsensical ones are refused - the cap keeps an absurd date out of the column.
func TestShareLinkTakesAnyReasonableLifetime(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Krumme Frist")

	if answer := testHarness.asUser(http.MethodPost, linkPath(testHarness, designID),
		map[string]any{"expires_in_hours": 5}); answer.status != http.StatusCreated {
		t.Fatalf("five hours answered %d: %s", answer.status, answer.rawBody)
	}

	for _, hours := range []int{-1, maxShareLinkHours + 1} {
		answer := testHarness.asUser(http.MethodPost, linkPath(testHarness, designID),
			map[string]any{"expires_in_hours": hours})
		if answer.status != http.StatusUnprocessableEntity {
			t.Fatalf("%d hours answered %d", hours, answer.status)
		}
		if key := answer.errorKey(t); key != "error.invalid_share_expiry" {
			t.Fatalf("unexpected error key %q", key)
		}
	}
}

// Everything at once is what someone handed a link usually wants; clicking each
// file is the alternative.
func TestShareLinkDownloadsEverythingAsOneArchive(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Mit Dateien")
	testHarness.insertFileVersion(designID, 10, "teil.stl")
	if failure := os.WriteFile(filepath.Join(testHarness.dataRoot, "teil.stl"), []byte("solid teil"), 0o644); failure != nil {
		t.Fatalf("write the shared file: %v", failure)
	}
	token := createShareLink(t, testHarness, designID, 24)

	answer := testHarness.anonymous(http.MethodGet, "/api/v1/public/share/"+token+"/download", nil)

	if answer.status != http.StatusOK {
		t.Fatalf("the archive answered %d: %s", answer.status, answer.rawBody)
	}
	// A single file is served as that file rather than as a one-entry archive.
	if answer.rawBody != "solid teil" {
		t.Fatalf("unexpected body %q", answer.rawBody)
	}

	// And it is refused once the link is gone.
	testHarness.database.Exec("DELETE FROM design_share_links WHERE token = ?", token)
	if gone := testHarness.anonymous(http.MethodGet, "/api/v1/public/share/"+token+"/download", nil); gone.status != http.StatusNotFound {
		t.Fatalf("the archive of a revoked link answered %d", gone.status)
	}
}

// The account view collects the links of every design in one place.
func TestUserShareLinksListsThemAcrossDesigns(t *testing.T) {
	testHarness := newHarness(t)
	firstDesign := testHarness.insertDesign(testHarness.userID, "Erstes")
	secondDesign := testHarness.insertDesign(testHarness.userID, "Zweites")
	createShareLink(t, testHarness, firstDesign, 24)
	createShareLink(t, testHarness, secondDesign, 0)
	foreign := testHarness.insertDesign(testHarness.adminID, "Fremd")
	testHarness.insertShareLink(foreign, testHarness.adminID)

	links := testHarness.asUser(http.MethodGet,
		"/api/v1/users/"+testHarness.publicID(testHarness.userID)+"/share-links", nil).list(t)

	if len(links) != 2 {
		t.Fatalf("%d links were listed, expected the caller's two", len(links))
	}
	names := map[string]bool{}
	for _, entry := range links {
		row := entry.(map[string]any)
		names[coerce.StringOr(row["design_name"], "")] = true
		// The row names its design so the existing delete can revoke it, and the
		// link's own id must survive next to it.
		if !publicid.Valid(coerce.StringOr(row["design_id"], "")) {
			t.Fatalf("design_id is not a public id: %v", row["design_id"])
		}
		if coerce.Int(row["id"]) == 0 {
			t.Fatalf("the link lost its id: %v", row)
		}
	}
	if !names["Erstes"] || !names["Zweites"] {
		t.Fatalf("unexpected designs: %v", names)
	}
}

// The owner sees that a link is being used, without the app recording who used
// it.
func TestShareLinkCountsItsViews(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Gezaehlt")
	token := createShareLink(t, testHarness, designID, 24)

	testHarness.anonymous(http.MethodGet, "/api/v1/public/share/"+token, nil)
	testHarness.anonymous(http.MethodGet, "/api/v1/public/share/"+token, nil)

	links := testHarness.asUser(http.MethodGet, linkPath(testHarness, designID), nil).list(t)
	if views := coerce.Int(links[0].(map[string]any)["view_count"]); views != 2 {
		t.Fatalf("the link counted %d views", views)
	}
}
