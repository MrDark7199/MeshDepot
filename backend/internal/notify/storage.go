package notify

// The warning a member gets when their own storage is filling up.
//
// It lives here rather than beside the download worker because two paths reach
// it now: a queued download, and an import handed over by the browser
// extension. A copy in each would drift, and this one is easy to get subtly
// wrong - see the comment on when it fires.

import (
	"database/sql"
	"fmt"

	"meshdepot/internal/quota"
)

// StorageNearlyFull tells a member once their own storage crosses the
// warning threshold.
//
// Only on the crossing: the check runs after every download, and a notification
// on each one past 80% would turn the bell into a counter of downloads rather
// than a warning. The previous state is derived from the size just added, which
// is what makes "was below before" answerable without keeping a flag.
func StorageNearlyFull(database *sql.DB, userID int) {
	usage := quota.Of(database, userID)
	if !usage.NearlyFull() {
		return
	}
	var lastAdded int64
	database.QueryRow(`
		SELECT COALESCE(df.size_bytes, 0)
		FROM design_files df JOIN designs d ON d.id = df.design_id
		WHERE d.user_id = ? ORDER BY df.id DESC LIMIT 1`, userID).Scan(&lastAdded)
	before := quota.Usage{UsedBytes: usage.UsedBytes - lastAdded, LimitBytes: usage.LimitBytes}
	if before.NearlyFull() {
		return
	}
	User(database, userID, "user_storage_80",
		"Your storage is nearly full",
		fmt.Sprintf("%d%% of your storage quota is in use. Delete designs or versions you no longer need, or ask an administrator for more space.", usage.Percent()),
		nil)
}
