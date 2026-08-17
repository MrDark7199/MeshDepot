package platforms

import "testing"

func TestThingIDExtraction(t *testing.T) {
	cases := map[string]string{
		"https://www.thingiverse.com/thing:12345":         "12345",
		"https://www.thingiverse.com/thing:99/files":      "99",
		"http://thingiverse.com/Thing-42":                 "42",
		"https://www.thingiverse.com/thing/7":             "7",
		"https://www.thingiverse.com/designs/cool-widget": "", // no ID
	}
	for url, want := range cases {
		match := thingIDPattern.FindStringSubmatch(url)
		got := ""
		if match != nil {
			got = match[1]
		}
		if got != want {
			t.Errorf("thingID(%q) = %q, want %q", url, got, want)
		}
	}
}

func TestPrintableExt(t *testing.T) {
	yes := []string{"part.stl", "model.3MF", "x.obj", "a.step", "b.STP"}
	no := []string{"readme.txt", "image.png", "notes.pdf", "x.stlx"}
	for _, name := range yes {
		if !printableExtPattern.MatchString(name) {
			t.Errorf("%q should be printable", name)
		}
	}
	for _, name := range no {
		if printableExtPattern.MatchString(name) {
			t.Errorf("%q should not be printable", name)
		}
	}
}

func TestSanitizeFileName(t *testing.T) {
	cases := map[string]string{
		"My File (1).stl":  "My_File__1_.stl",
		"sub/dir/part.3mf": "sub_dir_part.3mf",
		"clean-name.stl":   "clean-name.stl",
	}
	for input, want := range cases {
		if got := sanitizeFileName(input); got != want {
			t.Errorf("sanitizeFileName(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestSanitizeURL(t *testing.T) {
	got := sanitizeURL("https://cdn.example.com/files/My File.stl?token=abc")
	want := "https://cdn.example.com/files/My%20File.stl?token=abc"
	if got != want {
		t.Errorf("sanitizeURL = %q, want %q", got, want)
	}
	// Without scheme/host leave unchanged.
	if got := sanitizeURL("not a url"); got != "not a url" {
		t.Errorf("sanitizeURL passthrough = %q", got)
	}
}
