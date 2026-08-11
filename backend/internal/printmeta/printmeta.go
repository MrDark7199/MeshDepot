// Package printmeta extracts the print settings of sliced files - FDM G-code as
// well as the resin/MSLA formats - and is the single entry point for manual
// upload, ZIP extraction and platform sync.
//
// Sliced files are large and consist almost entirely of data nobody wants here:
// movement commands in G-code, layer bitmaps in resin files. Extract therefore
// takes an io.ReaderAt plus the file size instead of a byte slice, so each
// format reads only the few blocks it actually needs - a header, a parameter
// block, a ZIP directory - whether the file sits on disk or in memory. Deciding
// how much to read is exactly what this package is for; callers just hand over
// the file.
//
// The result is a flat map that is stored as JSON in design_file_entries.
// gcode_meta and rendered by the frontend's print-settings tab. Every resin
// format sets "kind": "resin" so the frontend can pick its field list; G-code
// carries no kind (rows written before resin support existed are FDM).
package printmeta

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"

	"meshdepot/internal/gcode"
)

// anycubicPrefix is how much of a Photon Workshop file is read to reach its
// HEADER section. The section sits directly behind the 48-byte file mark in
// every file seen so far; the generous margin costs one read and covers files
// that push it back behind a preview.
const anycubicPrefix = 256 * 1024

// Supported reports whether Extract can say anything about this file at all.
func Supported(filename string) bool {
	if gcode.IsGcode(filename) {
		return true
	}
	switch extension(filename) {
	case ".pwmx", ".pwmo", ".pws", ".pw0", ".pwms", ".pwmb", ".ctb", ".cbddlp", ".photon", ".sl1", ".sl1s":
		return true
	}
	return false
}

// Extract returns the print settings of a sliced file. Unknown formats, damaged
// files and files with nothing recognizable in them all yield nil - an entry
// simply has no settings then, which is not an error worth reporting.
func Extract(filename string, source io.ReaderAt, size int64) map[string]any {
	if source == nil || size <= 0 {
		return nil
	}
	switch {
	case gcode.IsGcode(filename):
		return nonEmpty(gcode.Parse(headAndTail(source, size)))
	case isAnycubic(filename):
		return nonEmpty(parseAnycubic(read(source, size, 0, anycubicPrefix)))
	case isChitubox(filename):
		return nonEmpty(parseChitubox(source, size))
	case isSL1(filename):
		return nonEmpty(parseSL1(source, size))
	}
	return nil
}

// ExtractJSON is Extract encoded for the gcode_meta column ("" if there is
// nothing to store).
func ExtractJSON(filename string, source io.ReaderAt, size int64) string {
	parsed := Extract(filename, source, size)
	if len(parsed) == 0 {
		return ""
	}
	marshaled, failure := json.Marshal(parsed)
	if failure != nil {
		return ""
	}
	return string(marshaled)
}

// ExtractFileJSON reads the settings straight off a stored file. This is the
// form the platform sync and the blob store use, where the file is on disk and
// loading it whole would be wasteful.
//
// filename and path are separate on purpose: blobs are stored under their hash
// without an extension, so the format can only be told from the original name.
func ExtractFileJSON(filename, path string) string {
	if path == "" || !Supported(filename) {
		return ""
	}
	file, failure := os.Open(path)
	if failure != nil {
		return ""
	}
	defer file.Close()
	info, failure := file.Stat()
	if failure != nil {
		return ""
	}
	return ExtractJSON(filename, file, info.Size())
}

// ExtractBytesJSON is the in-memory variant for uploads that are already held as
// a byte slice.
func ExtractBytesJSON(filename string, data []byte) string {
	return ExtractJSON(filename, newByteReader(data), int64(len(data)))
}

func isAnycubic(filename string) bool {
	switch extension(filename) {
	case ".pwmx", ".pwmo", ".pws", ".pw0", ".pwms", ".pwmb":
		return true
	}
	return false
}

func isChitubox(filename string) bool {
	switch extension(filename) {
	case ".ctb", ".cbddlp", ".photon":
		return true
	}
	return false
}

func isSL1(filename string) bool {
	switch extension(filename) {
	case ".sl1", ".sl1s":
		return true
	}
	return false
}

func extension(filename string) string { return strings.ToLower(filepath.Ext(filename)) }

// nonEmpty normalizes an empty result to nil so every caller can test for nil.
// A resin result that carries nothing but its "kind" discriminator counts as
// empty too - the format was recognized but held no usable value, and storing
// that would give the frontend an empty card to render.
func nonEmpty(parsed map[string]any) map[string]any {
	if len(parsed) == 0 {
		return nil
	}
	if _, hasKind := parsed["kind"]; hasKind && len(parsed) == 1 {
		return nil
	}
	return parsed
}

// read returns length bytes at offset, clamped to the file. A short read is not
// an error: the parsers guard their own offsets, and a truncated file should
// yield whatever settings survived rather than nothing.
func read(source io.ReaderAt, size, offset, length int64) []byte {
	if offset < 0 || offset >= size || length <= 0 {
		return nil
	}
	if offset+length > size {
		length = size - offset
	}
	buffer := make([]byte, length)
	count, failure := source.ReadAt(buffer, offset)
	if count == 0 && failure != nil {
		return nil
	}
	return buffer[:count]
}

// headAndTail reads the two regions a G-code parser looks at and joins them, so
// gcode.Parse sees the same text it would see from the whole file.
func headAndTail(source io.ReaderAt, size int64) []byte {
	if size <= gcode.HeadBytes+gcode.TailBytes {
		return read(source, size, 0, size)
	}
	head := read(source, size, 0, gcode.HeadBytes)
	tail := read(source, size, size-gcode.TailBytes, gcode.TailBytes)
	joined := make([]byte, 0, len(head)+1+len(tail))
	joined = append(joined, head...)
	joined = append(joined, '\n')
	return append(joined, tail...)
}

// byteReader adapts a byte slice to io.ReaderAt without pulling in bytes.Reader's
// unused seek/read state.
type byteReader []byte

func newByteReader(data []byte) io.ReaderAt { return byteReader(data) }

func (b byteReader) ReadAt(buffer []byte, offset int64) (int, error) {
	if offset < 0 || offset >= int64(len(b)) {
		return 0, io.EOF
	}
	count := copy(buffer, b[offset:])
	if count < len(buffer) {
		return count, io.EOF
	}
	return count, nil
}
