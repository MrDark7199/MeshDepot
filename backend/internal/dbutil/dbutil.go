// Package dbutil provides generic helpers to read SQL rows as map[string]any,
// the response shape the frontend consumes (see api/rows.go).
package dbutil

import (
	"database/sql"
	"log"
	"strings"
)

// Querier is the common interface of *sql.DB and *sql.Tx.
type Querier interface {
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
	Exec(query string, args ...any) (sql.Result, error)
}

// QueryMaps runs a query and returns every row as a map. []byte values are
// normalized to string (for clean JSON).
func QueryMaps(querier Querier, query string, args ...any) ([]map[string]any, error) {
	rows, failure := querier.Query(query, args...)
	if failure != nil {
		return nil, failure
	}
	defer rows.Close()
	columns, failure := rows.Columns()
	if failure != nil {
		return nil, failure
	}
	results := make([]map[string]any, 0)
	for rows.Next() {
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for index := range values {
			pointers[index] = &values[index]
		}
		if failure := rows.Scan(pointers...); failure != nil {
			return nil, failure
		}
		row := make(map[string]any, len(columns))
		for index, column := range columns {
			row[column] = normalize(values[index])
		}
		results = append(results, row)
	}
	return results, rows.Err()
}

// QueryMap returns the first row of a query.
//
// found=false means the query ran and matched nothing; a non-nil error means the
// query itself failed. The two must stay apart: the earlier two-value form
// returned a nil map for both, so every caller that read nil as "not found"
// silently treated a locked or broken database as an empty result - and acted on
// it. That is how FilesDeleteEntry came to delete a whole version when a COUNT(*)
// hit a busy timeout.
func QueryMap(querier Querier, query string, args ...any) (map[string]any, bool, error) {
	rows, failure := QueryMaps(querier, query, args...)
	if failure != nil {
		return nil, false, failure
	}
	if len(rows) == 0 {
		return nil, false, nil
	}
	return rows[0], true, nil
}

// normalize converts []byte to string (otherwise base64 ends up in the JSON).
func normalize(value any) any {
	if raw, ok := value.([]byte); ok {
		return string(raw)
	}
	return value
}

// ExecLogged runs a statement whose result is not needed and logs a failure with
// the beginning of the statement.
//
// Dropping the error - the previous house style at ~60 call sites - made a
// partial write failure completely invisible: no log, no error response, no
// metric. A symptom like "sometimes the tags are missing after a sync" was not
// diagnosable. Only the query is logged, never the arguments (they carry user
// data and secrets).
func ExecLogged(querier Querier, query string, args ...any) {
	if _, failure := querier.Exec(query, args...); failure != nil {
		log.Printf("[db] exec failed: %s: %v", queryHead(query), failure)
	}
}

// queryHead shortens a statement to its first words, enough to identify it in a
// log line.
func queryHead(query string) string {
	words := strings.Fields(query)
	if len(words) > 8 {
		return strings.Join(words[:8], " ") + " …"
	}
	return strings.Join(words, " ")
}

// IsUniqueViolation detects a UNIQUE constraint violation (SQLite).
func IsUniqueViolation(failure error) bool {
	return failure != nil && strings.Contains(failure.Error(), "UNIQUE constraint failed")
}
