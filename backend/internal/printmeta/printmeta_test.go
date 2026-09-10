package printmeta

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"meshdepot/internal/gcode"
)

// - fixture builders -----------------------------

type buffer struct{ data []byte }

func (b *buffer) grow(size int) {
	for len(b.data) < size {
		b.data = append(b.data, 0)
	}
}

func (b *buffer) u32(offset int, value uint32) {
	b.grow(offset + 4)
	binary.LittleEndian.PutUint32(b.data[offset:], value)
}

func (b *buffer) f32(offset int, value float32) {
	b.u32(offset, math.Float32bits(value))
}

func (b *buffer) text(offset int, value string) {
	b.grow(offset + len(value))
	copy(b.data[offset:], value)
}

// anycubicFile: 48-byte file mark, then a HEADER section with the layout all
// .pw* variants share.
func anycubicFile() []byte {
	const headerAddress = 0x30
	file := &buffer{}
	file.text(0x00, "ANYCUBIC")
	file.u32(0x0C, 1)             // version
	file.u32(0x10, 4)             // area count
	file.u32(0x14, headerAddress) // HEADER address
	file.u32(0x1C, 0)             // no preview
	file.u32(0x24, 0)             // no layer defs

	file.text(headerAddress, "HEADER")
	file.u32(headerAddress+12, 0x3C) // body length
	body := headerAddress + 16
	file.f32(body+0x00, 50.0)   // pixel size µm
	file.f32(body+0x04, 0.05)   // layer height
	file.f32(body+0x08, 2.5)    // exposure
	file.f32(body+0x0C, 0.5)    // light off
	file.f32(body+0x10, 30.0)   // bottom exposure
	file.f32(body+0x14, 6)      // bottom layers
	file.f32(body+0x18, 6.0)    // lift height
	file.f32(body+0x1C, 0.7)    // lift speed, mm/s as Photon Workshop writes it
	file.f32(body+0x20, 3.0)    // retract speed, mm/s
	file.f32(body+0x24, 27.35)  // volume ml
	file.u32(body+0x28, 8)      // anti aliasing
	file.u32(body+0x2C, 3840)   // resolution x
	file.u32(body+0x30, 2400)   // resolution y
	file.f32(body+0x34, 30.086) // weight g
	file.f32(body+0x38, 1.75)   // price
	return file.data
}

// chituboxFile: fixed header, parameter block and, for CTB, a slicer block
// pointing at a machine name.
func chituboxFile(magic uint32, version uint32) []byte {
	const (
		parameterOffset = 0x100
		slicerOffset    = 0x200
		machineOffset   = 0x300
	)
	file := &buffer{}
	file.u32(0x00, magic)
	file.u32(0x04, version)
	file.f32(0x08, 218.88) // bed X
	file.f32(0x0C, 122.88) // bed Y
	file.f32(0x20, 0.05)   // layer height
	file.f32(0x24, 2.4)    // exposure
	file.f32(0x28, 32.0)   // bottom exposure
	file.f32(0x2C, 0.2)    // light off
	file.u32(0x30, 5)      // bottom layers
	file.u32(0x34, 3840)   // resolution x
	file.u32(0x38, 2400)   // resolution y
	file.u32(0x44, 1234)   // layer count
	file.u32(0x4C, 7325)   // print time seconds → 2h 2m
	file.u32(0x54, parameterOffset)
	file.u32(0x58, 0x3C) // parameter size
	file.u32(0x5C, 4)    // anti aliasing

	file.f32(parameterOffset+0x00, 5.0)   // bottom lift height
	file.f32(parameterOffset+0x04, 60.0)  // bottom lift speed
	file.f32(parameterOffset+0x08, 6.0)   // lift height
	file.f32(parameterOffset+0x0C, 80.0)  // lift speed
	file.f32(parameterOffset+0x10, 150.0) // retract speed
	file.f32(parameterOffset+0x14, 41.5)  // volume ml
	file.f32(parameterOffset+0x18, 45.65) // weight g
	file.f32(parameterOffset+0x1C, 2.28)  // cost
	file.u32(parameterOffset+0x28, 5)     // bottom layer count

	if magic != magicCBDDLP {
		file.u32(0x68, slicerOffset)
		file.u32(0x6C, 0x4C)
		file.u32(slicerOffset+0x1C, machineOffset)
		file.u32(slicerOffset+0x20, 16)
		file.text(machineOffset, "Photon Mono X\x00\x00\x00")
	}
	file.grow(0x400)
	return file.data
}

func sl1File(t *testing.T) []byte {
	t.Helper()
	var out bytes.Buffer
	archive := zip.NewWriter(&out)
	write := func(name, content string) {
		writer, failure := archive.Create(name)
		if failure != nil {
			t.Fatalf("zip create %s: %v", name, failure)
		}
		if _, failure := writer.Write([]byte(content)); failure != nil {
			t.Fatalf("zip write %s: %v", name, failure)
		}
	}
	write("config.ini", strings.Join([]string{
		"action = print",
		"expTime = 2.2",
		"expTimeFirst = 25",
		"layerHeight = 0.05",
		"materialName = Prusa Orange Tough 0.05",
		"numFade = 8",
		"numFast = 1200",
		"numSlow = 40",
		"printProfile = 0.05 Normal",
		"printTime = 8130",
		"printerModel = SL1",
		"prusaSlicerVersion = 2.7.1",
		"usedMaterial = 33.44",
	}, "\n"))
	write("prusaslicer.ini", strings.Join([]string{
		"display_height = 68.04",
		"display_width = 120.96",
		"display_pixels_x = 1440",
		"display_pixels_y = 2560",
		"expTime = 99",                         // must lose against config.ini
		"printer_model = should not be picked", // wrong key spelling
	}, "\n"))
	if failure := archive.Close(); failure != nil {
		t.Fatalf("zip close: %v", failure)
	}
	return out.Bytes()
}

// - assertions --------------------------------

func extract(t *testing.T, filename string, data []byte) map[string]any {
	t.Helper()
	return Extract(filename, newByteReader(data), int64(len(data)))
}

func wantNumber(t *testing.T, result map[string]any, key string, expected float64) {
	t.Helper()
	value, present := result[key]
	if !present {
		t.Errorf("%s missing, got keys %v", key, keysOf(result))
		return
	}
	var actual float64
	switch typed := value.(type) {
	case float64:
		actual = typed
	case uint64:
		actual = float64(typed)
	default:
		t.Errorf("%s: unexpected type %T", key, value)
		return
	}
	if math.Abs(actual-expected) > 0.001 {
		t.Errorf("%s = %v, want %v", key, actual, expected)
	}
}

func wantString(t *testing.T, result map[string]any, key, expected string) {
	t.Helper()
	if actual, _ := result[key].(string); actual != expected {
		t.Errorf("%s = %q, want %q", key, actual, expected)
	}
}

func wantAbsent(t *testing.T, result map[string]any, key string) {
	t.Helper()
	if _, present := result[key]; present {
		t.Errorf("%s should be absent, got %v", key, result[key])
	}
}

func keysOf(result map[string]any) []string {
	keys := make([]string, 0, len(result))
	for key := range result {
		keys = append(keys, key)
	}
	return keys
}

// - tests ----------------------------------

func TestExtractAnycubic(t *testing.T) {
	// The whole .pw* family shares the HEADER layout, so one fixture covers all
	// of the extensions we accept.
	for _, name := range []string{"model.pwmx", "model.pwmo", "model.pws", "model.PWMX"} {
		result := extract(t, name, anycubicFile())
		if result == nil {
			t.Fatalf("%s: no settings extracted", name)
		}
		wantString(t, result, "kind", "resin")
		wantNumber(t, result, "layer_height", 0.05)
		wantNumber(t, result, "exposure_time", 2.5)
		wantNumber(t, result, "light_off_time", 0.5)
		wantNumber(t, result, "bottom_exposure_time", 30)
		wantNumber(t, result, "bottom_layers", 6)
		wantNumber(t, result, "lift_height", 6)
		wantNumber(t, result, "lift_speed", 42) // 0.7 mm/s → mm/min
		wantNumber(t, result, "retract_speed", 180)
		wantNumber(t, result, "resin_volume", 27.35)
		wantNumber(t, result, "resin_weight", 30.086)
		wantNumber(t, result, "resin_cost", 1.75)
		wantNumber(t, result, "pixel_size", 50)
		wantNumber(t, result, "resolution_x", 3840)
		wantNumber(t, result, "resolution_y", 2400)
		wantNumber(t, result, "anti_aliasing", 8)
	}
}

func TestExtractAnycubicDropsDerivedWeightAndPrice(t *testing.T) {
	// Without a resin profile Photon Workshop multiplies the volume by density 1
	// and price 1, so weight and price are literally the volume again (seen in
	// testdata/sample.pwmx). Those copies must not reach the UI.
	file := &buffer{data: anycubicFile()}
	const body = 0x30 + 16
	file.f32(body+0x24, 153.9666) // volume
	file.f32(body+0x34, 153.9666) // weight = volume × 1.0 g/ml
	file.f32(body+0x38, 153.9666) // price  = volume × 1.0 per ml
	result := extract(t, "model.pwmx", file.data)
	wantNumber(t, result, "resin_volume", 153.967)
	wantAbsent(t, result, "resin_weight")
	wantAbsent(t, result, "resin_cost")
}

func TestExtractAnycubicReadsOnlyThePrefix(t *testing.T) {
	// The point of the header-only path: a file far larger than the prefix must
	// still parse, without the layer stack ever being read.
	data := append(anycubicFile(), make([]byte, 4*anycubicPrefix)...)
	reader := &countingReader{inner: newByteReader(data)}
	result := Extract("big.pwmx", reader, int64(len(data)))
	if result == nil {
		t.Fatal("no settings extracted")
	}
	wantNumber(t, result, "layer_height", 0.05)
	if reader.bytesRead > anycubicPrefix {
		t.Errorf("read %d bytes, expected at most the %d-byte prefix", reader.bytesRead, anycubicPrefix)
	}
}

func TestExtractChitubox(t *testing.T) {
	cases := []struct {
		name    string
		magic   uint32
		version uint32
		machine string
	}{
		{"model.ctb", magicCTB, 3, "Photon Mono X"},
		{"model.ctb", magicCTBv4, 4, "Photon Mono X"},
		{"gk.ctb", magicGKtwo, 4, "Photon Mono X"},
		{"model.cbddlp", magicCBDDLP, 2, ""},
		{"model.photon", magicCBDDLP, 1, ""},
	}
	for _, testCase := range cases {
		result := extract(t, testCase.name, chituboxFile(testCase.magic, testCase.version))
		if result == nil {
			t.Fatalf("%s (magic %#x): no settings extracted", testCase.name, testCase.magic)
		}
		wantString(t, result, "kind", "resin")
		wantNumber(t, result, "layer_height", 0.05)
		wantNumber(t, result, "exposure_time", 2.4)
		wantNumber(t, result, "bottom_exposure_time", 32)
		wantNumber(t, result, "light_off_time", 0.2)
		wantNumber(t, result, "bottom_layers", 5)
		wantNumber(t, result, "resolution_x", 3840)
		wantNumber(t, result, "resolution_y", 2400)
		wantNumber(t, result, "layer_count", 1234)
		wantNumber(t, result, "anti_aliasing", 4)
		wantNumber(t, result, "display_width", 218.88)
		wantNumber(t, result, "display_height", 122.88)
		wantNumber(t, result, "resin_volume", 41.5)
		wantNumber(t, result, "resin_weight", 45.65)
		wantNumber(t, result, "resin_cost", 2.28)
		wantNumber(t, result, "lift_height", 6)
		wantNumber(t, result, "lift_speed", 80)
		wantNumber(t, result, "retract_speed", 150)
		wantString(t, result, "print_time", "2h 2m")
		// .cbddlp/.photon reuse the slicer pointer offsets as padding, so no
		// machine name must be invented for them.
		if testCase.machine == "" {
			wantAbsent(t, result, "printer")
		} else {
			wantString(t, result, "printer", testCase.machine)
		}
	}
}

func TestExtractChituboxRejectsForeignMagic(t *testing.T) {
	data := chituboxFile(magicCTB, 3)
	binary.LittleEndian.PutUint32(data[0:], 0xDEADBEEF)
	if result := extract(t, "model.ctb", data); result != nil {
		t.Errorf("expected nil for a foreign magic, got %v", result)
	}
}

func TestExtractSL1(t *testing.T) {
	result := extract(t, "model.sl1", sl1File(t))
	if result == nil {
		t.Fatal("no settings extracted")
	}
	wantString(t, result, "kind", "resin")
	wantNumber(t, result, "layer_height", 0.05)
	wantNumber(t, result, "exposure_time", 2.2) // config.ini wins over prusaslicer.ini
	wantNumber(t, result, "bottom_exposure_time", 25)
	wantNumber(t, result, "bottom_layers", 8)
	wantNumber(t, result, "resin_volume", 33.44)
	wantNumber(t, result, "layer_count", 1240) // numSlow + numFast
	wantNumber(t, result, "display_width", 120.96)
	wantNumber(t, result, "display_height", 68.04)
	wantNumber(t, result, "resolution_x", 1440)
	wantNumber(t, result, "resolution_y", 2560)
	wantString(t, result, "print_time", "2h 15m")
	wantString(t, result, "material", "Prusa Orange Tough 0.05")
	wantString(t, result, "printer", "SL1")
	wantString(t, result, "slicer", "PrusaSlicer 2.7.1")
}

func TestExtractGcodeStillWorks(t *testing.T) {
	// G-code must keep going through the same entry point, including the case
	// that matters: settings in the tail of a file larger than the head window.
	var builder strings.Builder
	builder.WriteString("; generated by PrusaSlicer 2.7.1\n")
	for builder.Len() < gcode.HeadBytes+gcode.TailBytes {
		builder.WriteString("G1 X10 Y10 E1.5 F1800\n")
	}
	builder.WriteString("; layer_height = 0.2\n; nozzle_diameter = 0.4\n")

	result := extract(t, "part.gcode", []byte(builder.String()))
	if result == nil {
		t.Fatal("no settings extracted")
	}
	wantNumber(t, result, "layer_height", 0.2)
	wantNumber(t, result, "nozzle_diameter", 0.4)
	wantString(t, result, "slicer", "PrusaSlicer 2.7.1")
	// G-code carries no discriminator: rows written before resin support have
	// none either, and the frontend treats "no kind" as FDM.
	wantAbsent(t, result, "kind")
}

func TestExtractIgnoresOtherFormats(t *testing.T) {
	for _, name := range []string{"model.stl", "readme.txt", "model.3mf", "noextension"} {
		if result := extract(t, name, []byte("solid whatever\n")); result != nil {
			t.Errorf("%s: expected nil, got %v", name, result)
		}
	}
}

func TestExtractSurvivesGarbage(t *testing.T) {
	// A file with the right extension but the wrong content must not panic and
	// must not produce a lone "kind" entry.
	for _, name := range []string{"x.pwmx", "x.ctb", "x.sl1", "x.photon"} {
		if result := extract(t, name, bytes.Repeat([]byte{0x41}, 512)); result != nil {
			t.Errorf("%s: expected nil, got %v", name, result)
		}
	}
	if result := Extract("x.pwmx", newByteReader(nil), 0); result != nil {
		t.Errorf("empty file: expected nil, got %v", result)
	}
}

func TestExtractFileJSON(t *testing.T) {
	// Blobs are stored under their hash without an extension, so the format can
	// only be recognized from the separate filename.
	directory := t.TempDir()
	blobPath := filepath.Join(directory, "e3b0c44298fc1c149afbf4c8996fb924")
	if failure := os.WriteFile(blobPath, chituboxFile(magicCTB, 3), 0o600); failure != nil {
		t.Fatalf("write blob: %v", failure)
	}

	encoded := ExtractFileJSON("model.ctb", blobPath)
	if !strings.Contains(encoded, `"kind":"resin"`) || !strings.Contains(encoded, `"resin_volume":41.5`) {
		t.Errorf("unexpected JSON: %s", encoded)
	}
	if ExtractFileJSON("model.stl", blobPath) != "" {
		t.Error("an unsupported name must yield no JSON")
	}
	if ExtractFileJSON("model.ctb", filepath.Join(directory, "missing")) != "" {
		t.Error("a missing file must yield no JSON")
	}
}

func TestSupported(t *testing.T) {
	supported := []string{"a.gcode", "a.gco", "a.g", "a.pwmx", "a.pwmo", "a.pws", "a.ctb", "a.cbddlp", "a.photon", "a.sl1", "a.SL1S"}
	for _, name := range supported {
		if !Supported(name) {
			t.Errorf("%s should be supported", name)
		}
	}
	for _, name := range []string{"a.stl", "a.3mf", "a.obj", "a.zip", "a"} {
		if Supported(name) {
			t.Errorf("%s should not be supported", name)
		}
	}
}

// countingReader records how much of a file a parser actually touched.
type countingReader struct {
	inner interface {
		ReadAt([]byte, int64) (int, error)
	}
	bytesRead int
}

func (c *countingReader) ReadAt(buffer []byte, offset int64) (int, error) {
	count, failure := c.inner.ReadAt(buffer, offset)
	c.bytesRead += count
	return count, failure
}

// TestExtractRealAnycubicFile runs the extractor against a genuine Photon Mono X
// file (~70 MB, not checked in - skipped when absent). Synthetic fixtures only
// prove the parser agrees with itself; this one pins it to what a slicer writes.
func TestExtractRealAnycubicFile(t *testing.T) {
	const samplePath = "../../testdata/sample.pwmx"
	if _, failure := os.Stat(samplePath); failure != nil {
		t.Skipf("sample file missing (%v) – test skipped", failure)
	}
	handle, failure := os.Open(samplePath)
	if failure != nil {
		t.Fatalf("open: %v", failure)
	}
	defer handle.Close()
	info, failure := handle.Stat()
	if failure != nil {
		t.Fatalf("stat: %v", failure)
	}
	result := Extract("sample.pwmx", handle, info.Size())
	if result == nil {
		t.Fatal("no settings extracted from the real file")
	}
	t.Logf("%v", result)
	wantNumber(t, result, "layer_height", 0.05)
	wantNumber(t, result, "resolution_x", 3840)
	wantNumber(t, result, "lift_speed", 42) // 0.7 mm/s in the file
	// This file carries the volume in all three money/mass fields.
	wantAbsent(t, result, "resin_weight")
	wantAbsent(t, result, "resin_cost")
}
