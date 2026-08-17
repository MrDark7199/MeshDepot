package blobstore

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"meshdepot/internal/storage"
)

const testHash = "a3f1c8e94b2d7f0a5c6e1b8d4a9f2c37"

func newTestLayout(t *testing.T) storage.UserLayout {
	t.Helper()
	return storage.New(t.TempDir()).User(testHash)
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func TestStoreBytesWritesTheBlobAtItsHash(t *testing.T) {
	layout := newTestLayout(t)
	data := []byte("content")

	info, failure := StoreBytes(layout, data)
	if failure != nil {
		t.Fatalf("store: %v", failure)
	}
	if info.Hash != sha256Hex(data) {
		t.Fatalf("hash %q does not match the content", info.Hash)
	}
	if info.Path != layout.Blob(info.Hash) {
		t.Fatalf("blob written to %q", info.Path)
	}
	if !info.IsNew {
		t.Fatal("the first store did not report the blob as new")
	}
	if info.SizeBytes != int64(len(data)) {
		t.Fatalf("size %d", info.SizeBytes)
	}
	stored, failure := os.ReadFile(info.Path)
	if failure != nil || !bytes.Equal(stored, data) {
		t.Fatalf("the content did not arrive: %v", failure)
	}
}

// Storing the same content twice must not rewrite the file - that is what lets
// a sync tell an unchanged file from a changed one.
func TestStoreDeduplicatesAcrossBothEntryPoints(t *testing.T) {
	layout := newTestLayout(t)
	data := []byte("content")

	first, _ := StoreBytes(layout, data)
	second, failure := StoreReader(layout, bytes.NewReader(data))
	if failure != nil {
		t.Fatalf("store: %v", failure)
	}
	if second.Hash != first.Hash || second.Path != first.Path {
		t.Fatalf("the same content produced two blobs: %q / %q", first.Path, second.Path)
	}
	if second.IsNew {
		t.Fatal("a known blob was reported as new")
	}
	if second.SizeBytes != first.SizeBytes {
		t.Fatalf("sizes differ: %d / %d", first.SizeBytes, second.SizeBytes)
	}
}

// Two users owning the same file keep their own copy: an account has to stay
// removable on its own.
func TestBlobsOfDifferentUsersStaySeparate(t *testing.T) {
	layout := storage.New(t.TempDir())
	data := []byte("content")

	first, _ := StoreBytes(layout.User(testHash), data)
	second, failure := StoreBytes(layout.User("0000000000000000000000000000ffff"), data)
	if failure != nil {
		t.Fatalf("store: %v", failure)
	}
	if first.Path == second.Path {
		t.Fatal("both users share one blob path")
	}
	if !second.IsNew {
		t.Fatal("the second user's blob was treated as already present")
	}
	if !exists(first.Path) || !exists(second.Path) {
		t.Fatal("a blob is missing")
	}
}

func TestStoreReaderMatchesStoreBytes(t *testing.T) {
	layout := newTestLayout(t)
	data := []byte("streamed content")

	fromReader, failure := StoreReader(layout, bytes.NewReader(data))
	if failure != nil {
		t.Fatalf("store: %v", failure)
	}
	if fromReader.Hash != sha256Hex(data) {
		t.Fatalf("hash %q does not match the content", fromReader.Hash)
	}
	stored, failure := os.ReadFile(fromReader.Path)
	if failure != nil || !bytes.Equal(stored, data) {
		t.Fatalf("the content did not arrive: %v", failure)
	}
}

func TestStoreBytesHandlesEmptyContent(t *testing.T) {
	layout := newTestLayout(t)

	info, failure := StoreBytes(layout, nil)
	if failure != nil {
		t.Fatalf("store: %v", failure)
	}
	if info.SizeBytes != 0 || !exists(info.Path) {
		t.Fatalf("empty content produced %+v", info)
	}
}

// The temp file must not survive, whether the blob was moved or discarded as a
// duplicate.
func TestStoreReaderLeavesNoTempFile(t *testing.T) {
	layout := newTestLayout(t)
	data := []byte("content")

	StoreReader(layout, bytes.NewReader(data))
	StoreReader(layout, bytes.NewReader(data))

	leftovers, failure := filepath.Glob(filepath.Join(layout.Temp(), "blob_*"))
	if failure != nil {
		t.Fatalf("glob: %v", failure)
	}
	if len(leftovers) != 0 {
		t.Fatalf("temp files were left behind: %v", leftovers)
	}
}

func TestStoreReportsAnUnwritableRoot(t *testing.T) {
	directory := t.TempDir()
	blocked := filepath.Join(directory, "root")
	if failure := os.WriteFile(blocked, []byte("not a directory"), 0o664); failure != nil {
		t.Fatalf("write blocker: %v", failure)
	}
	layout := storage.New(blocked).User(testHash)

	if _, failure := StoreBytes(layout, []byte("content")); failure == nil {
		t.Fatal("StoreBytes accepted an unwritable root")
	}
	if _, failure := StoreReader(layout, bytes.NewReader([]byte("content"))); failure == nil {
		t.Fatal("StoreReader accepted an unwritable root")
	}
}

func TestStoreReaderReportsReadFailure(t *testing.T) {
	layout := newTestLayout(t)
	if _, failure := StoreReader(layout, failingReader{}); failure == nil {
		t.Fatal("a failing reader did not report an error")
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("connection reset") }

// The point of the layout: a published file is a real file, and it is the same
// inode as the blob, so a version costs no second copy.
func TestLinkPublishesTheBlobAsTheSameInode(t *testing.T) {
	layout := newTestLayout(t)
	data := []byte("content")
	blob, _ := StoreBytes(layout, data)
	destination := filepath.Join(layout.Version(42, "1.0"), "part.stl")

	if failure := Link(blob.Path, destination); failure != nil {
		t.Fatalf("link: %v", failure)
	}
	published, failure := os.ReadFile(destination)
	if failure != nil || !bytes.Equal(published, data) {
		t.Fatalf("the published file does not hold the content: %v", failure)
	}
	if !sameContent(blob.Path, destination) {
		t.Fatal("the published file is a copy, not a link")
	}
}

func TestLinkIsIdempotentAndReplacesForeignContent(t *testing.T) {
	layout := newTestLayout(t)
	blob, _ := StoreBytes(layout, []byte("content"))
	destination := filepath.Join(layout.Version(42, "1.0"), "part.stl")

	if failure := Link(blob.Path, destination); failure != nil {
		t.Fatalf("first link: %v", failure)
	}
	if failure := Link(blob.Path, destination); failure != nil {
		t.Fatalf("second link: %v", failure)
	}

	// A file of different content in the way must be replaced, not kept.
	other, _ := StoreBytes(layout, []byte("other content"))
	if failure := Link(other.Path, destination); failure != nil {
		t.Fatalf("replacing link: %v", failure)
	}
	if !sameContent(other.Path, destination) {
		t.Fatal("the destination still points at the old content")
	}
}

// A blob stays as long as a version references it and goes with the last one.
func TestUnlinkKeepsTheBlobWhileItIsStillPublished(t *testing.T) {
	layout := newTestLayout(t)
	blob, _ := StoreBytes(layout, []byte("content"))
	first := filepath.Join(layout.Version(42, "1.0"), "part.stl")
	second := filepath.Join(layout.Version(42, "2.0"), "part.stl")
	if failure := Link(blob.Path, first); failure != nil {
		t.Fatalf("link: %v", failure)
	}
	if failure := Link(blob.Path, second); failure != nil {
		t.Fatalf("link: %v", failure)
	}

	if failure := Unlink(blob.Path, first); failure != nil {
		t.Fatalf("unlink: %v", failure)
	}
	if exists(first) {
		t.Fatal("the published file survived")
	}
	if !exists(blob.Path) {
		t.Fatal("the blob was removed although another version still links it")
	}

	if failure := Unlink(blob.Path, second); failure != nil {
		t.Fatalf("unlink: %v", failure)
	}
	if exists(blob.Path) {
		t.Fatal("the last link left but the blob stayed")
	}
}

func TestUnlinkToleratesMissingPaths(t *testing.T) {
	layout := newTestLayout(t)
	missing := filepath.Join(layout.Root(), "gone")

	if failure := Unlink(missing, missing); failure != nil {
		t.Fatalf("unlink of missing paths reported %v", failure)
	}
	if failure := Unlink("", ""); failure != nil {
		t.Fatalf("unlink of empty paths reported %v", failure)
	}
}

func TestMoveFileMovesContent(t *testing.T) {
	directory := t.TempDir()
	source := filepath.Join(directory, "source")
	destination := filepath.Join(directory, "destination")
	if failure := os.WriteFile(source, []byte("payload"), 0o664); failure != nil {
		t.Fatalf("write source: %v", failure)
	}

	if failure := moveFile(source, destination); failure != nil {
		t.Fatalf("move: %v", failure)
	}
	moved, failure := os.ReadFile(destination)
	if failure != nil || string(moved) != "payload" {
		t.Fatalf("the content did not arrive: %q/%v", moved, failure)
	}
	if exists(source) {
		t.Fatal("the source still exists after the rename")
	}
}

func TestMoveFileReportsUnwritableDestination(t *testing.T) {
	directory := t.TempDir()
	source := filepath.Join(directory, "source")
	if failure := os.WriteFile(source, []byte("payload"), 0o664); failure != nil {
		t.Fatalf("write source: %v", failure)
	}

	// The rename fails because the target directory does not exist, and the
	// copy fallback cannot create the file either.
	if failure := moveFile(source, filepath.Join(directory, "missing-directory", "destination")); failure == nil {
		t.Fatal("an unwritable destination did not report an error")
	}
	if !exists(source) {
		t.Fatal("the source was removed although the move failed")
	}
}

func TestMoveFileReportsMissingSource(t *testing.T) {
	directory := t.TempDir()
	if failure := moveFile(filepath.Join(directory, "missing"), filepath.Join(directory, "destination")); failure == nil {
		t.Fatal("a missing source did not report an error")
	}
}

func TestExists(t *testing.T) {
	directory := t.TempDir()
	existing := filepath.Join(directory, "file")
	os.WriteFile(existing, []byte("x"), 0o664)

	if !exists(existing) {
		t.Fatal("an existing file was not recognised")
	}
	if exists(filepath.Join(directory, "missing")) {
		t.Fatal("a missing file was reported as existing")
	}
}

// An unreadable link count must keep the blob rather than delete content that
// may still be published.
func TestLinksAnswersConservativelyForMissingFiles(t *testing.T) {
	if count := links(filepath.Join(t.TempDir(), "missing")); count != 0 {
		t.Fatalf("a missing file reported %d links", count)
	}
}

// The two hash levels above a blob would otherwise pile up: a library filled
// and emptied again leaves 65536 empty directories behind.
func TestUnlinkPrunesTheEmptyHashDirectories(t *testing.T) {
	layout := newTestLayout(t)
	kept, _ := StoreBytes(layout, []byte("content"))
	// A second blob sharing the first prefix level keeps that level alive.
	sharing := siblingHashPath(t, layout, kept.Hash)

	if failure := Unlink(kept.Path, ""); failure != nil {
		t.Fatalf("unlink: %v", failure)
	}
	if exists(filepath.Dir(kept.Path)) {
		t.Fatal("the second level survived although it is empty")
	}
	if !exists(filepath.Dir(sharing)) {
		t.Fatal("a level still holding another blob was removed")
	}
}

// siblingHashPath writes a blob whose hash shares the first prefix level with
// the given one, and returns its path.
func siblingHashPath(t *testing.T, layout storage.UserLayout, hash string) string {
	t.Helper()
	sibling := hash[0:2] + "ff" + hash[4:]
	path := layout.Blob(sibling)
	if failure := storage.WriteFile(path, []byte("sibling")); failure != nil {
		t.Fatalf("write the sibling blob: %v", failure)
	}
	return path
}
