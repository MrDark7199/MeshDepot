package platforms

import "testing"

func TestResolveCults3dImageURL(t *testing.T) {
	input := "https://images.cults3d.com/sig/200x200/filters:no_upscale()/https://fbi.cults3d.com/uploaders/1/x.jpg"
	want := "https://fbi.cults3d.com/uploaders/1/x.jpg"
	if got := resolveCults3dImageURL(input); got != want {
		t.Errorf("resolve = %q, want %q", got, want)
	}
	// Direct URL unchanged.
	direct := "https://fbi.cults3d.com/uploaders/1/y.png"
	if got := resolveCults3dImageURL(direct); got != direct {
		t.Errorf("resolve direct = %q", got)
	}
}

func TestIsCults3dFile(t *testing.T) {
	if !isCults3dFile("https://cults3d.com/x", "application/octet-stream", "") {
		t.Error("octet-stream should be a file")
	}
	if !isCults3dFile("https://cults3d.com/dl/1", "", `attachment; filename="part.stl"`) {
		t.Error("attachment should be a file")
	}
	if !isCults3dFile("https://cults3d.com/foo/model.zip?x=1", "text/html", "") {
		t.Error(".zip url should be a file")
	}
	if isCults3dFile("https://cults3d.com/page", "text/html", "") {
		t.Error("html page should not be a file")
	}
	if isCults3dFile("https://cults3d.com/x", "", `attachment; filename="readme.txt"`) {
		t.Error(".txt attachment should be excluded")
	}
}

func TestCults3dRespName(t *testing.T) {
	cases := []struct{ url, disposition, want string }{
		{"https://cults3d.com/d/1", `attachment; filename="My Part.stl"`, "My Part.stl"},
		{"https://cults3d.com/d/1", `attachment; filename*=UTF-8''my%20file.3mf`, "my file.3mf"},
		{"https://cults3d.com/files/widget.zip?token=abc", "", "widget.zip"},
	}
	for _, testCase := range cases {
		if got := cults3dRespName(testCase.url, testCase.disposition); got != testCase.want {
			t.Errorf("cults3dRespName(%q,%q) = %q, want %q", testCase.url, testCase.disposition, got, testCase.want)
		}
	}
}

func TestParseCults3dMetadata(t *testing.T) {
	htmlBody := `<html><head>
<meta property="og:title" content="Cool Dragon 🐉 | Cults3D">
<meta property="og:description" content="A fierce dragon model">
<meta property="og:image" content="https://images.cults3d.com/sig/100x100/https://fbi.cults3d.com/u/cover.jpg">
<script type="application/ld+json">{"author":{"name":"Jane Maker"},"keywords":["dragon","toy"]}</script>
</head><body>
<a href="/en/tags/fantasy">fantasy</a>
<a href="https://cults3d.com/en/users/janemaker">Jane</a>
</body></html>`
	meta := parseCults3dMetadata(htmlBody, "https://cults3d.com/en/3d-model/game/cool-dragon/")
	if meta.name != "Cool Dragon" {
		t.Errorf("name = %q, want %q", meta.name, "Cool Dragon")
	}
	if meta.description != "A fierce dragon model" {
		t.Errorf("description = %q", meta.description)
	}
	if meta.author != "Jane Maker" {
		t.Errorf("author = %q, want Jane Maker", meta.author)
	}
	if len(meta.tags) != 2 || meta.tags[0] != "dragon" || meta.tags[1] != "toy" {
		t.Errorf("tags = %v, want [dragon toy]", meta.tags)
	}
	if len(meta.imageURLs) == 0 || meta.imageURLs[0] == "" {
		t.Errorf("expected at least one image URL, got %v", meta.imageURLs)
	}
}
