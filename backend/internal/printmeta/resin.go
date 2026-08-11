package printmeta

import (
	"archive/zip"
	"encoding/binary"
	"io"
	"math"
	"path"
	"strconv"
	"strings"

	"meshdepot/internal/gcode"
	"meshdepot/internal/pwmx"
)

// ── Anycubic Photon Workshop (.pwmx, .pwmo, .pws, …) ─────────────────────────

// parseAnycubic reads the HEADER section shared by the whole Photon Workshop
// family. Only the layer image encoding differs between the extensions, so one
// reader covers all of them.
func parseAnycubic(prefix []byte) map[string]any {
	header, failure := pwmx.ParseHeader(prefix)
	if failure != nil {
		return nil
	}
	result := resinResult()
	putFloat(result, "layer_height", float64(header.LayerHeight))
	putFloat(result, "exposure_time", float64(header.ExposureTime))
	putFloat(result, "light_off_time", float64(header.LightOffTime))
	putFloat(result, "bottom_exposure_time", float64(header.BottomExposure))
	putFloat(result, "bottom_layers", float64(header.BottomLayers))
	putFloat(result, "lift_height", float64(header.LiftHeight))
	// Photon Workshop stores speeds in mm/s, Chitubox in mm/min. Normalize to
	// mm/min so one label fits both formats.
	putFloat(result, "lift_speed", float64(header.LiftSpeed)*60)
	putFloat(result, "retract_speed", float64(header.RetractSpeed)*60)
	// Weight and price are not measured, Photon Workshop derives them from the
	// volume using the resin profile's density and price per ml. Without a profile
	// both factors are 1, so all three fields carry the very same number - grams
	// that are really millilitres and a price of one unit per ml. Keep the volume
	// and drop the copies; an empty tile beats an invented weight.
	volume := float64(header.VolumeMl)
	putFloat(result, "resin_volume", volume)
	if float64(header.WeightG) != volume {
		putFloat(result, "resin_weight", float64(header.WeightG))
	}
	if float64(header.Price) != volume {
		putFloat(result, "resin_cost", float64(header.Price))
	}
	putFloat(result, "pixel_size", float64(header.PixelSizeUm))
	putUint(result, "resolution_x", uint64(header.ResolutionX))
	putUint(result, "resolution_y", uint64(header.ResolutionY))
	putUint(result, "anti_aliasing", uint64(header.AntiAliasing))
	return result
}

// ── Chitubox / Photon (.ctb, .cbddlp, .photon) ───────────────────────────────

// Magic numbers of the Chitubox file family. The fixed header is identical for
// every version of every one of them (only the blocks behind it differ), so one
// reader serves .cbddlp, .photon and .ctb v2 through v5.
const (
	magicCBDDLP = 0x12FD0019 // .cbddlp and .photon
	magicCTB    = 0x12FD0086 // .ctb v2/v3
	magicCTBv4  = 0x12FD0106 // .ctb v4/v5
	magicGKtwo  = 0xFF220810 // .ctb variant for UniFormation GKtwo
)

// chituboxHeaderBytes is the length of the fixed header up to and including the
// slicer-block pointer at 0x6c.
const chituboxHeaderBytes = 0x70

func parseChitubox(source io.ReaderAt, size int64) map[string]any {
	header := read(source, size, 0, chituboxHeaderBytes)
	if len(header) < 0x64 {
		return nil
	}
	magic := le32(header, 0x00)
	switch magic {
	case magicCBDDLP, magicCTB, magicCTBv4, magicGKtwo:
	default:
		return nil
	}

	result := resinResult()
	putFloat(result, "display_width", float64(f32(header, 0x08)))
	putFloat(result, "display_height", float64(f32(header, 0x0C)))
	putFloat(result, "layer_height", float64(f32(header, 0x20)))
	putFloat(result, "exposure_time", float64(f32(header, 0x24)))
	putFloat(result, "bottom_exposure_time", float64(f32(header, 0x28)))
	putFloat(result, "light_off_time", float64(f32(header, 0x2C)))
	putUint(result, "bottom_layers", uint64(le32(header, 0x30)))
	putUint(result, "resolution_x", uint64(le32(header, 0x34)))
	putUint(result, "resolution_y", uint64(le32(header, 0x38)))
	putUint(result, "layer_count", uint64(le32(header, 0x44)))
	putUint(result, "anti_aliasing", uint64(le32(header, 0x5C)))
	if seconds := le32(header, 0x4C); seconds > 0 {
		result["print_time"] = gcode.FormatDuration(int(seconds))
	}

	// The parameter block holds resin volume, weight and cost. It only exists
	// from version 2 on; version 1 .photon files stop after the fixed header.
	parameterOffset := int64(le32(header, 0x54))
	parameterSize := int64(le32(header, 0x58))
	if parameterOffset > 0 && parameterSize >= 0x2C {
		if block := read(source, size, parameterOffset, parameterSize); len(block) >= 0x2C {
			putFloat(result, "bottom_lift_height", float64(f32(block, 0x00)))
			putFloat(result, "bottom_lift_speed", float64(f32(block, 0x04)))
			putFloat(result, "lift_height", float64(f32(block, 0x08)))
			putFloat(result, "lift_speed", float64(f32(block, 0x0C)))
			putFloat(result, "retract_speed", float64(f32(block, 0x10)))
			putFloat(result, "resin_volume", float64(f32(block, 0x14)))
			putFloat(result, "resin_weight", float64(f32(block, 0x18)))
			putFloat(result, "resin_cost", float64(f32(block, 0x1C)))
		}
	}

	// .cbddlp/.photon reuse 0x64 upwards as padding, so the machine name is only
	// looked up for the CTB variants that actually carry a slicer block.
	if magic != magicCBDDLP && len(header) >= chituboxHeaderBytes {
		if name := chituboxMachine(source, size, int64(le32(header, 0x68)), int64(le32(header, 0x6C))); name != "" {
			result["printer"] = name
		}
	}
	return result
}

// chituboxMachine resolves the machine name, which the slicer block stores as an
// offset/length pair pointing elsewhere in the file rather than inline.
func chituboxMachine(source io.ReaderAt, size, offset, length int64) string {
	if offset <= 0 || length < 0x24 {
		return ""
	}
	block := read(source, size, offset, length)
	if len(block) < 0x24 {
		return ""
	}
	nameOffset := int64(le32(block, 0x1C))
	nameLength := int64(le32(block, 0x20))
	if nameOffset <= 0 || nameLength <= 0 || nameLength > 256 {
		return ""
	}
	return cleanString(read(source, size, nameOffset, nameLength))
}

// ── PrusaSlicer SL1 (.sl1, .sl1s) ────────────────────────────────────────────

// parseSL1 reads the two ini files inside the SL1 archive. A ZIP directory sits
// at the end of the file, which is why this needs the io.ReaderAt rather than a
// prefix - archive/zip then pulls only the entries asked for.
func parseSL1(source io.ReaderAt, size int64) map[string]any {
	archive, failure := zip.NewReader(source, size)
	if failure != nil {
		return nil
	}
	// config.ini wins over prusaslicer.ini: the former holds the values the
	// printer is actually driven with, the latter the full slicer profile.
	settings := map[string]string{}
	for _, name := range []string{"prusaslicer.ini", "config.ini"} {
		for key, value := range readZipINI(archive, name) {
			settings[key] = value
		}
	}
	if len(settings) == 0 {
		return nil
	}

	result := resinResult()
	putININumber(result, settings, "layer_height", "layerHeight")
	putININumber(result, settings, "exposure_time", "expTime")
	putININumber(result, settings, "bottom_exposure_time", "expTimeFirst")
	// SL1 has no bottom layer count; numFade is how many layers fade from the
	// first exposure down to the normal one, which is the same idea.
	putININumber(result, settings, "bottom_layers", "numFade")
	putININumber(result, settings, "resin_volume", "usedMaterial")
	putININumber(result, settings, "display_width", "display_width")
	putININumber(result, settings, "display_height", "display_height")
	putININumber(result, settings, "resolution_x", "display_pixels_x")
	putININumber(result, settings, "resolution_y", "display_pixels_y")
	// SL1 reports no total layer count, but splits it into the layers printed
	// with slow and with fast tilt.
	slow, hasSlow := iniNumber(settings, "numSlow")
	fast, hasFast := iniNumber(settings, "numFast")
	if hasSlow || hasFast {
		putFloat(result, "layer_count", slow+fast)
	}
	if seconds, ok := iniNumber(settings, "printTime"); ok && seconds > 0 {
		result["print_time"] = gcode.FormatDuration(int(seconds))
	}
	putINIString(result, settings, "material", "materialName")
	putINIString(result, settings, "printer", "printerModel")
	if version := settings["prusaSlicerVersion"]; version != "" {
		result["slicer"] = "PrusaSlicer " + version
	}
	return result
}

// readZipINI returns the "key = value" pairs of one archive member (empty if it
// is missing or unreadable).
func readZipINI(archive *zip.Reader, name string) map[string]string {
	result := map[string]string{}
	for _, entry := range archive.File {
		if !strings.EqualFold(path.Base(entry.Name), name) {
			continue
		}
		handle, failure := entry.Open()
		if failure != nil {
			return result
		}
		// Config files are a few KB; the cap only guards against a crafted entry
		// claiming to be one.
		data, _ := io.ReadAll(io.LimitReader(handle, 1<<20))
		handle.Close()
		for _, line := range strings.Split(string(data), "\n") {
			index := strings.Index(line, "=")
			if index <= 0 {
				continue
			}
			key := strings.TrimSpace(line[:index])
			value := strings.TrimSpace(strings.TrimRight(line[index+1:], "\r"))
			if key != "" && value != "" {
				result[key] = value
			}
		}
		return result
	}
	return result
}

// ── shared helpers ───────────────────────────────────────────────────────────

// resinResult seeds the map with the discriminator the frontend switches its
// field list on.
func resinResult() map[string]any { return map[string]any{"kind": "resin"} }

// putFloat stores a rounded value, dropping zero/NaN: resin slicers leave unused
// fields at zero, and an empty tile beats a wrong "0 mm".
func putFloat(result map[string]any, key string, value float64) {
	if math.IsNaN(value) || math.IsInf(value, 0) || value <= 0 {
		return
	}
	result[key] = math.Round(value*1000) / 1000
}

func putUint(result map[string]any, key string, value uint64) {
	if value == 0 {
		return
	}
	result[key] = value
}

func putININumber(result map[string]any, settings map[string]string, key string, iniKeys ...string) {
	if value, ok := iniNumber(settings, iniKeys...); ok {
		putFloat(result, key, value)
	}
}

func putINIString(result map[string]any, settings map[string]string, key, iniKey string) {
	if value := strings.TrimSpace(settings[iniKey]); value != "" {
		result[key] = value
	}
}

func iniNumber(settings map[string]string, keys ...string) (float64, bool) {
	for _, key := range keys {
		raw := strings.TrimSpace(settings[key])
		if raw == "" {
			continue
		}
		if value, failure := strconv.ParseFloat(raw, 64); failure == nil {
			return value, true
		}
	}
	return 0, false
}

// cleanString trims the trailing NULs of a fixed-width name field and drops
// anything unprintable.
func cleanString(data []byte) string {
	text := strings.TrimRight(string(data), "\x00")
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, text))
}

func le32(data []byte, offset int) uint32 {
	if offset < 0 || offset+4 > len(data) {
		return 0
	}
	return binary.LittleEndian.Uint32(data[offset : offset+4])
}

func f32(data []byte, offset int) float32 {
	return math.Float32frombits(le32(data, offset))
}
