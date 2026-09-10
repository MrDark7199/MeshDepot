// Package quota answers how much storage an account uses and may use. Its own
// package because two callers must not disagree: the download worker refuses a
// job that would exceed the limit, and the pages show what is left.
package quota

import (
	"database/sql"

	"meshdepot/internal/logx"
)

// warnAtPercent matches the server-wide storage warning, so both mean the same
// thing to a reader.
const warnAtPercent = 80

type Usage struct {
	UsedBytes int64
	// LimitBytes is 0 when the account has no limit.
	LimitBytes int64
	// Unknown means the figures could not be read. Both reads fail towards
	// "nothing used, no limit", which is the one answer that lets a download
	// through - so whoever guards on the quota has to see this rather than a
	// comfortable zero.
	Unknown bool
}

func (usage Usage) Unlimited() bool { return usage.LimitBytes <= 0 }

func (usage Usage) Exceeded() bool {
	return !usage.Unlimited() && usage.UsedBytes >= usage.LimitBytes
}

func (usage Usage) Percent() int {
	if usage.Unlimited() {
		return 0
	}
	return int(usage.UsedBytes * 100 / usage.LimitBytes)
}

func (usage Usage) NearlyFull() bool {
	return !usage.Unlimited() && usage.Percent() >= warnAtPercent
}

// Of sums the usage from the version rows rather than measuring the disk: blobs
// are shared between versions and designs, so the bytes on disk are less than
// what the versions list. The versions are what the library shows and what a
// member can act on by deleting something.
func Of(database *sql.DB, userID int) Usage {
	var usage Usage
	if failure := database.QueryRow(`
		SELECT COALESCE(SUM(df.size_bytes), 0)
		FROM design_files df
		JOIN designs d ON d.id = df.design_id
		WHERE d.user_id = ?`, userID).Scan(&usage.UsedBytes); failure != nil {
		logx.Errorf("[quota] usage of user %d could not be read: %v", userID, failure)
		usage.Unknown = true
	}

	var limit sql.NullInt64
	if failure := database.QueryRow("SELECT storage_quota_bytes FROM users WHERE id = ?", userID).Scan(&limit); failure != nil {
		logx.Errorf("[quota] limit of user %d could not be read: %v", userID, failure)
		usage.Unknown = true
	}
	if limit.Valid && limit.Int64 > 0 {
		usage.LimitBytes = limit.Int64
	}
	return usage
}
