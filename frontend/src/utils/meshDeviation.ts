/**
 * How far one mesh has moved away from another.
 *
 * For every vertex of the newer mesh this finds the distance to the nearest
 * point on the older mesh's surface. That is the honest answer to "what changed
 * and by how much" - a bounding box only says the model got taller, and an
 * overlay only says that something is different somewhere.
 *
 * The reference triangles go into a uniform grid rather than a tree: the
 * triangles of a printed part are of a similar size and spread over a similar
 * space, which is exactly the case a grid handles as well as anything more
 * clever, and it is built in one pass.
 */

export interface DeviationResult {
  /** One distance per sample vertex, in the model's own units. */
  distances: Float32Array
  /** The largest of them, which the colour scale and the legend run to. */
  maximum: number
}

/** A triangle soup with a grid over it, ready to be asked for distances. */
interface ReferenceGrid {
  positions: Float32Array
  triangles: Uint32Array
  minimum: [number, number, number]
  cell: number
  counts: [number, number, number]
  buckets: Map<number, number[]>
}

const CELLS_ACROSS = 48
/** How far the search spreads before it gives up and reports what it has. */
const MAX_RINGS = 8

export function buildReferenceGrid(positions: Float32Array, triangles: Uint32Array): ReferenceGrid {
  let minX = Infinity, minY = Infinity, minZ = Infinity
  let maxX = -Infinity, maxY = -Infinity, maxZ = -Infinity
  for (let index = 0; index < positions.length; index += 3) {
    const x = positions[index], y = positions[index + 1], z = positions[index + 2]
    if (x < minX) minX = x
    if (y < minY) minY = y
    if (z < minZ) minZ = z
    if (x > maxX) maxX = x
    if (y > maxY) maxY = y
    if (z > maxZ) maxZ = z
  }
  const spanX = maxX - minX, spanY = maxY - minY, spanZ = maxZ - minZ
  const cell = Math.max(Math.max(spanX, spanY, spanZ) / CELLS_ACROSS, 1e-4)
  const counts: [number, number, number] = [
    Math.max(1, Math.ceil(spanX / cell) + 1),
    Math.max(1, Math.ceil(spanY / cell) + 1),
    Math.max(1, Math.ceil(spanZ / cell) + 1),
  ]
  const grid: ReferenceGrid = {
    positions, triangles,
    minimum: [minX, minY, minZ], cell, counts, buckets: new Map(),
  }

  // A triangle is filed under every cell its own box touches, so a long thin one
  // is found from any of them rather than only from where its first corner sits.
  for (let triangle = 0; triangle < triangles.length; triangle += 3) {
    let loX = Infinity, loY = Infinity, loZ = Infinity
    let hiX = -Infinity, hiY = -Infinity, hiZ = -Infinity
    for (let corner = 0; corner < 3; corner++) {
      const at = triangles[triangle + corner] * 3
      const x = positions[at], y = positions[at + 1], z = positions[at + 2]
      if (x < loX) loX = x
      if (y < loY) loY = y
      if (z < loZ) loZ = z
      if (x > hiX) hiX = x
      if (y > hiY) hiY = y
      if (z > hiZ) hiZ = z
    }
    const fromX = cellOf(loX, minX, cell, counts[0]), toX = cellOf(hiX, minX, cell, counts[0])
    const fromY = cellOf(loY, minY, cell, counts[1]), toY = cellOf(hiY, minY, cell, counts[1])
    const fromZ = cellOf(loZ, minZ, cell, counts[2]), toZ = cellOf(hiZ, minZ, cell, counts[2])
    for (let ix = fromX; ix <= toX; ix++) {
      for (let iy = fromY; iy <= toY; iy++) {
        for (let iz = fromZ; iz <= toZ; iz++) {
          const key = ix + counts[0] * (iy + counts[1] * iz)
          const bucket = grid.buckets.get(key)
          if (bucket) bucket.push(triangle)
          else grid.buckets.set(key, [triangle])
        }
      }
    }
  }
  return grid
}

const cellOf = (value: number, minimum: number, cell: number, count: number) =>
  Math.min(count - 1, Math.max(0, Math.floor((value - minimum) / cell)))

/**
 * The distances of every sample vertex to the reference surface.
 *
 * onProgress is called now and then with 0..1 - the walk over a few hundred
 * thousand vertices is long enough that the caller wants to say so.
 */
export function deviationOf(
  grid: ReferenceGrid,
  samples: Float32Array,
  onProgress?: (fraction: number) => void,
): DeviationResult {
  const count = samples.length / 3
  const distances = new Float32Array(count)
  let maximum = 0
  const report = Math.max(1, Math.floor(count / 20))

  for (let vertex = 0; vertex < count; vertex++) {
    const x = samples[vertex * 3], y = samples[vertex * 3 + 1], z = samples[vertex * 3 + 2]
    distances[vertex] = nearestDistance(grid, x, y, z)
    if (distances[vertex] > maximum) maximum = distances[vertex]
    if (onProgress && vertex % report === 0) onProgress(vertex / count)
  }
  onProgress?.(1)
  return { distances, maximum }
}

/**
 * Rings of cells around the point, widening until the nearest hit so far is
 * closer than the ring itself can still be - at which point nothing further out
 * can beat it.
 */
function nearestDistance(grid: ReferenceGrid, x: number, y: number, z: number): number {
  const { minimum, cell, counts, buckets, positions, triangles } = grid
  const centreX = cellOf(x, minimum[0], cell, counts[0])
  const centreY = cellOf(y, minimum[1], cell, counts[1])
  const centreZ = cellOf(z, minimum[2], cell, counts[2])
  let best = Infinity

  for (let ring = 0; ring <= MAX_RINGS; ring++) {
    for (let ix = centreX - ring; ix <= centreX + ring; ix++) {
      if (ix < 0 || ix >= counts[0]) continue
      for (let iy = centreY - ring; iy <= centreY + ring; iy++) {
        if (iy < 0 || iy >= counts[1]) continue
        for (let iz = centreZ - ring; iz <= centreZ + ring; iz++) {
          if (iz < 0 || iz >= counts[2]) continue
          // Only the shell of the ring: everything inside it was walked already.
          const onShell = Math.abs(ix - centreX) === ring || Math.abs(iy - centreY) === ring || Math.abs(iz - centreZ) === ring
          if (ring > 0 && !onShell) continue
          const bucket = buckets.get(ix + counts[0] * (iy + counts[1] * iz))
          if (!bucket) continue
          for (const triangle of bucket) {
            const a = triangles[triangle] * 3, b = triangles[triangle + 1] * 3, c = triangles[triangle + 2] * 3
            const distance = pointTriangleDistance(
              x, y, z,
              positions[a], positions[a + 1], positions[a + 2],
              positions[b], positions[b + 1], positions[b + 2],
              positions[c], positions[c + 1], positions[c + 2],
            )
            if (distance < best) best = distance
          }
        }
      }
    }
    // Everything beyond this ring is at least this far away.
    if (best <= ring * cell) return best
  }
  return best === Infinity ? MAX_RINGS * cell : best
}

/**
 * Distance from a point to a triangle - the closest point may lie on a face, an
 * edge or a corner, so all seven regions are covered. Written out in scalars
 * rather than vectors: this runs millions of times and every allocation told.
 */
export function pointTriangleDistance(
  px: number, py: number, pz: number,
  ax: number, ay: number, az: number,
  bx: number, by: number, bz: number,
  cx: number, cy: number, cz: number,
): number {
  const abx = bx - ax, aby = by - ay, abz = bz - az
  const acx = cx - ax, acy = cy - ay, acz = cz - az
  const apx = px - ax, apy = py - ay, apz = pz - az

  const d1 = abx * apx + aby * apy + abz * apz
  const d2 = acx * apx + acy * apy + acz * apz
  if (d1 <= 0 && d2 <= 0) return Math.hypot(apx, apy, apz)

  const bpx = px - bx, bpy = py - by, bpz = pz - bz
  const d3 = abx * bpx + aby * bpy + abz * bpz
  const d4 = acx * bpx + acy * bpy + acz * bpz
  if (d3 >= 0 && d4 <= d3) return Math.hypot(bpx, bpy, bpz)

  const vc = d1 * d4 - d3 * d2
  if (vc <= 0 && d1 >= 0 && d3 <= 0) {
    const v = d1 / (d1 - d3)
    return Math.hypot(apx - abx * v, apy - aby * v, apz - abz * v)
  }

  const cpx = px - cx, cpy = py - cy, cpz = pz - cz
  const d5 = abx * cpx + aby * cpy + abz * cpz
  const d6 = acx * cpx + acy * cpy + acz * cpz
  if (d6 >= 0 && d5 <= d6) return Math.hypot(cpx, cpy, cpz)

  const vb = d5 * d2 - d1 * d6
  if (vb <= 0 && d2 >= 0 && d6 <= 0) {
    const w = d2 / (d2 - d6)
    return Math.hypot(apx - acx * w, apy - acy * w, apz - acz * w)
  }

  const va = d3 * d6 - d5 * d4
  if (va <= 0 && d4 - d3 >= 0 && d5 - d6 >= 0) {
    const w = (d4 - d3) / ((d4 - d3) + (d5 - d6))
    return Math.hypot(bpx - (cx - bx) * w, bpy - (cy - by) * w, bpz - (cz - bz) * w)
  }

  // Inside the face: the perpendicular.
  const denom = 1 / (va + vb + vc)
  const v = vb * denom, w = vc * denom
  return Math.hypot(apx - (abx * v + acx * w), apy - (aby * v + acy * w), apz - (abz * v + acz * w))
}
