import { Matrix } from '@babylonjs/core'
import type { Model3mfRequest, Model3mfResponse } from '../workers/model3mf'
import { decodeText } from './meshTools'

/**
 * Extracts and decompresses a file entry from a ZIP archive.
 * Supports stored (method 0) and deflated (method 8) entries.
 * Scans all local file headers and returns the first .model file
 * that contains vertex data.
 */
export const decompress = async (data: Uint8Array<ArrayBuffer>): Promise<Uint8Array> => {
  const ds = new DecompressionStream('deflate-raw')
  const writer = ds.writable.getWriter()
  const reader = ds.readable.getReader()
  writer.write(data)
  writer.close()
  const chunks: Uint8Array[] = []
  while (true) {
    const { value, done } = await reader.read()
    if (done) break
    chunks.push(value)
  }
  const total = chunks.reduce((s, c) => s + c.length, 0)
  const out = new Uint8Array(total)
  let pos = 0
  for (const chunk of chunks) { out.set(chunk, pos); pos += chunk.length }
  return out
}

/**
 * Reads the real 64-bit values from the ZIP64 extra field (header id 0x0001).
 * Only the fields whose 32-bit base value is 0xFFFFFFFF are present, in a
 * fixed order: uncompressed size, compressed size, local header offset.
 */
export const parseZip64Extra = (extra: Uint8Array, hasUncomp: boolean, hasComp: boolean, hasOff: boolean): { comp?: number; off?: number } => {
  const dv = new DataView(extra.buffer, extra.byteOffset, extra.byteLength)
  let p = 0
  while (p + 4 <= extra.byteLength) {
    const id = dv.getUint16(p, true)
    const sz = dv.getUint16(p + 2, true)
    if (id === 0x0001) {
      let q = p + 4
      const end = p + 4 + sz
      const res: { comp?: number; off?: number } = {}
      if (hasUncomp && q + 8 <= end) q += 8 // unkomprimierte Größe überspringen
      if (hasComp && q + 8 <= end) { res.comp = Number(dv.getBigUint64(q, true)); q += 8 }
      if (hasOff && q + 8 <= end) { res.off = Number(dv.getBigUint64(q, true)); q += 8 }
      return res
    }
    p += 4 + sz
  }
  return {}
}

/**
 * Pulls the .model/.config entries out of a 3MF (a ZIP) through the central
 * directory. That is the only place the compressed size is reliable: in the
 * local header a streaming slicer writes 0 (data descriptor, general-purpose
 * bit 3), and ZIP64 writes the 0xFFFFFFFF sentinel.
 */
export const extractModelFiles = async (buffer: ArrayBuffer): Promise<Map<string, Uint8Array>> => {
  const view = new DataView(buffer)
  const bytes = new Uint8Array(buffer)
  const files = new Map<string, Uint8Array>()

  // Search backwards for the end of central directory (its comment is normally empty).
  let eocd = -1
  for (let i = buffer.byteLength - 22; i >= 0; i--) {
    if (view.getUint32(i, true) === 0x06054b50) { eocd = i; break }
  }
  if (eocd < 0) return files

  let cdOffset = view.getUint32(eocd + 16, true)
  let cdCount = view.getUint16(eocd + 10, true)

  // The ZIP64 EOCD, for when the 32-bit fields are saturated.
  if ((cdOffset === 0xFFFFFFFF || cdCount === 0xFFFF) && eocd - 20 >= 0 && view.getUint32(eocd - 20, true) === 0x07064b50) {
    const z64eocd = Number(view.getBigUint64(eocd - 20 + 8, true))
    if (z64eocd >= 0 && z64eocd + 56 <= buffer.byteLength && view.getUint32(z64eocd, true) === 0x06064b50) {
      cdCount = Number(view.getBigUint64(z64eocd + 32, true))
      cdOffset = Number(view.getBigUint64(z64eocd + 48, true))
    }
  }

  let cd = cdOffset
  for (let n = 0; n < cdCount && cd + 46 <= buffer.byteLength; n++) {
    if (view.getUint32(cd, true) !== 0x02014b50) break
    const method = view.getUint16(cd + 10, true)
    let compSize = view.getUint32(cd + 20, true)
    const uncompSize = view.getUint32(cd + 24, true)
    const nameLen = view.getUint16(cd + 28, true)
    const extraLen = view.getUint16(cd + 30, true)
    const commentLen = view.getUint16(cd + 32, true)
    let localOff = view.getUint32(cd + 42, true)
    const name = new TextDecoder().decode(bytes.subarray(cd + 46, cd + 46 + nameLen))

    if (compSize === 0xFFFFFFFF || uncompSize === 0xFFFFFFFF || localOff === 0xFFFFFFFF) {
      const extra = bytes.subarray(cd + 46 + nameLen, cd + 46 + nameLen + extraLen)
      const z = parseZip64Extra(extra, uncompSize === 0xFFFFFFFF, compSize === 0xFFFFFFFF, localOff === 0xFFFFFFFF)
      if (compSize === 0xFFFFFFFF && z.comp != null) compSize = z.comp
      if (localOff === 0xFFFFFFFF && z.off != null) localOff = z.off
    }

    // .model/.config carry geometry + settings; plate_N.png are the slicer-rendered plate previews
    // (the no_light/top/pick variants are deliberately excluded via the `plate_<digits>` anchor).
    if (name.endsWith('.model') || name.endsWith('.config') || /(^|\/)plate_\d+\.png$/i.test(name)) {
      // The local header carries its own name/extra lengths, which may differ.
      const lNameLen = view.getUint16(localOff + 26, true)
      const lExtraLen = view.getUint16(localOff + 28, true)
      const dataStart = localOff + 30 + lNameLen + lExtraLen
      const chunk = bytes.subarray(dataStart, dataStart + compSize)
      const raw = method === 8 ? await decompress(chunk)
                : method === 0 ? chunk
                : null
      if (raw) files.set(name.replace(/^\/+/, ''), raw)
    }
    cd += 46 + nameLen + extraLen + commentLen
  }
  return files
}

export interface M3mfObject {
  verts?: number[]
  tris?: number[]
  components?: { path: string; objectid: string; matrix: Matrix }[]
}

export const findEntry = (files: Map<string, Uint8Array>, suffix: string): Uint8Array | undefined => {
  for (const [path, bytes] of files) if (path.endsWith(suffix)) return bytes
  return undefined
}

/**
 * Parses Metadata/model_settings.config: which extruder each object uses and which plate
 * (named colour group) each extruder belongs to. Object ids here match the <build> objectids
 * in the main model.
 */
/** One build plate as declared in model_settings.config, before its geometry is resolved. */
export interface PlateSpec { id: string; name: string; thumbFile: string; objectIds: string[] }

export const parseModelSettings = (bytes?: Uint8Array): {
  objExtruder: Map<string, string>
  plateNameForExtruder: Map<string, string>
  plates: PlateSpec[]
} => {
  const objExtruder = new Map<string, string>()
  const plateNameForExtruder = new Map<string, string>()
  const plates: PlateSpec[] = []
  if (!bytes) return { objExtruder, plateNameForExtruder, plates }

  const doc = new DOMParser().parseFromString(decodeText(bytes), 'text/xml')
  for (const obj of Array.from(doc.getElementsByTagName('object'))) {
    const id = obj.getAttribute('id')
    if (!id) continue
    // The extruder metadata is a direct child of <object> (parts carry their own metadata).
    for (const child of Array.from(obj.children)) {
      if (child.tagName === 'metadata' && child.getAttribute('key') === 'extruder') {
        const v = child.getAttribute('value')
        if (v) objExtruder.set(id, v)
      }
    }
  }
  for (const plate of Array.from(doc.getElementsByTagName('plate'))) {
    let name = '', id = '', thumbFile = ''
    const objectIds: string[] = []
    // Plate-level fields are direct <metadata> children; object ids live inside <model_instance>.
    for (const child of Array.from(plate.children)) {
      if (child.tagName === 'metadata') {
        const k = child.getAttribute('key'), v = child.getAttribute('value') || ''
        if (k === 'plater_name') name = v
        else if (k === 'plater_id') id = v
        else if (k === 'thumbnail_file') thumbFile = v
      } else if (child.tagName === 'model_instance') {
        for (const md of Array.from(child.getElementsByTagName('metadata'))) {
          if (md.getAttribute('key') === 'object_id') {
            const v = md.getAttribute('value')
            if (v) objectIds.push(v)
          }
        }
      }
    }
    plates.push({ id: id || String(plates.length + 1), name, thumbFile, objectIds })
    if (name) for (const oid of objectIds) {
      const ex = objExtruder.get(oid)
      if (ex && !plateNameForExtruder.has(ex)) plateNameForExtruder.set(ex, name)
    }
  }
  return { objExtruder, plateNameForExtruder, plates }
}

export const parseProjectColors = (bytes?: Uint8Array): (extruder: string) => string | undefined => {
  let filament: string[] = []
  let extruder: string[] = []
  if (bytes) {
    try {
      const json = JSON.parse(decodeText(bytes))
      if (Array.isArray(json.filament_colour)) filament = json.filament_colour
      if (Array.isArray(json.extruder_colour)) extruder = json.extruder_colour
    } catch { /* malformed settings → fall back to default colour */ }
  }
  return (ex: string) => {
    const idx = parseInt(ex) - 1 // extruder numbers are 1-based
    if (idx < 0) return undefined
    return filament[idx] || extruder[idx] || undefined
  }
}

/** Builds a Babylon Matrix from a 3MF transform string (12 values, row-vector convention). */
/**
 * Reads one .model entry into its objects (keyed by id) and its <build> items.
 *
 * The scan runs in a worker: it is the longest stretch of opening a 3MF, and
 * on the main thread nothing could be painted while it ran - the loading bar
 * sat frozen and the browser flagged the page as unresponsive. The worker
 * also cannot use DOMParser, which is why the geometry is read from the text
 * instead of from a DOM built with one element per vertex.
 *
 * A worker that fails to start (or throws) is not fatal: the caller falls
 * back to reading the file on this thread, which is slow but correct.
 */
export const parseModelFile = (bytes: Uint8Array, onProgress?: (fraction: number) => void): Promise<{
  objects: Map<string, M3mfObject>
  build: { objectid: string; matrix: Matrix }[]
}> => new Promise((resolve, reject) => {
  let worker: Worker
  try {
    worker = new Worker(new URL('../workers/model3mf.ts', import.meta.url), { type: 'module' })
  } catch (failure) {
    reject(failure)
    return
  }
  const finish = () => worker.terminate()

  worker.onmessage = (event: MessageEvent<Model3mfResponse>) => {
    const message = event.data
    if (message.type === 'progress') { onProgress?.(message.fraction); return }
    if (message.type === 'error') { finish(); reject(new Error(message.message)); return }

    const objects = new Map<string, M3mfObject>()
    for (const scanned of message.objects) {
      const object: M3mfObject = {}
      if (scanned.vertices.length) {
        object.verts = Array.from(scanned.vertices)
        object.tris = Array.from(scanned.triangles)
      }
      if (scanned.components.length) {
        object.components = scanned.components.map(component => ({
          path: component.path,
          objectid: component.objectid,
          matrix: matrixFromValues(component.transform),
        }))
      }
      objects.set(scanned.id, object)
    }
    const build = message.build.map(item => ({ objectid: item.objectid, matrix: matrixFromValues(item.transform) }))
    finish()
    resolve({ objects, build })
  }
  worker.onerror = failure => { finish(); reject(new Error(failure.message || 'worker failed')) }

  // The buffer is transferred, so this copy is the worker's from here on.
  const copy = bytes.slice()
  worker.postMessage({ bytes: copy.buffer, path: '' } satisfies Model3mfRequest, [copy.buffer])
})

export const matrixFromValues = (values: number[] | null): Matrix => {
  if (!values || values.length < 12) return Matrix.Identity()
  return Matrix.FromValues(
    values[0], values[1], values[2], 0,
    values[3], values[4], values[5], 0,
    values[6], values[7], values[8], 0,
    values[9], values[10], values[11], 1,
  )
}
