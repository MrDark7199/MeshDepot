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

func Open(path string) (*sql.DB, error) {
	dataSourceName := fmt.Sprintf(
		"file:%s?_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)&_pragma=busy_timeout(5000)",
		url.PathEscape(path),
	)
	database, failure := sql.Open("sqlite", dataSourceName)
	if failure != nil {
		return nil, fmt.Errorf("open sqlite: %w", failure)
	}
	// SQLite tolerates only a single writer.
	database.SetMaxOpenConns(1)
	if failure := database.Ping(); failure != nil {
		return nil, fmt.Errorf("ping sqlite: %w", failure)
	}
	return database, nil
}

// InitSchema creates all tables idempotently and, on an empty DB, seeds a
// default admin (admin/admin) that must pick a new password on first login.
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
		// The seed credentials are public knowledge, so the account is only usable
		// long enough to replace them.
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

// migrate adds columns that CREATE TABLE IF NOT EXISTS no longer inserts into
// an older DB. Each ADD COLUMN is idempotent: the "duplicate column name" error
// on a later boot is discarded.
func migrate(database *sql.DB) {
	alterStatements := []string{
		"ALTER TABLE sync_queue ADD COLUMN current_step TEXT DEFAULT NULL",
		"ALTER TABLE sync_queue ADD COLUMN step_current INTEGER DEFAULT NULL",
		"ALTER TABLE sync_queue ADD COLUMN step_total INTEGER DEFAULT NULL",
		// Print parameters extracted from G-code files, as JSON.
		"ALTER TABLE design_file_entries ADD COLUMN gcode_meta TEXT DEFAULT NULL",
		// Hand-made order within a folder. 0 for everything that was there before,
		// which keeps the old sort by path.
		"ALTER TABLE design_file_entries ADD COLUMN sort_order INTEGER NOT NULL DEFAULT 0",
		// When the job was claimed. resetStuck compared against created_at before,
		// which measures the wait in the queue rather than the runtime.
		"ALTER TABLE sync_queue ADD COLUMN started_at TEXT DEFAULT NULL",
		// Outward identifier; backfilled below and made unique afterwards.
		"ALTER TABLE users ADD COLUMN public_id TEXT NOT NULL DEFAULT ''",
		// Kept per account, so the cooldown also holds for members without a platform.
		"ALTER TABLE users ADD COLUMN last_manual_sync_at TEXT DEFAULT NULL",
		"ALTER TABLE users ADD COLUMN last_update_all_at TEXT DEFAULT NULL",
		"ALTER TABLE designs ADD COLUMN public_id TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE users ADD COLUMN custom_css TEXT NOT NULL DEFAULT ''",
		// The platform's own name for a synced collection. NULL predates the column;
		// the next sync fills it and leaves the local name alone.
		"ALTER TABLE collections ADD COLUMN source_name TEXT DEFAULT NULL",
		// Collections pushed out of sight; see the column comment in schema.sql.
		"ALTER TABLE collections ADD COLUMN is_hidden INTEGER NOT NULL DEFAULT 0",
		// Default 0: an upgrade must not start mailing people who never asked for it.
		"ALTER TABLE notification_prefs ADD COLUMN sync_update_email INTEGER NOT NULL DEFAULT 0",
		"ALTER TABLE notification_prefs ADD COLUMN download_failed_email INTEGER NOT NULL DEFAULT 0",
		"ALTER TABLE notification_prefs ADD COLUMN download_done_email INTEGER NOT NULL DEFAULT 0",
		"ALTER TABLE notification_prefs ADD COLUMN design_shared_email INTEGER NOT NULL DEFAULT 0",
		"ALTER TABLE notification_prefs ADD COLUMN storage_80_email INTEGER NOT NULL DEFAULT 0",
		// NULL means unlimited, which is what every existing account gets.
		"ALTER TABLE users ADD COLUMN storage_quota_bytes INTEGER DEFAULT NULL",
		// Empty means "follow the display language", which is what everyone had.
		"ALTER TABLE users ADD COLUMN date_format TEXT NOT NULL DEFAULT ''",
		// Only the hash is kept; see schema.sql.
		`CREATE TABLE IF NOT EXISTS api_keys (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id      INTEGER NOT NULL,
			name         TEXT    NOT NULL,
			key_hash     TEXT    NOT NULL UNIQUE,
			prefix       TEXT    NOT NULL,
			created_at   TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP,
			last_used_at TEXT    DEFAULT NULL,
			revoked_at   TEXT    DEFAULT NULL,
			FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE
		)`,
		"CREATE INDEX IF NOT EXISTS idx_api_keys_user ON api_keys (user_id)",
		// NULL keeps the old behaviour of never expiring.
		"ALTER TABLE api_keys ADD COLUMN expires_at TEXT DEFAULT NULL",
		// The member's own storage, as opposed to storage_80, which is about the
		// server as a whole and only concerns admins.
		"ALTER TABLE notification_prefs ADD COLUMN user_storage_80 INTEGER NOT NULL DEFAULT 1",
		"ALTER TABLE notification_prefs ADD COLUMN user_storage_80_email INTEGER NOT NULL DEFAULT 0",
	}
	// Its own table rather than a column on notifications: a member can have the
	// e-mail switched on and the bell off, leaving no notifications row.
	dbutil.ExecLogged(database, `CREATE TABLE IF NOT EXISTS notification_mail_queue (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id    INTEGER NOT NULL,
		type       TEXT    NOT NULL,
		title      TEXT    NOT NULL,
		body       TEXT,
		-- What the entry is about, so the digest can check at send time whether
		-- it still holds: the source URL for a download, empty otherwise.
		reference  TEXT    DEFAULT NULL,
		created_at TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP,
		sent_at    TEXT    DEFAULT NULL,
		FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE
	)`)
	dbutil.ExecLogged(database, "CREATE INDEX IF NOT EXISTS idx_notif_mail_pending ON notification_mail_queue (user_id, sent_at)")
	for _, statement := range alterStatements {
		database.Exec(statement) // Error (column already exists) intentionally ignored.
	}
	// Runs on every boot rather than once, so an insert path that forgets the
	// column heals itself instead of leaving a row no URL can address.
	backfillPublicIDs(database)
	backfillPublicIDsOf(database, "designs")
	// After the backfill, never before: InitSchema runs schema.sql as a single
	// Exec, so an index failing on two empty values takes the rest with it.
	dbutil.ExecLogged(database, "CREATE UNIQUE INDEX IF NOT EXISTS uq_users_public_id ON users (public_id)")
	dbutil.ExecLogged(database, "CREATE UNIQUE INDEX IF NOT EXISTS uq_designs_public_id ON designs (public_id)")

	// sessions.expires_at was written as RFC3339 while every other time column
	// uses CURRENT_TIMESTAMP's format. A no-op on already-converted rows.
	dbutil.ExecLogged(database, "UPDATE sessions SET expires_at = strftime('%Y-%m-%d %H:%M:%S', expires_at) WHERE expires_at LIKE '%T%'")
	// user_sessions: dead table, never read or written.
	dbutil.ExecLogged(database, "DROP TABLE IF EXISTS user_sessions")

	// The old seeds (15 s, 30 s for makerworld) are low enough to get an account
	// rate-limited, and the settings form no longer accepts them.
	dbutil.ExecLogged(database, "UPDATE app_settings SET value = '30' WHERE key LIKE 'download_cooldown_%' AND key <> 'download_cooldown_makerworld' AND CAST(value AS INTEGER) < 30")
	dbutil.ExecLogged(database, "UPDATE app_settings SET value = '300' WHERE key = 'download_cooldown_makerworld' AND CAST(value AS INTEGER) < 200")

	// The setting used to be stored but never read, so a member could have saved
	// any number down to one day. The floor comes from the setting itself, so
	// raising it as an admin also lifts the members below it.
	dbutil.ExecLogged(database, `
		UPDATE notification_prefs
		SET sync_min_age_days = (SELECT CAST(value AS INTEGER) FROM app_settings WHERE key = 'design_update_min_days')
		WHERE sync_min_age_days IS NOT NULL
		  AND sync_min_age_days < (SELECT CAST(value AS INTEGER) FROM app_settings WHERE key = 'design_update_min_days')`)

	// Designs whose cover was lost to the publishing bug (see publishCover in
	// internal/platforms/save.go): the first gallery image is what the cover would
	// have been. Only touches rows that have images and no cover.
	dbutil.ExecLogged(database, `
		UPDATE designs
		SET cover_path = (SELECT path FROM design_images
			WHERE design_id = designs.id ORDER BY sort_order ASC, created_at ASC LIMIT 1)
		WHERE (cover_path IS NULL OR cover_path = '')
		  AND EXISTS (SELECT 1 FROM design_images WHERE design_id = designs.id)`)

	// Which image is the cover is stored twice - designs.cover_path drives the
	// overview card, design_images.is_cover the gallery marker - and only the API
	// paths wrote both. Matched by path, falling back to the first image, in two
	// steps because SQLite does not resolve a correlation two levels deep.
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

	// Handled separately so the backfill only runs when the column is first added;
	// on later boots the duplicate-column error skips it.
	if _, failure := database.Exec("ALTER TABLE tags ADD COLUMN source TEXT NOT NULL DEFAULT 'import'"); failure == nil {
		// Imported tags always use #457b9d; a different colour was a deliberate choice.
		dbutil.ExecLogged(database, "UPDATE tags SET source = 'manual' WHERE color <> '#457b9d'")
	}
}

func backfillPublicIDs(database *sql.DB) {
	backfillPublicIDsOf(database, "users")
}

// Both tables that carry a public id run this on every boot, so an insert path
// that forgets the column heals itself.
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
