package pwmx

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"testing"
)

// TestProjectionSilhouette renders a side projection (along Y) of the
// reconstructed voxel volume. If the model silhouette emerges from it, the 3D
// reconstruction is correct in substance (not just formally).
func TestProjectionSilhouette(t *testing.T) {
	file := loadSample(t)
	grid, failure := file.buildGrid(DefaultMeshOptions())
	if failure != nil {
		t.Fatal(failure)
	}
	nx, ny, nz := grid.nx, grid.ny, grid.nz
	// X (width) × Z (height); summed over all Y.
	img := image.NewGray(image.Rect(0, 0, nx, nz))
	for z := 0; z < nz; z++ {
		for x := 0; x < nx; x++ {
			hit := false
			for y := 0; y < ny && !hit; y++ {
				if grid.at(x, y, z) {
					hit = true
				}
			}
			if hit {
				// z=0 is at the bottom; invert image Y so the top is on top.
				img.SetGray(x, nz-1-z, color.Gray{255})
			}
		}
	}
	var buffer bytes.Buffer
	_ = png.Encode(&buffer, img)
	_ = os.WriteFile("../../testdata/projection_out.png", buffer.Bytes(), 0o644)
	t.Logf("projection_out.png written (%dx%d)", nx, nz)
}
