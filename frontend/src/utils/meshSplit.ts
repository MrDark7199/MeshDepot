/**
 * Splitting a triangle soup into its separate objects, and writing the pieces
 * out as binary STL inside a ZIP.
 *
 * A single STL routinely holds several physically separate parts - a print
 * that was arranged on one plate and exported as one file. Nothing in the
 * format marks them: it is one flat list of triangles. What separates them is
 * connectivity, so the parts are recovered by treating every triangle as an
 * edge between its three corners and collecting the connected components.
 *
 * Positions are quantized to the same 1e-4 mm grid that weldSmooth in
 * StlViewer uses. Exporters write vertex coordinates per triangle, so the two
 * copies of a shared corner differ in the last float bits; comparing the raw
 * values would leave every triangle in its own component.
 */

/** Weld tolerance, matching weldSmooth: a 1e-4 mm grid. */
const QUANTIZE = 1e4

/** One separated object: its own triangle soup, 9 floats per triangle. */
export interface MeshPart {
  positions: Float32Array
  triangleCount: number
}

/**
 * Groups the triangles of a soup into connected components.
 *
 * `positions` is 9 floats per triangle (three XYZ corners), the layout Babylon
 * and the STL parser already use. Components are returned largest first, so
 * part-01 is the biggest object rather than whichever the exporter wrote first.
 *
 * `onProgress` is called with a 0..1 fraction. The function awaits a macrotask
 * every few hundred thousand triangles: on a 1.8M-triangle mesh the union-find
 * runs for seconds, and without yielding the viewer would freeze with no way to
 * show that anything is happening.
 */
export async function splitConnectedComponents(
  positions: Float32Array,
  onProgress?: (fraction: number) => void,
): Promise<MeshPart[]> {
  const triangleCount = Math.floor(positions.length / 9)
  if (triangleCount === 0) return []

  const yieldEvery = 200_000
  const breathe = () => new Promise<void>(resolve => setTimeout(resolve, 0))

  // ── 1. Map every corner onto a welded vertex id ────────────────────────────
  const vertexOfCorner = new Int32Array(triangleCount * 3)
  const vertexIds = new Map<string, number>()
  let vertexCount = 0
  for (let corner = 0; corner < triangleCount * 3; corner++) {
    const base = corner * 3
    const key =
      Math.round(positions[base] * QUANTIZE) + ',' +
      Math.round(positions[base + 1] * QUANTIZE) + ',' +
      Math.round(positions[base + 2] * QUANTIZE)
    let id = vertexIds.get(key)
    if (id === undefined) {
      id = vertexCount++
      vertexIds.set(key, id)
    }
    vertexOfCorner[corner] = id
    if (corner % (yieldEvery * 3) === 0) {
      onProgress?.((corner / (triangleCount * 3)) * 0.5)
      await breathe()
    }
  }

  // ── 2. Union-find over those vertices ──────────────────────────────────────
  // Union by size with full path compression: without it a long thin part
  // degenerates into a linked list and find() turns quadratic.
  const parent = new Int32Array(vertexCount)
  const size = new Int32Array(vertexCount).fill(1)
  for (let i = 0; i < vertexCount; i++) parent[i] = i

  const find = (start: number): number => {
    let root = start
    while (parent[root] !== root) root = parent[root]
    let walk = start
    while (parent[walk] !== root) {
      const next = parent[walk]
      parent[walk] = root
      walk = next
    }
    return root
  }
  const union = (a: number, b: number) => {
    const rootA = find(a)
    const rootB = find(b)
    if (rootA === rootB) return
    if (size[rootA] < size[rootB]) {
      parent[rootA] = rootB
      size[rootB] += size[rootA]
    } else {
      parent[rootB] = rootA
      size[rootA] += size[rootB]
    }
  }

  for (let triangle = 0; triangle < triangleCount; triangle++) {
    const corner = triangle * 3
    union(vertexOfCorner[corner], vertexOfCorner[corner + 1])
    union(vertexOfCorner[corner + 1], vertexOfCorner[corner + 2])
    if (triangle % yieldEvery === 0) {
      onProgress?.(0.5 + (triangle / triangleCount) * 0.4)
      await breathe()
    }
  }

  // ── 3. Collect the triangles per component ─────────────────────────────────
  const trianglesPerRoot = new Map<number, number[]>()
  for (let triangle = 0; triangle < triangleCount; triangle++) {
    const root = find(vertexOfCorner[triangle * 3])
    const bucket = trianglesPerRoot.get(root)
    if (bucket) bucket.push(triangle)
    else trianglesPerRoot.set(root, [triangle])
  }
  onProgress?.(0.95)
  await breathe()

  const parts = [...trianglesPerRoot.values()]
    .sort((a, b) => b.length - a.length)
    .map(triangles => {
      const out = new Float32Array(triangles.length * 9)
      for (let i = 0; i < triangles.length; i++) {
        out.set(positions.subarray(triangles[i] * 9, triangles[i] * 9 + 9), i * 9)
      }
      return { positions: out, triangleCount: triangles.length }
    })
  onProgress?.(1)
  return parts
}

/**
 * Writes one part as binary STL: an 80-byte header, the triangle count, then
 * 50 bytes per triangle (normal, three corners, attribute word).
 *
 * The normal is recomputed from the winding rather than carried over, because
 * the split does not preserve which facet a triangle came from. Slicers derive
 * their own normals anyway; a zero vector would also be legal, but some older
 * tools show such a mesh as inside-out.
 */
// The return type names its backing buffer: Blob and File only accept views
// over a plain ArrayBuffer, and a bare Uint8Array is typed as possibly sharing
// one. Stating it here spares every caller a cast.
export function writeBinaryStl(part: MeshPart): Uint8Array<ArrayBuffer> {
  const bytes = new Uint8Array(84 + part.triangleCount * 50)
  const view = new DataView(bytes.buffer)
  // The 80-byte header stays zeroed apart from this note. It must not begin
  // with "solid": some parsers then read the file as ASCII STL.
  const header = new TextEncoder().encode('Split by MeshDepot')
  bytes.set(header.subarray(0, 79), 0)
  view.setUint32(80, part.triangleCount, true)

  let offset = 84
  for (let triangle = 0; triangle < part.triangleCount; triangle++) {
    const base = triangle * 9
    const ax = part.positions[base], ay = part.positions[base + 1], az = part.positions[base + 2]
    const bx = part.positions[base + 3], by = part.positions[base + 4], bz = part.positions[base + 5]
    const cx = part.positions[base + 6], cy = part.positions[base + 7], cz = part.positions[base + 8]

    const ux = bx - ax, uy = by - ay, uz = bz - az
    const vx = cx - ax, vy = cy - ay, vz = cz - az
    let nx = uy * vz - uz * vy
    let ny = uz * vx - ux * vz
    let nz = ux * vy - uy * vx
    const length = Math.hypot(nx, ny, nz)
    if (length > 0) { nx /= length; ny /= length; nz /= length }
    else { nx = 0; ny = 0; nz = 0 }

    view.setFloat32(offset, nx, true); view.setFloat32(offset + 4, ny, true); view.setFloat32(offset + 8, nz, true)
    view.setFloat32(offset + 12, ax, true); view.setFloat32(offset + 16, ay, true); view.setFloat32(offset + 20, az, true)
    view.setFloat32(offset + 24, bx, true); view.setFloat32(offset + 28, by, true); view.setFloat32(offset + 32, bz, true)
    view.setFloat32(offset + 36, cx, true); view.setFloat32(offset + 40, cy, true); view.setFloat32(offset + 44, cz, true)
    view.setUint16(offset + 48, 0, true)
    offset += 50
  }
  return bytes
}

/** CRC-32 table, built once on first use. */
let crcTable: Uint32Array | null = null
function crc32(data: Uint8Array): number {
  if (!crcTable) {
    crcTable = new Uint32Array(256)
    for (let i = 0; i < 256; i++) {
      let value = i
      for (let bit = 0; bit < 8; bit++) value = value & 1 ? 0xedb88320 ^ (value >>> 1) : value >>> 1
      crcTable[i] = value >>> 0
    }
  }
  let crc = 0xffffffff
  for (let i = 0; i < data.length; i++) crc = crcTable[(crc ^ data[i]) & 0xff] ^ (crc >>> 8)
  return (crc ^ 0xffffffff) >>> 0
}

export interface ZipEntry {
  name: string
  data: Uint8Array
}

/**
 * Builds a ZIP with the stored (uncompressed) method.
 *
 * Deliberately hand-written rather than pulling in a zip library: the frontend
 * has two dependencies in total, and this needs ~60 lines. Stored rather than
 * deflated because the browser offers no synchronous deflate, and the archive
 * exists only to hand several files over in one download - not to be small.
 */
export function buildZip(entries: ZipEntry[]): Blob {
  const encoder = new TextEncoder()
  const chunks: Uint8Array[] = []
  const central: Uint8Array[] = []
  let offset = 0

  for (const entry of entries) {
    const nameBytes = encoder.encode(entry.name)
    const checksum = crc32(entry.data)

    const localHeader = new Uint8Array(30 + nameBytes.length)
    const localView = new DataView(localHeader.buffer)
    localView.setUint32(0, 0x04034b50, true)  // local file header signature
    localView.setUint16(4, 20, true)          // version needed
    localView.setUint16(6, 0, true)           // flags
    localView.setUint16(8, 0, true)           // method 0 = stored
    localView.setUint16(10, 0, true)          // modification time
    localView.setUint16(12, 0, true)          // modification date
    localView.setUint32(14, checksum, true)
    localView.setUint32(18, entry.data.length, true)  // compressed size
    localView.setUint32(22, entry.data.length, true)  // uncompressed size
    localView.setUint16(26, nameBytes.length, true)
    localView.setUint16(28, 0, true)          // extra field length
    localHeader.set(nameBytes, 30)

    const centralHeader = new Uint8Array(46 + nameBytes.length)
    const centralView = new DataView(centralHeader.buffer)
    centralView.setUint32(0, 0x02014b50, true)  // central directory signature
    centralView.setUint16(4, 20, true)          // version made by
    centralView.setUint16(6, 20, true)          // version needed
    centralView.setUint16(8, 0, true)
    centralView.setUint16(10, 0, true)
    centralView.setUint16(12, 0, true)
    centralView.setUint16(14, 0, true)
    centralView.setUint32(16, checksum, true)
    centralView.setUint32(20, entry.data.length, true)
    centralView.setUint32(24, entry.data.length, true)
    centralView.setUint16(28, nameBytes.length, true)
    centralView.setUint16(30, 0, true)          // extra field length
    centralView.setUint16(32, 0, true)          // comment length
    centralView.setUint16(34, 0, true)          // disk number
    centralView.setUint16(36, 0, true)          // internal attributes
    centralView.setUint32(38, 0, true)          // external attributes
    centralView.setUint32(42, offset, true)     // offset of the local header
    centralHeader.set(nameBytes, 46)

    chunks.push(localHeader, entry.data)
    central.push(centralHeader)
    offset += localHeader.length + entry.data.length
  }

  const centralSize = central.reduce((sum, part) => sum + part.length, 0)
  const end = new Uint8Array(22)
  const endView = new DataView(end.buffer)
  endView.setUint32(0, 0x06054b50, true)   // end of central directory
  endView.setUint16(8, entries.length, true)
  endView.setUint16(10, entries.length, true)
  endView.setUint32(12, centralSize, true)
  endView.setUint32(16, offset, true)

  // Assembled into one buffer rather than handed to Blob as a list of views:
  // Blob's parts must be backed by a plain ArrayBuffer, and a Uint8Array is
  // typed as possibly sharing one. One allocation is also cheaper than many.
  const archive = new Uint8Array(offset + centralSize + end.length)
  let cursor = 0
  for (const chunk of [...chunks, ...central, end]) {
    archive.set(chunk, cursor)
    cursor += chunk.length
  }
  return new Blob([archive], { type: 'application/zip' })
}
