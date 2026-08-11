package pwmx

import (
	"os"
	"testing"
)

func TestReconstructSTL(t *testing.T) {
	file := loadSample(t)
	stl, stats, failure := file.ReconstructSTL(DefaultMeshOptions())
	if failure != nil {
		t.Fatalf("ReconstructSTL: %v", failure)
	}
	t.Logf("grid=%dx%dx%d occupied=%d triangles=%d sizeMM=%.1fx%.1fx%.1f stlBytes=%d",
		stats.NX, stats.NY, stats.NZ, stats.Occupied, stats.Triangles, stats.SizeMM[0], stats.SizeMM[1], stats.SizeMM[2], len(stl))
	if stats.Triangles == 0 {
		t.Fatal("no triangles produced")
	}
	if stats.Occupied == 0 {
		t.Fatal("no voxel occupied")
	}
	_ = os.WriteFile("../../testdata/sample_out.stl", stl, 0o644)
}
