package api

import (
	"path/filepath"
	"strconv"
	"testing"

	"meshdepot/internal/coerce"
	"meshdepot/internal/db"
)

// Values belong to the field they were entered under, and a field belongs to one
// user - so a design read by somebody else carries none of the owner's values.
func TestCustomValuesStayWithTheirOwner(t *testing.T) {
	database, failure := db.Open(filepath.Join(t.TempDir(), "fields.db"))
	if failure != nil {
		t.Fatal(failure)
	}
	defer database.Close()
	if failure := db.InitSchema(database); failure != nil {
		t.Fatal(failure)
	}

	const owner, guest = 1, 2
	if _, failure := database.Exec(
		"INSERT INTO users (id, public_id, name, hash) VALUES (?, 'u2', 'guest', 'x')",
		guest); failure != nil {
		t.Fatal(failure)
	}
	result, failure := database.Exec(
		"INSERT INTO designs (user_id, public_id, name) VALUES (?, 'p1', 'Centurion')", owner)
	if failure != nil {
		t.Fatal(failure)
	}
	designID64, _ := result.LastInsertId()
	designID := int(designID64)

	fieldResult, failure := database.Exec(
		"INSERT INTO custom_fields (user_id, name, field_type) VALUES (?, 'Bought at', 'text')", owner)
	if failure != nil {
		t.Fatal(failure)
	}
	fieldID64, _ := fieldResult.LastInsertId()

	// The id arrives as a string key from JSON, so it is written that way here.
	storeCustomValues(database, designID, owner, map[string]any{
		strconv.Itoa(int(fieldID64)): "Filamentwelt",
	})

	ownerView := customValuesFor(database, designID, owner)
	if len(ownerView) != 1 || coerce.StringOr(ownerView[0]["value"], "") != "Filamentwelt" {
		t.Fatalf("the owner does not see their own value: %+v", ownerView)
	}

	guestView := customValuesFor(database, designID, guest)
	if len(guestView) != 0 {
		t.Fatalf("somebody else sees %d field(s) of this design: %+v", len(guestView), guestView)
	}

	// A value written against a field of another user changes nothing.
	storeCustomValues(database, designID, guest, map[string]any{strconv.Itoa(int(fieldID64)): "stolen"})
	ownerView = customValuesFor(database, designID, owner)
	if coerce.StringOr(ownerView[0]["value"], "") != "Filamentwelt" {
		t.Fatalf("a stranger overwrote the value: %+v", ownerView)
	}
}
