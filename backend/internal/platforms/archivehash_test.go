package platforms

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeZip builds an archive with the given entries at the given timestamp.
func writeZip(t *testing.T, path string, stamp time.Time, entries [][2]string) {
	t.Helper()
	buffer := new(bytes.Buffer)
	writer := zip.NewWriter(buffer)
	for _, entry := range entries {
		header := &zip.FileHeader{Name: entry[0], Method: zip.Deflate}
		header.Modified = stamp
		part, failure := writer.CreateHeader(header)
		if failure != nil {
			t.Fatal(failure)
		}
		part.Write([]byte(entry[1]))
	}
	writer.Close()
	if failure := os.WriteFile(path, buffer.Bytes(), 0o600); failure != nil {
		t.Fatal(failure)
	}
}

func TestTimestampsDoNotChangeTheHash(t *testing.T) {
	dir := t.TempDir()
	content := [][2]string{{"3D/3dmodel.model", "<model/>"}, {"Metadata/thumb.png", "PNGDATA"}}
	first := filepath.Join(dir, "a.3mf")
	second := filepath.Join(dir, "b.3mf")
	writeZip(t, first, time.Date(2020, 1, 1, 10, 0, 0, 0, time.UTC), content)
	writeZip(t, second, time.Date(2026, 9, 4, 23, 53, 0, 0, time.UTC), content)

	if bytes.Equal(mustRead(t, first), mustRead(t, second)) {
		t.Fatal("the two archives should differ byte-wise, or the test proves nothing")
	}
	if ContentHash(first, "raw-a") != ContentHash(second, "raw-b") {
		t.Fatal("repacked archive with identical contents must hash the same")
	}
}

func TestDifferentContentStillDiffers(t *testing.T) {
	dir := t.TempDir()
	stamp := time.Date(2020, 1, 1, 10, 0, 0, 0, time.UTC)
	first := filepath.Join(dir, "a.3mf")
	second := filepath.Join(dir, "b.3mf")
	writeZip(t, first, stamp, [][2]string{{"3D/3dmodel.model", "<model/>"}})
	writeZip(t, second, stamp, [][2]string{{"3D/3dmodel.model", "<model changed=\"1\"/>"}})
	if ContentHash(first, "raw-a") == ContentHash(second, "raw-b") {
		t.Fatal("changed content must change the hash")
	}
}

func TestEntryOrderDoesNotMatter(t *testing.T) {
	dir := t.TempDir()
	stamp := time.Date(2020, 1, 1, 10, 0, 0, 0, time.UTC)
	first := filepath.Join(dir, "a.3mf")
	second := filepath.Join(dir, "b.3mf")
	writeZip(t, first, stamp, [][2]string{{"one.txt", "A"}, {"two.txt", "B"}})
	writeZip(t, second, stamp, [][2]string{{"two.txt", "B"}, {"one.txt", "A"}})
	if ContentHash(first, "raw-a") != ContentHash(second, "raw-b") {
		t.Fatal("packing order must not change the hash")
	}
}

func TestNonArchiveFallsBackToBytes(t *testing.T) {
	dir := t.TempDir()
	plain := filepath.Join(dir, "model.stl")
	os.WriteFile(plain, []byte("not a zip at all"), 0o600)
	if got := ContentHash(plain, "byte-hash"); got != "byte-hash" {
		t.Fatalf("a non-archive must keep its byte hash, got %q", got)
	}
	missing := ContentHash(filepath.Join(dir, "gone.3mf"), "byte-hash")
	if missing != "byte-hash" {
		t.Fatalf("an unreadable file must keep its byte hash, got %q", missing)
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, failure := os.ReadFile(path)
	if failure != nil {
		t.Fatal(failure)
	}
	return data
}
