// Package blobstore implements the content-addressed half of the storage
// layout: file contents are written once per user under their SHA-256, and the
// version directories reference them as hard links.
//
// The store deliberately keeps no index of its own. The filesystem already
// answers both questions that matter - "do we have this content?" is the
// existence of the blob path, and "is this blob still needed?" is its link
// count - and an index would be a second source of truth that can drift.
package blobstore

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"meshdepot/internal/storage"
)

// Info describes a stored blob. IsNew is false when the content was already
// present, which is what makes a sync able to tell an unchanged file from a
// changed one.
type Info struct {
	Hash      string
	Path      string
	SizeBytes int64
	IsNew     bool
}

// StoreBytes stores an in-memory file (a ZIP entry, an uploaded image).
func StoreBytes(user storage.UserLayout, data []byte) (Info, error) {
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	blobPath := user.Blob(hash)
	if exists(blobPath) {
		return Info{Hash: hash, Path: blobPath, SizeBytes: int64(len(data)), IsNew: false}, nil
	}
	if failure := storage.WriteFile(blobPath, data); failure != nil {
		return Info{}, failure
	}
	return Info{Hash: hash, Path: blobPath, SizeBytes: int64(len(data)), IsNew: true}, nil
}

// StoreReader streams a reader into the store, hashing as it goes, so a
// multi-hundred-MB model never has to fit in memory. The temp file is created
// inside the user's directory to keep the final move a rename rather than a
// copy across filesystems.
func StoreReader(user storage.UserLayout, reader io.Reader) (Info, error) {
	tempDir := user.Temp()
	if failure := storage.MkdirAll(tempDir); failure != nil {
		return Info{}, failure
	}
	tempFile, failure := os.CreateTemp(tempDir, "blob_")
	if failure != nil {
		return Info{}, failure
	}
	tempPath := tempFile.Name()
	defer os.Remove(tempPath)

	hasher := sha256.New()
	size, failure := io.Copy(io.MultiWriter(tempFile, hasher), reader)
	tempFile.Close()
	if failure != nil {
		return Info{}, failure
	}
	hash := hex.EncodeToString(hasher.Sum(nil))
	blobPath := user.Blob(hash)
	if exists(blobPath) {
		return Info{Hash: hash, Path: blobPath, SizeBytes: size, IsNew: false}, nil
	}
	if failure := storage.MkdirAll(filepath.Dir(blobPath)); failure != nil {
		return Info{}, failure
	}
	if failure := moveFile(tempPath, blobPath); failure != nil {
		return Info{}, failure
	}
	return Info{Hash: hash, Path: blobPath, SizeBytes: size, IsNew: true}, nil
}

// Link publishes a blob into a version directory. The link makes the version a
// directory of real files while the content stays stored once; on a filesystem
// without hard links (some bind mounts) it degrades to a copy, which costs disk
// but never correctness.
func Link(blobPath, destination string) error {
	if failure := storage.MkdirAll(filepath.Dir(destination)); failure != nil {
		return failure
	}
	if exists(destination) {
		if sameContent(blobPath, destination) {
			return nil
		}
		if failure := os.Remove(destination); failure != nil {
			return failure
		}
	}
	if failure := os.Link(blobPath, destination); failure == nil {
		return nil
	}
	return copyFile(blobPath, destination)
}

// Unlink removes one published copy and, once no version references the content
// any more, the blob itself. Checking the link count is what keeps the store
// from growing forever without a separate garbage collector: the last link to
// leave takes the blob with it.
func Unlink(blobPath, published string) error {
	if published != "" {
		if failure := os.Remove(published); failure != nil && !os.IsNotExist(failure) {
			return failure
		}
	}
	if blobPath == "" || !exists(blobPath) {
		return nil
	}
	if links(blobPath) > 1 {
		return nil
	}
	if failure := os.Remove(blobPath); failure != nil && !os.IsNotExist(failure) {
		return failure
	}
	pruneEmptyPrefixes(blobPath)
	return nil
}

// pruneEmptyPrefixes removes the two hash directories above a blob once they
// hold nothing. Without this a library that is filled and emptied again leaves
// up to 65536 empty directories behind. os.Remove refuses a non-empty one, so
// the check is the call itself.
func pruneEmptyPrefixes(blobPath string) {
	directory := filepath.Dir(blobPath)
	for level := 0; level < 2; level++ {
		if failure := os.Remove(directory); failure != nil {
			return
		}
		directory = filepath.Dir(directory)
	}
}

// links returns the hard-link count of a file. When the platform does not
// report one it answers 2, the conservative result: an unknown link count keeps
// the blob rather than deleting content that may still be published.
func links(path string) uint64 {
	info, failure := os.Stat(path)
	if failure != nil {
		return 0
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		return uint64(stat.Nlink)
	}
	return 2
}

// sameContent reports whether two paths are already the same inode, which is
// the normal case when a version is republished unchanged.
func sameContent(first, second string) bool {
	firstInfo, failure := os.Stat(first)
	if failure != nil {
		return false
	}
	secondInfo, failure := os.Stat(second)
	if failure != nil {
		return false
	}
	return os.SameFile(firstInfo, secondInfo)
}

func exists(path string) bool {
	_, failure := os.Stat(path)
	return failure == nil
}

// moveFile moves a file, falling back to a copy across device boundaries.
func moveFile(source, destination string) error {
	if failure := os.Rename(source, destination); failure == nil {
		return nil
	}
	return copyFile(source, destination)
}

func copyFile(source, destination string) error {
	sourceFile, failure := os.Open(source)
	if failure != nil {
		return failure
	}
	defer sourceFile.Close()
	destinationFile, failure := os.Create(destination)
	if failure != nil {
		return failure
	}
	if _, failure := io.Copy(destinationFile, sourceFile); failure != nil {
		destinationFile.Close()
		return failure
	}
	return destinationFile.Close()
}
