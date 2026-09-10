package platforms

// Content hashing for archive formats.
//
// Some platforms build the archive at download time. MakerWorld does: two
// downloads of an unchanged model give two .3mf whose bytes differ only in the
// ZIP per-entry "last modified" fields - on a real pair, 176 bytes differed and
// every one sat in a local file header, while all 22 entries carried identical
// names and CRC32s.
//
// A byte hash of such a file changes on every sync, so hashing what the archive
// holds rather than how it was packed keeps the comparison stable.

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"sort"
)

// zipMagic is the local file header signature. 3MF and .zip are both containers.
var zipMagic = []byte{'P', 'K', 0x03, 0x04}

// ContentHash identifies what a file contains, ignoring how it was packed. For a
// ZIP it comes from each entry's name, uncompressed size and CRC32, sorted by
// name, all read from the central directory so nothing is decompressed.
//
// Everything else, and any archive that cannot be read, falls back to byteHash: a
// file we cannot interpret must compare by its bytes.
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
		// Directory entries carry no content and some packers emit them, some do not.
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
