package pwmx

import (
	"math"
	"os"
	"testing"
)

func bboxOfSTL(stl []byte) (dimensions [3]float32, triangleCount int) {
	triangleCount = int(uint32(stl[80]) | uint32(stl[81])<<8 | uint32(stl[82])<<16 | uint32(stl[83])<<24)
	minBound := [3]float32{math.MaxFloat32, math.MaxFloat32, math.MaxFloat32}
	maxBound := [3]float32{-math.MaxFloat32, -math.MaxFloat32, -math.MaxFloat32}
	for index := 0; index < triangleCount; index++ {
		offset := 84 + index*50 + 12
		for vertex := 0; vertex < 3; vertex++ {
			for axis := 0; axis < 3; axis++ {
				bits := uint32(stl[offset+vertex*12+axis*4]) | uint32(stl[offset+vertex*12+axis*4+1])<<8 |
					uint32(stl[offset+vertex*12+axis*4+2])<<16 | uint32(stl[offset+vertex*12+axis*4+3])<<24
				value := math.Float32frombits(bits)
				if value < minBound[axis] {
					minBound[axis] = value
				}
				if value > maxBound[axis] {
					maxBound[axis] = value
				}
			}
		}
	}
	return [3]float32{maxBound[0] - minBound[0], maxBound[1] - minBound[1], maxBound[2] - minBound[2]}, triangleCount
}

func TestMarchingCubesSweep(t *testing.T) {
	file := loadSample(t)
	for _, option := range []MeshOptions{
		{XYStep: 8, ZStep: 2, MinFill: 0.5},
		{XYStep: 8, ZStep: 2, MinFill: 0.7},
		{XYStep: 6, ZStep: 2, MinFill: 0.5},
		{XYStep: 6, ZStep: 2, MinFill: 0.7},
		{XYStep: 5, ZStep: 2, MinFill: 0.6},
	} {
		stl, stats, failure := file.ReconstructSTLMarchingCubes(option)
		if failure != nil {
			t.Fatal(failure)
		}
		dimensions, triangleCount := bboxOfSTL(stl)
		t.Logf("MC xy=%d z=%d grid=%dx%dx%d tris=%d stl=%.1fMB bbox=%.1fx%.1fx%.1f (gridMM=%.0fx%.0fx%.0f)",
			option.XYStep, option.ZStep, stats.NX, stats.NY, stats.NZ, triangleCount, float64(len(stl))/1e6,
			dimensions[0], dimensions[1], dimensions[2], stats.SizeMM[0], stats.SizeMM[1], stats.SizeMM[2])
		if triangleCount == 0 {
			t.Errorf("xy=%d: no triangles", option.XYStep)
		}
		// Sanity: bbox must not exceed the panel (192x120x70mm) → tables ok.
		if dimensions[0] > 200 || dimensions[1] > 130 || dimensions[2] > 75 {
			t.Errorf("xy=%d: bbox implausibly large %.1fx%.1fx%.1f (broken table?)", option.XYStep, dimensions[0], dimensions[1], dimensions[2])
		}
	}
	// Store one variant as an STL for inspection.
	stl, _, _ := file.ReconstructSTLMarchingCubes(MeshOptions{XYStep: 6, ZStep: 2, MinFill: 0.6})
	_ = os.WriteFile("../../testdata/mc_out.stl", stl, 0o644)
}
