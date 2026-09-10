// Package publicid converts between an internal rowid and the public_id the API,
// the storage layout and every response body use.
//
// The numeric primary key never leaves the process: it is what every foreign key
// references and what crypto.Encrypt derives a credential key from, so it cannot
// be replaced - but it is sequential, and therefore enumerable. The public id is
// the outward name for the same row: 128 random bits, stable for its lifetime
// because it appears in stored file paths and shared links.
package publicid

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"regexp"

	"meshdepot/internal/dbutil"
)

// shape is checked before the database is touched, so a malformed path parameter
// costs no query.
var shape = regexp.MustCompile(`^[0-9a-f]{32}$`)

// New generates 16 random bytes as 32 hex characters, the same construction the
// session ids use. It panics only if the entropy source fails.
func New() string {
	buffer := make([]byte, 16)
	if _, failure := rand.Read(buffer); failure != nil {
		panic("publicid: no system entropy: " + failure.Error())
	}
	return hex.EncodeToString(buffer)
}

func Valid(text string) bool {
	return shape.MatchString(text)
}

// Resolve maps a public id to users.id. found=false is an unknown id and an error
// a broken query - the first is a 404, the second a 500.
func Resolve(querier dbutil.Querier, publicID string) (int, bool, error) {
	return ResolveIn(querier, "users", publicID)
}

// ResolveIn is Resolve for any table carrying a public_id. The table name comes
// from the call site and is never input.
func ResolveIn(querier dbutil.Querier, table, publicID string) (int, bool, error) {
	if !Valid(publicID) {
		return 0, false, nil
	}
	var rowID int
	failure := querier.QueryRow("SELECT id FROM "+table+" WHERE public_id = ? LIMIT 1", publicID).Scan(&rowID)
	if errors.Is(failure, sql.ErrNoRows) {
		return 0, false, nil
	}
	if failure != nil {
		return 0, false, failure
	}
	return rowID, true, nil
}

func Of(querier dbutil.Querier, userID int) (string, bool, error) {
	return OfIn(querier, "users", userID)
}

func OfIn(querier dbutil.Querier, table string, rowID int) (string, bool, error) {
	var publicID string
	failure := querier.QueryRow("SELECT COALESCE(public_id, '') FROM "+table+" WHERE id = ? LIMIT 1", rowID).Scan(&publicID)
	if errors.Is(failure, sql.ErrNoRows) || (failure == nil && publicID == "") {
		return "", false, nil
	}
	if failure != nil {
		return "", false, failure
	}
	return publicID, true, nil
}
