import { Vector3 } from '@babylonjs/core'

/**
 * Parses g-code moves (G0/G1) into extrusion polylines; a travel move breaks
 * the line. The axes are converted from Z-up (g-code) to Y-up (Babylon), so
 * Babylon's Y is the height. Very large files are capped at MAX_POINTS.
 * Returns the paths plus the height range and the estimated layer height,
 * which the solid mode uses as the bead height.
 */
export const parseGcode = (text: string): { polylines: Vector3[][]; minH: number; maxH: number; layerH: number } | null => {
  const MAX_POINTS = 1_200_000
  const polylines: Vector3[][] = []
  let poly: Vector3[] = []
  let x = 0, y = 0, z = 0, e = 0
  let absXYZ = true, absE = true
  let minH = Infinity, maxH = -Infinity
  let count = 0
  const heights = new Set<number>()   // distinct extrusion heights → layer-height estimate
  const flush = () => { if (poly.length >= 2) polylines.push(poly); poly = [] }

  const rows = text.split('\n')
  for (const raw of rows) {
    if (count > MAX_POINTS) break
    let line = raw
    const semi = line.indexOf(';')
    if (semi >= 0) line = line.slice(0, semi)
    line = line.trim()
    if (!line) continue
    const parts = line.split(/\s+/)
    const cmd = parts[0].toUpperCase()

    if (cmd === 'G90') { absXYZ = true; continue }
    if (cmd === 'G91') { absXYZ = false; continue }
    if (cmd === 'M82') { absE = true; continue }
    if (cmd === 'M83') { absE = false; continue }
    if (cmd === 'G92') {
      for (let i = 1; i < parts.length; i++) {
        if (parts[i][0] === 'E' || parts[i][0] === 'e') { const v = parseFloat(parts[i].slice(1)); if (!isNaN(v)) e = v }
      }
      continue
    }
    if (cmd !== 'G0' && cmd !== 'G1' && cmd !== 'G00' && cmd !== 'G01') continue

    let nx = x, ny = y, nz = z, de = 0, hasE = false
    for (let i = 1; i < parts.length; i++) {
      const axis = parts[i][0].toUpperCase()
      const v = parseFloat(parts[i].slice(1))
      if (isNaN(v)) continue
      if (axis === 'X') nx = absXYZ ? v : x + v
      else if (axis === 'Y') ny = absXYZ ? v : y + v
      else if (axis === 'Z') nz = absXYZ ? v : z + v
      else if (axis === 'E') { hasE = true; de = absE ? v - e : v; e = absE ? v : e + v }
    }

    if (hasE && de > 1e-6) {
      // An extrusion move continues the polyline, opening one if needed.
      if (poly.length === 0) { poly.push(new Vector3(x, z, y)); count++ }
      poly.push(new Vector3(nx, nz, ny)); count++
      if (nz < minH) minH = nz
      if (nz > maxH) maxH = nz
      heights.add(Math.round(nz * 100) / 100)
    } else {
      flush() // Reisebewegung unterbricht den Weg
    }
    x = nx; y = ny; z = nz
  }
  flush()
  if (polylines.length === 0) return null
  return { polylines, minH, maxH, layerH: estimateLayerHeight(heights) }
}

/** The median height difference approximates the layer height; 0.2 mm when it cannot be told. */
export const estimateLayerHeight = (heights: Set<number>): number => {
  const sorted = [...heights].sort((a, b) => a - b)
  const diffs: number[] = []
  for (let i = 1; i < sorted.length; i++) { const d = sorted[i] - sorted[i - 1]; if (d > 1e-3) diffs.push(d) }
  if (diffs.length === 0) return 0.2
  diffs.sort((a, b) => a - b)
  return diffs[Math.floor(diffs.length / 2)] || 0.2
}
