-- MeshDepot SQLite schema (portiert aus docker/php/init.php).
-- ENUM -> TEXT + CHECK, TINYINT(1) -> INTEGER (0/1), DATETIME -> TEXT (CURRENT_TIMESTAMP),
-- ON UPDATE CURRENT_TIMESTAMP -> AFTER-UPDATE-Trigger, INSERT IGNORE -> INSERT OR IGNORE.
-- Idempotent: CREATE TABLE / TRIGGER / INDEX IF NOT EXISTS.

PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS users (
    id                   INTEGER PRIMARY KEY AUTOINCREMENT,
    -- Opaque outward identifier: it appears in every URL, every response body and
    -- every storage path, so the sequential primary key never leaves the process.
    -- Its unique index is created by migrate() rather than here, because an
    -- existing table has to have its rows backfilled first.
    public_id            TEXT    NOT NULL DEFAULT '',
    email                TEXT    DEFAULT NULL,
    -- Storage the account may use, in bytes. NULL = unlimited, the default.
    storage_quota_bytes  INTEGER DEFAULT NULL,
    -- Date notation for this account; '' follows the display language.
    date_format          TEXT    NOT NULL DEFAULT '',
    hash                 TEXT    NOT NULL,
    name                 TEXT    NOT NULL DEFAULT '',
    state                TEXT    NOT NULL DEFAULT 'active' CHECK (state IN ('active','inactive')),
    admin                INTEGER NOT NULL DEFAULT 0,
    language             TEXT    NOT NULL DEFAULT 'en',
    must_change_password INTEGER NOT NULL DEFAULT 0,
    avatar_path          TEXT    DEFAULT NULL,
    -- Style overrides the member wrote for themselves. Stored on the account
    -- rather than in their browser, so the look follows them to the next device
    -- and survives a reload.
    custom_css           TEXT    NOT NULL DEFAULT '',
    -- Start of the last manually triggered library sync. Carries the cooldown
    -- for members without a platform account, where no per-account stamp exists.
    last_manual_sync_at  TEXT    DEFAULT NULL,
    -- Start of the last "update all designs" run. Its own stamp: it queues the
    -- whole library for a re-download and is independent of the library sync.
    last_update_all_at   TEXT    DEFAULT NULL,
    totp_secret          TEXT    DEFAULT NULL,
    created_at           TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at           TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_users_email ON users (email);
CREATE TRIGGER IF NOT EXISTS trg_users_updated AFTER UPDATE ON users
BEGIN UPDATE users SET updated_at = CURRENT_TIMESTAMP WHERE id = NEW.id; END;

-- Persistente Server-Sessions (ersetzt die frühere In-Memory-Map): überleben
-- Neustarts, da die DB auf einem Volume liegt. persistent=1 → "Eingeloggt
-- bleiben" (lange Lebensdauer), sonst kurzlebige Session.
CREATE TABLE IF NOT EXISTS sessions (
    id         TEXT    PRIMARY KEY,
    user_id    INTEGER NOT NULL,
    persistent INTEGER NOT NULL DEFAULT 0,
    expires_at TEXT    NOT NULL,
    created_at TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_sessions_expires ON sessions (expires_at);
-- DeleteAllForUser (Passwortwechsel, Deaktivierung, Admin-Reset) löscht nach
-- user_id; ohne diesen Index ist das ein Full Table Scan.
CREATE INDEX IF NOT EXISTS idx_sessions_user ON sessions (user_id);

CREATE TABLE IF NOT EXISTS tags (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id    INTEGER NOT NULL,
    name       TEXT    NOT NULL,
    color      TEXT    NOT NULL DEFAULT '#457b9d',
    source     TEXT    NOT NULL DEFAULT 'import',
    created_at TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (user_id, name),
    FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE
);

-- Designs the user deleted and does not want back. The library sync inserts
-- whatever the platform lists, so without this table a deleted design returned
-- on the next run and had to be deleted again after every sync.
--
-- A design is pinned by (platform, source_id) where the URL yields one, and by
-- its URL otherwise; both are stored so either can match. Importing the same
-- design by hand removes the entry again - that is the way back.
CREATE TABLE IF NOT EXISTS sync_exclusions (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id         INTEGER NOT NULL,
    source_platform TEXT    NOT NULL,
    source_id       TEXT    NOT NULL DEFAULT '',
    source_url      TEXT    NOT NULL DEFAULT '',
    created_at      TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (user_id, source_platform, source_id, source_url),
    FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS designs (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    -- Outward identifier. The API addresses a design by this and never by the
    -- rowid: that one is sequential, so a link to design 70 told its reader
    -- that designs 1..69 exist and invited them to try the neighbours.
    public_id          TEXT    NOT NULL DEFAULT '',
    user_id            INTEGER NOT NULL,
    name               TEXT    NOT NULL,
    description        TEXT,
    source_url         TEXT,
    source_platform    TEXT    NOT NULL DEFAULT 'manual'
                       CHECK (source_platform IN ('thingiverse','printables','makerworld','thangs','cults3d','myminifactory','manual')),
    source_id          TEXT,
    cover_path         TEXT,
    category           TEXT,
    license            TEXT,
    author             TEXT,
    rating             INTEGER NOT NULL DEFAULT 0,
    print_time_minutes INTEGER DEFAULT NULL,
    notes              TEXT    DEFAULT NULL,
    source_deleted     INTEGER NOT NULL DEFAULT 0,
    last_synced_at     TEXT    DEFAULT NULL,
    is_hidden          INTEGER NOT NULL DEFAULT 0,
    created_at         TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at         TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_designs_user ON designs (user_id);
CREATE INDEX IF NOT EXISTS idx_designs_platform ON designs (source_platform);
CREATE TRIGGER IF NOT EXISTS trg_designs_updated AFTER UPDATE ON designs
BEGIN UPDATE designs SET updated_at = CURRENT_TIMESTAMP WHERE id = NEW.id; END;

CREATE TABLE IF NOT EXISTS design_tags (
    design_id INTEGER NOT NULL,
    tag_id    INTEGER NOT NULL,
    PRIMARY KEY (design_id, tag_id),
    FOREIGN KEY (design_id) REFERENCES designs (id) ON DELETE CASCADE,
    FOREIGN KEY (tag_id)    REFERENCES tags    (id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS design_files (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    design_id      INTEGER NOT NULL,
    version        TEXT    NOT NULL DEFAULT '1.0',
    filename       TEXT    NOT NULL,
    path           TEXT    NOT NULL,
    size_bytes     INTEGER NOT NULL DEFAULT 0,
    file_count     INTEGER NOT NULL DEFAULT 1,
    is_current     INTEGER NOT NULL DEFAULT 1,
    notes          TEXT,
    created_at     TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (design_id) REFERENCES designs (id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_df_design ON design_files (design_id);

CREATE TABLE IF NOT EXISTS design_file_entries (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    design_file_id INTEGER NOT NULL,
    filename       TEXT    NOT NULL,
    path           TEXT    NOT NULL,
    size_bytes     INTEGER NOT NULL DEFAULT 0,
    file_hash      TEXT    DEFAULT NULL,
    relative_path  TEXT    DEFAULT NULL,
    blob_hash      TEXT    DEFAULT NULL,
    gcode_meta     TEXT    DEFAULT NULL,
    created_at     TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (design_file_id) REFERENCES design_files (id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_dfe_file ON design_file_entries (design_file_id);
CREATE INDEX IF NOT EXISTS idx_dfe_hash ON design_file_entries (file_hash);

CREATE TABLE IF NOT EXISTS design_images (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    design_id  INTEGER NOT NULL,
    path       TEXT    NOT NULL,
    sort_order INTEGER NOT NULL DEFAULT 0,
    is_cover   INTEGER NOT NULL DEFAULT 0,
    created_at TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (design_id) REFERENCES designs (id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_di_design ON design_images (design_id);

CREATE TABLE IF NOT EXISTS collections (
    id                            INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id                       INTEGER NOT NULL,
    name                          TEXT    NOT NULL,
    description                   TEXT,
    cover_path                    TEXT,
    source_platform               TEXT    DEFAULT NULL,
    source_platform_collection_id TEXT    DEFAULT NULL,
    -- The name the platform last reported, without the "[Label] " prefix. It is
    -- what tells a local rename apart from a rename on the platform: as long as
    -- `name` still reads "[Label] source_name", nobody has touched it here and
    -- the sync may follow along.
    source_name                   TEXT    DEFAULT NULL,
    -- Hidden collections stay in the library but are left out of the collection
    -- list, the filter and a design's details. The collection tab can show them
    -- on request, which is the only way back to unhiding one.
    is_hidden                     INTEGER NOT NULL DEFAULT 0,
    created_at                    TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at                    TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_col_user ON collections (user_id);
CREATE TRIGGER IF NOT EXISTS trg_collections_updated AFTER UPDATE ON collections
BEGIN UPDATE collections SET updated_at = CURRENT_TIMESTAMP WHERE id = NEW.id; END;

CREATE TABLE IF NOT EXISTS design_collections (
    design_id     INTEGER NOT NULL,
    collection_id INTEGER NOT NULL,
    added_at      TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (design_id, collection_id),
    FOREIGN KEY (design_id)     REFERENCES designs     (id) ON DELETE CASCADE,
    FOREIGN KEY (collection_id) REFERENCES collections (id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS design_shares (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    design_id           INTEGER NOT NULL,
    owner_user_id       INTEGER NOT NULL,
    shared_with_user_id INTEGER NOT NULL,
    created_at          TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (design_id, shared_with_user_id),
    FOREIGN KEY (design_id)           REFERENCES designs (id) ON DELETE CASCADE,
    FOREIGN KEY (owner_user_id)       REFERENCES users   (id) ON DELETE CASCADE,
    FOREIGN KEY (shared_with_user_id) REFERENCES users   (id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_share_design ON design_shares (design_id);
CREATE INDEX IF NOT EXISTS idx_share_with ON design_shares (shared_with_user_id);

-- Links that hand a design to someone without an account: the token is the whole
-- credential, so it is 128 random bits and the row is the only place it lives.
-- expires_at NULL means the link runs until it is deleted.
CREATE TABLE IF NOT EXISTS design_share_links (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    design_id     INTEGER NOT NULL,
    user_id       INTEGER NOT NULL,
    token         TEXT    NOT NULL,
    expires_at    TEXT    DEFAULT NULL,
    last_used_at  TEXT    DEFAULT NULL,
    view_count    INTEGER NOT NULL DEFAULT 0,
    created_at    TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (design_id) REFERENCES designs (id) ON DELETE CASCADE,
    FOREIGN KEY (user_id)   REFERENCES users   (id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_dsl_token ON design_share_links (token);
CREATE INDEX IF NOT EXISTS idx_dsl_design ON design_share_links (design_id);

CREATE TABLE IF NOT EXISTS design_sync_log (
    id        INTEGER PRIMARY KEY AUTOINCREMENT,
    design_id INTEGER NOT NULL,
    status    TEXT    NOT NULL DEFAULT 'ok' CHECK (status IN ('ok','failed','up_to_date')),
    message   TEXT,
    synced_at TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (design_id) REFERENCES designs (id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_sl_design ON design_sync_log (design_id);

CREATE TABLE IF NOT EXISTS platform_accounts (
    id                     INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id                INTEGER NOT NULL,
    platform               TEXT    NOT NULL
                           CHECK (platform IN ('thingiverse','printables','makerworld','thangs','cults3d','myminifactory')),
    token                  TEXT,
    username               TEXT,
    state                  TEXT    NOT NULL DEFAULT 'active' CHECK (state IN ('active','inactive')),
    password_encrypted     TEXT    DEFAULT NULL,
    token_expires_at       TEXT    DEFAULT NULL,
    totp_secret            TEXT    DEFAULT NULL,
    session_cookie         TEXT    DEFAULT NULL,
    auto_library_sync      INTEGER NOT NULL DEFAULT 1,
    -- When a library sync was last started for this account. Written at trigger
    -- time and read as the cooldown of the manual "sync now" buttons, so the
    -- platform never sees a burst of requests from a clicked button.
    library_last_synced_at TEXT    DEFAULT NULL,
    sync_likes             INTEGER NOT NULL DEFAULT 1,
    sync_collections       INTEGER NOT NULL DEFAULT 1,
    created_at             TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at             TEXT    DEFAULT NULL,
    UNIQUE (user_id, platform),
    FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE
);
CREATE TRIGGER IF NOT EXISTS trg_pa_updated AFTER UPDATE ON platform_accounts
BEGIN UPDATE platform_accounts SET updated_at = CURRENT_TIMESTAMP WHERE id = NEW.id; END;

CREATE TABLE IF NOT EXISTS download_queue (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id      INTEGER NOT NULL,
    design_id    INTEGER DEFAULT NULL,
    source_url   TEXT    NOT NULL,
    platform     TEXT    NOT NULL,
    status       TEXT    NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','downloading','done','failed')),
    error_msg    TEXT,
    retry_count  INTEGER NOT NULL DEFAULT 0,
    created_at   TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    started_at   TEXT    DEFAULT NULL,
    done_at      TEXT    DEFAULT NULL,
    current_step TEXT    DEFAULT NULL,
    step_current INTEGER DEFAULT NULL,
    step_total   INTEGER DEFAULT NULL,
    FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_dq_status ON download_queue (status);

-- user_sessions wurde entfernt: Altlast der PHP-Portierung, von keinem
-- Anwendungscode gelesen oder geschrieben. Die aktiven Sessions stehen in
-- `sessions`. Bestehende DBs räumt migrate() auf.

CREATE TABLE IF NOT EXISTS notification_prefs (
    user_id           INTEGER NOT NULL PRIMARY KEY,
    sync_update       INTEGER NOT NULL DEFAULT 1,
    download_failed   INTEGER NOT NULL DEFAULT 1,
    download_done     INTEGER NOT NULL DEFAULT 1,
    design_shared     INTEGER NOT NULL DEFAULT 1,
    -- The server's total storage; only meaningful to an administrator, and only
    -- offered to one.
    storage_80        INTEGER NOT NULL DEFAULT 1,
    -- The member's own storage against their quota. Everyone sees this one.
    user_storage_80   INTEGER NOT NULL DEFAULT 1,
    -- Each type has a second switch for e-mail. The columns above stay the
    -- in-app entry, so a type can go to the bell, to the inbox, to both or
    -- nowhere. Default 0: nobody is mailed until they ask to be.
    sync_update_email     INTEGER NOT NULL DEFAULT 0,
    download_failed_email INTEGER NOT NULL DEFAULT 0,
    download_done_email   INTEGER NOT NULL DEFAULT 0,
    design_shared_email   INTEGER NOT NULL DEFAULT 0,
    storage_80_email      INTEGER NOT NULL DEFAULT 0,
    user_storage_80_email INTEGER NOT NULL DEFAULT 0,
    sync_min_age_days INTEGER DEFAULT 7,
    FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS notifications (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id    INTEGER NOT NULL,
    type       TEXT    NOT NULL,
    title      TEXT    NOT NULL,
    body       TEXT,
    design_id  INTEGER DEFAULT NULL,
    read_at    TEXT    DEFAULT NULL,
    created_at TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_notif_user ON notifications (user_id);

CREATE TABLE IF NOT EXISTS sync_queue (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    design_id  INTEGER NOT NULL,
    user_id    INTEGER NOT NULL,
    status     TEXT    NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','running','done','failed')),
    error_msg  TEXT,
    progress   INTEGER NOT NULL DEFAULT 0,
    current_step TEXT  DEFAULT NULL,
    step_current INTEGER DEFAULT NULL,
    step_total   INTEGER DEFAULT NULL,
    created_at TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    started_at TEXT    DEFAULT NULL,
    done_at    TEXT    DEFAULT NULL,
    FOREIGN KEY (design_id) REFERENCES designs (id) ON DELETE CASCADE,
    FOREIGN KEY (user_id)   REFERENCES users   (id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_sq_user_status ON sync_queue (user_id, status);

CREATE TABLE IF NOT EXISTS file_blobs (
    hash       TEXT    NOT NULL PRIMARY KEY,
    size_bytes INTEGER NOT NULL DEFAULT 0,
    stored_at  TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS pending_collection_assignments (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id          INTEGER NOT NULL,
    source_platform  TEXT    NOT NULL,
    source_design_id TEXT    NOT NULL,
    collection_id    INTEGER NOT NULL,
    created_at       TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (user_id, source_platform, source_design_id, collection_id),
    FOREIGN KEY (collection_id) REFERENCES collections (id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS design_translations (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    design_id  INTEGER NOT NULL,
    field      TEXT    NOT NULL CHECK (field IN ('name','description')),
    lang       TEXT    NOT NULL CHECK (lang IN ('original','en','de')),
    content    TEXT    NOT NULL,
    created_at TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (design_id, field, lang),
    FOREIGN KEY (design_id) REFERENCES designs (id) ON DELETE CASCADE
);
CREATE TRIGGER IF NOT EXISTS trg_dtr_updated AFTER UPDATE ON design_translations
BEGIN UPDATE design_translations SET updated_at = CURRENT_TIMESTAMP WHERE id = NEW.id; END;

-- Outbound request accounting. One row
-- per request that leaves the process for a platform, pruned after 48 hours.
-- The download cooldown paces jobs; this is what shows how many requests a job
-- actually costs.
CREATE TABLE IF NOT EXISTS platform_requests (
    id       INTEGER PRIMARY KEY AUTOINCREMENT,
    platform TEXT    NOT NULL,
    kind     TEXT    NOT NULL,
    status   INTEGER NOT NULL DEFAULT 0,
    at       TEXT    NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_pr_platform_at ON platform_requests (platform, at);

CREATE TABLE IF NOT EXISTS app_settings (
    key   TEXT NOT NULL PRIMARY KEY,
    value TEXT
);

INSERT OR IGNORE INTO app_settings (key, value) VALUES
    ('library_sync_hour', '3'),
    ('library_sync_force', '0'),
    ('library_sync_enabled', '1'),
    ('design_update_enabled', '1'),
    ('design_update_min_days', '7'),
    ('download_cooldown_default', '30'),
    ('download_cooldown_makerworld', '300'),
    ('download_cooldown_printables', '30'),
    ('download_cooldown_thingiverse', '30'),
    ('download_cooldown_thangs', '30'),
    ('download_cooldown_cults3d', '45'),
    ('download_cooldown_myminifactory', '30'),
    ('translation_enabled', '1'),
    ('queue_block_threshold', '3'),
    ('queue_block_hours', '24');

-- Keys for programmatic access, used by the browser extension and by anything
-- else that cannot hold a session: the session cookie is SameSite=Strict and is
-- not sent on a cross-site request.
--
-- Only the hash is stored. A key that the server can display again is one it can
-- also leak, and there is no reason to keep the plaintext: it is shown once at
-- creation and belongs to whoever wrote it down.
CREATE TABLE IF NOT EXISTS api_keys (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id      INTEGER NOT NULL,
    name         TEXT    NOT NULL,
    key_hash     TEXT    NOT NULL UNIQUE,
    -- The first characters of the key, so a person can tell two of them apart.
    prefix       TEXT    NOT NULL,
    created_at   TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_used_at TEXT    DEFAULT NULL,
    revoked_at   TEXT    DEFAULT NULL,
    -- When the key stops working. NULL means never, which is a choice rather
    -- than the default: the plaintext lives in a browser profile, a file on
    -- somebody's disk, and an expiry is what keeps a copied profile from being
    -- a permanent way in.
    expires_at   TEXT    DEFAULT NULL,
    FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_api_keys_user ON api_keys (user_id);

-- Fields a user defines for themselves. The definition belongs to the user, the
-- value to a design - which is also what keeps them private: a design shared
-- with somebody else is read with that person's own fields, and there are no
-- values under those, so nothing of the owner's shows through.
CREATE TABLE IF NOT EXISTS custom_fields (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id    INTEGER NOT NULL,
    name       TEXT    NOT NULL,
    -- text, int, float, boolean or select. Decides the input, how the value is
    -- rendered, and how it is filtered.
    field_type TEXT    NOT NULL DEFAULT 'text',
    -- The choices of a select, as a JSON array. Empty for every other type.
    options    TEXT    NOT NULL DEFAULT '',
    position   INTEGER NOT NULL DEFAULT 0,
    created_at TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (user_id, name),
    FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_custom_fields_user ON custom_fields (user_id);

CREATE TABLE IF NOT EXISTS design_custom_values (
    design_id INTEGER NOT NULL,
    field_id  INTEGER NOT NULL,
    value     TEXT    NOT NULL DEFAULT '',
    PRIMARY KEY (design_id, field_id),
    FOREIGN KEY (design_id) REFERENCES designs (id) ON DELETE CASCADE,
    FOREIGN KEY (field_id) REFERENCES custom_fields (id) ON DELETE CASCADE
);
