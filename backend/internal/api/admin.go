package api

import (
	"database/sql"
	"fmt"
	"meshdepot/internal/coerce"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"meshdepot/internal/auth"
	"meshdepot/internal/browser"
	"meshdepot/internal/dbutil"
	"meshdepot/internal/health"
	"meshdepot/internal/httpx"
	"meshdepot/internal/platforms/tor"
	"meshdepot/internal/publicid"
	"meshdepot/internal/queuestate"
	"meshdepot/internal/quota"
	"meshdepot/internal/scheduler"
	"meshdepot/internal/translate"
)

// settingBound is the allowed range of an app setting (all settings are int).
// tooLow names the error returned when the value undercuts min; only the
// download cooldowns set it, because silently clamping would show the admin a
// number they never chose. Everything else keeps the old clamp.
type settingBound struct {
	min, max int
	tooLow   string
}

// 30 seconds is the floor everywhere; makerworld needs far more because it
// answers a burst with a GeeTest captcha (HTTP 418) nothing here can solve.
func cooldownBound(minimum int, tooLow string) settingBound {
	return settingBound{min: minimum, max: 3600, tooLow: tooLow}
}

var settingsSchema = map[string]settingBound{
	"library_sync_hour":     {min: 0, max: 23},
	"library_sync_enabled":  {min: 0, max: 1},
	"design_update_enabled": {min: 0, max: 1},
	// A year is a lot, but an interval nobody reaches is still a valid answer to
	// "check my designs as rarely as possible".
	"design_update_min_days":          {min: scheduler.DesignUpdateMinDays, max: 365, tooLow: "error.design_update_interval_too_low"},
	"download_cooldown_default":       cooldownBound(30, "error.cooldown_too_low"),
	"download_cooldown_printables":    cooldownBound(30, "error.cooldown_too_low"),
	"download_cooldown_thingiverse":   cooldownBound(30, "error.cooldown_too_low"),
	"download_cooldown_makerworld":    cooldownBound(200, "error.cooldown_makerworld_too_low"),
	"download_cooldown_thangs":        cooldownBound(30, "error.cooldown_too_low"),
	"download_cooldown_cults3d":       cooldownBound(30, "error.cooldown_too_low"),
	"download_cooldown_myminifactory": cooldownBound(30, "error.cooldown_too_low"),
	"translation_enabled":             {min: 0, max: 1},
	// Auto-pause: how many anti-bot hits in a row pause a platform's queue, and for
	// how many hours. See queuestate and the worker's registerBlockStrike.
	"queue_block_threshold": {min: 1, max: 20},
	"queue_block_hours":     {min: 1, max: 720},
}

func (server *Server) AdminList(responseWriter http.ResponseWriter, request *http.Request) {
	rows, _ := dbutil.QueryMaps(server.DB, `
		SELECT u.public_id AS id, u.name, u.email, u.admin, u.state, u.must_change_password, u.created_at,
			COUNT(DISTINCT d.id) AS design_count,
			COALESCE(SUM(df.size_bytes), 0) AS used_bytes
		FROM users u
		LEFT JOIN designs d ON d.user_id = u.id
		LEFT JOIN design_files df ON df.design_id = d.id
		GROUP BY u.id ORDER BY u.name ASC`)
	httpx.Success(responseWriter, rows)
}

func (server *Server) AdminCreate(responseWriter http.ResponseWriter, request *http.Request) {
	var body map[string]any
	_ = httpx.DecodeJSON(request, &body)
	name := strings.TrimSpace(coerce.StringOr(body["name"], ""))
	password := coerce.StringOr(body["password"], "")
	if name == "" || password == "" {
		httpx.Error(responseWriter, http.StatusUnprocessableEntity, "error.credentials_required")
		return
	}
	if len(password) < 8 {
		httpx.Error(responseWriter, http.StatusUnprocessableEntity, "error.password_too_short")
		return
	}
	hash, failure := auth.HashPassword(password)
	if failure != nil {
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.server")
		return
	}
	insertResult, failure := server.DB.Exec(
		`INSERT INTO users (name, email, hash, admin, must_change_password, state, public_id) VALUES (?, ?, ?, ?, ?, 'active', ?)`,
		name, nullIfEmpty(strings.TrimSpace(coerce.StringOr(body["email"], ""))), hash, intFlag(body, "admin", 0), intFlag(body, "must_change_password", 0), publicid.New())
	if failure != nil {
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.server")
		return
	}
	id, _ := insertResult.LastInsertId()
	row, ok := server.fetchRow(responseWriter, "SELECT public_id AS id, name, email, admin, state, must_change_password FROM users WHERE id = ? LIMIT 1", id)
	if !ok {
		return
	}
	httpx.Success(responseWriter, row)
}

// requireUserByPublicID resolves the {id} path parameter to the internal user
// id, answering the request itself: 404 for a malformed or unknown id, 500 for a
// broken query. ok=false means the handler must return.
func (server *Server) requireUserByPublicID(responseWriter http.ResponseWriter, request *http.Request) (int, bool) {
	userID, found, failure := publicid.Resolve(server.DB, request.PathValue("id"))
	if failure != nil {
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.server")
		return 0, false
	}
	if !found {
		httpx.Error(responseWriter, http.StatusNotFound, "error.not_found")
		return 0, false
	}
	return userID, true
}

func (server *Server) AdminUpdate(responseWriter http.ResponseWriter, request *http.Request) {
	id, ok := server.requireUserByPublicID(responseWriter, request)
	if !ok {
		return
	}
	var body map[string]any
	_ = httpx.DecodeJSON(request, &body)
	var assignments []string
	var args []any
	if value, ok := body["name"]; ok {
		assignments = append(assignments, "name = ?")
		args = append(args, strings.TrimSpace(coerce.StringOr(value, "")))
	}
	if value, ok := body["email"]; ok {
		assignments = append(assignments, "email = ?")
		args = append(args, nullIfEmpty(strings.TrimSpace(coerce.StringOr(value, ""))))
	}
	if value, ok := body["admin"]; ok {
		// Revoking admin is as final as deactivating: the last admin could otherwise
		// demote themselves, and only editing the DB file brings the role back.
		if coerce.Int(value) != 1 && !server.guardLastAdmin(responseWriter, id) {
			return
		}
		assignments = append(assignments, "admin = ?")
		args = append(args, coerce.Int(value))
	}
	if value, ok := body["must_change_password"]; ok {
		assignments = append(assignments, "must_change_password = ?")
		args = append(args, coerce.Int(value))
	}
	if value, ok := body["storage_quota_bytes"]; ok {
		// At or below zero, and an explicit null, mean unlimited - so "no limit" has
		// one representation rather than competing with a stored 0.
		assignments = append(assignments, "storage_quota_bytes = ?")
		if value == nil {
			args = append(args, nil)
		} else if bytes := int64(coerce.Int(value)); bytes > 0 {
			args = append(args, bytes)
		} else {
			args = append(args, nil)
		}
	}
	if value, ok := body["state"]; ok {
		if coerce.StringOr(value, "") == "inactive" && !server.guardLastAdmin(responseWriter, id) {
			return
		}
		assignments = append(assignments, "state = ?")
		args = append(args, coerce.StringOr(value, ""))
	}
	if len(assignments) > 0 {
		args = append(args, id)
		if _, failure := server.DB.Exec("UPDATE users SET "+strings.Join(assignments, ", ")+" WHERE id = ?", args...); failure != nil {
			httpx.Error(responseWriter, http.StatusInternalServerError, "error.server")
			return
		}
		// Deactivating must drop the live sessions too, or an authenticated cookie
		// keeps working until it expires.
		if value, ok := body["state"]; ok && coerce.StringOr(value, "") != "active" {
			server.Auth.Sessions().DeleteAllForUser(id)
		}
	}
	row, ok := server.fetchRow(responseWriter, "SELECT public_id AS id, name, email, admin, state, must_change_password FROM users WHERE id = ? LIMIT 1", id)
	if !ok {
		return
	}
	httpx.Success(responseWriter, row)
}

func (server *Server) AdminDelete(responseWriter http.ResponseWriter, request *http.Request) {
	id, ok := server.requireUserByPublicID(responseWriter, request)
	if !ok {
		return
	}
	if !server.guardLastAdmin(responseWriter, id) {
		return
	}
	dbutil.ExecLogged(server.DB, "DELETE FROM users WHERE id = ?", id)
	server.Auth.Sessions().DeleteAllForUser(id)
	httpx.Success(responseWriter, nil)
}

func (server *Server) AdminResetPassword(responseWriter http.ResponseWriter, request *http.Request) {
	id, ok := server.requireUserByPublicID(responseWriter, request)
	if !ok {
		return
	}
	var body struct {
		New string `json:"new_password"`
	}
	_ = httpx.DecodeJSON(request, &body)
	if body.New == "" {
		httpx.Error(responseWriter, http.StatusUnprocessableEntity, "error.credentials_required")
		return
	}
	if len(body.New) < 8 {
		httpx.Error(responseWriter, http.StatusUnprocessableEntity, "error.password_too_short")
		return
	}
	hash, failure := auth.HashPassword(body.New)
	if failure != nil {
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.server")
		return
	}
	dbutil.ExecLogged(server.DB, "UPDATE users SET hash = ?, must_change_password = 1 WHERE id = ?", hash, id)
	// Locks out an attacker who had hijacked a session before the reset.
	server.Auth.Sessions().DeleteAllForUser(id)
	httpx.SuccessMessage(responseWriter, nil, "Password reset")
}

func (server *Server) AdminDetail(responseWriter http.ResponseWriter, request *http.Request) {
	// No resolution needed: the row is looked up by the same id the path carries.
	row, ok := server.fetchRow(responseWriter,
		`SELECT id AS numeric_id, public_id AS id, name, email, admin, state, must_change_password,
			storage_quota_bytes, created_at, updated_at
		 FROM users WHERE public_id = ? LIMIT 1`,
		request.PathValue("id"))
	if !ok {
		return
	}
	// What the account occupies, so the form can show the limit against what is in
	// use rather than as a number without a scale.
	if numericID := coerce.Int(row["numeric_id"]); numericID > 0 {
		row["used_bytes"] = quota.Of(server.DB, numericID).UsedBytes
	}
	// The sequential id never leaves the process; it was only read to measure.
	delete(row, "numeric_id")
	httpx.Success(responseWriter, row)
}

func (server *Server) AdminStats(responseWriter http.ResponseWriter, request *http.Request) {
	var userTotal, userActive int
	server.DB.QueryRow("SELECT COUNT(*), COALESCE(SUM(state = 'active'),0) FROM users").Scan(&userTotal, &userActive)
	countScalar := func(query string) int { var count int; server.DB.QueryRow(query).Scan(&count); return count }
	var fileCount, totalBytes int64
	server.DB.QueryRow("SELECT COUNT(*), COALESCE(SUM(size_bytes),0) FROM design_files").Scan(&fileCount, &totalBytes)
	platformCounts, _ := dbutil.QueryMaps(server.DB, `SELECT source_platform AS platform, COUNT(*) AS cnt FROM designs
		WHERE source_platform IS NOT NULL AND source_platform != '' GROUP BY source_platform ORDER BY cnt DESC`)
	perUser, _ := dbutil.QueryMaps(server.DB, `
		SELECT u.public_id AS id, u.name, COUNT(DISTINCT d.id) AS design_count,
			COALESCE(SUM(df.size_bytes),0) AS used_bytes,
			u.storage_quota_bytes
		FROM users u LEFT JOIN designs d ON d.user_id = u.id LEFT JOIN design_files df ON df.design_id = d.id
		WHERE u.state = 'active' GROUP BY u.id ORDER BY used_bytes DESC`)
	var newest any
	var newestStr string
	if server.DB.QueryRow("SELECT created_at FROM designs ORDER BY created_at DESC LIMIT 1").Scan(&newestStr) == nil {
		newest = newestStr
	}
	free, total := diskSpace(server.Cfg.BasePathData)
	httpx.Success(responseWriter, map[string]any{
		"user_count": userTotal, "active_users": userActive,
		"design_count": countScalar("SELECT COUNT(*) FROM designs"),
		"file_count":   fileCount, "total_bytes": totalBytes,
		"tag_count":        countScalar("SELECT COUNT(*) FROM tags"),
		"collection_count": countScalar("SELECT COUNT(*) FROM collections"),
		"synced_count":     countScalar("SELECT COUNT(*) FROM designs WHERE source_url IS NOT NULL AND source_url != ''"),
		"platforms":        platformCounts, "per_user": perUser, "newest_design_at": newest,
		"disk_free": free, "disk_total": total,
		// Counted per notification rather than per message: pending ones are bundled
		// into one e-mail per member.
		"mail_queued":        countScalar("SELECT COUNT(*) FROM notification_mail_queue WHERE sent_at IS NULL"),
		"mail_sent":          countScalar("SELECT COUNT(*) FROM notification_mail_queue WHERE sent_at IS NOT NULL"),
		"notification_count": countScalar("SELECT COUNT(*) FROM notifications"),
	})
}

// AdminHealth reports the internal subsystems: SQLite, storage, download and
// sync worker, scheduler, Tor and Chromium.
func (server *Server) AdminHealth(responseWriter http.ResponseWriter, request *http.Request) {
	checks := map[string]any{}

	// The database has no tile: this response is only reached through a session
	// looked up in that very database, so a broken one answers with a 401 or 500
	// long before the check could run.

	// 1) Storage.
	free, total := diskSpace(server.Cfg.BasePathData)
	used := total - free
	percent := 0
	if total > 0 {
		percent = int(float64(used) / float64(total) * 100)
	}
	storageStatus := "ok"
	if percent > 90 {
		storageStatus = "error"
	} else if percent > 75 {
		storageStatus = "warn"
	}
	checks["storage"] = map[string]any{
		"label_key": "health_storage_label", "label_vars": map[string]any{"path": server.Cfg.BasePathData},
		"status": storageStatus, "message_key": "health_storage_message",
		"vars": map[string]any{"free": fmtBytes(free), "total": fmtBytes(total), "pct": percent},
	}

	// 2) Download worker - queue depth from the table, liveness from the loop's own
	// heartbeat. A dead loop and an empty queue read identically otherwise.
	var downloadPending, downloadFailed int
	server.DB.QueryRow("SELECT COUNT(*) FROM download_queue WHERE status IN ('pending','downloading')").Scan(&downloadPending)
	server.DB.QueryRow("SELECT COUNT(*) FROM download_queue WHERE status='failed'").Scan(&downloadFailed)
	downloadStatus := "ok"
	if downloadFailed > 0 {
		downloadStatus = "warn"
	}
	downloadBeat, downloadRunning := server.Health.Get(health.DownloadWorker)
	checks["download_worker"] = loopCheck(loopReport{
		labelKey: "health_download_label", okKey: "health_download_ok", busyKey: "health_download_busy",
		okStatus: downloadStatus, vars: map[string]any{"pending": downloadPending, "failed": downloadFailed},
		status: downloadBeat, running: downloadRunning,
	})

	// 3) Sync worker - same, plus a warning once the queue piles up.
	var syncPending int
	server.DB.QueryRow("SELECT COUNT(*) FROM sync_queue WHERE status IN ('pending','running')").Scan(&syncPending)
	syncStatus, syncKey := "ok", "health_sync_ok"
	if syncPending >= syncBacklogWarn {
		syncStatus, syncKey = "warn", "health_sync_backlog"
	}
	syncBeat, syncRunning := server.Health.Get(health.SyncWorker)
	checks["sync_worker"] = loopCheck(loopReport{
		labelKey: "health_sync_label", okKey: syncKey, busyKey: "health_sync_busy",
		okStatus: syncStatus, vars: map[string]any{"pending": syncPending},
		status: syncBeat, running: syncRunning,
	})

	// 4) Scheduler - the worse of its two loops, since one dead loop stops syncing.
	schedulerBeat, schedulerRunning := server.Health.Worst(health.SchedulerForce, health.SchedulerAuto)
	schedulerCheck := loopCheck(loopReport{
		labelKey: "health_scheduler_label", okKey: "health_scheduler_ok", busyKey: "health_scheduler_busy",
		okStatus: "ok", vars: map[string]any{},
		status: schedulerBeat, running: schedulerRunning,
	})
	// A ticking loop is not a working one: with both switches off the loops keep
	// their heartbeat and do nothing. Said in the "off" status, because a green tile
	// with a sentence under it was read as "everything is fine".
	if schedulerCheck["status"] == "ok" {
		librarySync := settingEnabled(server.DB, "library_sync_enabled")
		designUpdates := settingEnabled(server.DB, "design_update_enabled")
		switch {
		case !librarySync && !designUpdates:
			schedulerCheck["message_key"] = "health_scheduler_all_off"
			schedulerCheck["status"] = "off"
		case !librarySync:
			schedulerCheck["message_key"] = "health_scheduler_library_off"
			schedulerCheck["status"] = "off"
		case !designUpdates:
			schedulerCheck["message_key"] = "health_scheduler_updates_off"
			schedulerCheck["status"] = "off"
		}
	}
	checks["scheduler"] = schedulerCheck

	// 5) Tor - SOCKS port reachable?
	if conn, failure := net.DialTimeout("tcp", tor.SocksAddr, time.Second); failure == nil {
		conn.Close()
		checks["tor"] = map[string]any{"label_key": "health_tor_label", "status": "ok", "message_key": "health_tor_ok"}
	} else {
		checks["tor"] = map[string]any{"label_key": "health_tor_label", "status": "error", "message_key": "health_tor_error", "detail": failure.Error()}
	}

	// 6) Chromium - the binary must be there and start. Only the start tells a
	// working install from a broken one: a missing shared library leaves the file
	// exactly where it was and still fails every download.
	fileInfo, failure := os.Stat(server.Cfg.ChromiumBin)
	switch {
	case failure != nil || fileInfo.IsDir():
		checks["browser"] = map[string]any{"label_key": "health_browser_label", "status": "error", "message_key": "health_browser_missing", "vars": map[string]any{"path": server.Cfg.ChromiumBin}}
	default:
		version, failure := browser.Probe(server.Cfg.ChromiumBin)
		if failure != nil {
			checks["browser"] = map[string]any{"label_key": "health_browser_label", "status": "error", "message_key": "health_browser_error", "vars": map[string]any{"path": server.Cfg.ChromiumBin, "detail": failure.Error()}}
		} else {
			checks["browser"] = map[string]any{"label_key": "health_browser_label", "status": "ok", "message_key": "health_browser_ok", "vars": map[string]any{"path": server.Cfg.ChromiumBin, "version": version}}
		}
	}

	httpx.Success(responseWriter, map[string]any{"checks": checks})
}

// syncBacklogWarn is the queue depth from which the sync worker counts as backed
// up. The scheduler enqueues at most 20 per tick, so less is a working queue.
const syncBacklogWarn = 50

// loopReport is the input of loopCheck - one background loop, its queue numbers
// and its heartbeat.
type loopReport struct {
	labelKey string
	// okKey is the message of a ticking loop, busyKey that of one inside a job.
	okKey, busyKey string
	// okStatus is the status a live loop gets; the queue numbers may already have
	// turned it into "warn".
	okStatus string
	vars     map[string]any
	status   health.Status
	// running is false when the loop never registered a heartbeat.
	running bool
}

// settingEnabled reads a 0/1 app setting, defaulting to enabled when the row is
// missing - what the rest of the app assumes for an unset switch.
func settingEnabled(database *sql.DB, key string) bool {
	var value string
	if database.QueryRow("SELECT value FROM app_settings WHERE key = ?", key).Scan(&value) != nil {
		return true
	}
	return value != "0"
}

// loopCheck turns a heartbeat into a health tile: the queue numbers describe the
// work, the heartbeat whether anyone is still doing it.
func loopCheck(report loopReport) map[string]any {
	check := map[string]any{"label_key": report.labelKey, "vars": report.vars}
	report.vars["age"] = fmtAge(report.status.Age)
	switch {
	case !report.running:
		check["status"] = "error"
		check["message_key"] = "health_loop_down"
	case report.status.Stale:
		check["status"] = "error"
		check["message_key"] = "health_loop_stale"
	case report.status.Busy:
		check["status"] = report.okStatus
		check["message_key"] = report.busyKey
	default:
		check["status"] = report.okStatus
		check["message_key"] = report.okKey
	}
	return check
}

func fmtAge(age time.Duration) string {
	if age < 0 {
		age = 0
	}
	switch {
	case age < time.Minute:
		return fmt.Sprintf("%ds", int(age.Seconds()))
	case age < time.Hour:
		return fmt.Sprintf("%dm %ds", int(age.Minutes()), int(age.Seconds())%60)
	default:
		return fmt.Sprintf("%dh %dm", int(age.Hours()), int(age.Minutes())%60)
	}
}

func (server *Server) GetSettings(responseWriter http.ResponseWriter, request *http.Request) {
	settings := server.loadSettings("SELECT key, value FROM app_settings")
	// The manual trigger is refused during its cooldown, so the page can disable the
	// button instead of offering a call that only comes back as 429.
	settings["library_sync_cooldown_seconds"] = syncCooldownRemaining(coerce.StringOr(settings["library_sync_last_run"], ""))
	// So the settings page can show what is suspended and offer a resume.
	settings["queue_blocks"] = queuestate.All(server.DB, time.Now())
	httpx.Success(responseWriter, settings)
}

// GetPublicSettings returns what every logged-in user needs: the sync hour that
// is displayed, and whether the library sync is switched on server-side.
func (server *Server) GetPublicSettings(responseWriter http.ResponseWriter, request *http.Request) {
	settings := server.loadSettings(
		"SELECT key, value FROM app_settings WHERE key IN ('library_sync_hour', 'library_sync_enabled', 'design_update_enabled', 'design_update_min_days')")
	// Whether notification mail can be sent at all, so the account page can grey out
	// its e-mail column. Only the fact is exposed, never the configuration.
	_, mailReady := server.mailConfig()
	settings["mail_enabled"] = mailReady
	httpx.Success(responseWriter, settings)
}

// SaveSettings stores the whitelisted settings. The whole body is validated
// before the first write, so a rejected cooldown leaves nothing half-saved.
func (server *Server) SaveSettings(responseWriter http.ResponseWriter, request *http.Request) {
	var body map[string]any
	_ = httpx.DecodeJSON(request, &body)
	accepted := map[string]int{}
	for key, bounds := range settingsSchema {
		value, ok := body[key]
		if !ok {
			continue
		}
		number := coerce.Int(value)
		if number < bounds.min {
			if bounds.tooLow != "" {
				httpx.Error(responseWriter, http.StatusUnprocessableEntity, bounds.tooLow)
				return
			}
			number = bounds.min
		}
		if number > bounds.max {
			number = bounds.max
		}
		accepted[key] = number
	}
	updated := map[string]any{}
	for key, number := range accepted {
		dbutil.ExecLogged(server.DB, "INSERT INTO app_settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value", key, strconv.Itoa(number))
		updated[key] = number
	}
	httpx.Success(responseWriter, updated)
}

// RunTranslationBackfill translates up to 5 designs without an 'original' name
// row; the frontend calls repeatedly until remaining=0 or processed=0.
func (server *Server) RunTranslationBackfill(responseWriter http.ResponseWriter, request *http.Request) {
	translator := translate.New(server.DB)
	if !translator.Enabled() {
		httpx.Success(responseWriter, map[string]any{"configured": false, "processed": 0, "failed": 0, "remaining": 0})
		return
	}
	const pending = `NOT EXISTS (SELECT 1 FROM design_translations t
		WHERE t.design_id = d.id AND t.field = 'name' AND t.lang = 'original')`

	rows, failure := server.DB.Query("SELECT d.id, d.name, d.description FROM designs d WHERE " + pending + " ORDER BY d.id ASC LIMIT 5")
	processed, failed := 0, 0
	if failure == nil {
		type job struct {
			id          int
			name        string
			description sql.NullString
		}
		var jobs []job
		for rows.Next() {
			var entry job
			if rows.Scan(&entry.id, &entry.name, &entry.description) == nil {
				jobs = append(jobs, entry)
			}
		}
		rows.Close()
		for _, entry := range jobs {
			var description *string
			if entry.description.Valid {
				description = &entry.description.String
			}
			if translator.ApplyToDesign(entry.id, entry.name, description, true) {
				processed++
			} else {
				failed++
			}
		}
	}

	var remaining int
	server.DB.QueryRow("SELECT COUNT(*) FROM designs d WHERE " + pending).Scan(&remaining)
	httpx.Success(responseWriter, map[string]any{"configured": true, "processed": processed, "failed": failed, "remaining": remaining})
}

// RunLibrarySync sets the force flag; the scheduler executes it.
func (server *Server) RunLibrarySync(responseWriter http.ResponseWriter, request *http.Request) {
	// Setting the flag while the sync is off would leave a "1" that fires the moment
	// someone switches the sync back on.
	if !server.guardLibrarySyncEnabled(responseWriter) {
		return
	}
	// This run walks every member's accounts, so pressing the button repeatedly hits
	// the platforms harder than any single member could.
	var lastRun string
	server.DB.QueryRow("SELECT COALESCE(value, '') FROM app_settings WHERE key='library_sync_last_run'").Scan(&lastRun)
	if syncCooldownRemaining(lastRun) > 0 {
		httpx.Error(responseWriter, http.StatusTooManyRequests, "error.sync_cooldown")
		return
	}
	dbutil.ExecLogged(server.DB, "INSERT INTO app_settings (key, value) VALUES ('library_sync_force', '1') ON CONFLICT(key) DO UPDATE SET value = '1'")
	dbutil.ExecLogged(server.DB, "INSERT INTO app_settings (key, value) VALUES ('library_sync_last_run', CURRENT_TIMESTAMP) ON CONFLICT(key) DO UPDATE SET value = CURRENT_TIMESTAMP")
	httpx.SuccessMessage(responseWriter, nil, "Library sync started")
}

func (server *Server) guardLastAdmin(responseWriter http.ResponseWriter, targetID int) bool {
	var isAdmin int
	if server.DB.QueryRow("SELECT admin FROM users WHERE id = ? LIMIT 1", targetID).Scan(&isAdmin) != nil || isAdmin != 1 {
		return true
	}
	var count int
	server.DB.QueryRow("SELECT COUNT(*) FROM users WHERE admin = 1 AND state = 'active'").Scan(&count)
	if count <= 1 {
		httpx.Error(responseWriter, http.StatusConflict, "error.last_admin")
		return false
	}
	return true
}

func (server *Server) loadSettings(query string) map[string]any {
	rows, _ := dbutil.QueryMaps(server.DB, query)
	result := map[string]any{}
	for _, row := range rows {
		key := coerce.StringOr(row["key"], "")
		value := coerce.StringOr(row["value"], "")
		if _, isInt := settingsSchema[key]; isInt {
			result[key], _ = strconv.Atoi(value)
		} else {
			result[key] = value
		}
	}
	return result
}

func diskSpace(path string) (free, total int64) {
	var stat syscall.Statfs_t
	if failure := syscall.Statfs(path, &stat); failure != nil {
		return 0, 0
	}
	return int64(stat.Bavail) * int64(stat.Bsize), int64(stat.Blocks) * int64(stat.Bsize)
}

func fmtBytes(bytes int64) string {
	switch {
	case bytes >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(bytes)/(1<<30))
	case bytes >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(bytes)/(1<<20))
	default:
		return fmt.Sprintf("%.0f KB", float64(bytes)/(1<<10))
	}
}
