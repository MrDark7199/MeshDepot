// Package printmeta extracts the print settings of sliced files - FDM G-code as
// well as the resin formats - for manual upload, ZIP extraction and sync alike.
//
// Sliced files are large and consist almost entirely of data nobody wants here,
// so Extract takes an io.ReaderAt plus the size rather than a byte slice: each
// format reads only the blocks it needs, whether the file is on disk or in
// memory.
//
// The result is a flat map stored as JSON in design_file_entries.gcode_meta.
// Every resin format sets "kind": "resin" so the frontend can pick its field
// list; G-code carries no kind.
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
// HEADER section. The margin costs one read and covers files that push the
// section back behind a preview.
const anycubicPrefix = 256 * 1024

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

// Extract returns nil for unknown formats, damaged files and files with nothing
// recognizable in them - an entry simply has no settings then.
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

// ExtractJSON is Extract encoded for the gcode_meta column.
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

// ExtractFileJSON reads straight off a stored file, for the sync and the blob
// store where loading it whole would be wasteful. filename and path are separate
// because blobs are stored under their hash, without an extension.
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

// ExtractBytesJSON is the in-memory variant for uploads already held as bytes.
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

// nonEmpty normalizes an empty result to nil, so every caller can test for nil.
// A resin result carrying nothing but its "kind" counts as empty: the format was
// recognized but held no value, and the frontend would render an empty card.
func nonEmpty(parsed map[string]any) map[string]any {
	if len(parsed) == 0 {
		return nil
	}
	if _, hasKind := parsed["kind"]; hasKind && len(parsed) == 1 {
		return nil
	}
	return parsed
}

// read clamps to the file. A short read is not an error: the parsers guard their
// own offsets, and a truncated file should yield whatever survived.
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

// headAndTail joins the two regions a G-code parser looks at, so gcode.Parse
// sees the same text it would from the whole file.
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

// byteReader adapts a byte slice to io.ReaderAt without bytes.Reader's unused
// seek state.
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
