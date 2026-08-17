package pwmx

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"testing"
)

// samplePath: real Photon-Mono-X file (not checked in, ~70MB). If it is missing,
// the tests are skipped.
const samplePath = "../../testdata/sample.pwmx"

func loadSample(t *testing.T) *File {
	t.Helper()
	data, failure := os.ReadFile(samplePath)
	if failure != nil {
		t.Skipf("sample file missing (%v) – test skipped", failure)
	}
	file, failure := Parse(data)
	if failure != nil {
		t.Fatalf("Parse: %v", failure)
	}
	return file
}

func TestParseHeader(t *testing.T) {
	file := loadSample(t)
	t.Logf("version=%d resX=%d resY=%d layerHeight=%.3f pixelUm=%.1f exposure=%.2f bottomExp=%.1f bottomLayers=%.0f liftH=%.1f volumeMl=%.1f weightG=%.1f layers=%d antiAlias=%d",
		file.Version, file.Header.ResolutionX, file.Header.ResolutionY, file.Header.LayerHeight, file.Header.PixelSizeUm,
		file.Header.ExposureTime, file.Header.BottomExposure, file.Header.BottomLayers, file.Header.LiftHeight,
		file.Header.VolumeMl, file.Header.WeightG, len(file.Layers), file.Header.AntiAliasing)

	if file.Header.ResolutionX != 3840 || file.Header.ResolutionY != 2400 {
		t.Errorf("resolution = %dx%d, expected 3840x2400", file.Header.ResolutionX, file.Header.ResolutionY)
	}
	if file.Header.LayerHeight < 0.049 || file.Header.LayerHeight > 0.051 {
		t.Errorf("layerHeight = %.4f, expected ~0.05", file.Header.LayerHeight)
	}
	if len(file.Layers) != 1389 {
		t.Errorf("layerCount = %d, expected 1389", len(file.Layers))
	}
}

func TestPreviewPNG(t *testing.T) {
	file := loadSample(t)
	preview := file.Preview
	t.Logf("preview = %dx%d, %d bytes", preview.Width, preview.Height, len(preview.Data))
	if preview.Width == 0 || preview.Height == 0 {
		t.Fatalf("no preview dimensions")
	}
	img := image.NewRGBA(image.Rect(0, 0, int(preview.Width), int(preview.Height)))
	for index := 0; index < int(preview.Width)*int(preview.Height); index++ {
		word := uint16(preview.Data[index*2]) | uint16(preview.Data[index*2+1])<<8 // RGB565 little-endian
		red := uint8((word>>11)&0x1F) << 3
		green := uint8((word>>5)&0x3F) << 2
		blue := uint8(word&0x1F) << 3
		img.Set(index%int(preview.Width), index/int(preview.Width), color.RGBA{red, green, blue, 255})
	}
	var buffer bytes.Buffer
	if failure := png.Encode(&buffer, img); failure != nil {
		t.Fatal(failure)
	}
	_ = os.WriteFile("../../testdata/preview_out.png", buffer.Bytes(), 0o644)
	t.Logf("preview_out.png written (%d bytes)", buffer.Len())
}

func TestDecodeLayerRunSum(t *testing.T) {
	file := loadSample(t)
	pixelCount := int(file.Header.ResolutionX) * int(file.Header.ResolutionY)

	// Check a few layers spread across the file: the run sum must equal pixelCount.
	for _, layerIndex := range []int{0, 1, len(file.Layers) / 2, len(file.Layers) - 1} {
		pixels, failure := file.DecodeLayer(layerIndex)
		if failure != nil {
			t.Errorf("DecodeLayer(%d): %v", layerIndex, failure)
			continue
		}
		var white int
		for _, value := range pixels {
			if value > 0 {
				white++
			}
		}
		t.Logf("layer %d: %d px, of which %d exposed (%.1f%%)", layerIndex, len(pixels), white, 100*float64(white)/float64(pixelCount))
	}

	// Middle layer as a PNG for visual inspection.
	middle := len(file.Layers) / 2
	if pixels, failure := file.DecodeLayer(middle); failure == nil {
		img := image.NewGray(image.Rect(0, 0, int(file.Header.ResolutionX), int(file.Header.ResolutionY)))
		copy(img.Pix, pixels)
		var buffer bytes.Buffer
		_ = png.Encode(&buffer, img)
		_ = os.WriteFile("../../testdata/layer_mid_out.png", buffer.Bytes(), 0o644)
		t.Logf("layer_mid_out.png written (layer %d, %d bytes)", middle, buffer.Len())
	}
}
