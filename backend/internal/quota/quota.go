// Package quota answers how much storage an account uses and may use.
//
// It is its own package because two callers need the same answer and must not
// disagree: the download worker refuses a job that would exceed the limit, and
// the account and admin pages show what is left.
package quota

import "database/sql"

// warnAtPercent is where "nearly full" begins. Chosen to match the existing
// server-wide storage warning, so both mean the same thing to a reader.
const warnAtPercent = 80

// Usage is what an account currently occupies and what it is allowed.
type Usage struct {
	UsedBytes int64
	// LimitBytes is 0 when the account has no limit.
	LimitBytes int64
}

// Unlimited reports whether the account has no limit at all.
func (usage Usage) Unlimited() bool { return usage.LimitBytes <= 0 }

// Exceeded reports whether the account is at or beyond its limit.
func (usage Usage) Exceeded() bool {
	return !usage.Unlimited() && usage.UsedBytes >= usage.LimitBytes
}

// Percent is how full the account is, 0 when it has no limit.
func (usage Usage) Percent() int {
	if usage.Unlimited() {
		return 0
	}
	return int(usage.UsedBytes * 100 / usage.LimitBytes)
}

// NearlyFull reports whether the account has crossed the warning threshold.
func (usage Usage) NearlyFull() bool {
	return !usage.Unlimited() && usage.Percent() >= warnAtPercent
}

// Of reads an account's usage and limit.
//
// Usage is summed from the version rows rather than measured on disk: blobs are
// shared between versions and between designs, so the bytes on disk are less
// than the sum of what the versions list. Counting the versions is what the
// library shows and what a member can act on by deleting something - a figure
// they cannot reconcile with the interface would be worse than none.
func Of(database *sql.DB, userID int) Usage {
	var usage Usage
	database.QueryRow(`
		SELECT COALESCE(SUM(df.size_bytes), 0)
		FROM design_files df
		JOIN designs d ON d.id = df.design_id
		WHERE d.user_id = ?`, userID).Scan(&usage.UsedBytes)

	var limit sql.NullInt64
	database.QueryRow("SELECT storage_quota_bytes FROM users WHERE id = ?", userID).Scan(&limit)
	if limit.Valid && limit.Int64 > 0 {
		usage.LimitBytes = limit.Int64
	}
	return usage
}
