// Package db opens the SQLite database and sets up the schema.
package db

import (
	"database/sql"
	_ "embed"
	"fmt"
	"meshdepot/internal/dbutil"
	"meshdepot/internal/publicid"
	"net/url"

	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite" // CGO-free SQLite driver (driver name "sqlite").
)

//go:embed schema.sql
var schemaSQL string

// Open opens the SQLite file in WAL mode with foreign keys enabled.
func Open(path string) (*sql.DB, error) {
	dataSourceName := fmt.Sprintf(
		"file:%s?_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)&_pragma=busy_timeout(5000)",
		url.PathEscape(path),
	)
	database, failure := sql.Open("sqlite", dataSourceName)
	if failure != nil {
		return nil, fmt.Errorf("open sqlite: %w", failure)
	}
	// SQLite tolerates only a single writer; a single pool slot prevents
	// "database is locked" between web requests and worker goroutines.
	database.SetMaxOpenConns(1)
	if failure := database.Ping(); failure != nil {
		return nil, fmt.Errorf("ping sqlite: %w", failure)
	}
	return database, nil
}

// InitSchema creates all tables idempotently and, on an empty DB, seeds a
// default admin (name=admin / password=admin) that must pick a new password on
// first login. The name is intentionally lowercase so that login with "admin"
// (SQLite matches case-sensitively) works right away.
func InitSchema(database *sql.DB) error {
	if _, failure := database.Exec(schemaSQL); failure != nil {
		return fmt.Errorf("apply schema: %w", failure)
	}
	migrate(database)
	var userCount int
	if failure := database.QueryRow("SELECT COUNT(*) FROM users").Scan(&userCount); failure != nil {
		return fmt.Errorf("count users: %w", failure)
	}
	if userCount == 0 {
		hash, failure := bcrypt.GenerateFromPassword([]byte("admin"), bcrypt.DefaultCost)
		if failure != nil {
			return fmt.Errorf("hash default admin: %w", failure)
		}
		// must_change_password: the seed credentials are public knowledge, so
		// the account is only usable long enough to replace them.
		_, failure = database.Exec(
			"INSERT INTO users (email, hash, name, state, admin, must_change_password, public_id) VALUES (NULL, ?, 'admin', 'active', 1, 1, ?)",
			string(hash), publicid.New(),
		)
		if failure != nil {
			return fmt.Errorf("seed default admin: %w", failure)
		}
	}
	return nil
}

// migrate adds columns that `CREATE TABLE IF NOT EXISTS` no longer inserts into
// already-existing tables of an older DB. Each ADD COLUMN is idempotent: if the
// column already exists, SQLite returns a "duplicate column name" error, which
// we deliberately discard.
func migrate(database *sql.DB) {
	alterStatements := []string{
		// Phase display of the background sync (cf. download_queue): which step is
		// currently running and its counter.
		"ALTER TABLE sync_queue ADD COLUMN current_step TEXT DEFAULT NULL",
		"ALTER TABLE sync_queue ADD COLUMN step_current INTEGER DEFAULT NULL",
		"ALTER TABLE sync_queue ADD COLUMN step_total INTEGER DEFAULT NULL",
		// Print parameters extracted from G-code files (JSON) per file entry.
		"ALTER TABLE design_file_entries ADD COLUMN gcode_meta TEXT DEFAULT NULL",
		// When the job was claimed. resetStuck previously compared against
		// created_at, which measures the wait in the queue, not the runtime: a job
		// that waited longer than stuckTimeout was reset the moment it started.
		"ALTER TABLE sync_queue ADD COLUMN started_at TEXT DEFAULT NULL",
		// Outward user identifier; backfilled below and made unique afterwards.
		"ALTER TABLE users ADD COLUMN public_id TEXT NOT NULL DEFAULT ''",
		// Cooldown of the manual library sync, kept per account so it also holds
		// for members without a platform account.
		"ALTER TABLE users ADD COLUMN last_manual_sync_at TEXT DEFAULT NULL",
		// Cooldown of the manual "update all designs" run.
		"ALTER TABLE users ADD COLUMN last_update_all_at TEXT DEFAULT NULL",
		// Outward design identifier; backfilled below and made unique afterwards.
		"ALTER TABLE designs ADD COLUMN public_id TEXT NOT NULL DEFAULT ''",
		// Per-account style overrides, previously kept in the browser alone.
		"ALTER TABLE users ADD COLUMN custom_css TEXT NOT NULL DEFAULT ''",
	}
	for _, statement := range alterStatements {
		database.Exec(statement) // Error (column already exists) intentionally ignored.
	}
	// Every account needs an outward identifier before the unique index below can
	// exist. Runs on every boot rather than once, so an insert path that ever
	// forgets the column heals itself instead of leaving a row that no URL and no
	// storage path can address.
	backfillPublicIDs(database)
	backfillPublicIDsOf(database, "designs")
	// After the backfill, never before: InitSchema runs schema.sql as a single
	// Exec, so an index that fails on two empty values would take the rest of the
	// script with it.
	dbutil.ExecLogged(database, "CREATE UNIQUE INDEX IF NOT EXISTS uq_users_public_id ON users (public_id)")
	dbutil.ExecLogged(database, "CREATE UNIQUE INDEX IF NOT EXISTS uq_designs_public_id ON designs (public_id)")

	// sessions.expires_at was written as RFC3339 while every other time column
	// uses CURRENT_TIMESTAMP's "YYYY-MM-DD HH:MM:SS". strftime parses both, so
	// this normalizes old rows and is a no-op on already-converted ones.
	dbutil.ExecLogged(database, "UPDATE sessions SET expires_at = strftime('%Y-%m-%d %H:%M:%S', expires_at) WHERE expires_at LIKE '%T%'")
	// user_sessions: dead table, never read or written.
	dbutil.ExecLogged(database, "DROP TABLE IF EXISTS user_sessions")

	// Download cooldowns below the current minimum: the old seeds (15 s, 30 s for
	// makerworld) are low enough to get an account rate-limited or blocked, and
	// the settings form no longer accepts them. Existing installations are lifted
	// to the same floor instead of keeping a value nobody can reproduce.
	dbutil.ExecLogged(database, "UPDATE app_settings SET value = '30' WHERE key LIKE 'download_cooldown_%' AND key <> 'download_cooldown_makerworld' AND CAST(value AS INTEGER) < 30")
	dbutil.ExecLogged(database, "UPDATE app_settings SET value = '300' WHERE key = 'download_cooldown_makerworld' AND CAST(value AS INTEGER) < 200")

	// Design update intervals shorter than the minimum: the setting used to be
	// stored but never read, so a member could have saved any number down to one
	// day. Now that the scheduler honours it, such a value would re-download a
	// whole library every day. NULL stays untouched - it means "no updates".
	// The floor is read from the setting rather than hard-coded, so raising it as
	// an admin also lifts the members who are below it.
	dbutil.ExecLogged(database, `
		UPDATE notification_prefs
		SET sync_min_age_days = (SELECT CAST(value AS INTEGER) FROM app_settings WHERE key = 'design_update_min_days')
		WHERE sync_min_age_days IS NOT NULL
		  AND sync_min_age_days < (SELECT CAST(value AS INTEGER) FROM app_settings WHERE key = 'design_update_min_days')`)

	// Designs whose cover was lost to the publishing bug (see publishCover in
	// internal/platforms/save.go): every downloaded design kept its gallery but
	// got no cover_path, so the overview showed the grey placeholder while the
	// detail view found the images. The first gallery image is what the cover
	// would have been. Runs on every boot rather than once - it only touches
	// rows that have images and no cover, which is a state nothing else creates.
	dbutil.ExecLogged(database, `
		UPDATE designs
		SET cover_path = (SELECT path FROM design_images
			WHERE design_id = designs.id ORDER BY sort_order ASC, created_at ASC LIMIT 1)
		WHERE (cover_path IS NULL OR cover_path = '')
		  AND EXISTS (SELECT 1 FROM design_images WHERE design_id = designs.id)`)

	// Designs whose gallery marks no cover although the design has one. Which
	// image is the cover is stored twice - designs.cover_path drives the
	// overview card, design_images.is_cover the marker in the gallery - and only
	// the API paths ever wrote both, so every downloaded design showed a card
	// image while no image in its own gallery was marked as the one. Matched by
	// path, falling back to the first image: the cover file is a hard link next
	// to the pictures directory, so its own path is not one of theirs.
	// Two steps rather than one ranked pick: the embedded SQLite does not resolve
	// a correlation two levels deep, which an ORDER BY over d.cover_path inside a
	// nested subquery would need. First the image the cover path names, then the
	// first image for the designs that still have none.
	dbutil.ExecLogged(database, `
		UPDATE design_images SET is_cover = 1
		WHERE is_cover = 0
		  AND EXISTS (SELECT 1 FROM designs d
			WHERE d.id = design_images.design_id AND d.cover_path = design_images.path)
		  AND NOT EXISTS (SELECT 1 FROM design_images x
			WHERE x.design_id = design_images.design_id AND x.is_cover = 1)`)
	dbutil.ExecLogged(database, `
		UPDATE design_images SET is_cover = 1
		WHERE id IN (
			SELECT (SELECT di.id FROM design_images di
				WHERE di.design_id = d.id
				ORDER BY di.sort_order ASC, di.created_at ASC, di.id ASC
				LIMIT 1)
			FROM designs d
			WHERE d.cover_path IS NOT NULL AND d.cover_path <> ''
			  AND NOT EXISTS (SELECT 1 FROM design_images x WHERE x.design_id = d.id AND x.is_cover = 1))`)

	// tags.source: origin ('manual' self-created | 'import' platform-side).
	// Handled separately so the one-time backfill only runs when the column is
	// first added (then ALTER TABLE returns no error); on later boots a
	// "duplicate column" error occurs and the backfill is skipped.
	if _, failure := database.Exec("ALTER TABLE tags ADD COLUMN source TEXT NOT NULL DEFAULT 'import'"); failure == nil {
		// Heuristic: imported tags always use #457b9d; anything with a different
		// color was a deliberate, self-made choice.
		dbutil.ExecLogged(database, "UPDATE tags SET source = 'manual' WHERE color <> '#457b9d'")
	}
}

// backfillPublicIDs gives every account still missing one an outward identifier.
func backfillPublicIDs(database *sql.DB) {
	backfillPublicIDsOf(database, "users")
}

// backfillPublicIDsOf gives every row of the table still missing one an outward
// identifier. Both tables that carry a public id use it, and both run it on
// every boot: an insert path that ever forgets the column then heals itself
// instead of leaving a row that no URL can address.
func backfillPublicIDsOf(database *sql.DB, table string) {
	// The table name is a literal from the two call sites, never input.
	rows, failure := database.Query("SELECT id FROM " + table + " WHERE public_id IS NULL OR public_id = ''")
	if failure != nil {
		return
	}
	var pending []int
	for rows.Next() {
		var rowID int
		if rows.Scan(&rowID) == nil {
			pending = append(pending, rowID)
		}
	}
	rows.Close()
	for _, rowID := range pending {
		dbutil.ExecLogged(database, "UPDATE "+table+" SET public_id = ? WHERE id = ?", publicid.New(), rowID)
	}
}
