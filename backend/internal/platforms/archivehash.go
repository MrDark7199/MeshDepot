package platforms

// Content hashing for archive formats.
//
// Some platforms build the archive at download time rather than serving a
// stored one. MakerWorld does: two downloads of an unchanged model give two
// .3mf of identical size whose bytes differ only in the ZIP per-entry
// "last modified" fields - verified on a real pair, where 176 bytes differed
// and every one sat at offset 10..13 of a local file header, while all 22
// entries carried identical names and CRC32s.
//
// A byte hash of such a file changes on every sync, so the sync sees a new file
// and publishes a version that contains nothing new. Hashing what the archive
// *holds* instead of how it was packed makes the comparison stable.
//
// This only affects the comparison. Blobs stay addressed by their byte hash:
// two archives with the same contents but different bytes really are different
// files on disk, and a stored blob must keep resolving to what was downloaded.

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"sort"
)

// zipMagic is the local file header signature every ZIP starts with. 3MF, and
// the .zip a few platforms hand out, are ZIP containers.
var zipMagic = []byte{'P', 'K', 0x03, 0x04}

// ContentHash returns a hash that identifies what a file contains, ignoring how
// it was packed.
//
// For a ZIP container it is derived from the entries: each name with its
// uncompressed size and CRC32, sorted by name, so neither the packing order nor
// the timestamps nor the compression level can change it. The CRCs come from
// the central directory, so nothing is decompressed.
//
// Everything else - and any archive that cannot be read - falls back to
// byteHash. A file we cannot interpret must compare by its bytes; guessing
// would be worse than the duplicate versions this avoids.
func ContentHash(path string, byteHash string) string {
	file, failure := os.Open(path)
	if failure != nil {
		return byteHash
	}
	defer file.Close()

	header := make([]byte, 4)
	if read, failure := file.Read(header); failure != nil || read != 4 {
		return byteHash
	}
	for index, expected := range zipMagic {
		if header[index] != expected {
			return byteHash
		}
	}

	info, failure := file.Stat()
	if failure != nil {
		return byteHash
	}
	reader, failure := zip.NewReader(file, info.Size())
	if failure != nil {
		return byteHash
	}

	lines := make([]string, 0, len(reader.File))
	for _, entry := range reader.File {
		// Directory entries carry no content and some packers emit them and
		// some do not - including them would reintroduce a difference that
		// says nothing about the model.
		if entry.FileInfo().IsDir() {
			continue
		}
		lines = append(lines, fmt.Sprintf("%s\x00%d\x00%08x", entry.Name, entry.UncompressedSize64, entry.CRC32))
	}
	if len(lines) == 0 {
		return byteHash
	}
	sort.Strings(lines)

	digest := sha256.New()
	for _, line := range lines {
		digest.Write([]byte(line))
		digest.Write([]byte{'\n'})
	}
	return hex.EncodeToString(digest.Sum(nil))
}
