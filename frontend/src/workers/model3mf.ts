/**
 * Worker that reads the geometry out of a 3MF `.model` file.
 *
 * This exists because the parse is the longest single stretch of opening a
 * model, and on the main thread nothing can be painted while it runs: the
 * loading bar froze mid-way and the browser reported the page as unresponsive.
 * Moving it here keeps the page interactive no matter how large the file is.
 *
 * A worker has no DOMParser, which is why the scan is text-based - see
 * utils/model3mfScan. That is also the faster route: the old path built one DOM
 * element per vertex before anything else could happen.
 *
 * The geometry travels back as transferables, so the arrays are moved rather
 * than copied - a second copy of a hundred megabytes would undo the point.
 */
import { scanModelXml, type ScannedModel } from '../utils/model3mfScan'

export interface Model3mfRequest {
  /** Raw bytes of one .model entry, already inflated by the caller. */
  bytes: ArrayBuffer
  /** Echoed back so the caller can match answers to requests. */
  path: string
}

export interface Model3mfObjectData {
  id: string
  vertices: Float32Array
  triangles: Uint32Array
  components: { path: string; objectid: string; transform: number[] | null }[]
}

export type Model3mfResponse =
  | { type: 'progress'; path: string; fraction: number }
  | { type: 'done'; path: string; objects: Model3mfObjectData[]; build: ScannedModel['build'] }
  | { type: 'error'; path: string; message: string }

self.onmessage = (event: MessageEvent<Model3mfRequest>) => {
  const { bytes, path } = event.data
  const post = (message: Model3mfResponse, transfer?: Transferable[]) =>
    (self as unknown as Worker).postMessage(message, transfer ?? [])

  try {
    const text = new TextDecoder('utf-8', { fatal: false }).decode(new Uint8Array(bytes))
    // Progress is reported per object. A file with one huge object therefore
    // jumps straight to the end - honest, since there is no finer boundary the
    // scan could report without slowing itself down.
    const model = scanModelXml(text, fraction => post({ type: 'progress', path, fraction }))

    const transfer: Transferable[] = []
    for (const object of model.objects) {
      transfer.push(object.vertices.buffer, object.triangles.buffer)
    }
    post({ type: 'done', path, objects: model.objects, build: model.build }, transfer)
  } catch (failure) {
    post({ type: 'error', path, message: failure instanceof Error ? failure.message : String(failure) })
  }
}
