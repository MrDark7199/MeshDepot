package platforms

// Library sync. For each active platform account it imports the platform
// collections: new designs land in the download_queue, known ones are linked
// directly, and members not yet present are noted as
// pending_collection_assignments. Likes are deliberately not synced.
//
// thingiverse, printables, makerworld and thangs go through Tor; cults3d scrapes
// HTML with a cookie session; myminifactory uses its data-library endpoints.

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"meshdepot/internal/coerce"
	"meshdepot/internal/dbutil"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"meshdepot/internal/logx"
)

type libraryCollection struct {
	id, name string
	urls     []string
}

type libraryAccount struct {
	userID          int
	platform        string
	syncCollections int
}

// LibrarySyncEnabled reports the server-wide switch, which outranks every
// per-account setting. A missing row counts as enabled, as the schema seeds it.
func LibrarySyncEnabled(database *sql.DB) bool {
	var value string
	if database.QueryRow("SELECT value FROM app_settings WHERE key='library_sync_enabled'").Scan(&value) != nil {
		return true
	}
	return value != "0"
}

// RunLibrarySync processes all active platform accounts, for the scheduler.
func RunLibrarySync(deps Deps) {
	if !LibrarySyncEnabled(deps.DB) {
		logx.Infof("[library-sync] disabled server-side - nothing to do")
		return
	}
	runLibraryAccounts(deps, deps.loadLibraryAccounts(
		`SELECT user_id, platform, sync_collections FROM platform_accounts WHERE state='active'`))
}

// RunLibrarySyncFor processes one user's accounts, optionally one platform - the
// manual "sync now". The server switch is checked here as well, since this is
// the last point every caller passes through.
func RunLibrarySyncFor(deps Deps, userID int, platform string) {
	if !LibrarySyncEnabled(deps.DB) {
		logx.Infof("[library-sync] disabled server-side - user %d skipped", userID)
		return
	}
	query := `SELECT user_id, platform, sync_collections FROM platform_accounts WHERE state='active' AND user_id=?`
	args := []any{userID}
	if platform != "" {
		query += " AND platform=?"
		args = append(args, platform)
	}
	runLibraryAccounts(deps, deps.loadLibraryAccounts(query, args...))
}

func (deps Deps) loadLibraryAccounts(query string, args ...any) []libraryAccount {
	rows, failure := deps.DB.Query(query, args...)
	if failure != nil {
		return nil
	}
	defer rows.Close()
	var accounts []libraryAccount
	for rows.Next() {
		var account libraryAccount
		if rows.Scan(&account.userID, &account.platform, &account.syncCollections) == nil {
			accounts = append(accounts, account)
		}
	}
	return accounts
}

// runLibraryAccounts fetches each account's collections and fills the queue,
// the collections and the pending assignments.
func runLibraryAccounts(deps Deps, accounts []libraryAccount) {
	if len(accounts) == 0 {
		logx.Infof("[library-sync] no active accounts to sync")
		return
	}
	for _, account := range accounts {
		if account.syncCollections != 1 {
			continue
		}
		var collections []libraryCollection
		if account.platform == "cults3d" {
			// The official GraphQL API is not behind Cloudflare and therefore reliable.
			// Without an API key, fall back to the Firefox scrape, which passes the JS
			// challenge.
			if nickname, apiKey := deps.cults3dAPICreds(account.userID); nickname != "" && apiKey != "" {
				collections = cults3dFetchCollectionsAPI(nickname, apiKey)
			} else if deps.Cfg.PlaywrightURL != "" {
				email, password := deps.cults3dCredentials(account.userID)
				if email == "" || password == "" {
					logx.Infof("[library-sync] cults3d (user %d): no credentials/API key - skipped", account.userID)
					continue
				}
				collections = cults3dFetchCollectionsFirefox(deps.Cfg.PlaywrightURL, email, password)
			} else {
				logx.Infof("[library-sync] cults3d (user %d): no API key and no Firefox resolver - skipped", account.userID)
				continue
			}
		} else {
			token, username := deps.libraryToken(account.userID, account.platform)
			if token == "" {
				logx.Infof("[library-sync] %s (user %d): no token/login - skipped", account.platform, account.userID)
				continue
			}
			collections = deps.fetchCollections(account.platform, token, username)
		}
		queued := deps.importCollections(account.userID, account.platform, collections)
		logx.Infof("[library-sync] %s (user %d): %d new design(s) enqueued", account.platform, account.userID, queued)
	}
}

// importCollections links known designs, enqueues new ones and notes their
// collection assignment as pending. Returns the number newly enqueued.
func (deps Deps) importCollections(userID int, platform string, collections []libraryCollection) int {
	queued := 0
	for _, collection := range collections {
		localCollectionID := findOrCreateCollection(deps.DB, userID, platform, collection.id, collection.name)
		for _, designURL := range collection.urls {
			if localDesignID := findLocalDesign(deps.DB, designURL, userID); localDesignID > 0 {
				dbutil.ExecLogged(deps.DB, "INSERT OR IGNORE INTO design_collections (design_id, collection_id, added_at) VALUES (?,?,CURRENT_TIMESTAMP)", localDesignID, localCollectionID)
				continue
			}
			if queueID := queueIfNew(deps.DB, designURL, platform, userID); queueID > 0 {
				queued++
			}
			if sourceID := extractLibSourceID(designURL, platform); sourceID != "" {
				dbutil.ExecLogged(deps.DB, `INSERT OR IGNORE INTO pending_collection_assignments
					(user_id, source_platform, source_design_id, collection_id) VALUES (?,?,?,?)`,
					userID, platform, sourceID, localCollectionID)
			}
		}
	}
	return queued
}

// cults3dCollectionIDPattern extracts "nickname/slug" from a printlist URL,
// compatible with the previous scrape variant.
var cults3dCollectionIDPattern = regexp.MustCompile(`(?i)/design-collections/([^/?#]+/[^/?#]+)`)

// cults3dFetchCollectionsAPI uses the official GraphQL API, which unlike the
// HTML scrape is not behind Cloudflare. Returns the model URLs per printlist.
func cults3dFetchCollectionsAPI(nickname, apiKey string) []libraryCollection {
	const query = `query={ myself { printlistsBatch(limit:100){ results { name url creationsBatch(limit:100){ results { url } } } } } }`
	request, failure := http.NewRequest(http.MethodPost, "https://cults3d.com/graphql", strings.NewReader(query))
	if failure != nil {
		return nil
	}
	request.SetBasicAuth(nickname, apiKey)
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, failure := (&http.Client{Timeout: 60 * time.Second}).Do(request)
	if failure != nil {
		logx.Errorf("[library-sync] cults3d API unreachable: %v", failure)
		return nil
	}
	defer response.Body.Close()
	var parsed struct {
		Data struct {
			Myself struct {
				PrintlistsBatch struct {
					Results []struct {
						Name           string `json:"name"`
						URL            string `json:"url"`
						CreationsBatch struct {
							Results []struct {
								URL string `json:"url"`
							} `json:"results"`
						} `json:"creationsBatch"`
					} `json:"results"`
				} `json:"printlistsBatch"`
			} `json:"myself"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if json.NewDecoder(response.Body).Decode(&parsed) != nil {
		return nil
	}
	if len(parsed.Errors) > 0 {
		logx.Errorf("[library-sync] cults3d API error: %s", parsed.Errors[0].Message)
		return nil
	}
	var output []libraryCollection
	for _, printlist := range parsed.Data.Myself.PrintlistsBatch.Results {
		id := printlist.URL
		if match := cults3dCollectionIDPattern.FindStringSubmatch(printlist.URL); match != nil {
			id = match[1]
		}
		var urls []string
		for _, creation := range printlist.CreationsBatch.Results {
			if creation.URL != "" {
				urls = append(urls, creation.URL)
			}
		}
		output = append(output, libraryCollection{id: id, name: printlist.Name, urls: uniqueStrings(urls)})
	}
	return output
}

// cults3dAPICreds reads the token field, stored encrypted as "nickname:apiKey".
func (deps Deps) cults3dAPICreds(userID int) (nickname, apiKey string) {
	var encryptedToken sql.NullString
	_ = deps.DB.QueryRow(
		`SELECT token FROM platform_accounts WHERE user_id=? AND platform='cults3d' LIMIT 1`, userID,
	).Scan(&encryptedToken)
	if !encryptedToken.Valid || encryptedToken.String == "" {
		return "", ""
	}
	decrypted, ok := deps.Crypto.Decrypt(encryptedToken.String, userID)
	if !ok {
		return "", ""
	}
	if separatorIndex := strings.Index(decrypted, ":"); separatorIndex > 0 {
		return decrypted[:separatorIndex], decrypted[separatorIndex+1:]
	}
	return "", ""
}

// cults3dFetchCollectionsFirefox drives the Firefox resolver sidecar: Firefox
// logs in and reads the rendered pages, passing the Cloudflare JS challenge that
// headless Chromium fails. On error or an empty result the account is skipped.
func cults3dFetchCollectionsFirefox(playwrightURL, email, password string) []libraryCollection {
	requestBody, _ := json.Marshal(map[string]any{"email": email, "password": password})
	endpoint := strings.TrimRight(playwrightURL, "/") + "/collections/cults3d"
	request, failure := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(requestBody))
	if failure != nil {
		return nil
	}
	request.Header.Set("Content-Type", "application/json")
	applyPlaywrightAuth(request)
	client := &http.Client{Timeout: 240 * time.Second}
	response, failure := client.Do(request)
	if failure != nil {
		logx.Errorf("[library-sync] cults3d resolver unreachable: %v", failure)
		return nil
	}
	defer response.Body.Close()
	var parsed struct {
		Collections []struct {
			ID   string   `json:"id"`
			Name string   `json:"name"`
			URLs []string `json:"urls"`
		} `json:"collections"`
		Error string `json:"error"`
	}
	if json.NewDecoder(response.Body).Decode(&parsed) != nil {
		return nil
	}
	if parsed.Error != "" {
		logx.Errorf("[library-sync] cults3d resolver error: %s", parsed.Error)
		return nil
	}
	var output []libraryCollection
	for _, collection := range parsed.Collections {
		output = append(output, libraryCollection{id: collection.ID, name: collection.Name, urls: uniqueStrings(collection.URLs)})
	}
	return output
}

// cults3dCredentials returns the decrypted login for the browser login.
func (deps Deps) cults3dCredentials(userID int) (email, password string) {
	account := deps.loadAccount(userID, "cults3d")
	return account.Username, account.Password
}

// libraryToken returns token and username, refreshed by auto-login where the
// platform supports one.
func (deps Deps) libraryToken(userID int, platform string) (token, username string) {
	account := deps.loadAccount(userID, platform)

	// MyMiniFactory: the API key alone covers profile and collections. Likes are
	// only reachable through the Cloudflare web session and are not synced.
	if platform == "myminifactory" {
		return account.Token, account.Username
	}

	token, _ = deps.refreshToken(account, userID, platform, nil)
	return token, account.Username
}

func (deps Deps) fetchCollections(platform, token, username string) []libraryCollection {
	switch platform {
	case "thingiverse":
		return deps.thingiverseFetchCollections(token, username)
	case "printables":
		return deps.printablesFetchCollections(token)
	case "makerworld":
		return deps.makerworldFetchCollections(token)
	case "thangs":
		return deps.thangsFetchCollections(token, username)
	case "myminifactory":
		return deps.myMiniFactoryFetchCollections(token, username)
	}
	return nil
}

// - Thingiverse (REST, Tor) -------------------------

func (deps Deps) thingiverseHeaders(token string) map[string]string {
	return map[string]string{"Authorization": "Bearer " + token, "Accept": "application/json"}
}
func (deps Deps) thingiverseFetchCollections(token, username string) []libraryCollection {
	// /users/me/collections is broken on Thingiverse; the real username must be used
	// or no collections come back.
	if username == "" {
		logx.Infof("[library-sync][thingiverse] collections: skipped - no username set")
		return nil
	}
	// Not through Tor: api.thingiverse.com blocks some Tor exit IPs with 403.
	collectionsURL := "https://api.thingiverse.com/users/" + url.PathEscape(username) + "/collections"
	body, code := directGet(collectionsURL, deps.thingiverseHeaders(token))
	var collections []map[string]any
	if json.Unmarshal(body, &collections) != nil {
		logx.Errorf("[library-sync][thingiverse] collections: failed (HTTP %d)", code)
		return nil
	}
	var output []libraryCollection
	for _, collection := range collections {
		collectionID := coerce.NumberText(collection["id"])
		if collectionID == "" {
			continue
		}
		var urls []string
		for page := 1; ; page++ {
			pageURL := fmt.Sprintf("https://api.thingiverse.com/collections/%s/things?page=%d&per_page=30", collectionID, page)
			time.Sleep(2 * time.Second)
			thingsBody, _ := directGet(pageURL, deps.thingiverseHeaders(token))
			var items []map[string]any
			if json.Unmarshal(thingsBody, &items) != nil || len(items) == 0 {
				break
			}
			for _, thing := range items {
				if id := coerce.NumberText(thing["id"]); id != "" {
					urls = append(urls, "https://www.thingiverse.com/thing:"+id)
				}
			}
			if len(items) < 30 {
				break
			}
		}
		output = append(output, libraryCollection{id: collectionID, name: coerce.Text(collection["name"]), urls: urls})
	}
	return output
}

// - Printables (GraphQL, Tor) ------------------------

func (deps Deps) printablesHeaders(token string) map[string]string {
	return map[string]string{"Content-Type": "application/json", "Authorization": "Bearer " + token}
}

func (deps Deps) printablesMyUserID(token string) string {
	body, _ := deps.torPost("https://api.printables.com/graphql/", `{"query":"{me{id}}"}`, deps.printablesHeaders(token))
	var parsed struct {
		Data struct {
			Me struct {
				ID json.Number `json:"id"`
			} `json:"me"`
		} `json:"data"`
	}
	_ = json.Unmarshal(body, &parsed)
	return parsed.Data.Me.ID.String()
}
func (deps Deps) printablesFetchCollections(token string) []libraryCollection {
	myUserID := deps.printablesMyUserID(token)
	if myUserID == "" {
		return nil
	}
	query, _ := json.Marshal(map[string]any{
		"query":     "query UserCollections($uid:ID!){userCollections(userId:$uid){id name}}",
		"variables": map[string]any{"uid": myUserID},
	})
	time.Sleep(1 * time.Second)
	body, _ := deps.torPost("https://api.printables.com/graphql/", string(query), deps.printablesHeaders(token))
	var parsed struct {
		Data struct {
			UserCollections []struct {
				ID   json.Number `json:"id"`
				Name string      `json:"name"`
			} `json:"userCollections"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &parsed) != nil {
		return nil
	}
	var output []libraryCollection
	for _, collection := range parsed.Data.UserCollections {
		collectionID := collection.ID.String()
		if collectionID == "" {
			continue
		}
		var urls []string
		cursor := ""
		for {
			variables := map[string]any{"cid": collectionID, "limit": 100, "cursor": nil}
			if cursor != "" {
				variables["cursor"] = cursor
			}
			collectionQuery, _ := json.Marshal(map[string]any{
				"query":     "query ColModels($cid:ID!,$limit:Int!,$cursor:String){moreCollectionModels(collectionId:$cid,ordering:added_to_collection,limit:$limit,cursor:$cursor){cursor items{print{id}}}}",
				"variables": variables,
			})
			time.Sleep(1 * time.Second)
			collectionBody, _ := deps.torPost("https://api.printables.com/graphql/", string(collectionQuery), deps.printablesHeaders(token))
			items, next := printablesParsePrintList(collectionBody, "moreCollectionModels")
			if len(items) == 0 {
				break
			}
			urls = append(urls, items...)
			cursor = next
			if cursor == "" || len(items) < 100 {
				break
			}
		}
		output = append(output, libraryCollection{id: collectionID, name: collection.Name, urls: urls})
	}
	return output
}

// printablesParsePrintList reads items[].print.id and the cursor from a field.
func printablesParsePrintList(body []byte, field string) ([]string, string) {
	var data struct {
		Data map[string]struct {
			Cursor string `json:"cursor"`
			Items  []struct {
				Print struct {
					ID json.Number `json:"id"`
				} `json:"print"`
			} `json:"items"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &data) != nil {
		return nil, ""
	}
	entry, ok := data.Data[field]
	if !ok {
		return nil, ""
	}
	var urls []string
	for _, item := range entry.Items {
		if id := item.Print.ID.String(); id != "" {
			urls = append(urls, "https://www.printables.com/model/"+id)
		}
	}
	return urls, entry.Cursor
}

// - MakerWorld (REST, Tor) --------------------------

func (deps Deps) makerworldHeaders(token string) map[string]string {
	return map[string]string{
		"Authorization": "Bearer " + token,
		"User-Agent":    "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36",
		"Referer":       "https://makerworld.com/",
	}
}
func (deps Deps) makerworldFetchCollections(token string) []libraryCollection {
	time.Sleep(1 * time.Second)
	body, _ := deps.torGet("https://makerworld.com/api/v1/design-service/my/favorites/listlite", deps.makerworldHeaders(token))
	collections := makerworldHits(body)
	var output []libraryCollection
	for _, collection := range collections {
		collectionID := coerce.NumberText(collection["id"])
		if collectionID == "" || coerce.Int(collection["designCnt"]) == 0 {
			continue
		}
		var urls []string
		for offset := 0; ; {
			pageURL := fmt.Sprintf("https://makerworld.com/api/v1/design-service/favorites/%s/designs?limit=20&offset=%d", collectionID, offset)
			time.Sleep(1 * time.Second)
			collectionBody, _ := deps.torGet(pageURL, deps.makerworldHeaders(token))
			hits := makerworldHits(collectionBody)
			if len(hits) == 0 {
				break
			}
			for _, hit := range hits {
				if id := makerworldHitID(hit); id != "" {
					urls = append(urls, "https://makerworld.com/en/models/"+id)
				}
			}
			offset += len(hits)
			if len(hits) < 20 {
				break
			}
		}
		output = append(output, libraryCollection{id: collectionID, name: coerce.Text(collection["title"]), urls: urls})
	}
	return output
}

func makerworldHits(body []byte) []map[string]any {
	var parsed struct {
		Hits []map[string]any `json:"hits"`
	}
	if json.Unmarshal(body, &parsed) != nil {
		return nil
	}
	return parsed.Hits
}

func makerworldHitID(hit map[string]any) string {
	if id := coerce.NumberText(hit["id"]); id != "" {
		return id
	}
	if design, ok := hit["design"].(map[string]any); ok {
		return coerce.NumberText(design["id"])
	}
	return ""
}

// - Thangs (REST, Tor) ----------------------------

func (deps Deps) thangsHeaders(token string) map[string]string {
	return map[string]string{"Authorization": token, "User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36"}
}
func (deps Deps) thangsFetchCollections(token, username string) []libraryCollection {
	if username == "" {
		return nil
	}
	collectionsURL := "https://thangs.com/api/users/" + url.PathEscape(username) + "/collections"
	time.Sleep(1 * time.Second)
	body, _ := deps.torGet(collectionsURL, deps.thangsHeaders(token))
	collections := thangsItems(body)
	var output []libraryCollection
	for _, collection := range collections {
		collectionID := coerce.NumberText(collection["id"])
		if collectionID == "" {
			continue
		}
		var urls []string
		if items, ok := collection["models"].([]any); ok {
			for _, model := range items {
				if modelMap, ok := model.(map[string]any); ok {
					if id := coerce.NumberText(modelMap["id"]); id != "" {
						urls = append(urls, "https://thangs.com/model/"+id)
					}
				}
			}
		}
		output = append(output, libraryCollection{id: collectionID, name: coerce.Text(collection["name"]), urls: urls})
	}
	return output
}

// thangsItems reads the list from the possible field names.
func thangsItems(body []byte) []map[string]any {
	var raw map[string]json.RawMessage
	if json.Unmarshal(body, &raw) != nil {
		var array []map[string]any
		if json.Unmarshal(body, &array) == nil {
			return array
		}
		return nil
	}
	for _, key := range []string{"models", "items", "data", "collections"} {
		if rawValue, ok := raw[key]; ok {
			var array []map[string]any
			if json.Unmarshal(rawValue, &array) == nil && len(array) > 0 {
				return array
			}
		}
	}
	return nil
}

// - MyMiniFactory (REST API v2, API key only) ----------------
// The key covers the user's public collections but does not resolve the
// identity, so the username says whose are pulled. Likes are behind the
// Cloudflare web session and are not synced.

var myMiniFactoryAPIHeaders = map[string]string{
	"Accept":     "application/json",
	"User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36",
}

// myMiniFactoryFetchCollections loads the public collections and, per
// collection, the object IDs it holds.
func (deps Deps) myMiniFactoryFetchCollections(apiKey, username string) []libraryCollection {
	if apiKey == "" || username == "" {
		return nil
	}
	listURL := fmt.Sprintf("https://www.myminifactory.com/api/v2/users/%s/collections?key=%s",
		url.PathEscape(username), url.QueryEscape(apiKey))
	body, status := directGet(listURL, myMiniFactoryAPIHeaders)
	if status != 200 {
		return nil
	}
	var response struct {
		Items []struct {
			ID   json.Number `json:"id"`
			Name string      `json:"name"`
		} `json:"items"`
	}
	if json.Unmarshal(body, &response) != nil {
		return nil
	}
	var output []libraryCollection
	for _, collection := range response.Items {
		collectionID := collection.ID.String()
		if collectionID == "" {
			continue
		}
		output = append(output, libraryCollection{id: collectionID, name: collection.Name, urls: deps.myMiniFactoryCollectionObjectURLs(collectionID, apiKey)})
	}
	return output
}

// myMiniFactoryCollectionObjectURLs pages 100 at a time until a page is short.
func (deps Deps) myMiniFactoryCollectionObjectURLs(collectionID, apiKey string) []string {
	var urls []string
	for page := 1; page <= 100; page++ {
		pageURL := fmt.Sprintf("https://www.myminifactory.com/api/v2/collections/%s/objects?key=%s&page=%d&per_page=100",
			collectionID, url.QueryEscape(apiKey), page)
		body, status := directGet(pageURL, myMiniFactoryAPIHeaders)
		if status != 200 {
			break
		}
		var response struct {
			Items []struct {
				ID json.Number `json:"id"`
			} `json:"items"`
		}
		if json.Unmarshal(body, &response) != nil || len(response.Items) == 0 {
			break
		}
		for _, object := range response.Items {
			if id := object.ID.String(); id != "" {
				urls = append(urls, "https://www.myminifactory.com/object/"+id)
			}
		}
		if len(response.Items) < 100 {
			break
		}
	}
	return urls
}

// IsExcludedFromSync reports whether the user deleted this design and asked for
// it to stay gone. Matched by (platform, source id) where the URL yields one and
// by URL otherwise, so an entry written from either side still matches.
//
// A probe the database could not answer counts as excluded. The two mistakes
// are not equal: a design wrongly held back appears on the next run, while one
// wrongly brought back is the very thing the member deleted and asked to be rid
// of.
func IsExcludedFromSync(db *sql.DB, userID int, platform, sourceID, designURL string) bool {
	if sourceID != "" {
		found, known := dbutil.Exists(db,
			"SELECT id FROM sync_exclusions WHERE user_id=? AND source_platform=? AND source_id=? LIMIT 1",
			userID, platform, sourceID)
		if found || !known {
			return true
		}
	}
	if designURL != "" {
		found, known := dbutil.Exists(db,
			"SELECT id FROM sync_exclusions WHERE user_id=? AND source_url=? LIMIT 1", userID, designURL)
		if found || !known {
			return true
		}
	}
	return false
}

// LiftSyncExclusion removes the block. Importing by hand is the way back:
// without this an excluded design could never be had again, and the import would
// appear to do nothing on the next sync.
func LiftSyncExclusion(db *sql.DB, userID int, platform, sourceID, designURL string) {
	if sourceID != "" {
		dbutil.ExecLogged(db, "DELETE FROM sync_exclusions WHERE user_id=? AND source_platform=? AND source_id=?",
			userID, platform, sourceID)
	}
	if designURL != "" {
		dbutil.ExecLogged(db, "DELETE FROM sync_exclusions WHERE user_id=? AND source_url=?", userID, designURL)
	}
}

// queueIfNew enqueues a design URL if it is neither in the library nor already
// queued. Returns the new queue ID, or 0 when skipped.
func queueIfNew(db *sql.DB, designURL, platform string, userID int) int {
	// Every probe below skips the design when the answer is unknown as well as
	// when it is yes: a database that cannot say whether this design is already
	// here would otherwise have it downloaded a second time. The next sync run
	// asks again.
	if found, known := dbutil.Exists(db,
		"SELECT id FROM designs WHERE user_id=? AND source_url=? LIMIT 1", userID, designURL); found || !known {
		return 0
	}
	// A design the user deleted and excluded stays gone; it used to come back on
	// every run.
	if IsExcludedFromSync(db, userID, platform, extractLibSourceID(designURL, platform), designURL) {
		return 0
	}
	if sourceID := extractLibSourceID(designURL, platform); sourceID != "" {
		if found, known := dbutil.Exists(db,
			"SELECT id FROM designs WHERE user_id=? AND source_platform=? AND source_id=? LIMIT 1",
			userID, platform, sourceID); found || !known {
			return 0
		}
	}
	if found, known := dbutil.Exists(db,
		"SELECT id FROM download_queue WHERE user_id=? AND source_url=? AND status IN ('pending','downloading') LIMIT 1",
		userID, designURL); found || !known {
		return 0
	}
	insertResult, failure := db.Exec("INSERT INTO download_queue (user_id, source_url, platform, status) VALUES (?,?,?,'pending')", userID, designURL, platform)
	if failure != nil {
		return 0
	}
	newID, _ := insertResult.LastInsertId()
	return int(newID)
}

// ResolvePendingCollections links a freshly downloaded design with the
// collections noted for it and deletes the resolved notes.
func ResolvePendingCollections(db *sql.DB, userID int, platform, sourceID string, designID int) {
	if sourceID == "" {
		return
	}
	rows, failure := db.Query(
		"SELECT id, collection_id FROM pending_collection_assignments WHERE user_id=? AND source_platform=? AND source_design_id=?",
		userID, platform, sourceID)
	if failure != nil {
		return
	}
	type pendingAssignment struct{ id, collectionID int }
	var pending []pendingAssignment
	for rows.Next() {
		var assignment pendingAssignment
		if rows.Scan(&assignment.id, &assignment.collectionID) == nil {
			pending = append(pending, assignment)
		}
	}
	rows.Close()
	for _, assignment := range pending {
		dbutil.ExecLogged(db, "INSERT OR IGNORE INTO design_collections (design_id, collection_id, added_at) VALUES (?,?,CURRENT_TIMESTAMP)", designID, assignment.collectionID)
		dbutil.ExecLogged(db, "DELETE FROM pending_collection_assignments WHERE id=?", assignment.id)
	}
}

func findLocalDesign(db *sql.DB, designURL string, userID int) int {
	var id int
	if db.QueryRow("SELECT id FROM designs WHERE user_id=? AND source_url=? LIMIT 1", userID, designURL).Scan(&id) == nil {
		return id
	}
	return 0
}

// findOrCreateCollection maps a platform collection onto a local one, matching
// on the platform's ID and never on the name, so a rename on the platform keeps
// every design assigned instead of growing an empty copy beside it.
//
// Whether that new name also reaches the library depends on who named it last,
// which source_name answers: it holds the name the platform reported last time,
// so an untouched collection reads exactly "[Label] source_name". Anything else
// means the user renamed it locally, and then the local name stays - the same
// rule the designs follow.
func findOrCreateCollection(db *sql.DB, userID int, platform, platformID, name string) int {
	label := Label(platform)
	prefixed := "[" + label + "] " + name

	var id int
	var existingName string
	var reportedName sql.NullString
	failure := db.QueryRow(
		"SELECT id, name, source_name FROM collections WHERE user_id=? AND source_platform=? AND source_platform_collection_id=? LIMIT 1",
		userID, platform, platformID).Scan(&id, &existingName, &reportedName)
	if failure == nil && id > 0 {
		// A row from before the column existed says nothing about who named it last, so
		// it counts as renamed locally: only a rename after this moves the local name.
		previous := reportedName.String
		if !reportedName.Valid {
			previous = ""
		}
		updatedName := existingName
		if previous != "" && existingName == "["+label+"] "+previous {
			updatedName = prefixed
		}
		if updatedName != existingName || reportedName.String != name {
			dbutil.ExecLogged(db,
				"UPDATE collections SET name=?, source_name=?, updated_at=CURRENT_TIMESTAMP WHERE id=?",
				updatedName, name, id)
		}
		return id
	}
	insertResult, failure := db.Exec(
		"INSERT INTO collections (user_id, name, source_platform, source_platform_collection_id, source_name) VALUES (?,?,?,?,?)",
		userID, prefixed, platform, platformID, name)
	if failure != nil {
		return 0
	}
	newID, _ := insertResult.LastInsertId()
	return int(newID)
}

var (
	libraryModelPattern  = regexp.MustCompile(`(?i)/model/(\d+)`)
	libraryThingPattern  = regexp.MustCompile(`(?i)thing:(\d+)`)
	libraryModelsPattern = regexp.MustCompile(`(?i)/models/(\d+)`)
	libraryObjectPattern = regexp.MustCompile(`(?i)/object/(\w+)`)
	// Cults3D source ID = last path segment, matching what the download stores.
	libraryCults3dPattern = regexp.MustCompile(`/([^/?#]+)/?$`)
	libraryTailPattern    = regexp.MustCompile(`[/:](\w+)/?$`)
)

func extractLibSourceID(designURL, platform string) string {
	var pattern *regexp.Regexp
	switch platform {
	case "printables":
		pattern = libraryModelPattern
	case "thingiverse":
		pattern = libraryThingPattern
	case "makerworld":
		pattern = libraryModelsPattern
	case "myminifactory":
		pattern = libraryObjectPattern
	case "cults3d":
		pattern = libraryCults3dPattern
	default:
		pattern = libraryTailPattern
	}
	if match := pattern.FindStringSubmatch(designURL); match != nil {
		return match[1]
	}
	return ""
}
