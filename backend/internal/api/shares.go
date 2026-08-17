package api

import (
	"meshdepot/internal/coerce"
	"net/http"
	"strings"

	"meshdepot/internal/dbutil"
	"meshdepot/internal/httpx"
	"meshdepot/internal/notify"
)

// ownsDesign checks whether the user owns the design (writes 404 if not).
func (server *Server) ownsDesign(responseWriter http.ResponseWriter, designID, currentUserID int) bool {
	_, ok := server.fetchRow(responseWriter, "SELECT id FROM designs WHERE id = ? AND user_id = ? LIMIT 1", designID, currentUserID)
	return ok
}

// SharesIndex returns all users a design is shared with.
func (server *Server) SharesIndex(responseWriter http.ResponseWriter, request *http.Request) {
	currentUserID := userID(request)
	designID, ok := server.designID(responseWriter, request, "designId")
	if !ok {
		return
	}
	if !server.ownsDesign(responseWriter, designID, currentUserID) {
		return
	}
	rows, _ := dbutil.QueryMaps(server.DB, `
		SELECT ds.id, ds.shared_with_user_id, u.name AS shared_with_name,
			u.email AS shared_with_email, ds.created_at
		FROM design_shares ds JOIN users u ON u.id = ds.shared_with_user_id
		WHERE ds.design_id = ? AND ds.owner_user_id = ? ORDER BY u.name ASC`, designID, currentUserID)
	httpx.Success(responseWriter, rows)
}

// SharesStore shares a design by email with active users and notifies them.
func (server *Server) SharesStore(responseWriter http.ResponseWriter, request *http.Request) {
	currentUserID := userID(request)
	designID, ok := server.designID(responseWriter, request, "designId")
	if !ok {
		return
	}
	if !server.ownsDesign(responseWriter, designID, currentUserID) {
		return
	}
	var body struct {
		Emails []string `json:"emails"`
	}
	_ = httpx.DecodeJSON(request, &body)
	emails := []string{}
	for _, email := range body.Emails {
		if trimmed := strings.TrimSpace(email); trimmed != "" {
			emails = append(emails, trimmed)
		}
	}
	if len(emails) == 0 {
		httpx.Error(responseWriter, http.StatusUnprocessableEntity, "error.emails_required")
		return
	}

	// Both names only decorate the notification text; the fallbacks are fine, the
	// share itself must not fail because of them.
	designName := "Design"
	if designRow, found := server.optionalRow("shares: design name", "SELECT name FROM designs WHERE id = ? LIMIT 1", designID); found {
		designName = coerce.StringOr(designRow["name"], "Design")
	}
	ownerName := "Someone"
	if ownerRow, found := server.optionalRow("shares: owner name", "SELECT name FROM users WHERE id = ? LIMIT 1", currentUserID); found {
		ownerName = coerce.StringOr(ownerRow["name"], "Someone")
	}

	sharedCount := 0
	matched := 0
	for _, email := range emails {
		// By name as well as by email: the account list shows names, and a share
		// dialog that only accepts an address forces the owner to know one.
		target, found := server.optionalRow("shares: recipient lookup",
			"SELECT id FROM users WHERE (email = ? COLLATE NOCASE OR name = ? COLLATE NOCASE) AND state = 'active' LIMIT 1", email, email)
		if !found || coerce.Int(target["id"]) == currentUserID {
			continue
		}
		matched++
		targetID := coerce.Int(target["id"])
		insertResult, failure := server.DB.Exec("INSERT OR IGNORE INTO design_shares (design_id, owner_user_id, shared_with_user_id) VALUES (?, ?, ?)", designID, currentUserID, targetID)
		if failure == nil {
			if affected, _ := insertResult.RowsAffected(); affected > 0 {
				notify.User(server.DB, targetID, "design_shared", ownerName+" shared a design with you", `"`+designName+`"`, &designID)
			}
		}
		sharedCount++
	}
	// Nothing matched: saying "Shared" there is a lie the caller cannot see
	// through - the old dialog answered a typed "a" with a success toast.
	if matched == 0 {
		httpx.Error(responseWriter, http.StatusUnprocessableEntity, "error.no_users_matched")
		return
	}
	httpx.SuccessMessage(responseWriter, map[string]any{"shared_count": sharedCount}, "Shared")
}

func (server *Server) SharesDestroy(responseWriter http.ResponseWriter, request *http.Request) {
	currentUserID := userID(request)
	designID, ok := server.designID(responseWriter, request, "designId")
	if !ok {
		return
	}
	shareID, ok := pathInt(request, "shareId")
	if !ok {
		httpx.Error(responseWriter, http.StatusNotFound, "error.not_found")
		return
	}
	if !server.ownsDesign(responseWriter, designID, currentUserID) {
		return
	}
	dbutil.ExecLogged(server.DB, "DELETE FROM design_shares WHERE id = ? AND design_id = ? AND owner_user_id = ?", shareID, designID, currentUserID)
	httpx.SuccessMessage(responseWriter, nil, "Share removed")
}
