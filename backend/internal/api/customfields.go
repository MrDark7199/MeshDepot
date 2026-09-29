package api

// Custom fields: what a user wants to record about a design beyond the built-in
// columns. The definition belongs to the user and is the same on every design;
// the value belongs to a design.
//
// Privacy falls out of that shape rather than out of a check: values are read
// through the reader's own definitions, so a design shared with somebody else
// carries none of the owner's.

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"meshdepot/internal/coerce"
	"meshdepot/internal/dbutil"
	"meshdepot/internal/httpx"
	"meshdepot/internal/logx"
)

// customFieldTypes are the shapes a value can take. A select carries its
// choices in options; the rest ignore that column.
var customFieldTypes = map[string]bool{
	"text": true, "int": true, "float": true, "boolean": true, "select": true, "multiselect": true,
}

// choosableTypes are the ones whose values the user picks from a list they
// defined, so both need their choices stored.
var choosableTypes = map[string]bool{"select": true, "multiselect": true}

const maxCustomFields = 40

type customFieldBody struct {
	Name    string   `json:"name"`
	Type    string   `json:"field_type"`
	Options []string `json:"options"`
}

// CustomFieldsList returns the fields of the signed-in user, in their order.
func (server *Server) CustomFieldsList(responseWriter http.ResponseWriter, request *http.Request) {
	// usage_count says how many designs carry the field, so deleting it can state
	// what is about to go with it.
	rows, failure := dbutil.QueryMaps(server.DB,
		`SELECT f.id, f.name, f.field_type, f.options, f.position,
		        (SELECT COUNT(*) FROM design_custom_values v WHERE v.field_id = f.id) AS usage_count
		 FROM custom_fields f
		 WHERE f.user_id = ? ORDER BY f.position ASC, f.id ASC`, userID(request))
	if failure != nil {
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.server")
		return
	}
	httpx.Success(responseWriter, decodeFieldOptions(rows))
}

// decodeFieldOptions turns the stored JSON into a list, so the client never has
// to parse a string out of a field.
func decodeFieldOptions(rows []map[string]any) []map[string]any {
	for _, row := range rows {
		choices := []string{}
		if raw := coerce.StringOr(row["options"], ""); raw != "" {
			_ = json.Unmarshal([]byte(raw), &choices)
		}
		row["options"] = choices
	}
	return rows
}

// cleanCustomField checks a definition and returns it ready to store.
func cleanCustomField(body customFieldBody) (name, fieldType, options string, errorKey string) {
	name = strings.TrimSpace(body.Name)
	if name == "" {
		return "", "", "", "error.name_required"
	}
	if len([]rune(name)) > 60 {
		return "", "", "", "error.name_too_long"
	}
	fieldType = strings.TrimSpace(body.Type)
	if fieldType == "" {
		fieldType = "text"
	}
	if !customFieldTypes[fieldType] {
		return "", "", "", "error.custom_field_type_invalid"
	}
	if !choosableTypes[fieldType] {
		return name, fieldType, "", ""
	}

	var choices []string
	seen := map[string]bool{}
	for _, choice := range body.Options {
		trimmed := strings.TrimSpace(choice)
		if trimmed == "" || seen[strings.ToLower(trimmed)] {
			continue
		}
		seen[strings.ToLower(trimmed)] = true
		choices = append(choices, trimmed)
	}
	// A field to choose from, without choices, is one nobody can fill in.
	if len(choices) == 0 {
		return "", "", "", "error.custom_field_options_required"
	}
	encoded, failure := json.Marshal(choices)
	if failure != nil {
		return "", "", "", "error.server"
	}
	return name, fieldType, string(encoded), ""
}

func (server *Server) CustomFieldsCreate(responseWriter http.ResponseWriter, request *http.Request) {
	currentUserID := userID(request)
	var body customFieldBody
	if failure := httpx.DecodeJSON(request, &body); failure != nil {
		httpx.Error(responseWriter, http.StatusUnprocessableEntity, "error.invalid_request")
		return
	}
	name, fieldType, options, errorKey := cleanCustomField(body)
	if errorKey != "" {
		httpx.Error(responseWriter, http.StatusUnprocessableEntity, errorKey)
		return
	}

	var count int
	if failure := server.DB.QueryRow("SELECT COUNT(*) FROM custom_fields WHERE user_id = ?", currentUserID).Scan(&count); failure != nil {
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.server")
		return
	}
	if count >= maxCustomFields {
		httpx.Error(responseWriter, http.StatusUnprocessableEntity, "error.custom_field_limit")
		return
	}

	result, failure := server.DB.Exec(
		`INSERT INTO custom_fields (user_id, name, field_type, options, position)
		 VALUES (?, ?, ?, ?, ?)`, currentUserID, name, fieldType, options, count)
	if failure != nil {
		// The only constraint that can bite here is the one on (user_id, name).
		httpx.Error(responseWriter, http.StatusConflict, "error.custom_field_exists")
		return
	}
	id, _ := result.LastInsertId()
	httpx.SuccessStatus(responseWriter, http.StatusCreated, map[string]any{"id": id}, "Field created")
}

func (server *Server) CustomFieldsUpdate(responseWriter http.ResponseWriter, request *http.Request) {
	currentUserID := userID(request)
	id, ok := pathInt(request, "id")
	if !ok {
		httpx.Error(responseWriter, http.StatusNotFound, "error.not_found")
		return
	}
	var body customFieldBody
	if failure := httpx.DecodeJSON(request, &body); failure != nil {
		httpx.Error(responseWriter, http.StatusUnprocessableEntity, "error.invalid_request")
		return
	}
	name, fieldType, options, errorKey := cleanCustomField(body)
	if errorKey != "" {
		httpx.Error(responseWriter, http.StatusUnprocessableEntity, errorKey)
		return
	}
	result, failure := server.DB.Exec(
		`UPDATE custom_fields SET name = ?, field_type = ?, options = ?
		 WHERE id = ? AND user_id = ?`, name, fieldType, options, id, currentUserID)
	if failure != nil {
		httpx.Error(responseWriter, http.StatusConflict, "error.custom_field_exists")
		return
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		httpx.Error(responseWriter, http.StatusNotFound, "error.not_found")
		return
	}
	httpx.Success(responseWriter, map[string]any{"id": id})
}

// CustomFieldsDelete removes a field and, with it, every value recorded under
// it - the foreign key sees to that.
func (server *Server) CustomFieldsDelete(responseWriter http.ResponseWriter, request *http.Request) {
	id, ok := pathInt(request, "id")
	if !ok {
		httpx.Error(responseWriter, http.StatusNotFound, "error.not_found")
		return
	}
	if _, failure := server.DB.Exec("DELETE FROM custom_fields WHERE id = ? AND user_id = ?",
		id, userID(request)); failure != nil {
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.server")
		return
	}
	httpx.Success(responseWriter, map[string]any{"deleted": true})
}

// customValuesFor reads the values of one design through the reader's own
// fields. Someone looking at a shared design sees their own fields and no
// values, which is the point.
func customValuesFor(database dbutil.Querier, designID, readerID int) []map[string]any {
	// present says whether the field was added to this design. Without it an empty
	// value could not be told from a field nobody put on the design, and every
	// design would carry every field - which is what made a yes/no look like a no
	// everywhere.
	rows, failure := dbutil.QueryMaps(database,
		`SELECT f.id, f.name, f.field_type, f.options, f.position,
		        COALESCE(v.value, '') AS value,
		        CASE WHEN v.field_id IS NULL THEN 0 ELSE 1 END AS present
		 FROM custom_fields f
		 LEFT JOIN design_custom_values v ON v.field_id = f.id AND v.design_id = ?
		 WHERE f.user_id = ? ORDER BY f.position ASC, f.id ASC`, designID, readerID)
	if failure != nil {
		logx.Errorf("[custom-fields] reading values for design %d failed: %v", designID, failure)
		return nil
	}
	return decodeFieldOptions(rows)
}

// storeCustomValues writes the fields a design carries. The map is the complete
// set for that design: a field in it is stored, empty value included, and one of
// the user's fields that is missing from it is removed from the design. That is
// what lets a field be added and taken off again, rather than every design
// carrying every field.
//
// Only fields the user owns are touched, so a stray id changes nothing.
func storeCustomValues(database dbutil.Querier, designID, currentUserID int, values map[string]any) {
	owned := map[int]bool{}
	rows, failure := dbutil.QueryMaps(database, "SELECT id FROM custom_fields WHERE user_id = ?", currentUserID)
	if failure != nil {
		logx.Errorf("[custom-fields] reading fields of user %d failed: %v", currentUserID, failure)
		return
	}
	for _, row := range rows {
		owned[coerce.Int(row["id"])] = true
	}

	wanted := map[int]string{}
	for key, raw := range values {
		fieldID, failure := strconv.Atoi(key)
		if failure != nil || !owned[fieldID] {
			continue
		}
		wanted[fieldID] = strings.TrimSpace(coerce.StringOr(raw, ""))
	}

	for fieldID := range owned {
		value, keep := wanted[fieldID]
		if !keep {
			dbutil.ExecLogged(database, "DELETE FROM design_custom_values WHERE design_id = ? AND field_id = ?",
				designID, fieldID)
			continue
		}
		dbutil.ExecLogged(database,
			`INSERT INTO design_custom_values (design_id, field_id, value) VALUES (?, ?, ?)
			 ON CONFLICT (design_id, field_id) DO UPDATE SET value = excluded.value`,
			designID, fieldID, value)
	}
}
