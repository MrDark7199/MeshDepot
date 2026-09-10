import { VertexData } from '@babylonjs/core'

export function is3dFile(filename: string): boolean {
  const ext = filename.toLowerCase().split('.').pop() || ''
  return ['stl', 'obj', '3mf'].includes(ext)
}

export const decodeText = (bytes: Uint8Array): string => new TextDecoder('utf-8', { fatal: false }).decode(bytes)

/** Chunked, so a large PNG does not blow the call stack. */
export const bytesToBase64 = (bytes: Uint8Array): string => {
  let bin = ''
  const CHUNK = 0x8000
  for (let i = 0; i < bytes.length; i += CHUNK) {
    bin += String.fromCharCode(...bytes.subarray(i, i + CHUNK))
  }
  return btoa(bin)
}

/**
 * STL files may ship zero-length facet normals - our marching-cubes resin
 * reconstruction does - which would leave the mesh unlit. Callers then recompute
 * the normals from the geometry.
 */
export const hasUsableNormals = (normals: ArrayLike<number>): boolean => {
  for (let i = 0; i + 2 < normals.length; i += 3) {
    if (normals[i] !== 0 || normals[i + 1] !== 0 || normals[i + 2] !== 0) return true
  }
  return false
}

/**
 * Welds coincident vertices of a triangle soup into indexed geometry with
 * averaged normals. Marching-cubes STL emits independent triangles, so every
 * facet is flat-shaded and the voxel surface looks blocky; sharing vertices at
 * identical positions makes the same geometry read as a smooth surface. Positions
 * are quantized to a fine grid so tiny float differences still weld.
 */
export const weldSmooth = (positions: ArrayLike<number>): { positions: number[]; indices: number[]; normals: number[] } => {
  const map = new Map<string, number>()
  const outPos: number[] = []
  const indices: number[] = new Array(positions.length / 3)
  const q = 1e4 // weld tolerance: 1e-4 mm grid
  for (let i = 0, v = 0; i < positions.length; i += 3, v++) {
    const key = Math.round(positions[i] * q) + ',' + Math.round(positions[i + 1] * q) + ',' + Math.round(positions[i + 2] * q)
    let idx = map.get(key)
    if (idx === undefined) {
      idx = outPos.length / 3
      map.set(key, idx)
      outPos.push(positions[i], positions[i + 1], positions[i + 2])
    }
    indices[v] = idx
  }
  const normals: number[] = []
  VertexData.ComputeNormals(outPos, indices, normals)
  return { positions: outPos, indices, normals }
}
