package api

import (
	"fmt"
	"net/http"
	"testing"

	"meshdepot/internal/coerce"
	"meshdepot/internal/publicid"
)

func TestCollectionsIndexShowsOnlyOwnCollectionsWithTheirDesignCount(t *testing.T) {
	testHarness := newHarness(t)
	mine := testHarness.insertCollection(testHarness.userID, "Halterungen")
	foreign := testHarness.insertCollection(testHarness.adminID, "Fremdsammlung")
	firstDesign := testHarness.insertDesign(testHarness.userID, "Halter A")
	secondDesign := testHarness.insertDesign(testHarness.userID, "Halter B")
	testHarness.addToCollection(firstDesign, mine)
	testHarness.addToCollection(secondDesign, mine)
	testHarness.addToCollection(testHarness.insertDesign(testHarness.adminID, "Fremd"), foreign)

	answer := testHarness.asUser(http.MethodGet, "/api/v1/collections", nil)

	if answer.status != http.StatusOK {
		t.Fatalf("the index answered %d: %s", answer.status, answer.rawBody)
	}
	collections := answer.list(t)
	if len(collections) != 1 {
		t.Fatalf("expected exactly the own collection, got %d", len(collections))
	}
	row := collections[0].(map[string]any)
	if coerce.Int(row["id"]) != mine {
		t.Fatalf("the index returned collection %v", row["id"])
	}
	if count := coerce.Int(row["design_count"]); count != 2 {
		t.Fatalf("design_count is %d, expected 2", count)
	}
}

func TestCollectionsIndexCountsAnEmptyCollectionAsZero(t *testing.T) {
	testHarness := newHarness(t)
	testHarness.insertCollection(testHarness.userID, "Leer")

	collections := testHarness.asUser(http.MethodGet, "/api/v1/collections", nil).list(t)

	if len(collections) != 1 {
		t.Fatalf("expected one collection, got %d", len(collections))
	}
	if count := coerce.Int(collections[0].(map[string]any)["design_count"]); count != 0 {
		t.Fatalf("design_count is %d, expected 0", count)
	}
}

func TestCollectionsStoreCreatesACollectionForTheCaller(t *testing.T) {
	testHarness := newHarness(t)

	answer := testHarness.asUser(http.MethodPost, "/api/v1/collections", map[string]any{
		"name":        "  Ersatzteile  ",
		"description": "Alles zum Nachdrucken",
	})

	if answer.status != http.StatusCreated {
		t.Fatalf("store answered %d: %s", answer.status, answer.rawBody)
	}
	created := answer.data(t)
	if created["name"] != "Ersatzteile" {
		t.Fatalf("the name was not trimmed: %v", created["name"])
	}
	if created["description"] != "Alles zum Nachdrucken" {
		t.Fatalf("unexpected description %v", created["description"])
	}
	owner := testHarness.scalarInt("SELECT user_id FROM collections WHERE id = ?", coerce.Int(created["id"]))
	if owner != testHarness.userID {
		t.Fatalf("the collection belongs to user %d instead of the caller", owner)
	}
}

func TestCollectionsStoreRejectsABlankName(t *testing.T) {
	testHarness := newHarness(t)

	answer := testHarness.asUser(http.MethodPost, "/api/v1/collections", map[string]any{"name": "   "})

	if answer.status != http.StatusUnprocessableEntity {
		t.Fatalf("a blank name answered %d", answer.status)
	}
	if key := answer.errorKey(t); key != "error.name_required" {
		t.Fatalf("unexpected error key %q", key)
	}
	if testHarness.count("SELECT COUNT(*) FROM collections") != 0 {
		t.Fatal("a nameless collection was created")
	}
}

// Two collections of the same name (case-insensitive, trimmed) are rejected so
// the picker and filter stay unambiguous.
func TestCollectionsStoreRejectsADuplicateName(t *testing.T) {
	testHarness := newHarness(t)
	testHarness.insertCollection(testHarness.userID, "Deko")

	answer := testHarness.asUser(http.MethodPost, "/api/v1/collections", map[string]any{"name": "  deko  "})

	if answer.status != http.StatusConflict {
		t.Fatalf("a duplicate name answered %d, want 409: %s", answer.status, answer.rawBody)
	}
	if key := answer.errorKey(t); key != "error.collection_name_taken" {
		t.Fatalf("unexpected error key %q", key)
	}
	if count := testHarness.count("SELECT COUNT(*) FROM collections WHERE user_id = ?", testHarness.userID); count != 1 {
		t.Fatalf("a second collection was created (%d total)", count)
	}
}

func TestCollectionsUpdateChangesNameAndDescription(t *testing.T) {
	testHarness := newHarness(t)
	collectionID := testHarness.insertCollection(testHarness.userID, "Alt")

	answer := testHarness.asUser(http.MethodPut, fmt.Sprintf("/api/v1/collections/%d", collectionID), map[string]any{
		"name":        "  Neu  ",
		"description": " Beschreibung ",
	})

	if answer.status != http.StatusOK {
		t.Fatalf("update answered %d: %s", answer.status, answer.rawBody)
	}
	if name := testHarness.scalar("SELECT name FROM collections WHERE id = ?", collectionID); name != "Neu" {
		t.Fatalf("the name is %q", name)
	}
	if description := testHarness.scalar("SELECT description FROM collections WHERE id = ?", collectionID); description != "Beschreibung" {
		t.Fatalf("the description is %q", description)
	}
}

// Issue #9: hiding a collection keeps it but removes it from the normal list;
// the collection tab still reaches it via include_hidden, and unhiding restores
// it to the default list.
func TestCollectionsHideExcludesFromDefaultListButKeepsItReachable(t *testing.T) {
	testHarness := newHarness(t)
	collectionID := testHarness.insertCollection(testHarness.userID, "Geheim")

	if hide := testHarness.asUser(http.MethodPut, fmt.Sprintf("/api/v1/collections/%d", collectionID), map[string]any{"is_hidden": true}); hide.status != http.StatusOK {
		t.Fatalf("hiding answered %d: %s", hide.status, hide.rawBody)
	}
	if hidden := testHarness.count("SELECT is_hidden FROM collections WHERE id = ?", collectionID); hidden != 1 {
		t.Fatalf("is_hidden is %d, want 1", hidden)
	}
	if visible := testHarness.asUser(http.MethodGet, "/api/v1/collections", nil).list(t); len(visible) != 0 {
		t.Fatalf("the hidden collection is still in the default list (%d shown)", len(visible))
	}
	if all := testHarness.asUser(http.MethodGet, "/api/v1/collections?include_hidden=1", nil).list(t); len(all) != 1 {
		t.Fatalf("include_hidden listed %d, want 1", len(all))
	}

	if unhide := testHarness.asUser(http.MethodPut, fmt.Sprintf("/api/v1/collections/%d", collectionID), map[string]any{"is_hidden": false}); unhide.status != http.StatusOK {
		t.Fatalf("unhiding answered %d: %s", unhide.status, unhide.rawBody)
	}
	if visible := testHarness.asUser(http.MethodGet, "/api/v1/collections", nil).list(t); len(visible) != 1 {
		t.Fatalf("after unhiding, the default list shows %d, want 1", len(visible))
	}
}

// A non-boolean is_hidden is rejected like the other malformed update fields.
func TestCollectionsUpdateRejectsNonBooleanHidden(t *testing.T) {
	testHarness := newHarness(t)
	collectionID := testHarness.insertCollection(testHarness.userID, "Alt")

	answer := testHarness.asUser(http.MethodPut, fmt.Sprintf("/api/v1/collections/%d", collectionID), map[string]any{"is_hidden": "yes"})

	if answer.status != http.StatusUnprocessableEntity {
		t.Fatalf("a string is_hidden answered %d: %s", answer.status, answer.rawBody)
	}
}

// A non-string value used to be handed to the driver unchanged, which turned a
// malformed request into a 500.
func TestCollectionsUpdateRejectsNonStringValues(t *testing.T) {
	testHarness := newHarness(t)
	collectionID := testHarness.insertCollection(testHarness.userID, "Alt")

	answer := testHarness.asUser(http.MethodPut, fmt.Sprintf("/api/v1/collections/%d", collectionID), map[string]any{
		"name": map[string]any{"nested": true},
	})

	if answer.status != http.StatusUnprocessableEntity {
		t.Fatalf("an object as name answered %d: %s", answer.status, answer.rawBody)
	}
	if key := answer.errorKey(t); key != "error.invalid_input" {
		t.Fatalf("unexpected error key %q", key)
	}
	if name := testHarness.scalar("SELECT name FROM collections WHERE id = ?", collectionID); name != "Alt" {
		t.Fatalf("the name was changed to %q", name)
	}
}

func TestCollectionsUpdateRejectsAnEmptyName(t *testing.T) {
	testHarness := newHarness(t)
	collectionID := testHarness.insertCollection(testHarness.userID, "Alt")

	answer := testHarness.asUser(http.MethodPut, fmt.Sprintf("/api/v1/collections/%d", collectionID), map[string]any{"name": "  "})

	if key := answer.errorKey(t); key != "error.name_required" {
		t.Fatalf("unexpected error key %q", key)
	}
	if name := testHarness.scalar("SELECT name FROM collections WHERE id = ?", collectionID); name != "Alt" {
		t.Fatalf("the name became %q", name)
	}
}

// An empty body is not an error - there is simply nothing to write.
func TestCollectionsUpdateWithoutAnyFieldChangesNothing(t *testing.T) {
	testHarness := newHarness(t)
	collectionID := testHarness.insertCollection(testHarness.userID, "Alt")

	answer := testHarness.asUser(http.MethodPut, fmt.Sprintf("/api/v1/collections/%d", collectionID), map[string]any{})

	if answer.status != http.StatusOK {
		t.Fatalf("an empty update answered %d", answer.status)
	}
	if name := testHarness.scalar("SELECT name FROM collections WHERE id = ?", collectionID); name != "Alt" {
		t.Fatalf("the name became %q", name)
	}
}

func TestCollectionsUpdateDoesNotTouchAForeignCollection(t *testing.T) {
	testHarness := newHarness(t)
	foreign := testHarness.insertCollection(testHarness.adminID, "Fremd")

	answer := testHarness.asUser(http.MethodPut, fmt.Sprintf("/api/v1/collections/%d", foreign), map[string]any{"name": "Gekapert"})

	if answer.status != http.StatusNotFound {
		t.Fatalf("a foreign collection answered %d", answer.status)
	}
	if name := testHarness.scalar("SELECT name FROM collections WHERE id = ?", foreign); name != "Fremd" {
		t.Fatalf("the foreign collection was renamed to %q", name)
	}
}

func TestCollectionsUpdateWithANonNumericIDIsNotFound(t *testing.T) {
	testHarness := newHarness(t)

	answer := testHarness.asUser(http.MethodPut, "/api/v1/collections/abc", map[string]any{"name": "Neu"})

	if answer.status != http.StatusNotFound {
		t.Fatalf("a non-numeric id answered %d", answer.status)
	}
}

func TestCollectionsDestroyRemovesTheCollectionButKeepsItsDesigns(t *testing.T) {
	testHarness := newHarness(t)
	collectionID := testHarness.insertCollection(testHarness.userID, "Weg damit")
	designID := testHarness.insertDesign(testHarness.userID, "Bleibt")
	testHarness.addToCollection(designID, collectionID)

	answer := testHarness.asUser(http.MethodDelete, fmt.Sprintf("/api/v1/collections/%d", collectionID), nil)

	if answer.status != http.StatusOK {
		t.Fatalf("destroy answered %d: %s", answer.status, answer.rawBody)
	}
	if testHarness.count("SELECT COUNT(*) FROM collections WHERE id = ?", collectionID) != 0 {
		t.Fatal("the collection still exists")
	}
	if testHarness.count("SELECT COUNT(*) FROM designs WHERE id = ?", designID) != 1 {
		t.Fatal("the design was deleted along with the collection")
	}
	if testHarness.count("SELECT COUNT(*) FROM design_collections WHERE collection_id = ?", collectionID) != 0 {
		t.Fatal("the membership rows survived the collection")
	}
}

func TestCollectionsDestroyLeavesAForeignCollectionAlone(t *testing.T) {
	testHarness := newHarness(t)
	foreign := testHarness.insertCollection(testHarness.adminID, "Fremd")

	answer := testHarness.asUser(http.MethodDelete, fmt.Sprintf("/api/v1/collections/%d", foreign), nil)

	if answer.status != http.StatusNotFound {
		t.Fatalf("a foreign collection answered %d", answer.status)
	}
	if testHarness.count("SELECT COUNT(*) FROM collections WHERE id = ?", foreign) != 1 {
		t.Fatal("the foreign collection was deleted")
	}
}

func TestCollectionDesignsReturnsOwnAndSharedDesignsSortedByName(t *testing.T) {
	testHarness := newHarness(t)
	collectionID := testHarness.insertCollection(testHarness.userID, "Gemischt")
	own := testHarness.insertDesign(testHarness.userID, "Bravo")
	shared := testHarness.insertDesign(testHarness.adminID, "Alpha")
	invisible := testHarness.insertDesign(testHarness.adminID, "Charlie")
	testHarness.shareDesign(shared, testHarness.adminID, testHarness.userID)
	testHarness.addToCollection(own, collectionID)
	testHarness.addToCollection(shared, collectionID)
	testHarness.addToCollection(invisible, collectionID)

	designs := testHarness.asUser(http.MethodGet, fmt.Sprintf("/api/v1/collections/%d/designs", collectionID), nil).list(t)

	if len(designs) != 2 {
		t.Fatalf("expected the own and the shared design, got %d", len(designs))
	}
	first := designs[0].(map[string]any)
	second := designs[1].(map[string]any)
	if first["name"] != "Alpha" || second["name"] != "Bravo" {
		t.Fatalf("the designs are not sorted by name: %v, %v", first["name"], second["name"])
	}
	if coerce.Int(first["is_shared"]) != 1 {
		t.Fatal("the shared design is not marked as shared")
	}
	if first["shared_by_name"] != "administrator" {
		t.Fatalf("the owner name is missing: %v", first["shared_by_name"])
	}
	if coerce.Int(second["is_shared"]) != 0 {
		t.Fatal("the own design is marked as shared")
	}
}

func TestCollectionDesignsRejectsAForeignCollection(t *testing.T) {
	testHarness := newHarness(t)
	foreign := testHarness.insertCollection(testHarness.adminID, "Fremd")

	answer := testHarness.asUser(http.MethodGet, fmt.Sprintf("/api/v1/collections/%d/designs", foreign), nil)

	if answer.status != http.StatusNotFound {
		t.Fatalf("a foreign collection answered %d", answer.status)
	}
}

func TestCollectionAddableDesignsExcludesDesignsAlreadyInTheCollection(t *testing.T) {
	testHarness := newHarness(t)
	collectionID := testHarness.insertCollection(testHarness.userID, "Sammlung")
	alreadyIn := testHarness.insertDesign(testHarness.userID, "Drin")
	stillOut := testHarness.insertDesign(testHarness.userID, "Draussen")
	testHarness.addToCollection(alreadyIn, collectionID)

	answer := testHarness.asUser(http.MethodGet, fmt.Sprintf("/api/v1/collections/%d/addable-designs", collectionID), nil)

	payload := answer.data(t)
	if total := coerce.Int(payload["total"]); total != 1 {
		t.Fatalf("total is %d, expected 1", total)
	}
	items := payload["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["id"] != testHarness.designPID(stillOut) {
		t.Fatalf("unexpected addable designs: %s", answer.rawBody)
	}
}

func TestCollectionAddableDesignsIncludesSharedDesigns(t *testing.T) {
	testHarness := newHarness(t)
	collectionID := testHarness.insertCollection(testHarness.userID, "Sammlung")
	shared := testHarness.insertDesign(testHarness.adminID, "Geteilt")
	testHarness.insertDesign(testHarness.adminID, "Nicht geteilt")
	testHarness.shareDesign(shared, testHarness.adminID, testHarness.userID)

	payload := testHarness.asUser(http.MethodGet, fmt.Sprintf("/api/v1/collections/%d/addable-designs", collectionID), nil).data(t)

	items := payload["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("expected only the shared design, got %d", len(items))
	}
	row := items[0].(map[string]any)
	if row["id"] != testHarness.designPID(shared) {
		t.Fatalf("unexpected design %v", row["id"])
	}
	if coerce.Int(row["is_shared"]) != 1 {
		t.Fatal("the shared design is not marked as shared")
	}
}

func TestCollectionAddableDesignsFiltersBySearch(t *testing.T) {
	testHarness := newHarness(t)
	collectionID := testHarness.insertCollection(testHarness.userID, "Sammlung")
	byName := testHarness.insertDesign(testHarness.userID, "Zahnrad")
	byAuthor := testHarness.insertDesign(testHarness.userID, "Etwas anderes")
	testHarness.insertDesign(testHarness.userID, "Voellig unbeteiligt")
	testHarness.setDesignFields(byAuthor, map[string]any{"author": "Zahnradbauer"})

	payload := testHarness.asUser(http.MethodGet,
		fmt.Sprintf("/api/v1/collections/%d/addable-designs?search=zahnrad", collectionID), nil).data(t)

	if total := coerce.Int(payload["total"]); total != 2 {
		t.Fatalf("total is %d, expected the name and the author hit", total)
	}
	found := map[string]bool{}
	for _, item := range payload["items"].([]any) {
		found[coerce.StringOr(item.(map[string]any)["id"], "")] = true
	}
	if !found[testHarness.designPID(byName)] || !found[testHarness.designPID(byAuthor)] {
		t.Fatalf("the search missed a hit: %v", found)
	}
}

// A search for "50%" must not turn into a wildcard that matches everything.
func TestCollectionAddableDesignsEscapesSearchWildcards(t *testing.T) {
	testHarness := newHarness(t)
	collectionID := testHarness.insertCollection(testHarness.userID, "Sammlung")
	wanted := testHarness.insertDesign(testHarness.userID, "Fuellgrad 50% Test")
	testHarness.insertDesign(testHarness.userID, "Ohne Angabe")

	payload := testHarness.asUser(http.MethodGet,
		fmt.Sprintf("/api/v1/collections/%d/addable-designs?search=50%%25", collectionID), nil).data(t)

	items := payload["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["id"] != testHarness.designPID(wanted) {
		t.Fatalf("the wildcard was not escaped: %v", items)
	}
}

func TestCollectionAddableDesignsPaginates(t *testing.T) {
	testHarness := newHarness(t)
	collectionID := testHarness.insertCollection(testHarness.userID, "Sammlung")
	for index := 0; index < 5; index++ {
		testHarness.insertDesign(testHarness.userID, fmt.Sprintf("Design %d", index))
	}

	firstPage := testHarness.asUser(http.MethodGet,
		fmt.Sprintf("/api/v1/collections/%d/addable-designs?per_page=2&page=1", collectionID), nil).data(t)
	if total := coerce.Int(firstPage["total"]); total != 5 {
		t.Fatalf("total is %d although the page holds two rows", total)
	}
	if count := len(firstPage["items"].([]any)); count != 2 {
		t.Fatalf("the first page holds %d rows", count)
	}

	lastPage := testHarness.asUser(http.MethodGet,
		fmt.Sprintf("/api/v1/collections/%d/addable-designs?per_page=2&page=3", collectionID), nil).data(t)
	if count := len(lastPage["items"].([]any)); count != 1 {
		t.Fatalf("the last page holds %d rows", count)
	}
}

// per_page is clamped to 1..100 and page to at least 1, so hostile values cannot
// ask for the whole table or a negative offset.
func TestCollectionAddableDesignsClampsPaginationParameters(t *testing.T) {
	testHarness := newHarness(t)
	collectionID := testHarness.insertCollection(testHarness.userID, "Sammlung")
	for index := 0; index < 3; index++ {
		testHarness.insertDesign(testHarness.userID, fmt.Sprintf("Design %d", index))
	}

	zeroPerPage := testHarness.asUser(http.MethodGet,
		fmt.Sprintf("/api/v1/collections/%d/addable-designs?per_page=0", collectionID), nil).data(t)
	if count := len(zeroPerPage["items"].([]any)); count != 1 {
		t.Fatalf("per_page=0 returned %d rows instead of the minimum of one", count)
	}

	negativePage := testHarness.asUser(http.MethodGet,
		fmt.Sprintf("/api/v1/collections/%d/addable-designs?page=-5&per_page=2", collectionID), nil).data(t)
	if count := len(negativePage["items"].([]any)); count != 2 {
		t.Fatalf("page=-5 returned %d rows instead of the first page", count)
	}
}

func TestCollectionAddableDesignsRejectsAForeignCollection(t *testing.T) {
	testHarness := newHarness(t)
	foreign := testHarness.insertCollection(testHarness.adminID, "Fremd")

	answer := testHarness.asUser(http.MethodGet, fmt.Sprintf("/api/v1/collections/%d/addable-designs", foreign), nil)

	if answer.status != http.StatusNotFound {
		t.Fatalf("a foreign collection answered %d", answer.status)
	}
}

func TestCollectionAddDesignsAddsOwnAndSharedDesigns(t *testing.T) {
	testHarness := newHarness(t)
	collectionID := testHarness.insertCollection(testHarness.userID, "Sammlung")
	own := testHarness.insertDesign(testHarness.userID, "Eigen")
	shared := testHarness.insertDesign(testHarness.adminID, "Geteilt")
	testHarness.shareDesign(shared, testHarness.adminID, testHarness.userID)

	answer := testHarness.asUser(http.MethodPost, fmt.Sprintf("/api/v1/collections/%d/designs", collectionID),
		map[string]any{"design_ids": []string{testHarness.designPID(own), testHarness.designPID(shared)}})

	if answer.status != http.StatusOK {
		t.Fatalf("adding answered %d: %s", answer.status, answer.rawBody)
	}
	if count := testHarness.count("SELECT COUNT(*) FROM design_collections WHERE collection_id = ?", collectionID); count != 2 {
		t.Fatalf("the collection holds %d designs, expected 2", count)
	}
}

// A design the caller may not see must be skipped silently - the loop keeps
// going so the designs they are allowed to add still land.
func TestCollectionAddDesignsSkipsInaccessibleDesigns(t *testing.T) {
	testHarness := newHarness(t)
	collectionID := testHarness.insertCollection(testHarness.userID, "Sammlung")
	own := testHarness.insertDesign(testHarness.userID, "Eigen")
	foreign := testHarness.insertDesign(testHarness.adminID, "Fremd")

	answer := testHarness.asUser(http.MethodPost, fmt.Sprintf("/api/v1/collections/%d/designs", collectionID),
		map[string]any{"design_ids": []string{testHarness.designPID(foreign), testHarness.designPID(own), publicid.New()}})

	if answer.status != http.StatusOK {
		t.Fatalf("adding answered %d: %s", answer.status, answer.rawBody)
	}
	if count := testHarness.count("SELECT COUNT(*) FROM design_collections WHERE collection_id = ?", collectionID); count != 1 {
		t.Fatalf("the collection holds %d designs, expected only the own one", count)
	}
	if testHarness.count("SELECT COUNT(*) FROM design_collections WHERE collection_id = ? AND design_id = ?", collectionID, foreign) != 0 {
		t.Fatal("a foreign design was added")
	}
}

func TestCollectionAddDesignsIsIdempotent(t *testing.T) {
	testHarness := newHarness(t)
	collectionID := testHarness.insertCollection(testHarness.userID, "Sammlung")
	designID := testHarness.insertDesign(testHarness.userID, "Eigen")
	body := map[string]any{"design_ids": []string{testHarness.designPID(designID)}}

	testHarness.asUser(http.MethodPost, fmt.Sprintf("/api/v1/collections/%d/designs", collectionID), body)
	answer := testHarness.asUser(http.MethodPost, fmt.Sprintf("/api/v1/collections/%d/designs", collectionID), body)

	if answer.status != http.StatusOK {
		t.Fatalf("the second call answered %d: %s", answer.status, answer.rawBody)
	}
	if count := testHarness.count("SELECT COUNT(*) FROM design_collections WHERE collection_id = ?", collectionID); count != 1 {
		t.Fatalf("the design was added %d times", count)
	}
}

func TestCollectionAddDesignsRejectsAForeignCollection(t *testing.T) {
	testHarness := newHarness(t)
	foreign := testHarness.insertCollection(testHarness.adminID, "Fremd")
	designID := testHarness.insertDesign(testHarness.userID, "Eigen")

	answer := testHarness.asUser(http.MethodPost, fmt.Sprintf("/api/v1/collections/%d/designs", foreign),
		map[string]any{"design_ids": []string{testHarness.designPID(designID)}})

	if answer.status != http.StatusNotFound {
		t.Fatalf("a foreign collection answered %d", answer.status)
	}
	if testHarness.count("SELECT COUNT(*) FROM design_collections WHERE collection_id = ?", foreign) != 0 {
		t.Fatal("a design was added to a foreign collection")
	}
}

func TestCollectionRemoveDesignRemovesOnlyTheMembership(t *testing.T) {
	testHarness := newHarness(t)
	collectionID := testHarness.insertCollection(testHarness.userID, "Sammlung")
	designID := testHarness.insertDesign(testHarness.userID, "Eigen")
	otherDesign := testHarness.insertDesign(testHarness.userID, "Bleibt drin")
	testHarness.addToCollection(designID, collectionID)
	testHarness.addToCollection(otherDesign, collectionID)

	answer := testHarness.asUser(http.MethodDelete,
		fmt.Sprintf("/api/v1/collections/%d/designs/%s", collectionID, testHarness.designPID(designID)), nil)

	if answer.status != http.StatusOK {
		t.Fatalf("removing answered %d: %s", answer.status, answer.rawBody)
	}
	if testHarness.count("SELECT COUNT(*) FROM design_collections WHERE collection_id = ? AND design_id = ?", collectionID, designID) != 0 {
		t.Fatal("the membership survived")
	}
	if testHarness.count("SELECT COUNT(*) FROM designs WHERE id = ?", designID) != 1 {
		t.Fatal("the design itself was deleted")
	}
	if testHarness.count("SELECT COUNT(*) FROM design_collections WHERE collection_id = ?", collectionID) != 1 {
		t.Fatal("the other design was removed as well")
	}
}

func TestCollectionRemoveDesignRejectsAForeignCollection(t *testing.T) {
	testHarness := newHarness(t)
	foreign := testHarness.insertCollection(testHarness.adminID, "Fremd")
	designID := testHarness.insertDesign(testHarness.adminID, "Fremdes Design")
	testHarness.addToCollection(designID, foreign)

	answer := testHarness.asUser(http.MethodDelete,
		fmt.Sprintf("/api/v1/collections/%d/designs/%s", foreign, testHarness.designPID(designID)), nil)

	if answer.status != http.StatusNotFound {
		t.Fatalf("a foreign collection answered %d", answer.status)
	}
	if testHarness.count("SELECT COUNT(*) FROM design_collections WHERE collection_id = ?", foreign) != 1 {
		t.Fatal("the membership in the foreign collection was removed")
	}
}

func TestCollectionRemoveDesignWithANonNumericDesignIDIsNotFound(t *testing.T) {
	testHarness := newHarness(t)
	collectionID := testHarness.insertCollection(testHarness.userID, "Sammlung")

	answer := testHarness.asUser(http.MethodDelete,
		fmt.Sprintf("/api/v1/collections/%d/designs/abc", collectionID), nil)

	if answer.status != http.StatusNotFound {
		t.Fatalf("a non-numeric design id answered %d", answer.status)
	}
}
