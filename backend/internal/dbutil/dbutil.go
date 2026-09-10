// Package dbutil provides generic helpers to read SQL rows as map[string]any,
// the response shape the frontend consumes (see api/rows.go).
package dbutil

import (
	"database/sql"
	"errors"
	"strings"

	"meshdepot/internal/logx"
)

type Querier interface {
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
	Exec(query string, args ...any) (sql.Result, error)
}

// QueryMaps returns every row as a map, with []byte normalized to string.
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

// QueryMap returns the first row. found=false means the query ran and matched
// nothing, a non-nil error means it failed - the two must stay apart. The earlier
// two-value form returned nil for both, so callers treated a locked database as
// an empty result: that is how FilesDeleteEntry came to delete a whole version
// when a COUNT(*) hit a busy timeout.
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

// Exists reports whether the query matched a row, and whether the question could
// be answered at all.
//
// The second result is the point. A probe written as `Scan(...) == nil` reads a
// busy or briefly unreachable database as "no such row", and the caller then
// creates the duplicate it was checking for. Callers that must not do that pass
// on the "unknown" case instead of treating it as "no". The failure is logged
// here so it cannot pass unnoticed either.
func Exists(querier Querier, query string, args ...any) (found bool, known bool) {
	var marker int
	failure := querier.QueryRow(query, args...).Scan(&marker)
	if failure == nil {
		return true, true
	}
	if errors.Is(failure, sql.ErrNoRows) {
		return false, true
	}
	logx.Errorf("[db] probe failed: %s: %v", queryHead(query), failure)
	return false, false
}

func normalize(value any) any {
	if raw, ok := value.([]byte); ok {
		return string(raw)
	}
	return value
}

// ExecLogged runs a statement whose result is not needed and logs a failure with
// the beginning of it. Dropping the error at ~60 call sites made a partial write
// invisible - "sometimes the tags are missing after a sync" was not diagnosable.
// Only the query is logged, never the arguments.
func ExecLogged(querier Querier, query string, args ...any) {
	if _, failure := querier.Exec(query, args...); failure != nil {
		logx.Errorf("[db] exec failed: %s: %v", queryHead(query), failure)
	}
}

func queryHead(query string) string {
	words := strings.Fields(query)
	if len(words) > 8 {
		return strings.Join(words[:8], " ") + " …"
	}
	return strings.Join(words, " ")
}

func IsUniqueViolation(failure error) bool {
	return failure != nil && strings.Contains(failure.Error(), "UNIQUE constraint failed")
}
