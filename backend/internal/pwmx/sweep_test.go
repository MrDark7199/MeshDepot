package pwmx

import "testing"

func TestMeshSweep(t *testing.T) {
	file := loadSample(t)
	for _, option := range []MeshOptions{
		{XYStep: 6, ZStep: 2, MinFill: 0.35},
		{XYStep: 8, ZStep: 2, MinFill: 0.35},
		{XYStep: 10, ZStep: 3, MinFill: 0.35},
		{XYStep: 10, ZStep: 3, MinFill: 0.5},
	} {
		stl, stats, failure := file.ReconstructSTL(option)
		if failure != nil {
			t.Fatal(failure)
		}
		t.Logf("xy=%2d z=%2d -> grid=%dx%dx%d occ=%d tris=%d stl=%.1fMB",
			option.XYStep, option.ZStep, stats.NX, stats.NY, stats.NZ, stats.Occupied, stats.Triangles, float64(len(stl))/1e6)
	}
}
