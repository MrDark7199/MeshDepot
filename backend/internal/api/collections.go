package api

import (
	"net/http"
	"strconv"
	"strings"

	"meshdepot/internal/dbutil"
	"meshdepot/internal/httpx"
	"meshdepot/internal/publicid"
)

// requireCollection checks existence + ownership of a collection.
func (server *Server) requireCollection(responseWriter http.ResponseWriter, collectionID, currentUserID int) (map[string]any, bool) {
	return server.fetchRow(responseWriter, "SELECT * FROM collections WHERE id = ? AND user_id = ? LIMIT 1", collectionID, currentUserID)
}

// CollectionsIndex returns all collections of the user incl. design_count.
func (server *Server) CollectionsIndex(responseWriter http.ResponseWriter, request *http.Request) {
	// Hidden collections are left out unless they are asked for: the collection
	// tab needs them to offer unhiding, everything else must not show them.
	hiddenClause := "AND c.is_hidden = 0"
	if request.URL.Query().Get("include_hidden") == "1" {
		hiddenClause = ""
	}
	rows, _ := dbutil.QueryMaps(server.DB, `
		SELECT c.*, COUNT(dc.design_id) AS design_count
		FROM collections c
		LEFT JOIN design_collections dc ON dc.collection_id = c.id
		WHERE c.user_id = ? `+hiddenClause+`
		GROUP BY c.id ORDER BY c.name ASC`, userID(request))
	httpx.Success(responseWriter, rows)
}

func (server *Server) CollectionsStore(responseWriter http.ResponseWriter, request *http.Request) {
	var body struct {
		Name        string `json:"name"`
		Description any    `json:"description"`
	}
	_ = httpx.DecodeJSON(request, &body)
	name := strings.TrimSpace(body.Name)
	if name == "" {
		httpx.Error(responseWriter, http.StatusUnprocessableEntity, "error.name_required")
		return
	}
	// No two collections of the same name for one user - the same name twice only
	// confuses the picker and the filter. Case-insensitive, matching the client's
	// own "already exists" check. Hidden collections count too.
	var existing int
	server.DB.QueryRow("SELECT COUNT(*) FROM collections WHERE user_id = ? AND name = ? COLLATE NOCASE", userID(request), name).Scan(&existing)
	if existing > 0 {
		httpx.Error(responseWriter, http.StatusConflict, "error.collection_name_taken")
		return
	}
	insertResult, failure := server.DB.Exec("INSERT INTO collections (user_id, name, description) VALUES (?, ?, ?)", userID(request), name, body.Description)
	if failure != nil {
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.server")
		return
	}
	id, _ := insertResult.LastInsertId()
	row, ok := server.fetchRow(responseWriter, "SELECT * FROM collections WHERE id = ? LIMIT 1", id)
	if !ok {
		return
	}
	httpx.SuccessStatus(responseWriter, http.StatusCreated, row, "Collection created")
}

// boolToInt maps a JSON boolean to the 0/1 the schema stores.
func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

// CollectionsUpdate changes name, description and whether the collection is hidden.
func (server *Server) CollectionsUpdate(responseWriter http.ResponseWriter, request *http.Request) {
	currentUserID := userID(request)
	id, ok := pathInt(request, "id")
	if !ok {
		httpx.Error(responseWriter, http.StatusNotFound, "error.not_found")
		return
	}
	if _, ok := server.requireCollection(responseWriter, id, currentUserID); !ok {
		return
	}
	var body map[string]any
	_ = httpx.DecodeJSON(request, &body)
	var assignments []string
	var args []any
	// Field names are restricted below, but the values have to be checked too:
	// raw JSON went into the query, so an object value produced a driver error
	// (500) and "" produced a nameless collection - exactly what CollectionsStore
	// rejects. Same column, same rules.
	// Hiding is a flag, not text, so it is read before the text fields below.
	if value, present := body["is_hidden"]; present {
		hidden, isBool := value.(bool)
		if !isBool {
			httpx.Error(responseWriter, http.StatusUnprocessableEntity, "error.invalid_input")
			return
		}
		assignments = append(assignments, "is_hidden = ?")
		args = append(args, boolToInt(hidden))
	}
	for _, field := range []string{"name", "description"} {
		value, present := body[field]
		if !present {
			continue
		}
		text, isString := value.(string)
		if !isString {
			httpx.Error(responseWriter, http.StatusUnprocessableEntity, "error.invalid_input")
			return
		}
		text = strings.TrimSpace(text)
		if field == "name" && text == "" {
			httpx.Error(responseWriter, http.StatusUnprocessableEntity, "error.name_required")
			return
		}
		assignments = append(assignments, field+" = ?")
		args = append(args, text)
	}
	if len(assignments) > 0 {
		args = append(args, id)
		if _, failure := server.DB.Exec("UPDATE collections SET "+strings.Join(assignments, ", ")+" WHERE id = ?", args...); failure != nil {
			httpx.Error(responseWriter, http.StatusInternalServerError, "error.server")
			return
		}
	}
	httpx.SuccessMessage(responseWriter, nil, "Updated")
}

// CollectionsDestroy deletes a collection (designs are kept).
func (server *Server) CollectionsDestroy(responseWriter http.ResponseWriter, request *http.Request) {
	currentUserID := userID(request)
	id, ok := pathInt(request, "id")
	if !ok {
		httpx.Error(responseWriter, http.StatusNotFound, "error.not_found")
		return
	}
	if _, ok := server.requireCollection(responseWriter, id, currentUserID); !ok {
		return
	}
	if _, failure := server.DB.Exec("DELETE FROM collections WHERE id = ?", id); failure != nil {
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.server")
		return
	}
	httpx.SuccessMessage(responseWriter, nil, "Deleted")
}

// CollectionDesigns returns all designs of a collection (own + shared).
func (server *Server) CollectionDesigns(responseWriter http.ResponseWriter, request *http.Request) {
	currentUserID := userID(request)
	id, ok := pathInt(request, "id")
	if !ok {
		httpx.Error(responseWriter, http.StatusNotFound, "error.not_found")
		return
	}
	if _, ok := server.requireCollection(responseWriter, id, currentUserID); !ok {
		return
	}
	rows, _ := dbutil.QueryMaps(server.DB, `
		SELECT d.*, df.id AS current_file_id, df.version AS current_version,
			df.filename, df.size_bytes,
			CASE WHEN d.user_id != ? THEN 1 ELSE 0 END AS is_shared,
			owner_u.name AS shared_by_name`+translationSelect+`
		FROM designs d
		JOIN design_collections dc ON dc.design_id = d.id AND dc.collection_id = ?
		LEFT JOIN design_files df ON df.design_id = d.id AND df.is_current = 1
		LEFT JOIN design_shares ds_check ON ds_check.design_id = d.id AND ds_check.shared_with_user_id = ?
		LEFT JOIN users owner_u ON owner_u.id = ds_check.owner_user_id`+translationJoin+`
		WHERE d.user_id = ? OR ds_check.shared_with_user_id = ?
		ORDER BY d.name ASC`, currentUserID, id, currentUserID, currentUserID, currentUserID)
	httpx.Success(responseWriter, rows)
}

// CollectionAddableDesigns returns, paginated, designs that can still be added to
// the collection (own/shared, not already contained).
func (server *Server) CollectionAddableDesigns(responseWriter http.ResponseWriter, request *http.Request) {
	currentUserID := userID(request)
	id, ok := pathInt(request, "id")
	if !ok {
		httpx.Error(responseWriter, http.StatusNotFound, "error.not_found")
		return
	}
	if _, ok := server.requireCollection(responseWriter, id, currentUserID); !ok {
		return
	}
	search := strings.TrimSpace(queryStr(request, "search", ""))
	page := max(1, queryInt(request, "page", 1))
	perPage := min(100, max(1, queryInt(request, "per_page", 40)))
	offset := (page - 1) * perPage

	whereClause := `(d.user_id = ? OR ds_check.shared_with_user_id = ?)
		AND d.id NOT IN (SELECT design_id FROM design_collections WHERE collection_id = ?)`
	// baseArgs in order of appearance in baseQuery: join-uid, where-uid, where-uid, cid
	baseArgs := []any{currentUserID, currentUserID, currentUserID, id}
	if search != "" {
		whereClause += " AND (d.name LIKE ? ESCAPE '\\' OR d.author LIKE ? ESCAPE '\\' OR d.description LIKE ? ESCAPE '\\' OR tr_name_de.content LIKE ? ESCAPE '\\')"
		like := likePattern(search)
		baseArgs = append(baseArgs, like, like, like, like)
	}
	baseQuery := `FROM designs d
		LEFT JOIN design_shares ds_check ON ds_check.design_id = d.id AND ds_check.shared_with_user_id = ?` +
		translationJoin + " WHERE " + whereClause

	var total int
	_ = server.DB.QueryRow("SELECT COUNT(*) "+baseQuery, baseArgs...).Scan(&total)

	// List: additional CASE-uid at the start.
	listArgs := append([]any{currentUserID}, baseArgs...)
	items, _ := dbutil.QueryMaps(server.DB,
		`SELECT d.public_id AS id, d.name, d.author, d.source_platform, d.cover_path,
			CASE WHEN d.user_id != ? THEN 1 ELSE 0 END AS is_shared`+translationSelect+` `+baseQuery+
			" ORDER BY d.updated_at DESC LIMIT "+strconv.Itoa(perPage)+" OFFSET "+strconv.Itoa(offset), listArgs...)
	httpx.Success(responseWriter, map[string]any{"items": items, "total": total})
}

// CollectionAddDesigns adds designs (own/shared only, INSERT OR IGNORE).
func (server *Server) CollectionAddDesigns(responseWriter http.ResponseWriter, request *http.Request) {
	currentUserID := userID(request)
	id, ok := pathInt(request, "id")
	if !ok {
		httpx.Error(responseWriter, http.StatusNotFound, "error.not_found")
		return
	}
	if _, ok := server.requireCollection(responseWriter, id, currentUserID); !ok {
		return
	}
	// Public ids, like everywhere else the client names a design. An id it
	// cannot resolve is skipped rather than refused: the list it was picked from
	// may have changed underneath, and the rest of the selection is still valid.
	var body struct {
		DesignIDs []string `json:"design_ids"`
	}
	_ = httpx.DecodeJSON(request, &body)
	for _, publicDesignID := range body.DesignIDs {
		designIDValue, found, failure := publicid.ResolveIn(server.DB, "designs", publicDesignID)
		if failure != nil || !found {
			continue
		}
		_, mayAccess := server.optionalRow("collections: design access", `
			SELECT d.id FROM designs d
			LEFT JOIN design_shares ds ON ds.design_id = d.id AND ds.shared_with_user_id = ?
			WHERE d.id = ? AND (d.user_id = ? OR ds.shared_with_user_id = ?) LIMIT 1`, currentUserID, designIDValue, currentUserID, currentUserID)
		if !mayAccess {
			continue
		}
		dbutil.ExecLogged(server.DB, "INSERT OR IGNORE INTO design_collections (design_id, collection_id) VALUES (?, ?)", designIDValue, id)
	}
	httpx.SuccessMessage(responseWriter, nil, "Designs added to collection")
}

func (server *Server) CollectionRemoveDesign(responseWriter http.ResponseWriter, request *http.Request) {
	currentUserID := userID(request)
	id, ok := pathInt(request, "id")
	if !ok {
		httpx.Error(responseWriter, http.StatusNotFound, "error.not_found")
		return
	}
	designID, ok := server.designID(responseWriter, request, "designId")
	if !ok {
		return
	}
	if _, ok := server.requireCollection(responseWriter, id, currentUserID); !ok {
		return
	}
	dbutil.ExecLogged(server.DB, "DELETE FROM design_collections WHERE collection_id = ? AND design_id = ?", id, designID)
	httpx.SuccessMessage(responseWriter, nil, "Design removed from collection")
}
