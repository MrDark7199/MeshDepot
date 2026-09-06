package api

import (
	"fmt"
	"net/http"
	"testing"
)

// "Decor" and "decor" are one tag, not two: the UNIQUE index compares them
// case-sensitively, so without an explicit check both would exist and a filter
// on one would miss everything filed under the other.
func TestTagsCreateRejectsAnExistingNameInAnotherCasing(t *testing.T) {
	testHarness := newHarness(t)
	testHarness.database.Exec("INSERT INTO tags (user_id, name, color, source) VALUES (?, 'decor', '#457b9d', 'manual')", testHarness.userID)

	answer := testHarness.asUser(http.MethodPost, "/api/v1/tags", map[string]any{"name": "Decor"})

	if key := answer.errorKey(t); key != "error.tag_exists" {
		t.Fatalf("unexpected error key %q", key)
	}
	var count int
	testHarness.database.QueryRow("SELECT COUNT(*) FROM tags WHERE user_id = ?", testHarness.userID).Scan(&count)
	if count != 1 {
		t.Fatalf("a second tag was created, %d exist", count)
	}
}

func TestTagsIndexOnlyReturnsOwnTags(t *testing.T) {
	testHarness := newHarness(t)
	testHarness.database.Exec("INSERT INTO tags (user_id, name, color, source) VALUES (?, 'mine', '#457b9d', 'manual')", testHarness.userID)
	testHarness.database.Exec("INSERT INTO tags (user_id, name, color, source) VALUES (?, 'foreign', '#457b9d', 'manual')", testHarness.adminID)

	answer := testHarness.asUser(http.MethodGet, "/api/v1/tags", nil)

	rows := answer.list(t)
	if len(rows) != 1 {
		t.Fatalf("expected one tag, got %d: %s", len(rows), answer.rawBody)
	}
	if name := rows[0].(map[string]any)["name"]; name != "mine" {
		t.Fatalf("a foreign tag leaked: %v", name)
	}
}

// Self-created tags come first, then the most used ones - that is the order the
// tag picker relies on.
func TestTagsIndexSortsManualFirstThenUsage(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Design")
	importedID := testHarness.insertTag(testHarness.userID, "imported", "import")
	testHarness.insertTag(testHarness.userID, "manual", "manual")
	testHarness.database.Exec("INSERT INTO design_tags (design_id, tag_id) VALUES (?, ?)", designID, importedID)

	rows := testHarness.asUser(http.MethodGet, "/api/v1/tags", nil).list(t)

	if len(rows) != 2 {
		t.Fatalf("expected two tags, got %d", len(rows))
	}
	if name := rows[0].(map[string]any)["name"]; name != "manual" {
		t.Fatalf("the manual tag is not first: %v", name)
	}
	if count := rows[1].(map[string]any)["usage_count"]; count != float64(1) {
		t.Fatalf("unexpected usage count %v", count)
	}
}

func TestTagsSearchFiltersByName(t *testing.T) {
	testHarness := newHarness(t)
	testHarness.insertTag(testHarness.userID, "dragon", "manual")
	testHarness.insertTag(testHarness.userID, "castle", "manual")

	rows := testHarness.asUser(http.MethodGet, "/api/v1/tags/search?q=rag", nil).list(t)

	if len(rows) != 1 || rows[0].(map[string]any)["name"] != "dragon" {
		t.Fatalf("unexpected hits %v", rows)
	}
}

// A '%' in the query is a literal character, not a wildcard - otherwise the
// search matches everything.
func TestTagsSearchEscapesWildcards(t *testing.T) {
	testHarness := newHarness(t)
	testHarness.insertTag(testHarness.userID, "50% infill", "manual")
	testHarness.insertTag(testHarness.userID, "solid", "manual")

	rows := testHarness.asUser(http.MethodGet, "/api/v1/tags/search?q=50%25", nil).list(t)

	if len(rows) != 1 || rows[0].(map[string]any)["name"] != "50% infill" {
		t.Fatalf("the wildcard was not escaped: %v", rows)
	}
}

func TestTagsSearchClampsTheLimit(t *testing.T) {
	testHarness := newHarness(t)
	for index := 0; index < 25; index++ {
		testHarness.insertTag(testHarness.userID, fmt.Sprintf("tag-%02d", index), "manual")
	}

	for query, expected := range map[string]int{
		"":          20,
		"?limit=5":  5,
		"?limit=0":  20,
		"?limit=99": 20,
		"?limit=xx": 20,
	} {
		rows := testHarness.asUser(http.MethodGet, "/api/v1/tags/search"+query, nil).list(t)
		if len(rows) != expected {
			t.Fatalf("%q returned %d tags, expected %d", query, len(rows), expected)
		}
	}
}

func TestTagsStoreCreatesATag(t *testing.T) {
	testHarness := newHarness(t)

	answer := testHarness.asUser(http.MethodPost, "/api/v1/tags", map[string]any{"name": "  dragon  ", "color": "#ff0000"})

	if answer.status != http.StatusCreated {
		t.Fatalf("unexpected status %d: %s", answer.status, answer.rawBody)
	}
	created := answer.data(t)
	if created["name"] != "dragon" {
		t.Fatalf("the name was not trimmed: %v", created["name"])
	}
	if created["color"] != "#ff0000" || created["source"] != "manual" {
		t.Fatalf("unexpected tag %v", created)
	}
}

func TestTagsStoreUsesTheDefaultColor(t *testing.T) {
	testHarness := newHarness(t)

	created := testHarness.asUser(http.MethodPost, "/api/v1/tags", map[string]any{"name": "dragon"}).data(t)

	if created["color"] != "#457b9d" {
		t.Fatalf("unexpected default colour %v", created["color"])
	}
}

func TestTagsStoreRejectsAnEmptyName(t *testing.T) {
	testHarness := newHarness(t)

	for _, body := range []map[string]any{{}, {"name": ""}, {"name": "   "}} {
		answer := testHarness.asUser(http.MethodPost, "/api/v1/tags", body)
		if answer.status != http.StatusUnprocessableEntity {
			t.Fatalf("%v answered %d", body, answer.status)
		}
		if key := answer.errorKey(t); key != "error.name_required" {
			t.Fatalf("unexpected error key %q", key)
		}
	}
}

func TestTagsStoreReportsADuplicate(t *testing.T) {
	testHarness := newHarness(t)
	testHarness.asUser(http.MethodPost, "/api/v1/tags", map[string]any{"name": "dragon"})

	answer := testHarness.asUser(http.MethodPost, "/api/v1/tags", map[string]any{"name": "dragon"})

	if answer.status != http.StatusUnprocessableEntity {
		t.Fatalf("unexpected status %d", answer.status)
	}
	if key := answer.errorKey(t); key != "error.tag_exists" {
		t.Fatalf("unexpected error key %q", key)
	}
}

// The same name may exist once per user.
func TestTagsStoreAllowsTheSameNameForAnotherUser(t *testing.T) {
	testHarness := newHarness(t)
	testHarness.asUser(http.MethodPost, "/api/v1/tags", map[string]any{"name": "dragon"})

	answer := testHarness.asAdmin(http.MethodPost, "/api/v1/tags", map[string]any{"name": "dragon"})

	if answer.status != http.StatusCreated {
		t.Fatalf("unexpected status %d: %s", answer.status, answer.rawBody)
	}
}

func TestTagsDestroyRemovesOwnTag(t *testing.T) {
	testHarness := newHarness(t)
	tagID := testHarness.insertTag(testHarness.userID, "dragon", "manual")

	answer := testHarness.asUser(http.MethodDelete, fmt.Sprintf("/api/v1/tags/%d", tagID), nil)

	if answer.status != http.StatusOK {
		t.Fatalf("unexpected status %d: %s", answer.status, answer.rawBody)
	}
	if remaining := testHarness.count("SELECT COUNT(*) FROM tags WHERE id = ?", tagID); remaining != 0 {
		t.Fatal("the tag survived the delete")
	}
}

// A foreign tag must look exactly like a missing one.
func TestTagsDestroyDoesNotTouchForeignTags(t *testing.T) {
	testHarness := newHarness(t)
	tagID := testHarness.insertTag(testHarness.adminID, "foreign", "manual")

	answer := testHarness.asUser(http.MethodDelete, fmt.Sprintf("/api/v1/tags/%d", tagID), nil)

	if answer.status != http.StatusNotFound {
		t.Fatalf("unexpected status %d", answer.status)
	}
	if remaining := testHarness.count("SELECT COUNT(*) FROM tags WHERE id = ?", tagID); remaining != 1 {
		t.Fatal("a foreign tag was deleted")
	}
}

func TestTagsDestroyRejectsANonNumericID(t *testing.T) {
	testHarness := newHarness(t)

	answer := testHarness.asUser(http.MethodDelete, "/api/v1/tags/abc", nil)

	if answer.status != http.StatusNotFound {
		t.Fatalf("unexpected status %d", answer.status)
	}
}

func TestTagsSetForDesignReplacesTheSet(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Design")
	first := testHarness.insertTag(testHarness.userID, "first", "manual")
	second := testHarness.insertTag(testHarness.userID, "second", "manual")
	testHarness.database.Exec("INSERT INTO design_tags (design_id, tag_id) VALUES (?, ?)", designID, first)

	answer := testHarness.asUser(http.MethodPut, fmt.Sprintf("/api/v1/designs/%s/tags", testHarness.designPID(designID)),
		map[string]any{"tag_ids": []int{second}})

	if answer.status != http.StatusOK {
		t.Fatalf("unexpected status %d: %s", answer.status, answer.rawBody)
	}
	assigned := testHarness.scalar("SELECT GROUP_CONCAT(tag_id) FROM design_tags WHERE design_id = ?", designID)
	if assigned != fmt.Sprint(second) {
		t.Fatalf("unexpected assignment %q", assigned)
	}
}

// A tag that loses its last design and belongs to the user is removed with it -
// otherwise the tag list fills up with dead entries.
func TestTagsSetForDesignDropsOrphanedTags(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Design")
	otherDesignID := testHarness.insertDesign(testHarness.userID, "Other")
	orphan := testHarness.insertTag(testHarness.userID, "orphan", "manual")
	shared := testHarness.insertTag(testHarness.userID, "shared", "manual")
	testHarness.database.Exec("INSERT INTO design_tags (design_id, tag_id) VALUES (?, ?), (?, ?), (?, ?)",
		designID, orphan, designID, shared, otherDesignID, shared)

	testHarness.asUser(http.MethodPut, fmt.Sprintf("/api/v1/designs/%s/tags", testHarness.designPID(designID)), map[string]any{"tag_ids": []int{}})

	if testHarness.count("SELECT COUNT(*) FROM tags WHERE id = ?", orphan) != 0 {
		t.Fatal("the orphaned tag survived")
	}
	if testHarness.count("SELECT COUNT(*) FROM tags WHERE id = ?", shared) != 1 {
		t.Fatal("a tag still used by another design was deleted")
	}
}

// The design_tags foreign key only checks that the tag exists, not who owns it -
// so the handler has to filter, or any user could attach a foreign tag.
func TestTagsSetForDesignIgnoresForeignTagIDs(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Design")
	ownTag := testHarness.insertTag(testHarness.userID, "own", "manual")
	foreignTag := testHarness.insertTag(testHarness.adminID, "foreign", "manual")

	answer := testHarness.asUser(http.MethodPut, fmt.Sprintf("/api/v1/designs/%s/tags", testHarness.designPID(designID)),
		map[string]any{"tag_ids": []int{ownTag, foreignTag}})

	if answer.status != http.StatusOK {
		t.Fatalf("unexpected status %d: %s", answer.status, answer.rawBody)
	}
	assigned := testHarness.scalar("SELECT GROUP_CONCAT(tag_id) FROM design_tags WHERE design_id = ?", designID)
	if assigned != fmt.Sprint(ownTag) {
		t.Fatalf("a foreign tag was attached: %q", assigned)
	}
	if testHarness.count("SELECT COUNT(*) FROM tags WHERE id = ?", foreignTag) != 1 {
		t.Fatal("the foreign tag was deleted as an orphan")
	}
}

func TestTagsSetForDesignRejectsAForeignDesign(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.adminID, "Foreign")

	answer := testHarness.asUser(http.MethodPut, fmt.Sprintf("/api/v1/designs/%s/tags", testHarness.designPID(designID)),
		map[string]any{"tag_ids": []int{}})

	if answer.status != http.StatusNotFound {
		t.Fatalf("unexpected status %d", answer.status)
	}
}

func TestTagsSetForDesignRejectsANonNumericDesignID(t *testing.T) {
	testHarness := newHarness(t)

	answer := testHarness.asUser(http.MethodPut, "/api/v1/designs/abc/tags", map[string]any{"tag_ids": []int{}})

	if answer.status != http.StatusNotFound {
		t.Fatalf("unexpected status %d", answer.status)
	}
}
