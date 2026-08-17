// Package gcode extracts print parameters from the G-code files of the common
// slicers (PrusaSlicer, OrcaSlicer, SuperSlicer, Bambu Studio, Cura). Slicers
// store their settings as a comment block at the start OR the end of the file,
// so only the head and tail are scanned instead of the whole (often very large)
// file.
package gcode

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// HeadBytes and TailBytes report how much of a G-code file Parse actually looks
// at. They are exported so a caller can read just those two slices off disk and
// hand the concatenation to Parse instead of loading a multi-hundred-MB file.
const (
	HeadBytes = 256 * 1024 // head: Cura header, Prusa header
	TailBytes = 64 * 1024  // tail: Prusa/Orca/Bambu config block
)

// IsGcode recognizes G-code files by their extension.
func IsGcode(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".gcode", ".gco", ".g":
		return true
	}
	return false
}

// Parse returns the detected, normalized print parameters. Missing fields are
// omitted; if nothing is recognizable the map is empty.
func Parse(data []byte) map[string]any {
	text := headAndTail(data)
	lines := strings.Split(text, "\n")

	keyValues := map[string]string{}               // "; key = value"  (Prusa/Orca/Bambu/Super)
	colonPairs := map[string]string{}              // ";Key: value"    (Cura)
	var nozzleTempFallback, bedTempFallback string // fallback from M104/M109 resp. M140/M190

	for _, rawLine := range lines {
		line := strings.TrimRight(rawLine, "\r")
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, ";") {
			body := strings.TrimSpace(strings.TrimLeft(trimmed, ";"))
			if index := strings.Index(body, "="); index > 0 {
				key := strings.ToLower(strings.TrimSpace(body[:index]))
				value := strings.TrimSpace(body[index+1:])
				if key != "" && value != "" {
					keyValues[key] = value
				}
			} else if index := strings.Index(body, ":"); index > 0 {
				key := strings.ToLower(strings.TrimSpace(body[:index]))
				value := strings.TrimSpace(body[index+1:])
				if key != "" && value != "" {
					colonPairs[key] = value
				}
			}
			continue
		}
		// G-code temperature commands as a fallback (Cura has no temp comments).
		if nozzleTempFallback == "" {
			if value := tempArg(trimmed, "M104", "M109"); value != "" {
				nozzleTempFallback = value
			}
		}
		if bedTempFallback == "" {
			if value := tempArg(trimmed, "M140", "M190"); value != "" {
				bedTempFallback = value
			}
		}
	}

	result := map[string]any{}

	// ── Layer height ──
	if value, ok := firstFloat(pick(keyValues, "layer_height"), colonPairs["layer height"]); ok {
		result["layer_height"] = value
	}
	if value, ok := firstFloat(pick(keyValues, "first_layer_height", "initial_layer_print_height")); ok {
		result["first_layer_height"] = value
	}

	// ── Temperatures ── (Orca: nozzle_temperature, Prusa: temperature)
	if value, ok := firstFloat(pick(keyValues, "nozzle_temperature", "temperature", "first_layer_temperature"), nozzleTempFallback); ok {
		result["nozzle_temp"] = value
	}
	if value, ok := firstFloat(pick(keyValues, "bed_temperature", "first_layer_bed_temperature", "hot_plate_temp"), bedTempFallback); ok {
		result["bed_temp"] = value
	}

	// ── Infill ── (Prusa: fill_density, Orca: sparse_infill_density) - percent
	if value, ok := firstFloat(pick(keyValues, "fill_density", "sparse_infill_density")); ok {
		result["infill"] = value
	}

	// ── Nozzle diameter ──
	if value, ok := firstFloat(pick(keyValues, "nozzle_diameter")); ok {
		result["nozzle_diameter"] = value
	}

	// ── Filament type ──
	if value := pick(keyValues, "filament_type"); value != "" {
		result["filament_type"] = firstToken(value)
	}

	// ── Filament usage ──
	if value, ok := firstFloat(pick(keyValues, "total filament used [g]", "filament used [g]", "filament_weight_total")); ok {
		result["filament_used_g"] = value
	}
	if value, ok := firstFloat(pick(keyValues, "total filament used [mm]", "filament used [mm]")); ok {
		result["filament_used_m"] = round2(value / 1000.0) // mm → m
	} else if value := colonPairs["filament used"]; value != "" { // Cura: "1.23m"
		if meters, ok := firstFloat(value); ok {
			result["filament_used_m"] = round2(meters)
		}
	}

	// ── Print time ──
	if value := pick(keyValues, "estimated printing time (normal mode)", "estimated printing time", "total estimated time"); value != "" {
		result["print_time"] = value
	} else if value := colonPairs["time"]; value != "" { // Cura: seconds
		if seconds, failure := strconv.Atoi(strings.Fields(value)[0]); failure == nil {
			result["print_time"] = FormatDuration(seconds)
		}
	}

	// ── Slicer ──
	if value := detectSlicer(lines); value != "" {
		result["slicer"] = value
	}

	return result
}

// headAndTail returns the head and tail region of the file as a single string.
func headAndTail(data []byte) string {
	if len(data) <= HeadBytes+TailBytes {
		return string(data)
	}
	return string(data[:HeadBytes]) + "\n" + string(data[len(data)-TailBytes:])
}

// pick returns the first non-empty value for the named keys.
func pick(values map[string]string, keys ...string) string {
	for _, key := range keys {
		if value, ok := values[key]; ok && strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

var floatPattern = regexp.MustCompile(`-?\d+(?:\.\d+)?`)

// firstFloat extracts the first number from the first non-empty candidate.
// (Prusa sometimes lists values comma-separated: "210,210,215".)
func firstFloat(candidates ...string) (float64, bool) {
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		if match := floatPattern.FindString(candidate); match != "" {
			if value, failure := strconv.ParseFloat(match, 64); failure == nil {
				return value, true
			}
		}
	}
	return 0, false
}

// firstToken returns the first token before whitespace/semicolon/comma.
func firstToken(value string) string {
	value = strings.TrimSpace(value)
	for _, separator := range []string{";", ",", " "} {
		if index := strings.Index(value, separator); index > 0 {
			value = value[:index]
		}
	}
	return value
}

var tempPattern = regexp.MustCompile(`S(\d+(?:\.\d+)?)`)

// tempArg returns the S argument if the line starts with one of the commands.
func tempArg(line string, commands ...string) string {
	upper := strings.ToUpper(line)
	for _, command := range commands {
		if strings.HasPrefix(upper, command+" ") || strings.HasPrefix(upper, command+"\t") {
			if match := tempPattern.FindStringSubmatch(upper); match != nil && match[1] != "0" {
				return match[1]
			}
		}
	}
	return ""
}

// detectSlicer recognizes the slicer name from header lines like
// "; generated by PrusaSlicer 2.7" or ";Generated with Cura_SteamEngine 5.6".
func detectSlicer(lines []string) string {
	for _, rawLine := range lines {
		content := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(rawLine), ";"))
		lower := strings.ToLower(content)
		for _, prefix := range []string{"generated by ", "generated with "} {
			if strings.HasPrefix(lower, prefix) {
				return strings.TrimSpace(content[len(prefix):])
			}
		}
	}
	return ""
}

func round2(value float64) float64 { return float64(int(value*100+0.5)) / 100 }

// FormatDuration renders a second count as "1h 5m" or "5m 30s".
func FormatDuration(seconds int) string {
	hours := seconds / 3600
	minutes := (seconds % 3600) / 60
	if hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, minutes)
	}
	return fmt.Sprintf("%dm %ds", minutes, seconds%60)
}
