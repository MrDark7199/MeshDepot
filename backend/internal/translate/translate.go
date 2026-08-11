// Package translate is a translation service based on the unofficial Google
// Translate endpoint
// (translate.googleapis.com, client=gtx) - free, without an API key.
//
// It translates design texts (name/description) into canonical English (in the
// designs row) plus display variants per language in design_translations. HTML
// descriptions are translated text node by text node so the markup stays
// unchanged. Every failure returns false/nil instead of throwing - translation
// must never interrupt download/sync/save.
package translate

import (
	"database/sql"
	"encoding/json"
	"io"
	"meshdepot/internal/dbutil"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	nethtml "golang.org/x/net/html"
)

const (
	defaultEndpoint     = "https://translate.googleapis.com/translate_a/t?client=gtx&sl=auto&tl="
	canonicalTarget     = "en"
	requestTimeout      = 20 * time.Second
	batchCharLimit      = 4000
	defaultRequestPause = 200 * time.Millisecond
	// maxTranslateResponseBytes bounds the reply. Requests are capped at
	// batchCharLimit, so anything larger is not a translation.
	maxTranslateResponseBytes = 8 << 20
)

// displayTargets are the display languages additionally stored in
// design_translations (Google code => enum value).
var displayTargets = []struct{ google, language string }{{"de", "de"}}

// Service wraps the translation service. Endpoint, client and pause are fields
// rather than constants so the request path can be exercised against a local
// test server instead of the live Google endpoint.
type Service struct {
	DB           *sql.DB
	endpoint     string
	httpClient   *http.Client
	requestPause time.Duration
}

func New(db *sql.DB) *Service {
	return &Service{
		DB:           db,
		endpoint:     defaultEndpoint,
		httpClient:   &http.Client{Timeout: requestTimeout},
		requestPause: defaultRequestPause,
	}
}

// Enabled reports whether automatic translation is active in app_settings.
func (service *Service) Enabled() bool {
	var value string
	if failure := service.DB.QueryRow("SELECT value FROM app_settings WHERE key='translation_enabled'").Scan(&value); failure != nil {
		return false
	}
	return value == "1"
}

// requestBatch translates plaintext strings in input order (a single batch).
func (service *Service) requestBatch(texts []string, targetLanguage string) ([]string, bool) {
	if len(texts) == 0 {
		return []string{}, true
	}
	form := url.Values{}
	for _, text := range texts {
		form.Add("q", text)
	}
	request, failure := http.NewRequest(http.MethodPost, service.endpoint+url.QueryEscape(targetLanguage), strings.NewReader(form.Encode()))
	if failure != nil {
		return nil, false
	}
	request.Header.Set("User-Agent", "Mozilla/5.0")
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, failure := service.httpClient.Do(request)
	if failure != nil {
		return nil, false
	}
	body, _ := io.ReadAll(io.LimitReader(response.Body, maxTranslateResponseBytes))
	response.Body.Close()
	time.Sleep(service.requestPause)
	if response.StatusCode != http.StatusOK {
		return nil, false
	}

	var decoded []json.RawMessage
	if json.Unmarshal(body, &decoded) != nil || len(decoded) != len(texts) {
		return nil, false
	}
	outputs := make([]string, 0, len(decoded))
	for _, item := range decoded {
		// Element is either a string or [translated, detectedLang].
		var plainString string
		if json.Unmarshal(item, &plainString) == nil {
			outputs = append(outputs, plainString)
			continue
		}
		var elements []json.RawMessage
		if json.Unmarshal(item, &elements) == nil && len(elements) > 0 {
			var firstString string
			if json.Unmarshal(elements[0], &firstString) == nil {
				outputs = append(outputs, firstString)
				continue
			}
		}
		return nil, false
	}
	return outputs, true
}

// splitLongText breaks a long text into pieces below batchCharLimit (preferably
// at line breaks, then sentence ends, otherwise a hard UTF-8-safe cut).
func splitLongText(text string) []string {
	if len(text) <= batchCharLimit {
		return []string{text}
	}
	var chunks []string
	remaining := text
	for len(remaining) > batchCharLimit {
		slice := remaining[:batchCharLimit]
		newlineIndex := strings.LastIndex(slice, "\n")
		sentenceIndex := strings.LastIndex(slice, ". ")
		cut := 0
		if newlineIndex >= 0 && newlineIndex+1 > cut {
			cut = newlineIndex + 1
		}
		if sentenceIndex >= 0 && sentenceIndex+2 > cut {
			cut = sentenceIndex + 2
		}
		if cut < batchCharLimit/2 {
			// No good boundary - hard cut at a UTF-8-safe position.
			cut = batchCharLimit
			for cut > 0 && !utf8.RuneStart(remaining[cut]) {
				cut--
			}
		}
		chunks = append(chunks, remaining[:cut])
		remaining = remaining[cut:]
	}
	if remaining != "" {
		chunks = append(chunks, remaining)
	}
	return chunks
}

// translateTextList translates plaintext strings in size-limited batches.
func (service *Service) translateTextList(texts []string, targetLanguage string) ([]string, bool) {
	var chunks []string
	var owners []int
	for index, text := range texts {
		for _, chunk := range splitLongText(text) {
			chunks = append(chunks, chunk)
			owners = append(owners, index)
		}
	}

	results := make([]string, len(texts))
	start := 0
	for start < len(chunks) {
		end := start
		length := 0
		for end < len(chunks) && (length+len(chunks[end]) <= batchCharLimit || end == start) {
			length += len(chunks[end])
			end++
		}
		translated, ok := service.requestBatch(chunks[start:end], targetLanguage)
		if !ok {
			return nil, false
		}
		for offset, piece := range translated {
			results[owners[start+offset]] += piece
		}
		start = end
	}
	return results, true
}

// translate translates a list into a target language. HTML inputs keep their
// markup (node by node), plaintext is translated directly.
func (service *Service) translate(texts []string, targetLanguage string) ([]string, bool) {
	if len(texts) == 0 {
		return []string{}, true
	}
	outputs := make([]string, 0, len(texts))
	for _, text := range texts {
		if looksLikeHTML(text) {
			translated, ok := service.translateHTML(text, targetLanguage)
			if !ok {
				return nil, false
			}
			outputs = append(outputs, translated)
		} else {
			translated, ok := service.translateTextList([]string{text}, targetLanguage)
			if !ok {
				return nil, false
			}
			outputs = append(outputs, translated[0])
		}
	}
	return outputs, true
}

// looksLikeHTML roughly detects whether a text contains HTML markup.
func looksLikeHTML(text string) bool {
	for index := 0; index+1 < len(text); index++ {
		if text[index] == '<' {
			nextChar := text[index+1]
			if (nextChar >= 'a' && nextChar <= 'z') || (nextChar >= 'A' && nextChar <= 'Z') {
				return true
			}
		}
	}
	return false
}

// translateHTML translates only the text nodes of an HTML fragment and keeps the
// markup (x/net/html instead of DOMDocument).
func (service *Service) translateHTML(htmlFragment, targetLanguage string) (string, bool) {
	document, failure := nethtml.Parse(strings.NewReader(`<html><body><div id="__wrap__">` + htmlFragment + `</div></body></html>`))
	if failure != nil {
		return "", false
	}
	wrapper := findByID(document, "__wrap__")
	if wrapper == nil {
		return "", false
	}

	var nodes []*nethtml.Node
	var walk func(node *nethtml.Node)
	walk = func(node *nethtml.Node) {
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			switch child.Type {
			case nethtml.TextNode:
				if strings.TrimSpace(child.Data) != "" {
					nodes = append(nodes, child)
				}
			case nethtml.ElementNode:
				if child.Data == "script" || child.Data == "style" {
					continue
				}
				walk(child)
			}
		}
	}
	walk(wrapper)

	if len(nodes) > 0 {
		texts := make([]string, len(nodes))
		for index, node := range nodes {
			texts[index] = node.Data
		}
		translated, ok := service.translateTextList(texts, targetLanguage)
		if !ok {
			return "", false
		}
		for index, node := range nodes {
			node.Data = translated[index]
		}
	}

	var builder strings.Builder
	for child := wrapper.FirstChild; child != nil; child = child.NextSibling {
		if nethtml.Render(&builder, child) != nil {
			return "", false
		}
	}
	return builder.String(), true
}

// findByID searches the tree for the element with the given id.
func findByID(node *nethtml.Node, id string) *nethtml.Node {
	if node.Type == nethtml.ElementNode {
		for _, attribute := range node.Attr {
			if attribute.Key == "id" && attribute.Val == id {
				return node
			}
		}
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if found := findByID(child, id); found != nil {
			return found
		}
	}
	return nil
}

// upsertRow writes a design_translations row (unique per design/field/language).
func (service *Service) upsertRow(designID int, field, language, content string) {
	dbutil.ExecLogged(service.DB,
		`INSERT INTO design_translations (design_id, field, lang, content) VALUES (?,?,?,?)
		 ON CONFLICT(design_id, field, lang) DO UPDATE SET content=excluded.content`,
		designID, field, language, content)
}

// OriginalOf returns the stored original text (lang='original') of a design
// field, or "" if none exists. Counterpart to GoogleTranslator::originalOf -
// used during sync to detect whether the source text changed and needs
// re-translation.
func (service *Service) OriginalOf(designID int, field string) string {
	var value string
	if failure := service.DB.QueryRow(
		"SELECT content FROM design_translations WHERE design_id=? AND field=? AND lang='original' LIMIT 1",
		designID, field,
	).Scan(&value); failure != nil {
		return ""
	}
	return value
}

// deleteDisplayRows removes the display-language rows of a design.
func (service *Service) deleteDisplayRows(designID int) {
	for _, target := range displayTargets {
		dbutil.ExecLogged(service.DB, "DELETE FROM design_translations WHERE design_id=? AND lang=?", designID, target.language)
	}
}

// ApplyToDesign is the complete translation pipeline for a design.
// storeOriginal=true persists the inputs as 'original' rows (platform
// downloads/syncs), false for edits.
func (service *Service) ApplyToDesign(designID int, name string, description *string, storeOriginal bool) bool {
	if !service.Enabled() {
		return false
	}
	hasDescription := description != nil && strings.TrimSpace(*description) != ""
	texts := []string{name}
	if hasDescription {
		texts = append(texts, *description)
	}

	english, ok := service.translate(texts, canonicalTarget)
	if !ok {
		service.deleteDisplayRows(designID)
		return false
	}

	if storeOriginal {
		service.upsertRow(designID, "name", "original", name)
		if hasDescription {
			service.upsertRow(designID, "description", "original", *description)
		}
	}

	englishName := truncateRunes(english[0], 255)
	if hasDescription {
		dbutil.ExecLogged(service.DB, "UPDATE designs SET name=?, description=? WHERE id=?", englishName, english[1], designID)
	} else {
		dbutil.ExecLogged(service.DB, "UPDATE designs SET name=? WHERE id=?", englishName, designID)
	}

	for _, target := range displayTargets {
		translated, ok := service.translate(texts, target.google)
		if !ok {
			service.deleteDisplayRows(designID)
			continue
		}
		service.upsertRow(designID, "name", target.language, truncateRunes(translated[0], 255))
		if hasDescription {
			service.upsertRow(designID, "description", target.language, translated[1])
		}
	}
	return true
}

// truncateRunes shortens a string to at most maxRunes runes.
func truncateRunes(value string, maxRunes int) string {
	if utf8.RuneCountInString(value) <= maxRunes {
		return value
	}
	count := 0
	for index := range value {
		if count == maxRunes {
			return value[:index]
		}
		count++
	}
	return value
}
