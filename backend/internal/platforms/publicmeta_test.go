package platforms

import "testing"

// The merge rule is the point of this path, so it is tested without a network:
// gaps are filled, existing values kept, and the description replaced only when
// the platform genuinely has more. Printables hands out a summary line to the
// page and the whole text to its API, which is what this is for.
func TestEnrichKeepsWhatTheBrowserFound(t *testing.T) {
	request := BrowserImportRequest{
		Name:        "From the page",
		Author:      "someone",
		Description: "A first sentence.",
		Tags:        []string{"page-tag"},
		ImageURLs:   []string{"https://example.invalid/from-page.png"},
	}
	public := PublicMeta{
		Name:        "From the API",
		Author:      "somebody else",
		Description: "A first sentence. And four paragraphs more, which the page never showed.",
		Tags:        []string{"api-tag"},
		ImageURLs:   []string{"https://example.invalid/from-api.png"},
	}
	mergePublicMetadata(&request, public)

	if request.Name != "From the page" || request.Author != "someone" {
		t.Fatalf("an existing value was overwritten: %q / %q", request.Name, request.Author)
	}
	if len(request.Tags) != 1 || request.Tags[0] != "page-tag" {
		t.Fatalf("tags were replaced: %v", request.Tags)
	}
	if request.Description != public.Description {
		t.Fatalf("the longer description did not win: %q", request.Description)
	}
	if len(request.ImageURLs) != 2 {
		t.Fatalf("pictures should be joined, got %v", request.ImageURLs)
	}
}

// The other direction: nothing read from the page, everything from the platform.
func TestEnrichFillsEveryGap(t *testing.T) {
	request := BrowserImportRequest{}
	public := PublicMeta{
		Name: "Spool Bot", Author: "mantisrobot",
		Description: "The whole text.", Tags: []string{"robot"},
		ImageURLs: []string{"https://example.invalid/a.png"},
	}
	mergePublicMetadata(&request, public)

	if request.Name != "Spool Bot" || request.Author != "mantisrobot" ||
		request.Description != "The whole text." || len(request.Tags) != 1 || len(request.ImageURLs) != 1 {
		t.Fatalf("a gap was left unfilled: %+v", request)
	}
}

// A shorter answer must not cost the description the browser already had.
func TestEnrichNeverShortensTheDescription(t *testing.T) {
	request := BrowserImportRequest{Description: "A long description read straight from the page."}
	mergePublicMetadata(&request, PublicMeta{Description: "Short."})

	if request.Description != "A long description read straight from the page." {
		t.Fatalf("the longer description was replaced by a shorter one: %q", request.Description)
	}
}

// A platform lists everything the designer uploaded, models and brochures alike.
// Importing the lot put a PDF in the library beside the parts; the Thingiverse
// downloader has always preferred the printable ones, and this path now does the
// same.
func TestPreferPrintableDropsWhatIsNotAModel(t *testing.T) {
	chosen := preferPrintable([]BrowserImportFile{
		{Name: "brochure.pdf", URL: "https://example.invalid/brochure.pdf"},
		{Name: "body.stl", URL: "https://example.invalid/body.stl"},
		{Name: "photo.jpg", URL: "https://example.invalid/photo.jpg"},
		{Name: "case.3mf", URL: "https://example.invalid/case.3mf"},
	})
	if len(chosen) != 2 || chosen[0].Name != "body.stl" || chosen[1].Name != "case.3mf" {
		t.Fatalf("expected the two models, got %+v", chosen)
	}
}

// Without a name from the client - Chrome does not know one when it reports a
// download - the address carries it instead.
func TestPreferPrintableFallsBackToTheAddress(t *testing.T) {
	chosen := preferPrintable([]BrowserImportFile{
		{URL: "https://example.invalid/files/manual.pdf"},
		{URL: "https://example.invalid/files/bracket.stl?signature=abc"},
	})
	if len(chosen) != 1 || chosen[0].URL != "https://example.invalid/files/bracket.stl?signature=abc" {
		t.Fatalf("expected the model judged by its address, got %+v", chosen)
	}
}

// Nothing recognisable means nothing is dropped. A Printables download is one
// archive with no printable extension of its own, and a design whose files are
// named unusually should still arrive whole.
func TestPreferPrintableKeepsEverythingWhenNothingMatches(t *testing.T) {
	entries := []BrowserImportFile{
		{Name: "all-files.zip", URL: "https://example.invalid/all.zip"},
		{Name: "notes.txt", URL: "https://example.invalid/notes.txt"},
	}
	if chosen := preferPrintable(entries); len(chosen) != 2 {
		t.Fatalf("expected both kept, got %+v", chosen)
	}
}
