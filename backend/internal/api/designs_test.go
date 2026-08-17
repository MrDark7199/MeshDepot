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

// indexItems runs DesignsIndex with the given query string and returns the item
// list together with the reported total.
func (testHarness *harness) indexItems(query string) ([]map[string]any, int) {
	testHarness.t.Helper()
	answer := testHarness.asUser(http.MethodGet, "/api/v1/designs"+query, nil)
	if answer.status != http.StatusOK {
		testHarness.t.Fatalf("the index answered %d: %s", answer.status, answer.rawBody)
	}
	payload := answer.data(testHarness.t)
	rows := []map[string]any{}
	for _, item := range payload["items"].([]any) {
		rows = append(rows, item.(map[string]any))
	}
	return rows, coerce.Int(payload["total"])
}

// names collects the design names of an item list in order.
func names(rows []map[string]any) []string {
	result := make([]string, 0, len(rows))
	for _, row := range rows {
		result = append(result, coerce.StringOr(row["name"], ""))
	}
	return result
}

func TestDesignsIndexReturnsOwnDesignsWithTagsAttached(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Eigenes Design")
	untagged := testHarness.insertDesign(testHarness.userID, "Ohne Tag")
	testHarness.insertDesign(testHarness.adminID, "Fremdes Design")
	tagID := testHarness.insertTag(testHarness.userID, "Werkzeug", "manual")
	testHarness.attachTag(designID, tagID)

	rows, total := testHarness.indexItems("")

	if total != 2 {
		t.Fatalf("total is %d, expected the two own designs", total)
	}
	byID := map[string]map[string]any{}
	for _, row := range rows {
		byID[coerce.StringOr(row["id"], "")] = row
	}
	tags := byID[testHarness.designPID(designID)]["tags"].([]any)
	if len(tags) != 1 || tags[0].(map[string]any)["name"] != "Werkzeug" {
		t.Fatalf("the tag was not attached: %v", byID[testHarness.designPID(designID)]["tags"])
	}
	if list := byID[testHarness.designPID(untagged)]["tags"].([]any); len(list) != 0 {
		t.Fatalf("a design without tags carries %v", list)
	}
	if coerce.Int(byID[testHarness.designPID(designID)]["is_shared"]) != 0 {
		t.Fatal("an own design is marked as shared")
	}
}

func TestDesignsIndexHidesHiddenDesignsUnlessAsked(t *testing.T) {
	testHarness := newHarness(t)
	testHarness.insertDesign(testHarness.userID, "Sichtbar")
	hidden := testHarness.insertDesign(testHarness.userID, "Versteckt")
	testHarness.setDesignFields(hidden, map[string]any{"is_hidden": 1})

	visible, _ := testHarness.indexItems("")
	if len(visible) != 1 || visible[0]["name"] != "Sichtbar" {
		t.Fatalf("the hidden design leaked into the list: %v", names(visible))
	}

	all, _ := testHarness.indexItems("?show_hidden=1")
	if len(all) != 2 {
		t.Fatalf("show_hidden returned %d designs", len(all))
	}
}

func TestDesignsIndexSearchesNameAuthorAndDescription(t *testing.T) {
	testHarness := newHarness(t)
	byName := testHarness.insertDesign(testHarness.userID, "Zahnrad klein")
	byAuthor := testHarness.insertDesign(testHarness.userID, "Irgendwas")
	byDescription := testHarness.insertDesign(testHarness.userID, "Noch was")
	testHarness.insertDesign(testHarness.userID, "Voellig unbeteiligt")
	testHarness.setDesignFields(byAuthor, map[string]any{"author": "Zahnradbauer"})
	testHarness.setDesignFields(byDescription, map[string]any{"description": "Ein Zahnrad im Text"})

	rows, total := testHarness.indexItems("?search=zahnrad")

	if total != 3 {
		t.Fatalf("total is %d, expected three hits", total)
	}
	found := map[string]bool{}
	for _, row := range rows {
		found[coerce.StringOr(row["id"], "")] = true
	}
	if !found[testHarness.designPID(byName)] || !found[testHarness.designPID(byAuthor)] || !found[testHarness.designPID(byDescription)] {
		t.Fatalf("the search missed a column: %v", found)
	}
}

func TestDesignsIndexEscapesSearchWildcards(t *testing.T) {
	testHarness := newHarness(t)
	testHarness.insertDesign(testHarness.userID, "Fuellgrad 50% Test")
	testHarness.insertDesign(testHarness.userID, "Ohne Angabe")

	rows, total := testHarness.indexItems("?search=50%25")

	if total != 1 || len(rows) != 1 || rows[0]["name"] != "Fuellgrad 50% Test" {
		t.Fatalf("the wildcard was not escaped: %v", names(rows))
	}
}

func TestDesignsIndexFiltersByPlatform(t *testing.T) {
	testHarness := newHarness(t)
	thingiverse := testHarness.insertDesign(testHarness.userID, "Von Thingiverse")
	testHarness.setDesignFields(thingiverse, map[string]any{"source_platform": "thingiverse"})
	testHarness.insertDesign(testHarness.userID, "Manuell")

	viaSourcePlatform, _ := testHarness.indexItems("?source_platform=thingiverse")
	if len(viaSourcePlatform) != 1 || viaSourcePlatform[0]["id"] != testHarness.designPID(thingiverse) {
		t.Fatalf("source_platform did not filter: %v", names(viaSourcePlatform))
	}

	// The frontend still sends the older parameter name on some screens.
	viaPlatform, _ := testHarness.indexItems("?platform=thingiverse")
	if len(viaPlatform) != 1 || viaPlatform[0]["id"] != testHarness.designPID(thingiverse) {
		t.Fatalf("platform did not filter: %v", names(viaPlatform))
	}
}

func TestDesignsIndexSortsByName(t *testing.T) {
	testHarness := newHarness(t)
	testHarness.insertDesign(testHarness.userID, "beta")
	testHarness.insertDesign(testHarness.userID, "Alpha")
	testHarness.insertDesign(testHarness.userID, "Gamma")

	ascending, _ := testHarness.indexItems("?sort=name&dir=asc")
	if got := names(ascending); got[0] != "Alpha" || got[1] != "beta" || got[2] != "Gamma" {
		t.Fatalf("ascending order is %v - the comparison is not case-insensitive", got)
	}

	descending, _ := testHarness.indexItems("?sort=name&dir=desc")
	if got := names(descending); got[0] != "Gamma" || got[2] != "Alpha" {
		t.Fatalf("descending order is %v", got)
	}
}

// An unknown sort field or direction must not reach the SQL - it falls back to
// the default instead.
func TestDesignsIndexIgnoresAnUnknownSortField(t *testing.T) {
	testHarness := newHarness(t)
	testHarness.insertDesign(testHarness.userID, "Alpha")
	testHarness.insertDesign(testHarness.userID, "Beta")

	rows, total := testHarness.indexItems("?sort=name);DROP+TABLE+designs;--&dir=sideways")

	if total != 2 || len(rows) != 2 {
		t.Fatalf("the fallback did not return both designs: %s", names(rows))
	}
	if testHarness.count("SELECT COUNT(*) FROM designs") != 2 {
		t.Fatal("the designs table did not survive the sort parameter")
	}
}

func TestDesignsIndexPaginates(t *testing.T) {
	testHarness := newHarness(t)
	for index := 0; index < 5; index++ {
		testHarness.insertDesign(testHarness.userID, fmt.Sprintf("Design %d", index))
	}

	firstPage, total := testHarness.indexItems("?per_page=2&page=1")
	if total != 5 {
		t.Fatalf("total is %d although a page holds two rows", total)
	}
	if len(firstPage) != 2 {
		t.Fatalf("the first page holds %d rows", len(firstPage))
	}

	lastPage, _ := testHarness.indexItems("?per_page=2&page=3")
	if len(lastPage) != 1 {
		t.Fatalf("the last page holds %d rows", len(lastPage))
	}

	clamped, _ := testHarness.indexItems("?per_page=0&page=-3")
	if len(clamped) != 1 {
		t.Fatalf("per_page=0 returned %d rows instead of the minimum of one", len(clamped))
	}
}

func TestDesignsIndexIncludesSharedDesigns(t *testing.T) {
	testHarness := newHarness(t)
	own := testHarness.insertDesign(testHarness.userID, "Eigen")
	shared := testHarness.insertDesign(testHarness.adminID, "Geteilt")
	testHarness.insertDesign(testHarness.adminID, "Nicht geteilt")
	testHarness.shareDesign(shared, testHarness.adminID, testHarness.userID)

	rows, total := testHarness.indexItems("")

	if total != 2 {
		t.Fatalf("total is %d, expected the own and the shared design", total)
	}
	byID := map[string]map[string]any{}
	for _, row := range rows {
		byID[coerce.StringOr(row["id"], "")] = row
	}
	if byID[testHarness.designPID(own)] == nil || byID[testHarness.designPID(shared)] == nil {
		t.Fatalf("unexpected designs: %v", names(rows))
	}
	if coerce.Int(byID[testHarness.designPID(shared)]["is_shared"]) != 1 {
		t.Fatal("the shared design is not marked as shared")
	}
	if byID[testHarness.designPID(shared)]["shared_by_name"] != "administrator" {
		t.Fatalf("the owner name is missing: %v", byID[testHarness.designPID(shared)]["shared_by_name"])
	}
}

func TestDesignsIndexWithSharedOnlyLeavesOutOwnDesigns(t *testing.T) {
	testHarness := newHarness(t)
	testHarness.insertDesign(testHarness.userID, "Eigen")
	shared := testHarness.insertDesign(testHarness.adminID, "Geteilt")
	testHarness.shareDesign(shared, testHarness.adminID, testHarness.userID)

	rows, total := testHarness.indexItems("?shared_only=1")

	if total != 1 || len(rows) != 1 {
		t.Fatalf("shared_only returned %d of %d designs", len(rows), total)
	}
	if rows[0]["id"] != testHarness.designPID(shared) {
		t.Fatalf("shared_only returned design %v", rows[0]["id"])
	}
}

// since_id went with the numeric ids: it filtered by rowid, and no client is
// told a rowid any more. A leftover value must not quietly change the page.
func TestDesignsIndexIgnoresTheRetiredSinceIDParameter(t *testing.T) {
	testHarness := newHarness(t)
	for index := 0; index < 5; index++ {
		testHarness.insertDesign(testHarness.userID, fmt.Sprintf("Design %d", index))
	}

	rows, total := testHarness.indexItems("?since_id=3&per_page=2")

	if total != 5 || len(rows) != 2 {
		t.Fatalf("since_id still filters: %d rows of %d, expected one page of two out of five", len(rows), total)
	}
}

func TestDesignsIndexFiltersByTagsWithAndSemantics(t *testing.T) {
	testHarness := newHarness(t)
	bothTags := testHarness.insertDesign(testHarness.userID, "Beide")
	oneTag := testHarness.insertDesign(testHarness.userID, "Nur einer")
	firstTag := testHarness.insertTag(testHarness.userID, "Werkzeug", "manual")
	secondTag := testHarness.insertTag(testHarness.userID, "Ersatzteil", "manual")
	testHarness.attachTag(bothTags, firstTag)
	testHarness.attachTag(bothTags, secondTag)
	testHarness.attachTag(oneTag, firstTag)

	rows, _ := testHarness.indexItems(fmt.Sprintf("?tag_ids=%d,%d", firstTag, secondTag))

	if len(rows) != 1 || rows[0]["id"] != testHarness.designPID(bothTags) {
		t.Fatalf("the tag filter is not an AND: %v", names(rows))
	}
}

func TestDesignsIndexRejectsTooManyTagFilters(t *testing.T) {
	testHarness := newHarness(t)
	filter := "1"
	for index := 0; index < maxTagFilters; index++ {
		filter += ",1"
	}

	answer := testHarness.asUser(http.MethodGet, "/api/v1/designs?tag_ids="+filter, nil)

	if answer.status != http.StatusUnprocessableEntity {
		t.Fatalf("an oversized tag filter answered %d", answer.status)
	}
	if key := answer.errorKey(t); key != "error.too_many_tag_filters" {
		t.Fatalf("unexpected error key %q", key)
	}
}

// An unparsable tag id used to become tag_id = 0 and silently return an empty
// list; it has to be a 422 instead.
func TestDesignsIndexRejectsAnUnusableTagID(t *testing.T) {
	testHarness := newHarness(t)

	for _, filter := range []string{"abc", "0", "-1", "1,abc"} {
		answer := testHarness.asUser(http.MethodGet, "/api/v1/designs?tag_ids="+filter, nil)
		if answer.status != http.StatusUnprocessableEntity {
			t.Fatalf("tag_ids=%q answered %d", filter, answer.status)
		}
		if key := answer.errorKey(t); key != "error.invalid_input" {
			t.Fatalf("tag_ids=%q produced the error key %q", filter, key)
		}
	}
}

// The promise of the public id: a design is addressed by it, and the rowid it
// replaces never travels. A response that still carried the numeric id would
// hand out exactly the sequential number the change exists to hide.
func TestDesignsAreAddressedByPublicIDOnly(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Mit oeffentlicher Id")
	publicID := testHarness.designPID(designID)

	detail := testHarness.asUser(http.MethodGet, fmt.Sprintf("/api/v1/designs/%s", publicID), nil).data(t)
	if detail["id"] != publicID {
		t.Fatalf("the detail view answers with the id %v", detail["id"])
	}
	if _, leaked := detail["public_id"]; leaked {
		t.Fatal("the response carries public_id next to id")
	}

	rows, _ := testHarness.indexItems("")
	if len(rows) != 1 || rows[0]["id"] != publicID {
		t.Fatalf("the list answers with %v", rows)
	}

	// The rowid is not an id the API accepts, whatever it happens to be.
	byRowID := testHarness.asUser(http.MethodGet, fmt.Sprintf("/api/v1/designs/%d", designID), nil)
	if byRowID.status != http.StatusNotFound {
		t.Fatalf("the rowid still opens the design: %d", byRowID.status)
	}
}

// An id of the right shape that names nothing is a 404, not a 500.
func TestDesignsAnswerAnUnknownPublicIDWithNotFound(t *testing.T) {
	testHarness := newHarness(t)

	for _, id := range []string{publicid.New(), "nonsense", ""} {
		answer := testHarness.asUser(http.MethodGet, "/api/v1/designs/"+id, nil)
		if answer.status != http.StatusNotFound {
			t.Fatalf("the id %q answered %d", id, answer.status)
		}
	}
}

func TestDesignsShowReturnsTagsImagesAndShares(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Mein Design")
	tagID := testHarness.insertTag(testHarness.userID, "Werkzeug", "manual")
	testHarness.attachTag(designID, tagID)
	testHarness.insertDesignImage(designID, "1/bild.jpg", 0)
	testHarness.shareDesign(designID, testHarness.userID, testHarness.adminID)

	answer := testHarness.asUser(http.MethodGet, fmt.Sprintf("/api/v1/designs/%s", testHarness.designPID(designID)), nil)

	if answer.status != http.StatusOK {
		t.Fatalf("show answered %d: %s", answer.status, answer.rawBody)
	}
	design := answer.data(t)
	if len(design["tags"].([]any)) != 1 {
		t.Fatalf("the tags are missing: %v", design["tags"])
	}
	if len(design["images"].([]any)) != 1 {
		t.Fatalf("the images are missing: %v", design["images"])
	}
	shares := design["shares"].([]any)
	if len(shares) != 1 || shares[0].(map[string]any)["shared_with_email"] != "admin@example.org" {
		t.Fatalf("the shares are missing: %v", design["shares"])
	}
	if coerce.Int(design["is_shared"]) != 0 {
		t.Fatal("the own design is marked as shared")
	}
}

// The recipient of a share sees the design, but not who else it was shared with.
func TestDesignsShowHidesTheShareListFromTheRecipient(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.adminID, "Geteilt")
	testHarness.shareDesign(designID, testHarness.adminID, testHarness.userID)

	design := testHarness.asUser(http.MethodGet, fmt.Sprintf("/api/v1/designs/%s", testHarness.designPID(designID)), nil).data(t)

	if coerce.Int(design["is_shared"]) != 1 {
		t.Fatal("the shared design is not marked as shared")
	}
	if design["shared_by_name"] != "administrator" {
		t.Fatalf("the owner name is missing: %v", design["shared_by_name"])
	}
	if shares := design["shares"].([]any); len(shares) != 0 {
		t.Fatalf("the recipient sees the share list: %v", shares)
	}
}

func TestDesignsShowRejectsAForeignDesign(t *testing.T) {
	testHarness := newHarness(t)
	foreign := testHarness.insertDesign(testHarness.adminID, "Fremd")

	answer := testHarness.asUser(http.MethodGet, fmt.Sprintf("/api/v1/designs/%s", testHarness.designPID(foreign)), nil)

	if answer.status != http.StatusNotFound {
		t.Fatalf("a foreign design answered %d", answer.status)
	}
	if key := answer.errorKey(t); key != "error.not_found" {
		t.Fatalf("unexpected error key %q", key)
	}
}

func TestDesignsShowWithANonNumericIDIsNotFound(t *testing.T) {
	testHarness := newHarness(t)

	answer := testHarness.asUser(http.MethodGet, "/api/v1/designs/not-a-number", nil)

	if answer.status != http.StatusNotFound {
		t.Fatalf("a non-numeric id answered %d", answer.status)
	}
}

func TestDesignsStoreCreatesADesignForTheCaller(t *testing.T) {
	testHarness := newHarness(t)

	answer := testHarness.asUser(http.MethodPost, "/api/v1/designs", map[string]any{
		"name":            "Neues Design",
		"description":     "Beschreibung",
		"source_url":      "https://www.thingiverse.com/thing:12345",
		"source_platform": "thingiverse",
		"source_id":       "12345",
		"category":        "Werkzeug",
		"license":         "CC-BY",
		"author":          "Jemand",
	})

	if answer.status != http.StatusCreated {
		t.Fatalf("store answered %d: %s", answer.status, answer.rawBody)
	}
	created := answer.data(t)
	if created["name"] != "Neues Design" || created["source_platform"] != "thingiverse" {
		t.Fatalf("unexpected design %s", answer.rawBody)
	}
	owner := testHarness.scalarInt("SELECT user_id FROM designs WHERE public_id = ?", created["id"])
	if owner != testHarness.userID {
		t.Fatalf("the design belongs to user %d instead of the caller", owner)
	}
}

func TestDesignsStoreDefaultsToTheManualPlatform(t *testing.T) {
	testHarness := newHarness(t)

	created := testHarness.asUser(http.MethodPost, "/api/v1/designs", map[string]any{"name": "Ohne Plattform"}).data(t)

	if created["source_platform"] != "manual" {
		t.Fatalf("the platform is %v", created["source_platform"])
	}
}

// nullStr has to turn blank optional fields into NULL - otherwise every
// "IS NOT NULL" check downstream has to carry an "AND != ”" along.
func TestDesignsStoreWritesBlankOptionalFieldsAsNull(t *testing.T) {
	testHarness := newHarness(t)

	created := testHarness.asUser(http.MethodPost, "/api/v1/designs", map[string]any{
		"name":        "Mit Leerfeldern",
		"description": "",
		"source_url":  "   ",
		"author":      nil,
	}).data(t)

	nullColumns := testHarness.scalarInt(
		"SELECT (description IS NULL) + (source_url IS NULL) + (author IS NULL) FROM designs WHERE public_id = ?", created["id"])
	if nullColumns != 3 {
		t.Fatalf("only %d of the three blank fields became NULL", nullColumns)
	}
}

func TestDesignsStoreRejectsABlankName(t *testing.T) {
	testHarness := newHarness(t)

	answer := testHarness.asUser(http.MethodPost, "/api/v1/designs", map[string]any{"name": "   "})

	if answer.status != http.StatusUnprocessableEntity {
		t.Fatalf("a blank name answered %d", answer.status)
	}
	if key := answer.errorKey(t); key != "error.name_required" {
		t.Fatalf("unexpected error key %q", key)
	}
	if testHarness.count("SELECT COUNT(*) FROM designs") != 0 {
		t.Fatal("a nameless design was created")
	}
}

func TestDesignsUpdateWritesTheWhitelistedFields(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Alt")

	answer := testHarness.asUser(http.MethodPut, fmt.Sprintf("/api/v1/designs/%s", testHarness.designPID(designID)), map[string]any{
		"name":     "Neu",
		"category": "Werkzeug",
		"notes":    "Mit 0,2 mm gedruckt",
	})

	if answer.status != http.StatusOK {
		t.Fatalf("update answered %d: %s", answer.status, answer.rawBody)
	}
	if name := testHarness.scalar("SELECT name FROM designs WHERE id = ?", designID); name != "Neu" {
		t.Fatalf("the name is %q", name)
	}
	if notes := testHarness.scalar("SELECT notes FROM designs WHERE id = ?", designID); notes != "Mit 0,2 mm gedruckt" {
		t.Fatalf("the notes are %q", notes)
	}
}

// The whitelist is the whole protection here: without it a caller could hand
// their design to somebody else by sending user_id.
func TestDesignsUpdateIgnoresFieldsOutsideTheWhitelist(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Alt")

	answer := testHarness.asUser(http.MethodPut, fmt.Sprintf("/api/v1/designs/%s", testHarness.designPID(designID)), map[string]any{
		"user_id": testHarness.adminID,
		"id":      9999,
	})

	if answer.status != http.StatusOK {
		t.Fatalf("update answered %d: %s", answer.status, answer.rawBody)
	}
	if owner := testHarness.scalarInt("SELECT user_id FROM designs WHERE id = ?", designID); owner != testHarness.userID {
		t.Fatalf("the design was handed to user %d", owner)
	}
}

func TestDesignsUpdateCoercesTheNumericFields(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Alt")

	testHarness.asUser(http.MethodPut, fmt.Sprintf("/api/v1/designs/%s", testHarness.designPID(designID)), map[string]any{
		"rating":             "4",
		"is_hidden":          true,
		"print_time_minutes": "",
	})

	if rating := testHarness.scalarInt("SELECT rating FROM designs WHERE id = ?", designID); rating != 4 {
		t.Fatalf("the rating is %d", rating)
	}
	if hidden := testHarness.scalarInt("SELECT is_hidden FROM designs WHERE id = ?", designID); hidden != 1 {
		t.Fatalf("is_hidden is %d", hidden)
	}
	if isNull := testHarness.scalarInt("SELECT print_time_minutes IS NULL FROM designs WHERE id = ?", designID); isNull != 1 {
		t.Fatal("an empty print time did not become NULL")
	}
}

func TestDesignsUpdateWithoutAnyFieldChangesNothing(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Alt")

	answer := testHarness.asUser(http.MethodPut, fmt.Sprintf("/api/v1/designs/%s", testHarness.designPID(designID)), map[string]any{})

	if answer.status != http.StatusOK {
		t.Fatalf("an empty update answered %d", answer.status)
	}
	if name := testHarness.scalar("SELECT name FROM designs WHERE id = ?", designID); name != "Alt" {
		t.Fatalf("the name became %q", name)
	}
}

func TestDesignsUpdateDoesNotTouchAForeignDesign(t *testing.T) {
	testHarness := newHarness(t)
	foreign := testHarness.insertDesign(testHarness.adminID, "Fremd")

	answer := testHarness.asUser(http.MethodPut, fmt.Sprintf("/api/v1/designs/%s", testHarness.designPID(foreign)), map[string]any{"name": "Gekapert"})

	if answer.status != http.StatusNotFound {
		t.Fatalf("a foreign design answered %d", answer.status)
	}
	if name := testHarness.scalar("SELECT name FROM designs WHERE id = ?", foreign); name != "Fremd" {
		t.Fatalf("the foreign design was renamed to %q", name)
	}
}

// Being the recipient of a share is not ownership: it must not allow writing.
func TestDesignsUpdateIsDeniedToTheShareRecipient(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.adminID, "Geteilt")
	testHarness.shareDesign(designID, testHarness.adminID, testHarness.userID)

	answer := testHarness.asUser(http.MethodPut, fmt.Sprintf("/api/v1/designs/%s", testHarness.designPID(designID)), map[string]any{"name": "Umbenannt"})

	if answer.status != http.StatusNotFound {
		t.Fatalf("the share recipient could write: %d", answer.status)
	}
	if name := testHarness.scalar("SELECT name FROM designs WHERE id = ?", designID); name != "Geteilt" {
		t.Fatalf("the design was renamed to %q", name)
	}
}

func TestDesignsDestroyRemovesTheDesignItsRowsAndItsFolder(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Weg damit")
	tagID := testHarness.insertTag(testHarness.userID, "Werkzeug", "manual")
	collectionID := testHarness.insertCollection(testHarness.userID, "Sammlung")
	testHarness.attachTag(designID, tagID)
	testHarness.addToCollection(designID, collectionID)
	testHarness.shareDesign(designID, testHarness.userID, testHarness.adminID)

	folder := testHarness.userLayout(testHarness.userID).Design(designID)
	if failure := os.MkdirAll(folder, 0o775); failure != nil {
		t.Fatalf("create the design folder: %v", failure)
	}
	if failure := os.WriteFile(filepath.Join(folder, "modell.stl"), []byte("solid"), 0o664); failure != nil {
		t.Fatalf("create the design file: %v", failure)
	}

	answer := testHarness.asUser(http.MethodDelete, fmt.Sprintf("/api/v1/designs/%s", testHarness.designPID(designID)), nil)

	if answer.status != http.StatusOK {
		t.Fatalf("destroy answered %d: %s", answer.status, answer.rawBody)
	}
	if testHarness.count("SELECT COUNT(*) FROM designs WHERE id = ?", designID) != 0 {
		t.Fatal("the design still exists")
	}
	if _, failure := os.Stat(folder); !os.IsNotExist(failure) {
		t.Fatalf("the design folder survived: %v", failure)
	}
	if testHarness.count("SELECT COUNT(*) FROM design_tags WHERE design_id = ?", designID) != 0 {
		t.Fatal("the tag assignments survived")
	}
	if testHarness.count("SELECT COUNT(*) FROM design_collections WHERE design_id = ?", designID) != 0 {
		t.Fatal("the collection memberships survived")
	}
	if testHarness.count("SELECT COUNT(*) FROM design_shares WHERE design_id = ?", designID) != 0 {
		t.Fatal("the shares survived")
	}
	if testHarness.count("SELECT COUNT(*) FROM tags WHERE id = ?", tagID) != 1 {
		t.Fatal("the tag itself was deleted along with the design")
	}
}

func TestDesignsDestroyLeavesAForeignDesignAlone(t *testing.T) {
	testHarness := newHarness(t)
	foreign := testHarness.insertDesign(testHarness.adminID, "Fremd")

	answer := testHarness.asUser(http.MethodDelete, fmt.Sprintf("/api/v1/designs/%s", testHarness.designPID(foreign)), nil)

	if answer.status != http.StatusNotFound {
		t.Fatalf("a foreign design answered %d", answer.status)
	}
	if testHarness.count("SELECT COUNT(*) FROM designs WHERE id = ?", foreign) != 1 {
		t.Fatal("the foreign design was deleted")
	}
}

func TestDesignsSyncQueuesTheDesign(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Mit Quelle")
	testHarness.setDesignFields(designID, map[string]any{"source_url": "https://www.thingiverse.com/thing:12345"})

	answer := testHarness.asUser(http.MethodPost, fmt.Sprintf("/api/v1/designs/%s/sync", testHarness.designPID(designID)), nil)

	if answer.status != http.StatusOK {
		t.Fatalf("sync answered %d: %s", answer.status, answer.rawBody)
	}
	if status := answer.data(t)["status"]; status != "queued" {
		t.Fatalf("unexpected status %v", status)
	}
	if testHarness.count("SELECT COUNT(*) FROM sync_queue WHERE design_id = ? AND status = 'pending'", designID) != 1 {
		t.Fatal("no pending job was created")
	}
}

// sync_queue has no unique constraint, so the duplicate check is the only thing
// keeping a second job for the same design out.
func TestDesignsSyncDoesNotQueueTheSameDesignTwice(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Mit Quelle")
	testHarness.setDesignFields(designID, map[string]any{"source_url": "https://www.thingiverse.com/thing:12345"})
	path := fmt.Sprintf("/api/v1/designs/%s/sync", testHarness.designPID(designID))

	testHarness.asUser(http.MethodPost, path, nil)
	answer := testHarness.asUser(http.MethodPost, path, nil)

	if status := answer.data(t)["status"]; status != "already_queued" {
		t.Fatalf("unexpected status %v", status)
	}
	if count := testHarness.count("SELECT COUNT(*) FROM sync_queue WHERE design_id = ?", designID); count != 1 {
		t.Fatalf("%d jobs were created", count)
	}
}

// A finished job no longer blocks: the design may be synced again.
func TestDesignsSyncQueuesAgainAfterAFinishedJob(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Mit Quelle")
	testHarness.setDesignFields(designID, map[string]any{"source_url": "https://www.thingiverse.com/thing:12345"})
	path := fmt.Sprintf("/api/v1/designs/%s/sync", testHarness.designPID(designID))

	testHarness.asUser(http.MethodPost, path, nil)
	if _, failure := testHarness.database.Exec("UPDATE sync_queue SET status = 'done' WHERE design_id = ?", designID); failure != nil {
		t.Fatalf("finish the job: %v", failure)
	}
	answer := testHarness.asUser(http.MethodPost, path, nil)

	if status := answer.data(t)["status"]; status != "queued" {
		t.Fatalf("unexpected status %v", status)
	}
	if count := testHarness.count("SELECT COUNT(*) FROM sync_queue WHERE design_id = ?", designID); count != 2 {
		t.Fatalf("%d jobs exist, expected the finished one plus a new one", count)
	}
}

func TestDesignsSyncNeedsASourceURL(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Ohne Quelle")

	answer := testHarness.asUser(http.MethodPost, fmt.Sprintf("/api/v1/designs/%s/sync", testHarness.designPID(designID)), nil)

	if answer.status != http.StatusUnprocessableEntity {
		t.Fatalf("a design without a source url answered %d", answer.status)
	}
	if key := answer.errorKey(t); key != "error.no_source_url" {
		t.Fatalf("unexpected error key %q", key)
	}
	if testHarness.count("SELECT COUNT(*) FROM sync_queue") != 0 {
		t.Fatal("a job was created anyway")
	}
}

func TestDesignsSyncRejectsAForeignDesign(t *testing.T) {
	testHarness := newHarness(t)
	foreign := testHarness.insertDesign(testHarness.adminID, "Fremd")
	testHarness.setDesignFields(foreign, map[string]any{"source_url": "https://www.thingiverse.com/thing:12345"})

	answer := testHarness.asUser(http.MethodPost, fmt.Sprintf("/api/v1/designs/%s/sync", testHarness.designPID(foreign)), nil)

	if answer.status != http.StatusNotFound {
		t.Fatalf("a foreign design answered %d", answer.status)
	}
	if testHarness.count("SELECT COUNT(*) FROM sync_queue") != 0 {
		t.Fatal("a job for a foreign design was created")
	}
}

func TestDesignsDuplicatesFindsMatchesByURLAndByName(t *testing.T) {
	testHarness := newHarness(t)
	sourceURL := "https://www.thingiverse.com/thing:12345"
	designID := testHarness.insertDesign(testHarness.userID, "Zahnrad Modul 1")
	sameURL := testHarness.insertDesign(testHarness.userID, "Voellig anderer Name")
	samePrefix := testHarness.insertDesign(testHarness.userID, "Zahnrad Modul 2")
	testHarness.insertDesign(testHarness.userID, "Nichts damit zu tun")
	testHarness.setDesignFields(designID, map[string]any{"source_url": sourceURL})
	testHarness.setDesignFields(sameURL, map[string]any{"source_url": sourceURL})

	duplicates := testHarness.asUser(http.MethodGet, fmt.Sprintf("/api/v1/designs/%s/duplicates", testHarness.designPID(designID)), nil).list(t)

	found := map[string]bool{}
	for _, entry := range duplicates {
		found[coerce.StringOr(entry.(map[string]any)["id"], "")] = true
	}
	if !found[testHarness.designPID(sameURL)] || !found[testHarness.designPID(samePrefix)] {
		t.Fatalf("a duplicate was missed: %v", found)
	}
	if found[testHarness.designPID(designID)] {
		t.Fatal("the design lists itself as a duplicate")
	}
	if len(duplicates) != 2 {
		t.Fatalf("%d duplicates were reported: %v", len(duplicates), found)
	}
}

func TestDesignsDuplicatesStaysWithinTheOwnLibrary(t *testing.T) {
	testHarness := newHarness(t)
	sourceURL := "https://www.thingiverse.com/thing:12345"
	designID := testHarness.insertDesign(testHarness.userID, "Zahnrad Modul 1")
	foreign := testHarness.insertDesign(testHarness.adminID, "Zahnrad Modul 1")
	testHarness.setDesignFields(designID, map[string]any{"source_url": sourceURL})
	testHarness.setDesignFields(foreign, map[string]any{"source_url": sourceURL})

	duplicates := testHarness.asUser(http.MethodGet, fmt.Sprintf("/api/v1/designs/%s/duplicates", testHarness.designPID(designID)), nil).list(t)

	if len(duplicates) != 0 {
		t.Fatalf("a foreign design was reported as a duplicate: %v", duplicates)
	}
}

func TestDesignsDuplicatesRejectsAForeignDesign(t *testing.T) {
	testHarness := newHarness(t)
	foreign := testHarness.insertDesign(testHarness.adminID, "Fremd")

	answer := testHarness.asUser(http.MethodGet, fmt.Sprintf("/api/v1/designs/%s/duplicates", testHarness.designPID(foreign)), nil)

	if answer.status != http.StatusNotFound {
		t.Fatalf("a foreign design answered %d", answer.status)
	}
}

func TestDesignCollectionsListsOnlyTheOwnCollections(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Geteilt")
	testHarness.shareDesign(designID, testHarness.userID, testHarness.adminID)
	second := testHarness.insertCollection(testHarness.userID, "Zweite")
	first := testHarness.insertCollection(testHarness.userID, "Erste")
	foreign := testHarness.insertCollection(testHarness.adminID, "Fremde Sammlung")
	testHarness.addToCollection(designID, first)
	testHarness.addToCollection(designID, second)
	testHarness.addToCollection(designID, foreign)

	collections := testHarness.asUser(http.MethodGet, fmt.Sprintf("/api/v1/designs/%s/collections", testHarness.designPID(designID)), nil).list(t)

	if len(collections) != 2 {
		t.Fatalf("%d collections were returned", len(collections))
	}
	if collections[0].(map[string]any)["name"] != "Erste" {
		t.Fatalf("the collections are not sorted by name: %v", collections)
	}
}

func TestDesignCollectionsWithANonNumericIDIsNotFound(t *testing.T) {
	testHarness := newHarness(t)

	answer := testHarness.asUser(http.MethodGet, "/api/v1/designs/abc/collections", nil)

	if answer.status != http.StatusNotFound {
		t.Fatalf("a non-numeric id answered %d", answer.status)
	}
}

func TestDesignsCheckUrlFindsAnExistingDesign(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Schon da")
	testHarness.setDesignFields(designID, map[string]any{
		"source_platform": "thingiverse",
		"source_id":       "12345",
	})

	payload := testHarness.asUser(http.MethodGet,
		"/api/v1/designs/check-url?url=https://www.thingiverse.com/thing:12345", nil).data(t)

	if payload["exists"] != true {
		t.Fatalf("the design was not found: %v", payload)
	}
	if payload["design"].(map[string]any)["id"] != testHarness.designPID(designID) {
		t.Fatalf("a different design was returned: %v", payload["design"])
	}
}

// The check is per user: a design of somebody else must not show up as "already
// in your library".
func TestDesignsCheckUrlIgnoresForeignDesigns(t *testing.T) {
	testHarness := newHarness(t)
	foreign := testHarness.insertDesign(testHarness.adminID, "Fremd")
	testHarness.setDesignFields(foreign, map[string]any{
		"source_platform": "thingiverse",
		"source_id":       "12345",
	})

	payload := testHarness.asUser(http.MethodGet,
		"/api/v1/designs/check-url?url=https://www.thingiverse.com/thing:12345", nil).data(t)

	if payload["exists"] != false {
		t.Fatalf("a foreign design was reported as existing: %v", payload)
	}
}

func TestDesignsCheckUrlAnswersEmptyForUnusableURLs(t *testing.T) {
	testHarness := newHarness(t)

	cases := map[string]string{
		"an empty url":            "",
		"an unknown host":         "https://example.org/some/model",
		"a host without an id":    "https://www.thingiverse.com/search",
		"a myminifactory listing": "https://www.myminifactory.com/users/someone",
	}

	for description, url := range cases {
		payload := testHarness.asUser(http.MethodGet, "/api/v1/designs/check-url?url="+url, nil).data(t)
		if payload["exists"] != false || payload["design"] != nil {
			t.Fatalf("%s produced %v", description, payload)
		}
	}
}

func TestExtractSourceIDPerPlatform(t *testing.T) {
	cases := []struct {
		platform string
		url      string
		expected string
	}{
		{"thingiverse", "https://www.thingiverse.com/thing:12345", "12345"},
		{"thingiverse", "https://www.thingiverse.com/thing-12345", "12345"},
		{"thingiverse", "https://www.thingiverse.com/nothing-here", ""},
		{"printables", "https://www.printables.com/model/98765-lampe", "98765"},
		{"printables", "https://www.printables.com/social/1234-user", ""},
		{"makerworld", "https://makerworld.com/en/models/55555", "55555"},
		{"thangs", "https://thangs.com/designer/x/3d-model/44444", "44444"},
		{"cults3d", "https://cults3d.com/en/3d-model/tool/mein-modell", "tool/mein-modell"},
		{"cults3d", "https://cults3d.com/de/drucklisten/etwas-anderes-777", "etwas-anderes-777"},
		{"myminifactory", "https://www.myminifactory.com/object/3d-print-drache-98765", "98765"},
		{"myminifactory", "https://www.myminifactory.com/object/drache-ohne-nummer", "drache-ohne-nummer"},
		{"myminifactory", "https://www.myminifactory.com/users/jemand", ""},
		{"unbekannt", "https://example.org/model/1", ""},
	}

	for _, testCase := range cases {
		if actual := extractSourceID(testCase.platform, testCase.url); actual != testCase.expected {
			t.Fatalf("%s %q yielded %q, expected %q", testCase.platform, testCase.url, actual, testCase.expected)
		}
	}
}

// The manual platform has no cover source, so the fetch fails without any
// outbound request.
func TestDesignsFetchCoverReportsAFailedFetch(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Manuell")
	testHarness.setDesignFields(designID, map[string]any{"source_url": "https://example.org/model/1"})

	answer := testHarness.asUser(http.MethodPost, fmt.Sprintf("/api/v1/designs/%s/fetch-cover", testHarness.designPID(designID)), nil)

	if answer.status != http.StatusBadGateway {
		t.Fatalf("the failed fetch answered %d: %s", answer.status, answer.rawBody)
	}
	if key := answer.errorKey(t); key != "error.cover_fetch_failed" {
		t.Fatalf("unexpected error key %q", key)
	}
	if path := testHarness.scalar("SELECT cover_path FROM designs WHERE id = ?", designID); path != "" {
		t.Fatalf("a cover path was written anyway: %q", path)
	}
}

func TestDesignsFetchCoverNeedsASourceURL(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Ohne Quelle")

	answer := testHarness.asUser(http.MethodPost, fmt.Sprintf("/api/v1/designs/%s/fetch-cover", testHarness.designPID(designID)), nil)

	if answer.status != http.StatusUnprocessableEntity {
		t.Fatalf("a design without a source url answered %d", answer.status)
	}
	if key := answer.errorKey(t); key != "error.no_source_url" {
		t.Fatalf("unexpected error key %q", key)
	}
}

func TestDesignsFetchCoverRejectsAForeignDesign(t *testing.T) {
	testHarness := newHarness(t)
	foreign := testHarness.insertDesign(testHarness.adminID, "Fremd")
	testHarness.setDesignFields(foreign, map[string]any{"source_url": "https://example.org/model/1"})

	answer := testHarness.asUser(http.MethodPost, fmt.Sprintf("/api/v1/designs/%s/fetch-cover", testHarness.designPID(foreign)), nil)

	if answer.status != http.StatusNotFound {
		t.Fatalf("a foreign design answered %d", answer.status)
	}
}

// The column feeds the outgoing link and the sync. A value that leads nowhere is
// a dead link and a design the sync can never resolve, so it is refused rather
// than stored. "http://afeefafefefaffefef" is the case that used to slip
// through: it parses, and it has a host, but there is no such site.
func TestDesignsUpdateRefusesASourceURLThatIsNotOne(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Mit Quelle")

	for _, invalid := range []string{
		"meins",
		"http://afeefafefefaffefef",
		"https://localhost",
		"http://example.123",
		"http://-example.org/x",
		"javascript:alert(1)",
		"ftp://example.org/x",
	} {
		answer := testHarness.asUser(http.MethodPut, fmt.Sprintf("/api/v1/designs/%s", testHarness.designPID(designID)),
			map[string]any{"source_url": invalid})
		if answer.status != http.StatusUnprocessableEntity {
			t.Errorf("%q answered %d, want 422", invalid, answer.status)
		}
	}
	for _, valid := range []string{"https://www.printables.com/model/1", "http://example.org/x", "http://192.168.1.5:8080/x", ""} {
		answer := testHarness.asUser(http.MethodPut, fmt.Sprintf("/api/v1/designs/%s", testHarness.designPID(designID)),
			map[string]any{"source_url": valid})
		if answer.status != http.StatusOK {
			t.Errorf("%q answered %d, want 200: %s", valid, answer.status, answer.rawBody)
		}
	}
}

// Nobody types "https://" in front of an address they copied, so a bare host is
// accepted and stored with the scheme the sync needs.
func TestDesignsUpdateFillsInTheMissingScheme(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Mit Quelle")

	answer := testHarness.asUser(http.MethodPut, fmt.Sprintf("/api/v1/designs/%s", testHarness.designPID(designID)),
		map[string]any{"source_url": "www.printables.com/model/1"})

	if answer.status != http.StatusOK {
		t.Fatalf("a url without a scheme answered %d: %s", answer.status, answer.rawBody)
	}
	var stored string
	if failure := testHarness.database.QueryRow("SELECT source_url FROM designs WHERE id = ?", designID).Scan(&stored); failure != nil {
		t.Fatalf("read the stored url: %v", failure)
	}
	if stored != "https://www.printables.com/model/1" {
		t.Fatalf("stored %q, want the https scheme filled in", stored)
	}
}
