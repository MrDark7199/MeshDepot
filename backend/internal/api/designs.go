package api

import (
	"database/sql"
	"fmt"
	"io"
	"meshdepot/internal/coerce"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"meshdepot/internal/dbutil"
	"meshdepot/internal/httpx"
	"meshdepot/internal/platforms"
	"meshdepot/internal/publicid"
	"meshdepot/internal/storage"
	"meshdepot/internal/translate"
)

// Translation fragments: expose the German display translations as
// name_de/description_de next to the canonical fields (designs alias d).
const translationSelect = `, tr_name_de.content AS name_de, tr_desc_de.content AS description_de`

const translationJoin = `
	LEFT JOIN design_translations tr_name_de ON tr_name_de.design_id = d.id AND tr_name_de.field = 'name' AND tr_name_de.lang = 'de'
	LEFT JOIN design_translations tr_desc_de ON tr_desc_de.design_id = d.id AND tr_desc_de.field = 'description' AND tr_desc_de.lang = 'de'`

// maxTagFilters caps how many tag ids the filter accepts - each one adds a
// self-join to the query.
const maxTagFilters = 20

// designID resolves the design path parameter - the outward public id - to the
// internal designs.id. ok=false => a response (404 or 500) was written.
//
// Nothing outside the process knows the rowid. It is sequential, so a link to
// design 70 told its reader that 1..69 exist and invited them to walk the
// neighbours; every one of those tries was answered with a 404, but the shape
// of the library leaked all the same.
func (server *Server) designID(responseWriter http.ResponseWriter, request *http.Request, name string) (int, bool) {
	id, found, failure := publicid.ResolveIn(server.DB, "designs", request.PathValue(name))
	if failure != nil {
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.server")
		return 0, false
	}
	if !found {
		httpx.Error(responseWriter, http.StatusNotFound, "error.not_found")
		return 0, false
	}
	return id, true
}

// requireOwnership loads a design and checks ownership. ok=false => a
// response (500 or 404) was written.
//
// A design that exists but belongs to someone else is answered with 404, not
// 403: a 403 would confirm the id exists.
func (server *Server) requireOwnership(responseWriter http.ResponseWriter, designID, currentUserID int) (designRow, bool) {
	design, found, failure := loadDesign(server.DB, designID)
	if failure != nil {
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.server")
		return designRow{}, false
	}
	if !found || design.UserID != currentUserID {
		httpx.Error(responseWriter, http.StatusNotFound, "error.not_found")
		return designRow{}, false
	}
	return design, true
}

// DesignsIndex returns visible designs (own + shared) with filters, pagination
// and attached tags.
func (server *Server) DesignsIndex(responseWriter http.ResponseWriter, request *http.Request) {
	currentUserID := userID(request)
	search := strings.TrimSpace(queryStr(request, "search", ""))
	platform := queryStr(request, "source_platform", "")
	if platform == "" {
		platform = queryStr(request, "platform", "")
	}
	tagIDsParam := queryStr(request, "tag_ids", "")
	sharedOnly := queryBool(request, "shared_only")
	showHidden := queryBool(request, "show_hidden")
	page := max(1, queryInt(request, "page", 1))
	perPage := min(1000, max(1, queryInt(request, "per_page", 50)))
	offset := (page - 1) * perPage
	// Sorting is applied globally (before the pagination), otherwise each page
	// would be an independently sorted updated_at window.
	sortField := queryStr(request, "sort", "updated_at")
	if sortField != "name" && sortField != "platform" && sortField != "updated_at" {
		sortField = "updated_at"
	}
	sortDir := queryStr(request, "dir", "desc")
	if sortDir != "asc" {
		sortDir = "desc"
	}

	sharedSelect := `SELECT d.*, df.id AS current_file_id, df.version AS current_version,
		df.filename, df.size_bytes, df.file_count, 1 AS is_shared, u.name AS shared_by_name` + translationSelect + `
		FROM design_shares ds
		JOIN designs d ON d.id = ds.design_id
		JOIN users u ON u.id = ds.owner_user_id
		LEFT JOIN design_files df ON df.design_id = d.id AND df.is_current = 1` + translationJoin

	// Sorting and pagination happen in SQL. Loading every visible design into
	// memory to sort and slice it there meant 10.000 rows were materialized to
	// serve a page of 50.
	orderClause := " ORDER BY " + sortColumn(sortField) + " " + strings.ToUpper(sortDir) + ", id DESC"

	if sharedOnly {
		sharedInner := sharedSelect + " WHERE ds.shared_with_user_id = ?"
		var total int
		if failure := server.DB.QueryRow("SELECT COUNT(*) FROM ("+sharedInner+")", currentUserID).Scan(&total); failure != nil {
			httpx.Error(responseWriter, http.StatusInternalServerError, "error.server")
			return
		}
		rows, failure := dbutil.QueryMaps(server.DB,
			"SELECT * FROM ("+sharedInner+")"+orderClause+" LIMIT ? OFFSET ?", currentUserID, perPage, offset)
		if failure != nil {
			httpx.Error(responseWriter, http.StatusInternalServerError, "error.server")
			return
		}
		server.attachTags(rows, currentUserID)
		httpx.Success(responseWriter, map[string]any{"items": rows, "total": total})
		return
	}

	whereClause := "d.user_id = ?"
	args := []any{currentUserID}
	if !showHidden {
		whereClause += " AND d.is_hidden = 0"
	}
	if search != "" {
		whereClause += " AND (d.name LIKE ? ESCAPE '\\' OR d.author LIKE ? ESCAPE '\\' OR d.description LIKE ? ESCAPE '\\')"
		like := likePattern(search)
		args = append(args, like, like, like)
	}
	if platform != "" {
		whereClause += " AND d.source_platform = ?"
		args = append(args, platform)
	}
	// Every tag id becomes its own JOIN (AND semantics). Only validated ints are
	// interpolated, and the count is capped: an unparsable id used to silently
	// become tag_id = 0 (empty result instead of a 422), and ?tag_ids=1,1,1,…
	// built an arbitrarily large self-join.
	tagJoin := ""
	if tagIDsParam != "" {
		rawIDs := strings.Split(tagIDsParam, ",")
		if len(rawIDs) > maxTagFilters {
			httpx.Error(responseWriter, http.StatusUnprocessableEntity, "error.too_many_tag_filters")
			return
		}
		for index, rawTagID := range rawIDs {
			id, failure := strconv.Atoi(strings.TrimSpace(rawTagID))
			if failure != nil || id <= 0 {
				httpx.Error(responseWriter, http.StatusUnprocessableEntity, "error.invalid_input")
				return
			}
			tagJoin += fmt.Sprintf(" JOIN design_tags dt%d ON dt%d.design_id = d.id AND dt%d.tag_id = %d", index, index, index, id)
		}
	}

	ownQuery := `SELECT d.*, df.id AS current_file_id, df.version AS current_version,
		df.filename, df.size_bytes, df.file_count, 0 AS is_shared, NULL AS shared_by_name` + translationSelect + `
		FROM designs d
		LEFT JOIN design_files df ON df.design_id = d.id AND df.is_current = 1` + translationJoin + tagJoin +
		" WHERE " + whereClause

	// since_id is gone with the numeric ids: it filtered by rowid, and no client
	// is told a rowid any more, so nothing could supply a meaningful value. The
	// frontend already reloads the page it is on instead.
	sharedWhere := "ds.shared_with_user_id = ?"
	sharedArgs := []any{currentUserID}
	// Own and shared designs as one result set, so the database can sort and
	// paginate across both.
	unionQuery := ownQuery + " UNION ALL " + sharedSelect + " WHERE " + sharedWhere
	unionArgs := append(append([]any{}, args...), sharedArgs...)

	var total int
	if failure := server.DB.QueryRow("SELECT COUNT(*) FROM ("+unionQuery+")", unionArgs...).Scan(&total); failure != nil {
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.server")
		return
	}
	pagedDesigns, failure := dbutil.QueryMaps(server.DB,
		"SELECT * FROM ("+unionQuery+")"+orderClause+" LIMIT ? OFFSET ?",
		append(unionArgs, perPage, offset)...)
	if failure != nil {
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.server")
		return
	}
	server.attachTags(pagedDesigns, currentUserID)
	httpx.Success(responseWriter, map[string]any{"items": pagedDesigns, "total": total})
}

// sortColumn maps the sort parameter to the column of the result set. Names are
// compared case-insensitively, as the previous in-memory sort did.
func sortColumn(sortField string) string {
	switch sortField {
	case "name":
		return "LOWER(name)"
	case "platform":
		return "source_platform"
	default:
		return "updated_at"
	}
}

// DesignsShow returns a design with tags, images and (for the owner) shares.
func (server *Server) DesignsShow(responseWriter http.ResponseWriter, request *http.Request) {
	currentUserID := userID(request)
	designID, ok := server.designID(responseWriter, request, "id")
	if !ok {
		return
	}
	design, ok := server.fetchRow(responseWriter, `
		SELECT d.*,
			CASE WHEN d.user_id != ? THEN 1 ELSE 0 END AS is_shared,
			shared_u.name AS shared_by_name`+translationSelect+`
		FROM designs d
		LEFT JOIN design_shares ds_check ON ds_check.design_id = d.id AND ds_check.shared_with_user_id = ?
		LEFT JOIN users shared_u ON shared_u.id = ds_check.owner_user_id`+translationJoin+`
		WHERE d.id = ? AND (d.user_id = ? OR ds_check.shared_with_user_id = ?)
		LIMIT 1`, currentUserID, currentUserID, designID, currentUserID, currentUserID)
	if !ok {
		return
	}
	tags, _ := dbutil.QueryMaps(server.DB, `
		SELECT t.id, t.name, t.color,
			(SELECT COUNT(*) FROM design_tags dt2 WHERE dt2.tag_id = t.id) AS usage_count
		FROM design_tags dt JOIN tags t ON t.id = dt.tag_id
		WHERE dt.design_id = ? ORDER BY usage_count DESC, t.name ASC`, designID)
	design["tags"] = tags
	images, _ := dbutil.QueryMaps(server.DB, "SELECT * FROM design_images WHERE design_id = ? ORDER BY sort_order ASC, created_at ASC", designID)
	design["images"] = images
	if coerce.Int(design["user_id"]) == currentUserID {
		shares, _ := dbutil.QueryMaps(server.DB, `
			SELECT ds.id, ds.shared_with_user_id, u.name AS shared_with_name,
				u.email AS shared_with_email, ds.created_at
			FROM design_shares ds JOIN users u ON u.id = ds.shared_with_user_id
			WHERE ds.design_id = ? AND ds.owner_user_id = ? ORDER BY u.name ASC`, designID, currentUserID)
		design["shares"] = shares
	} else {
		design["shares"] = []any{}
	}
	httpx.Success(responseWriter, design)
}

func (server *Server) DesignsStore(responseWriter http.ResponseWriter, request *http.Request) {
	var body map[string]any
	_ = httpx.DecodeJSON(request, &body)
	name, _ := body["name"].(string)
	if strings.TrimSpace(name) == "" {
		httpx.Error(responseWriter, http.StatusUnprocessableEntity, "error.name_required")
		return
	}
	// Checked on the way in as well as on update - a design created with a dead
	// source url is one the sync can never resolve.
	sourceURL, ok := normalizeSourceURL(coerce.StringOr(body["source_url"], ""))
	if !ok {
		httpx.Error(responseWriter, http.StatusUnprocessableEntity, "error.invalid_url")
		return
	}
	body["source_url"] = sourceURL
	platform := coerce.StringOr(body["source_platform"], "manual")
	insertResult, failure := server.DB.Exec(
		`INSERT INTO designs (user_id, public_id, name, description, source_url, source_platform, source_id, category, license, author)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		userID(request), publicid.New(), name, nullStr(body["description"]), nullStr(body["source_url"]), platform,
		nullStr(body["source_id"]), nullStr(body["category"]), nullStr(body["license"]), nullStr(body["author"]))
	if failure != nil {
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.internal")
		return
	}
	id, _ := insertResult.LastInsertId()
	row, ok := server.fetchRow(responseWriter, "SELECT * FROM designs WHERE id = ? LIMIT 1", id)
	if !ok {
		return
	}
	httpx.SuccessStatus(responseWriter, http.StatusCreated, row, "Design created")
}

// designUpdatable are the fields changeable via update.
var designUpdatable = []string{"name", "description", "source_url", "source_platform", "source_id", "category", "license", "author", "rating", "print_time_minutes", "notes", "is_hidden"}
var designIntFields = map[string]bool{"rating": true, "print_time_minutes": true, "is_hidden": true}

// hostLabel is one label of a hostname: letters, digits and hyphens, and no
// hyphen at either end.
var hostLabel = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?$`)

// normalizeSourceURL validates the source url and returns what should be
// stored. An empty value is kept (the field is optional).
//
// The column feeds the link in the UI and the sync, so the value has to be a
// reachable address rather than merely something url.Parse accepts: "http://a"
// parses fine and has a host, but there is no such site, and the design it
// belongs to can never be resolved. A registrable name is therefore required -
// at least two labels with a letters-only tld, or a literal IP.
//
// The scheme may be left out. Nobody types "https://" in front of an address
// they copied, and refusing "printables.com/model/1" for that reason is a rule
// the user has to learn from an error message; it is prefixed instead.
func normalizeSourceURL(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", true
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	parsed, failure := url.Parse(raw)
	if failure != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", false
	}
	if !validHostname(parsed.Hostname()) {
		return "", false
	}
	return parsed.String(), true
}

// validHostname reports whether the host part is one a browser could resolve.
func validHostname(host string) bool {
	if host == "" {
		return false
	}
	if net.ParseIP(host) != nil {
		return true
	}
	labels := strings.Split(strings.TrimSuffix(host, "."), ".")
	if len(labels) < 2 {
		return false
	}
	for _, label := range labels {
		if len(label) > 63 || !hostLabel.MatchString(label) {
			return false
		}
	}
	// The tld carries no digits or hyphens, which is what separates a real
	// address from a typo like "http://afeefafefefaffefef".
	tld := labels[len(labels)-1]
	if len(tld) < 2 {
		return false
	}
	for _, character := range tld {
		if character < 'a' || character > 'z' {
			if character < 'A' || character > 'Z' {
				return false
			}
		}
	}
	return true
}

// DesignsUpdate changes existing fields of a design. Note: the re-translation
// (GoogleTranslator) is carried over in the service port.
func (server *Server) DesignsUpdate(responseWriter http.ResponseWriter, request *http.Request) {
	currentUserID := userID(request)
	designID, ok := server.designID(responseWriter, request, "id")
	if !ok {
		return
	}
	if _, ok := server.requireOwnership(responseWriter, designID, currentUserID); !ok {
		return
	}
	var body map[string]any
	_ = httpx.DecodeJSON(request, &body)
	if value, present := body["source_url"]; present {
		normalized, ok := normalizeSourceURL(coerce.StringOr(value, ""))
		if !ok {
			httpx.Error(responseWriter, http.StatusUnprocessableEntity, "error.invalid_url")
			return
		}
		// Stored with the scheme the input may have left out, so the sync and the
		// outgoing link get an address they can use.
		body["source_url"] = normalized
	}
	var assignments []string
	var args []any
	for _, field := range designUpdatable {
		value, present := body[field]
		if !present {
			continue
		}
		assignments = append(assignments, field+" = ?")
		if designIntFields[field] {
			args = append(args, nullInt(value))
		} else {
			args = append(args, value)
		}
	}
	if len(assignments) > 0 {
		args = append(args, designID)
		if _, failure := server.DB.Exec("UPDATE designs SET "+strings.Join(assignments, ", ")+" WHERE id = ?", args...); failure != nil {
			httpx.Error(responseWriter, http.StatusInternalServerError, "error.server")
			return
		}
	}

	// Changed name/description: re-translate into canonical EN + display languages
	// (storeOriginal=false so platform originals stay preserved for the sync).
	// Fail-safe: endpoint down → input stays saved.
	if _, hasName := body["name"]; hasName || hasKey(body, "description") {
		var name string
		var description sql.NullString
		if server.DB.QueryRow("SELECT name, description FROM designs WHERE id=?", designID).Scan(&name, &description) == nil {
			var descriptionPointer *string
			if description.Valid {
				descriptionPointer = &description.String
			}
			translate.New(server.DB).ApplyToDesign(designID, name, descriptionPointer, false)
		}
	}
	httpx.SuccessMessage(responseWriter, nil, "Updated")
}

// hasKey checks whether a key is present in the JSON body.
func hasKey(body map[string]any, key string) bool {
	_, ok := body[key]
	return ok
}

// DesignsDestroy deletes a design incl. its STL directory.
func (server *Server) DesignsDestroy(responseWriter http.ResponseWriter, request *http.Request) {
	currentUserID := userID(request)
	designID, ok := server.designID(responseWriter, request, "id")
	if !ok {
		return
	}
	if _, ok := server.requireOwnership(responseWriter, designID, currentUserID); !ok {
		return
	}
	// Read the content before the rows go: the cascade takes the entries with the
	// design, and nothing would connect it to its blobs afterwards.
	released := entriesToRelease(server.DB, "df.design_id = ?", designID)

	// The row first, the files after: a directory removed up front would be gone
	// even if the delete failed, leaving a design that lists files it no longer
	// has.
	if _, failure := server.DB.Exec("DELETE FROM designs WHERE id = ?", designID); failure != nil {
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.server")
		return
	}
	user := server.userLayout(request)
	_ = os.RemoveAll(user.Design(designID))
	releaseEntries(user, released)
	httpx.SuccessMessage(responseWriter, nil, "Deleted")
}

// DesignsSync puts a design into the sync_queue.
func (server *Server) DesignsSync(responseWriter http.ResponseWriter, request *http.Request) {
	currentUserID := userID(request)
	designID, ok := server.designID(responseWriter, request, "id")
	if !ok {
		return
	}
	design, ok := server.requireOwnership(responseWriter, designID, currentUserID)
	if !ok {
		return
	}
	if design.SourceURL == "" {
		httpx.Error(responseWriter, http.StatusUnprocessableEntity, "error.no_source_url")
		return
	}
	// A failed probe is reported instead of enqueueing a second job for the same
	// design: sync_queue has no unique constraint.
	_, alreadyQueued, failure := dbutil.QueryMap(server.DB, "SELECT id FROM sync_queue WHERE design_id = ? AND status IN ('pending','running') LIMIT 1", designID)
	if failure != nil {
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.server")
		return
	}
	if alreadyQueued {
		httpx.SuccessMessage(responseWriter, map[string]any{"design_id": request.PathValue("id"), "status": "already_queued"}, "Already queued")
		return
	}
	if _, failure := server.DB.Exec("INSERT INTO sync_queue (design_id, user_id, status) VALUES (?, ?, 'pending')", designID, currentUserID); failure != nil {
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.server")
		return
	}
	httpx.SuccessMessage(responseWriter, map[string]any{"design_id": request.PathValue("id"), "status": "queued"}, "Sync queued")
}

// DesignsDuplicates finds possible duplicates via source_url and name similarity.
// Note: SQLite has no SOUNDEX - therefore only a LIKE prefix match (instead of SOUNDEX).
func (server *Server) DesignsDuplicates(responseWriter http.ResponseWriter, request *http.Request) {
	currentUserID := userID(request)
	designID, ok := server.designID(responseWriter, request, "id")
	if !ok {
		return
	}
	design, ok := server.requireOwnership(responseWriter, designID, currentUserID)
	if !ok {
		return
	}
	duplicates := []map[string]any{}
	// Keyed by the public id the rows now carry. The design itself is already
	// excluded by both queries; what this guards is a candidate that matches on
	// the url as well as on the name.
	seen := map[string]bool{}
	collect := func(rows []map[string]any) {
		for _, candidate := range rows {
			if id := coerce.StringOr(candidate["id"], ""); id != "" && !seen[id] {
				seen[id] = true
				duplicates = append(duplicates, candidate)
			}
		}
	}
	if design.SourceURL != "" {
		rows, _ := dbutil.QueryMaps(server.DB, "SELECT public_id AS id, name, source_url, cover_path FROM designs WHERE user_id = ? AND source_url = ? AND id != ? LIMIT 5", currentUserID, design.SourceURL, designID)
		collect(rows)
	}
	prefix := strings.ToLower(design.Name)
	if len(prefix) > 10 {
		prefix = prefix[:10]
	}
	rows, _ := dbutil.QueryMaps(server.DB, "SELECT public_id AS id, name, source_url, cover_path FROM designs WHERE user_id = ? AND id != ? AND LOWER(name) LIKE ? ESCAPE '\\' LIMIT 5", currentUserID, designID, likePattern(prefix))
	collect(rows)
	httpx.Success(responseWriter, duplicates)
}

// DesignCollections returns the collections a design belongs to.
func (server *Server) DesignCollections(responseWriter http.ResponseWriter, request *http.Request) {
	currentUserID := userID(request)
	designID, ok := server.designID(responseWriter, request, "id")
	if !ok {
		return
	}
	rows, _ := dbutil.QueryMaps(server.DB, `
		SELECT c.* FROM collections c
		JOIN design_collections dc ON dc.collection_id = c.id
		WHERE dc.design_id = ? AND c.user_id = ? ORDER BY c.name ASC`, designID, currentUserID)
	httpx.Success(responseWriter, rows)
}

// platformPatterns maps platform -> source-ID regex.
var platformPatterns = map[string]*regexp.Regexp{
	"thingiverse": regexp.MustCompile(`(?i)(?:thing[:\-/])(\d+)`),
	"printables":  regexp.MustCompile(`model/(\d+)`),
	"makerworld":  regexp.MustCompile(`(?i)models/(\d+)`),
	"thangs":      regexp.MustCompile(`(?i)(?:model|3dmodel)/(\d+)`),
}

// DesignsCheckUrl checks whether a design already exists for a platform URL.
func (server *Server) DesignsCheckUrl(responseWriter http.ResponseWriter, request *http.Request) {
	currentUserID := userID(request)
	sourceURL := strings.TrimSpace(queryStr(request, "url", ""))
	noResult := map[string]any{"exists": false, "design": nil}
	if sourceURL == "" {
		httpx.Success(responseWriter, noResult)
		return
	}
	platform := detectPlatform(sourceURL)
	if platform == "" {
		httpx.Success(responseWriter, noResult)
		return
	}
	sourceID := extractSourceID(platform, sourceURL)
	if sourceID == "" {
		httpx.Success(responseWriter, noResult)
		return
	}
	design, found, failure := dbutil.QueryMap(server.DB, "SELECT public_id AS id, name FROM designs WHERE user_id = ? AND source_platform = ? AND source_id = ? LIMIT 1", currentUserID, platform, sourceID)
	if failure != nil {
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.server")
		return
	}
	httpx.Success(responseWriter, map[string]any{"exists": found, "design": design})
}

// detectPlatform recognizes the platform from the URL's hostname. Delegates to
// platforms.DetectPlatform so both layers share the same host-anchored logic
// (a substring match would be an SSRF vector - see the note there).
func detectPlatform(url string) string {
	return platforms.DetectPlatform(url)
}

var cults3dSlugPattern = regexp.MustCompile(`(?i)cults3d\.com/[^/]+/3d-model/([^/]+/[^/?#]+)`)
var cults3dIDPattern = regexp.MustCompile(`/3d-model/.*?-(\d+)(?:[/?#]|$)`)
var myMiniFactoryObjectPattern = regexp.MustCompile(`(?i)myminifactory\.com/object/([^/?#]+)`)
var trailingIDPattern = regexp.MustCompile(`-(\d+)$`)

// extractSourceID extracts the source ID per platform.
func extractSourceID(platform, url string) string {
	switch platform {
	case "cults3d":
		if match := cults3dSlugPattern.FindStringSubmatch(url); match != nil {
			return match[1]
		}
		if match := cults3dIDPattern.FindStringSubmatch(url); match != nil {
			return match[1]
		}
		return strings.TrimRight(url[strings.LastIndex(url, "/")+1:], "/")
	case "myminifactory":
		if match := myMiniFactoryObjectPattern.FindStringSubmatch(url); match != nil {
			if id := trailingIDPattern.FindStringSubmatch(match[1]); id != nil {
				return id[1]
			}
			return match[1]
		}
		return ""
	default:
		if pattern := platformPatterns[platform]; pattern != nil {
			if match := pattern.FindStringSubmatch(url); match != nil {
				return match[1]
			}
		}
		return ""
	}
}

// attachTags attaches a tags array to each design.
func (server *Server) attachTags(designs []map[string]any, currentUserID int) {
	if len(designs) == 0 {
		return
	}
	designIDs := make([]any, 0, len(designs))
	placeholders := make([]string, 0, len(designs))
	for _, design := range designs {
		designIDs = append(designIDs, coerce.Int(design["id"]))
		placeholders = append(placeholders, "?")
	}
	args := append([]any{currentUserID}, designIDs...)
	rows, _ := dbutil.QueryMaps(server.DB, `
		SELECT dt.design_id, t.id, t.name, t.color,
			(SELECT COUNT(*) FROM design_tags dt2 WHERE dt2.tag_id = t.id
			   AND dt2.design_id IN (SELECT id FROM designs WHERE user_id = ?)) AS usage_count
		FROM design_tags dt JOIN tags t ON t.id = dt.tag_id
		WHERE dt.design_id IN (`+strings.Join(placeholders, ",")+`)
		ORDER BY usage_count DESC, t.name ASC`, args...)
	byDesign := map[int][]map[string]any{}
	for _, row := range rows {
		designID := coerce.Int(row["design_id"])
		byDesign[designID] = append(byDesign[designID], map[string]any{"id": row["id"], "name": row["name"], "color": row["color"]})
	}
	for _, design := range designs {
		tagList := byDesign[coerce.Int(design["id"])]
		if tagList == nil {
			tagList = []map[string]any{}
		}
		design["tags"] = tagList
	}
}

// DesignsFetchCover loads a cover image from the source platform and stores it locally.
func (server *Server) DesignsFetchCover(responseWriter http.ResponseWriter, request *http.Request) {
	currentUserID := userID(request)
	designID, ok := server.designID(responseWriter, request, "id")
	if !ok {
		return
	}
	design, ok := server.requireOwnership(responseWriter, designID, currentUserID)
	if !ok {
		return
	}
	if design.SourceURL == "" {
		httpx.Error(responseWriter, http.StatusUnprocessableEntity, "error.no_source_url")
		return
	}
	coverPath := server.downloadCover(design.SourceURL, design.SourcePlatform, server.owner(request), designID)
	if coverPath == "" {
		httpx.Error(responseWriter, http.StatusBadGateway, "error.cover_fetch_failed")
		return
	}
	if _, failure := server.DB.Exec("UPDATE designs SET cover_path = ? WHERE id = ?", coverPath, designID); failure != nil {
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.server")
		return
	}
	httpx.Success(responseWriter, map[string]any{"cover_path": coverPath})
}

// downloadCover fetches the cover (Thingiverse REST / Printables GraphQL) and
// stores it as the design's cover.{ext}.
func (server *Server) downloadCover(url, platform string, owner platforms.Owner, designID int) string {
	imageURL := ""
	switch {
	case platform == "thingiverse":
		if match := regexp.MustCompile(`thing:(\d+)`).FindStringSubmatch(url); match != nil {
			token := server.Deps.PlatformToken(owner.ID, "thingiverse")
			body := httpGet(fmt.Sprintf("https://api.thingiverse.com/things/%s/images", match[1]), map[string]string{"Authorization": "Bearer " + token})
			imageURL = jsonPath(body, "0.sizes.0.url")
		}
	case platform == "printables":
		if match := regexp.MustCompile(`printables\.com/model/(\d+)`).FindStringSubmatch(url); match != nil {
			query := `{"query":"query{print(id:` + match[1] + `){ image{filePath} }}"}`
			body := httpPostJSON("https://api.printables.com/graphql/", query)
			if filePath := jsonPath(body, "data.print.image.filePath"); filePath != "" {
				imageURL = "https://media.printables.com/" + filePath
			}
		}
	}
	if imageURL == "" {
		return ""
	}
	data := httpGetBytes(imageURL)
	if len(data) == 0 {
		return ""
	}
	destination := owner.Layout.Cover(designID, filepath.Ext(imageURL))
	if failure := storage.WriteFile(destination, data); failure != nil {
		return ""
	}
	return owner.Layout.Rel(destination)
}

// httpGet reads a URL (with headers) as a string.
func httpGet(url string, headers map[string]string) string {
	return string(httpDo(url, "GET", "", headers))
}

func httpGetBytes(url string) []byte { return httpDo(url, "GET", "", nil) }

func httpPostJSON(url, body string) string {
	return string(httpDo(url, "POST", body, map[string]string{"Content-Type": "application/json"}))
}

// outboundClient is used for the metadata/cover fetches below. These run
// synchronously inside HTTP handlers, so they need a deadline: a platform server
// that accepts the connection and then stalls would otherwise block the handler
// goroutine forever (io.LimitReader bounds the size, not the time). It is a
// dedicated client on purpose - setting a Timeout on http.DefaultClient would
// silently change the behaviour of every other user of the process-wide default.
var outboundClient = &http.Client{Timeout: 30 * time.Second}

func httpDo(url, method, body string, headers map[string]string) []byte {
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	request, failure := http.NewRequest(method, url, reader)
	if failure != nil {
		return nil
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response, failure := outboundClient.Do(request)
	if failure != nil {
		return nil
	}
	defer response.Body.Close()
	output, _ := io.ReadAll(io.LimitReader(response.Body, 32<<20))
	return output
}
