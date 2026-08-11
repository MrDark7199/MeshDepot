package pwmx

import (
	"bytes"
	"encoding/binary"
	"io"
	"math"
	"strconv"
)

// MeshOptions controls the downsampling resolution of the 3D reconstruction.
// Smaller steps = finer (more detail). Thanks to greedy meshing (coplanar faces
// are merged into large rectangles) the STL stays small despite fine voxels.
type MeshOptions struct {
	XYStep    int     // pixel block size in X/Y (>=1)
	ZStep     int     // only every ZStep-th layer (>=1)
	Threshold uint8   // grayscale > Threshold counts as exposed/filled
	MinFill   float32 // a voxel counts as filled only from this occupancy fraction (0..1).
	// Filters thin support structures/antialiasing edges → fewer triangles, cleaner model.
}

// DefaultMeshOptions: for Marching Cubes (smooth, recognizable surface).
//
// Cubic 0.4mm voxels on a 50µm/50µm printer: 8 pixels in X/Y, 8 layers in Z.
// Sampling finer in Z than in X/Y is wasted - with Marching Cubes every grid
// plane costs triangles, while the staircase the eye notices is the one in X/Y.
// Measured on a 1389-layer Photon Mono X file: 2.7M triangles, 131MB of STL,
// about a second to build. Halving XYStep again quadruples that.
//
// MinFill 0.5 removes thin supports/honeycomb walls (which would otherwise
// produce huge triangle counts) → manageable file size with a recognizable model.
func DefaultMeshOptions() MeshOptions {
	return MeshOptions{XYStep: 6, ZStep: 4, Threshold: 0, MinFill: 0.3}
}

// MeshStats summarizes the result (for logging/debugging).
type MeshStats struct {
	NX, NY, NZ int
	Occupied   int
	Triangles  int
	SizeMM     [3]float32 // voxel-space extent in mm (X,Y,Z=print height)
}

// grid3d is a bit occupancy grid (1 bit/voxel, memory-efficient for fine grids).
type grid3d struct {
	nx, ny, nz int
	bits       []uint64
}

func newGrid(nx, ny, nz int) *grid3d {
	return &grid3d{nx, ny, nz, make([]uint64, (nx*ny*nz+63)/64)}
}
func (grid *grid3d) at(x, y, z int) bool {
	if x < 0 || y < 0 || z < 0 || x >= grid.nx || y >= grid.ny || z >= grid.nz {
		return false
	}
	index := (z*grid.ny+y)*grid.nx + x
	return grid.bits[index>>6]&(uint64(1)<<(uint(index)&63)) != 0
}
func (grid *grid3d) set(x, y, z int) {
	index := (z*grid.ny+y)*grid.nx + x
	grid.bits[index>>6] |= uint64(1) << (uint(index) & 63)
}
func (grid *grid3d) setIndex(index int) { grid.bits[index>>6] |= uint64(1) << (uint(index) & 63) }

// buildGrid decodes the RLE of each (ZStep-)layer and marks occupied cells.
// Instead of expanding each layer into a 9.2M-pixel array, it walks the runs
// directly (O(#runs)) and marks covered downsample cells (block "any").
func (file *File) buildGrid(options MeshOptions) (*grid3d, error) {
	resolutionX, resolutionY := int(file.Header.ResolutionX), int(file.Header.ResolutionY)
	if options.XYStep < 1 {
		options.XYStep = 1
	}
	if options.ZStep < 1 {
		options.ZStep = 1
	}
	step := options.XYStep
	nx, ny := resolutionX/step, resolutionY/step
	nz := (len(file.Layers) + options.ZStep - 1) / options.ZStep
	if nx == 0 || ny == 0 || nz == 0 {
		return nil, errEmpty(resolutionX, resolutionY, len(file.Layers))
	}
	grid := newGrid(nx, ny, nz)
	maxX, maxY := nx*step, ny*step // ignore remainder pixels at the right/bottom edge
	pixelCount := resolutionX * resolutionY
	minFill := 1 // occupied-pixel threshold per voxel (block "any" when MinFill<=0)
	if options.MinFill > 0 {
		if fillPixels := int(options.MinFill * float32(step*step)); fillPixels > 1 {
			minFill = fillPixels
		}
	}
	counts := make([]int32, nx*ny) // occupied pixels per cell, reused per layer
	for layerIndex := 0; layerIndex < nz; layerIndex++ {
		for cellIndex := range counts {
			counts[cellIndex] = 0
		}
		layer := file.Layers[layerIndex*options.ZStep]
		dataStart, dataEnd := int(layer.DataAddress), int(layer.DataAddress)+int(layer.DataLength)
		if dataStart < 0 || dataEnd > len(file.raw) || dataStart > dataEnd {
			continue
		}
		rle := file.raw[dataStart:dataEnd]
		position := 0
		for index := 0; index+1 < len(rle); index += 2 {
			word := uint16(rle[index])<<8 | uint16(rle[index+1])
			run := int(word & 0x0FFF)
			if run == 0 {
				continue
			}
			if uint8((word>>12)&0xF)*17 > options.Threshold {
				countRun(counts, position, run, resolutionX, step, nx, maxX, maxY)
			}
			position += run
			if position >= pixelCount {
				break
			}
		}
		base := layerIndex * nx * ny
		threshold := int32(minFill)
		for cell := 0; cell < nx*ny; cell++ {
			if counts[cell] >= threshold {
				grid.setIndex(base + cell)
			}
		}
	}
	return grid, nil
}

// countRun adds the exposed pixels of the run [position,position+run) to the
// covered downsample cells. A run can cross row boundaries → processed row by row.
func countRun(counts []int32, position, run, resolutionX, step, nx, maxX, maxY int) {
	end := position + run
	for position < end {
		y := position / resolutionX
		x := position - y*resolutionX
		rowEnd := (y + 1) * resolutionX
		segmentEnd := end
		if rowEnd < segmentEnd {
			segmentEnd = rowEnd
		}
		if y < maxY {
			xEnd := segmentEnd - y*resolutionX // exclusive, within the row
			if xEnd > maxX {
				xEnd = maxX
			}
			if x < xEnd {
				cellY := y / step
				for cellX := x / step; cellX*step < xEnd; cellX++ {
					if cellX >= nx {
						break
					}
					low := cellX * step
					if low < x {
						low = x
					}
					high := (cellX + 1) * step
					if high > xEnd {
						high = xEnd
					}
					counts[cellY*nx+cellX] += int32(high - low)
				}
			}
		}
		position = segmentEnd
	}
}

// ReconstructSTL reconstructs a surface mesh from the layer stack and returns it
// as a binary STL. Greedy meshing merges coplanar boundary faces into large
// rectangles (drastically fewer triangles at the same resolution). Output is
// Z-up (print height = Z); the viewer orients Z-up models itself.
func (file *File) ReconstructSTL(options MeshOptions) ([]byte, MeshStats, error) {
	grid, failure := file.buildGrid(options)
	if failure != nil {
		return nil, MeshStats{}, failure
	}
	cellSize := [3]float32{
		float32(options.XYStep) * file.Header.PixelSizeUm / 1000,
		float32(options.XYStep) * file.Header.PixelSizeUm / 1000,
		float32(options.ZStep) * file.Header.LayerHeight,
	}

	var body bytes.Buffer
	triangleCount := 0
	// emitQuad writes two triangles for the rectangle origin, origin+edge1, origin+edge1+edge2, origin+edge2.
	emitQuad := func(origin, edge1, edge2, normal [3]float32) {
		cornerA := origin
		cornerB := add(origin, edge1)
		cornerC := add(add(origin, edge1), edge2)
		cornerD := add(origin, edge2)
		writeTri(&body, normal, cornerA, cornerB, cornerC)
		writeTri(&body, normal, cornerA, cornerC, cornerD)
		triangleCount += 2
	}
	occupied := greedyMesh(grid, cellSize, emitQuad)

	var output bytes.Buffer
	output.Write(make([]byte, 80))
	_ = binary.Write(&output, binary.LittleEndian, uint32(triangleCount))
	output.Write(body.Bytes())

	stats := MeshStats{NX: grid.nx, NY: grid.ny, NZ: grid.nz, Occupied: occupied, Triangles: triangleCount,
		SizeMM: [3]float32{float32(grid.nx) * cellSize[0], float32(grid.ny) * cellSize[1], float32(grid.nz) * cellSize[2]}}
	return output.Bytes(), stats, nil
}

// greedyMesh extracts the surface of the occupancy grid with greedy meshing
// (coplanar neighboring faces → large quads) and calls emit per quad. It returns
// the number of occupied voxels. Classic algorithm over the 3 axes × 2 directions.
func greedyMesh(grid *grid3d, cellSize [3]float32, emit func(origin, edge1, edge2, normal [3]float32)) int {
	dims := [3]int{grid.nx, grid.ny, grid.nz}
	occupied := 0
	for x := 0; x < grid.nx; x++ { // count voxels along the way
		for y := 0; y < grid.ny; y++ {
			for z := 0; z < grid.nz; z++ {
				if grid.at(x, y, z) {
					occupied++
				}
			}
		}
	}

	for sweepAxis := 0; sweepAxis < 3; sweepAxis++ {
		uAxis := (sweepAxis + 1) % 3
		vAxis := (sweepAxis + 2) % 3
		cursor := [3]int{}
		neighborOffset := [3]int{}
		neighborOffset[sweepAxis] = 1
		mask := make([]int8, dims[uAxis]*dims[vAxis]) // +1: face in +d, -1: in -d, 0: none
		voxelAt := func(point [3]int) bool { return grid.at(point[0], point[1], point[2]) }

		for cursor[sweepAxis] = -1; cursor[sweepAxis] < dims[sweepAxis]; {
			// Compute the mask for the cut plane between cursor[sweepAxis] and cursor[sweepAxis]+1.
			maskIndex := 0
			for cursor[vAxis] = 0; cursor[vAxis] < dims[vAxis]; cursor[vAxis]++ {
				for cursor[uAxis] = 0; cursor[uAxis] < dims[uAxis]; cursor[uAxis]++ {
					var here, ahead bool
					if cursor[sweepAxis] >= 0 {
						here = voxelAt(cursor)
					}
					if cursor[sweepAxis] < dims[sweepAxis]-1 {
						cursorAhead := cursor
						cursorAhead[sweepAxis]++
						ahead = voxelAt(cursorAhead)
					}
					switch {
					case here == ahead:
						mask[maskIndex] = 0
					case here:
						mask[maskIndex] = 1
					default:
						mask[maskIndex] = -1
					}
					maskIndex++
				}
			}
			cursor[sweepAxis]++

			// Greedily merge the mask into rectangles.
			maskIndex = 0
			for row := 0; row < dims[vAxis]; row++ {
				for col := 0; col < dims[uAxis]; {
					cellValue := mask[maskIndex]
					if cellValue == 0 {
						col++
						maskIndex++
						continue
					}
					width := 1
					for col+width < dims[uAxis] && mask[maskIndex+width] == cellValue {
						width++
					}
					height := 1
				grow:
					for row+height < dims[vAxis] {
						for offsetX := 0; offsetX < width; offsetX++ {
							if mask[maskIndex+offsetX+height*dims[uAxis]] != cellValue {
								break grow
							}
						}
						height++
					}
					// Translate the quad into real coordinates.
					var quadPos, spanU, spanV [3]int
					quadPos[sweepAxis] = cursor[sweepAxis]
					quadPos[uAxis] = col
					quadPos[vAxis] = row
					spanU[uAxis] = width
					spanV[vAxis] = height
					quadOrigin := realCoord(quadPos, cellSize)
					edge1 := realVec(spanU, cellSize)
					edge2 := realVec(spanV, cellSize)
					var normal [3]float32
					normal[sweepAxis] = float32(cellValue) // +d or -d
					if cellValue > 0 {
						emit(quadOrigin, edge1, edge2, normal)
					} else {
						emit(quadOrigin, edge2, edge1, normal) // flip winding for the outward normal
					}
					// Clear the mask
					for rowOffset := 0; rowOffset < height; rowOffset++ {
						for offsetX := 0; offsetX < width; offsetX++ {
							mask[maskIndex+offsetX+rowOffset*dims[uAxis]] = 0
						}
					}
					col += width
					maskIndex += width
				}
			}
		}
	}
	return occupied
}

func realCoord(point [3]int, cellSize [3]float32) [3]float32 {
	return [3]float32{float32(point[0]) * cellSize[0], float32(point[1]) * cellSize[1], float32(point[2]) * cellSize[2]}
}
func realVec(vector [3]int, cellSize [3]float32) [3]float32 {
	return [3]float32{float32(vector[0]) * cellSize[0], float32(vector[1]) * cellSize[1], float32(vector[2]) * cellSize[2]}
}
func add(first, second [3]float32) [3]float32 {
	return [3]float32{first[0] + second[0], first[1] + second[1], first[2] + second[2]}
}

func writeTri(writer io.Writer, normal, vertexA, vertexB, vertexC [3]float32) {
	writeVec(writer, normal)
	writeVec(writer, vertexA)
	writeVec(writer, vertexB)
	writeVec(writer, vertexC)
	_, _ = writer.Write([]byte{0, 0})
}

func writeVec(writer io.Writer, vector [3]float32) {
	var buffer [12]byte
	binary.LittleEndian.PutUint32(buffer[0:], math.Float32bits(vector[0]))
	binary.LittleEndian.PutUint32(buffer[4:], math.Float32bits(vector[1]))
	binary.LittleEndian.PutUint32(buffer[8:], math.Float32bits(vector[2]))
	_, _ = writer.Write(buffer[:])
}

func errEmpty(resolutionX, resolutionY, layerCount int) error {
	return &emptyError{resolutionX, resolutionY, layerCount}
}

type emptyError struct{ resolutionX, resolutionY, layerCount int }

func (emptyErr *emptyError) Error() string {
	return "pwmx: empty/too coarse grid (res " +
		strconv.Itoa(emptyErr.resolutionX) + "x" + strconv.Itoa(emptyErr.resolutionY) + ", " + strconv.Itoa(emptyErr.layerCount) + " layers)"
}
