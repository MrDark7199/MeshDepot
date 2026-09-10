package api

import (
	"meshdepot/internal/coerce"
	"net/http"
	"strings"

	"meshdepot/internal/dbutil"
	"meshdepot/internal/httpx"
)

// TagsIndex returns the user's tags with usage_count, sorted by usage then name.
func (server *Server) TagsIndex(responseWriter http.ResponseWriter, request *http.Request) {
	rows, failure := dbutil.QueryMaps(server.DB,
		`SELECT t.*, COUNT(dt.design_id) AS usage_count
		   FROM tags t
		   LEFT JOIN design_tags dt ON dt.tag_id = t.id
		  WHERE t.user_id = ?
		  GROUP BY t.id
		  ORDER BY (t.source = 'manual') DESC, usage_count DESC, t.name ASC`, userID(request))
	if failure != nil {
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.server")
		return
	}
	httpx.Success(responseWriter, rows)
}

// TagsSearch returns a limited hit list for the tag combobox, self-created tags
// first, so the frontend need not preload every tag.
func (server *Server) TagsSearch(responseWriter http.ResponseWriter, request *http.Request) {
	query := strings.TrimSpace(queryStr(request, "q", ""))
	limit := queryInt(request, "limit", 20)
	if limit < 1 || limit > 50 {
		limit = 20
	}
	rows, failure := dbutil.QueryMaps(server.DB,
		`SELECT t.*, COUNT(dt.design_id) AS usage_count
		   FROM tags t
		   LEFT JOIN design_tags dt ON dt.tag_id = t.id
		  WHERE t.user_id = ? AND t.name LIKE ? ESCAPE '\'
		  GROUP BY t.id
		  ORDER BY (t.source = 'manual') DESC, usage_count DESC, t.name ASC
		  LIMIT ?`, userID(request), likePattern(query), limit)
	if failure != nil {
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.server")
		return
	}
	httpx.Success(responseWriter, rows)
}

func (server *Server) TagsStore(responseWriter http.ResponseWriter, request *http.Request) {
	var body struct {
		Name  string `json:"name"`
		Color string `json:"color"`
	}
	_ = httpx.DecodeJSON(request, &body)
	name := strings.TrimSpace(body.Name)
	if name == "" {
		httpx.Error(responseWriter, http.StatusUnprocessableEntity, "error.name_required")
		return
	}
	color := body.Color
	if color == "" {
		color = "#457b9d"
	}
	// The UNIQUE index is case-sensitive and would accept "Decor" next to "decor",
	// which would filter and count separately.
	found, known := dbutil.Exists(server.DB, "SELECT id FROM tags WHERE user_id=? AND name=? COLLATE NOCASE LIMIT 1",
		userID(request), name)
	if !known {
		// The UNIQUE index is case-sensitive, so it would not catch what this
		// probe is for: a failed answer must not become a second spelling.
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.server")
		return
	}
	if found {
		httpx.Error(responseWriter, http.StatusUnprocessableEntity, "error.tag_exists")
		return
	}
	insertResult, failure := server.DB.Exec("INSERT INTO tags (user_id, name, color, source) VALUES (?, ?, ?, 'manual')", userID(request), name, color)
	if failure != nil {
		if dbutil.IsUniqueViolation(failure) {
			httpx.Error(responseWriter, http.StatusUnprocessableEntity, "error.tag_exists")
			return
		}
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.server")
		return
	}
	id, _ := insertResult.LastInsertId()
	row, ok := server.fetchRow(responseWriter, "SELECT * FROM tags WHERE id = ? LIMIT 1", id)
	if !ok {
		return
	}
	httpx.SuccessStatus(responseWriter, http.StatusCreated, row, "Tag created")
}

func (server *Server) TagsDestroy(responseWriter http.ResponseWriter, request *http.Request) {
	tagID, ok := pathInt(request, "id")
	if !ok {
		httpx.Error(responseWriter, http.StatusNotFound, "error.not_found")
		return
	}
	if _, ok := server.fetchRow(responseWriter, "SELECT id FROM tags WHERE id = ? AND user_id = ? LIMIT 1", tagID, userID(request)); !ok {
		return
	}
	if _, failure := server.DB.Exec("DELETE FROM tags WHERE id = ?", tagID); failure != nil {
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.server")
		return
	}
	httpx.SuccessMessage(responseWriter, nil, "Deleted")
}

// TagsSetForDesign replaces a design's tags and cleans up orphans.
func (server *Server) TagsSetForDesign(responseWriter http.ResponseWriter, request *http.Request) {
	currentUserID := userID(request)
	designID, ok := server.designID(responseWriter, request, "designId")
	if !ok {
		return
	}
	if _, ok := server.fetchRow(responseWriter, "SELECT id FROM designs WHERE id = ? AND user_id = ? LIMIT 1", designID, currentUserID); !ok {
		return
	}

	var body struct {
		TagIDs []int `json:"tag_ids"`
	}
	_ = httpx.DecodeJSON(request, &body)

	// Restricted to tags the user owns: design_tags' foreign key only checks that the
	// row exists, not who owns it, so any member could otherwise attach a foreign tag.
	owned := make(map[int]bool)
	if len(body.TagIDs) > 0 {
		ownedRows, _ := dbutil.QueryMaps(server.DB, "SELECT id FROM tags WHERE user_id = ?", currentUserID)
		for _, entry := range ownedRows {
			if id, ok := coerce.Int64(entry["id"]); ok {
				owned[int(id)] = true
			}
		}
	}
	tagIDs := make([]int, 0, len(body.TagIDs))
	for _, id := range body.TagIDs {
		if owned[id] {
			tagIDs = append(tagIDs, id)
		}
	}

	existing, _ := dbutil.QueryMaps(server.DB, "SELECT tag_id FROM design_tags WHERE design_id = ?", designID)
	removed := make(map[int64]bool)
	for _, entry := range existing {
		if id, ok := coerce.Int64(entry["tag_id"]); ok {
			removed[id] = true
		}
	}
	for _, id := range tagIDs {
		delete(removed, int64(id))
	}

	transaction, failure := server.DB.Begin()
	if failure != nil {
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.server")
		return
	}
	if _, failure := transaction.Exec("DELETE FROM design_tags WHERE design_id = ?", designID); failure != nil {
		_ = transaction.Rollback()
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.server")
		return
	}
	for _, tagID := range tagIDs {
		if _, failure := transaction.Exec("INSERT OR IGNORE INTO design_tags (design_id, tag_id) VALUES (?, ?)", designID, tagID); failure != nil {
			_ = transaction.Rollback()
			httpx.Error(responseWriter, http.StatusInternalServerError, "error.server")
			return
		}
	}
	if failure := transaction.Commit(); failure != nil {
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.server")
		return
	}

	// Orphans: removed, no longer used anywhere, and owned by the user.
	for tagID := range removed {
		var count int
		_ = server.DB.QueryRow("SELECT COUNT(*) FROM design_tags WHERE tag_id = ?", tagID).Scan(&count)
		if count == 0 {
			dbutil.ExecLogged(server.DB, "DELETE FROM tags WHERE id = ? AND user_id = ?", tagID, currentUserID)
		}
	}
	httpx.SuccessMessage(responseWriter, nil, "Tags updated")
}
