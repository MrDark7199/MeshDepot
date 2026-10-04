/**
 * Worker that measures how far one mesh has moved from another.
 *
 * On the main thread this would freeze the page for seconds: a part of a few
 * hundred thousand vertices is asked against a grid of as many triangles, and
 * nothing can be painted while it runs - the same reason the 3MF parse lives in
 * a worker.
 *
 * The arrays travel as transferables, so they are moved rather than copied.
 */
import { buildReferenceGrid, deviationOf } from '../utils/meshDeviation'

export interface DeviationRequest {
  /** The older mesh: the surface the distances are measured to. */
  referencePositions: ArrayBuffer
  referenceTriangles: ArrayBuffer
  /** The newer mesh: one distance comes back per vertex of it. */
  samplePositions: ArrayBuffer
}

export type DeviationResponse =
  | { type: 'progress'; fraction: number }
  | { type: 'done'; distances: Float32Array; maximum: number }
  | { type: 'error'; message: string }

self.onmessage = (event: MessageEvent<DeviationRequest>) => {
  const post = (message: DeviationResponse, transfer?: Transferable[]) =>
    (self as unknown as Worker).postMessage(message, transfer ?? [])
  try {
    const grid = buildReferenceGrid(
      new Float32Array(event.data.referencePositions),
      new Uint32Array(event.data.referenceTriangles),
    )
    const result = deviationOf(grid, new Float32Array(event.data.samplePositions),
      fraction => post({ type: 'progress', fraction }))
    post({ type: 'done', distances: result.distances, maximum: result.maximum }, [result.distances.buffer])
  } catch (failure) {
    post({ type: 'error', message: failure instanceof Error ? failure.message : String(failure) })
  }
}
