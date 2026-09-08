package platforms

// Metadata a platform will hand out without anybody signing in.
//
// The browser extension is why this exists. It reads a design from the page it
// is standing on, and that works until a site loads its description later, on a
// click or a scroll: Printables does exactly that, so the extension sees the one
// summary line and nothing else, however carefully it looks.
//
// The platform's own API has the full text and answers a public model without
// credentials. So the division of labour is the honest one: the browser supplies
// what only a browser can get - a download link issued for its own session - and
// the server supplies what it has always been good at, the metadata.
//
// Nothing here logs in or reads a stored credential. A platform that refuses to
// answer anonymously simply returns nothing, and the extension's reading stands.

import (
	"encoding/json"
	"fmt"
	"strings"
)

// PublicMeta is what could be learned. Every field is optional: a caller merges
// what is there and keeps what it already had.
type PublicMeta struct {
	Name        string
	Description string
	Author      string
	Tags        []string
	ImageURLs   []string
}

// PublicMetadata asks a platform about a design without any credentials.
//
// ok is false when the platform is not supported here, the URL carries no id, or
// the answer was unusable - all of which are ordinary and leave the caller with
// whatever it had.
func PublicMetadata(platform, sourceURL string) (PublicMeta, bool) {
	switch platform {
	case "printables":
		return printablesPublicMetadata(sourceURL)
	default:
		return PublicMeta{}, false
	}
}

// printablesPublicMetadata reads a print through the public GraphQL API.
//
// The same query the downloader uses, minus the Authorization header: a public
// model is answered without one, which is the whole point of this path.
func printablesPublicMetadata(sourceURL string) (PublicMeta, bool) {
	match := printablesModelIDPattern.FindStringSubmatch(sourceURL)
	if match == nil {
		return PublicMeta{}, false
	}

	query := fmt.Sprintf(
		"{ print(id: %s) { name description user { publicUsername } images { filePath } tags { name } } }",
		match[1])
	body, _ := json.Marshal(map[string]string{"query": query})
	raw := Printables{}.graphQL(string(body), map[string]string{
		"Content-Type": "application/json",
		"Accept":       "application/json",
		"User-Agent":   "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36",
		"Referer":      "https://www.printables.com/",
	})

	var answer struct {
		Data struct {
			Print *struct {
				Name        string `json:"name"`
				Description string `json:"description"`
				User        struct {
					PublicUsername string `json:"publicUsername"`
				} `json:"user"`
				Images []struct {
					FilePath string `json:"filePath"`
				} `json:"images"`
				Tags []struct {
					Name string `json:"name"`
				} `json:"tags"`
			} `json:"print"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &answer) != nil || answer.Data.Print == nil {
		return PublicMeta{}, false
	}
	design := answer.Data.Print

	meta := PublicMeta{
		Name:        strings.TrimSpace(design.Name),
		Description: strings.TrimSpace(design.Description),
		Author:      strings.TrimSpace(design.User.PublicUsername),
	}
	for _, tag := range design.Tags {
		if name := strings.TrimSpace(tag.Name); name != "" {
			meta.Tags = append(meta.Tags, name)
		}
	}
	for _, image := range design.Images {
		if path := strings.TrimSpace(image.FilePath); path != "" {
			meta.ImageURLs = append(meta.ImageURLs, "https://media.printables.com/"+path)
		}
	}
	return meta, true
}
