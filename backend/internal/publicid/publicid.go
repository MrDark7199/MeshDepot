// Package publicid converts between an internal rowid and the public_id that
// the API, the storage layout and every response body use. Accounts and designs
// both carry one.
//
// The numeric primary key never leaves the process. It is what every foreign
// key references and what crypto.Encrypt derives a user's credential key from
// (see internal/crypto), so it cannot be replaced - but it is also sequential,
// which makes a row enumerable to anyone who can count. The public id is the
// outward name for the same row: 128 random bits, unguessable, and stable for
// its lifetime because it appears in stored file paths and in shared links.
package publicid

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"regexp"

	"meshdepot/internal/dbutil"
)

// shape is what a public id looks like. Handlers check it before touching the
// database, so a malformed path parameter costs no query.
var shape = regexp.MustCompile(`^[0-9a-f]{32}$`)

// New generates an outward identifier: 16 random bytes as 32 hex characters,
// the same construction the session ids use (internal/auth/session.go). It
// panics only if the system entropy source fails, which is not a condition an
// account creation could sensibly continue through.
func New() string {
	buffer := make([]byte, 16)
	if _, failure := rand.Read(buffer); failure != nil {
		panic("publicid: no system entropy: " + failure.Error())
	}
	return hex.EncodeToString(buffer)
}

// Valid reports whether text has the shape of a public id.
func Valid(text string) bool {
	return shape.MatchString(text)
}

// Resolve maps a public id to the internal users.id. found=false is an unknown
// id, an error is a broken query - never the same thing, because the first is a
// 404 and the second a 500.
func Resolve(querier dbutil.Querier, publicID string) (int, bool, error) {
	return ResolveIn(querier, "users", publicID)
}

// ResolveIn is Resolve for any table that carries a public_id - accounts and
// designs both do. The table name comes from the call site and is never input;
// the id is bound as a parameter like everywhere else.
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

// Of maps an internal users.id to its public id.
func Of(querier dbutil.Querier, userID int) (string, bool, error) {
	return OfIn(querier, "users", userID)
}

// OfIn is Of for any table that carries a public_id.
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
