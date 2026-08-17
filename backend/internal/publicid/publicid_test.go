package publicid_test

import (
	"database/sql"
	"path/filepath"
	"testing"

	"meshdepot/internal/db"
	"meshdepot/internal/publicid"
)

func TestNewProducesThirtyTwoHexCharacters(t *testing.T) {
	value := publicid.New()
	if len(value) != 32 {
		t.Fatalf("length %d: %q", len(value), value)
	}
	if !publicid.Valid(value) {
		t.Fatalf("a freshly generated id is not valid: %q", value)
	}
}

// The ids end up in file paths and URLs, so a repeat would collide two accounts
// on disk. 128 random bits make that unreachable; this only guards against a
// generator that stopped being random at all.
func TestNewDoesNotRepeat(t *testing.T) {
	seen := make(map[string]bool, 1000)
	for index := 0; index < 1000; index++ {
		value := publicid.New()
		if seen[value] {
			t.Fatalf("%q was generated twice", value)
		}
		seen[value] = true
	}
}

func TestValidRejectsAnythingButThirtyTwoHexCharacters(t *testing.T) {
	valid := publicid.New()
	cases := map[string]bool{
		valid:                              true,
		"1":                                false, // a numeric id from before the cut
		"42":                               false,
		"":                                 false,
		valid[:31]:                         false,
		valid + "a":                        false,
		"ABCDEF0123456789ABCDEF0123456789": false, // uppercase: one shape, not two
		"g1234567890123456789012345678901": false,
		"../../etc/passwd":                 false,
	}
	for input, want := range cases {
		if got := publicid.Valid(input); got != want {
			t.Errorf("publicid.Valid(%q) = %v, want %v", input, got, want)
		}
	}
}

func TestResolveAndOfAreInverse(t *testing.T) {
	database := newTestDatabase(t)
	publicID := publicid.New()
	result, failure := database.Exec(
		"INSERT INTO users (email, hash, name, state, public_id) VALUES ('a@example.org', 'x', 'a', 'active', ?)", publicID)
	if failure != nil {
		t.Fatalf("insert: %v", failure)
	}
	insertedID, _ := result.LastInsertId()

	userID, found, failure := publicid.Resolve(database, publicID)
	if failure != nil || !found {
		t.Fatalf("Resolve: found=%v failure=%v", found, failure)
	}
	if userID != int(insertedID) {
		t.Fatalf("Resolve returned %d, want %d", userID, insertedID)
	}
	back, found, failure := publicid.Of(database, userID)
	if failure != nil || !found || back != publicID {
		t.Fatalf("Of returned %q (found=%v, failure=%v)", back, found, failure)
	}
}

// An unknown id is a 404 and a broken query a 500 - the caller can only make
// that distinction if the two never look alike here.
func TestResolveReportsUnknownAndMalformedAsNotFound(t *testing.T) {
	database := newTestDatabase(t)

	for _, input := range []string{publicid.New(), "1", "nonsense"} {
		userID, found, failure := publicid.Resolve(database, input)
		if failure != nil {
			t.Fatalf("publicid.Resolve(%q) reported an error: %v", input, failure)
		}
		if found || userID != 0 {
			t.Fatalf("publicid.Resolve(%q) found user %d", input, userID)
		}
	}
	if _, found, failure := publicid.Of(database, 999); found || failure != nil {
		t.Fatalf("Of on a missing user: found=%v failure=%v", found, failure)
	}
}

func newTestDatabase(t *testing.T) *sql.DB {
	t.Helper()
	database, failure := db.Open(filepath.Join(t.TempDir(), "meshdepot.db"))
	if failure != nil {
		t.Fatalf("open database: %v", failure)
	}
	t.Cleanup(func() { database.Close() })
	if failure := db.InitSchema(database); failure != nil {
		t.Fatalf("init schema: %v", failure)
	}
	return database
}
