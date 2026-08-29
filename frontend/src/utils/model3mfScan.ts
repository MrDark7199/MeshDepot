/**
 * Reading the geometry out of a 3MF `.model` file without building a DOM.
 *
 * The viewer used to hand the whole XML to DOMParser and then walk it with
 * getElementsByTagName. For a million-vertex model that means a million DOM
 * elements built before anything else can happen: one synchronous call the page
 * cannot interrupt, which is what froze the loading screen and had the browser
 * report the tab as unresponsive.
 *
 * Scanning the text instead never creates those objects. It is also the only
 * option once this runs in a Web Worker, where there is no DOMParser at all.
 *
 * The scan is deliberately narrow: it reads the geometry and the object graph
 * and nothing else. Anything structural and small stays with the caller.
 */

/** One `<object>` from the resources section. */
export interface ScannedObject {
  id: string
  /** Flat XYZ triples, empty for an object that is only a set of components. */
  vertices: Float32Array
  /** Vertex indices, three per triangle. */
  triangles: Uint32Array
  /** Referenced objects, each with its own placement. */
  components: { path: string; objectid: string; transform: number[] | null }[]
}

/** One `<item>` from the build section: an object placed in the scene. */
export interface ScannedBuildItem {
  objectid: string
  transform: number[] | null
}

export interface ScannedModel {
  objects: ScannedObject[]
  build: ScannedBuildItem[]
}

/** Reads one attribute off an element's raw attribute text. */
function attribute(raw: string, name: string): string | null {
  // Word boundary in front, so `p:path` does not also match a lookup of `path`
  // and `v1` does not match inside `v10`.
  const match = new RegExp(`(?:^|\\s)${name}\\s*=\\s*"([^"]*)"`).exec(raw)
  return match ? match[1] : null
}

/** Parses a 3MF transform: twelve numbers, row-major 4x3. Null when absent. */
function parseTransform(raw: string | null): number[] | null {
  if (!raw) return null
  const numbers = raw.trim().split(/\s+/).map(Number)
  return numbers.length === 12 && numbers.every(value => Number.isFinite(value)) ? numbers : null
}

/**
 * Counts occurrences of a self-closing tag so the typed arrays can be sized up
 * front rather than grown. Counting is far cheaper than the parse that follows.
 */
function countTags(text: string, tag: string): number {
  let count = 0
  let at = 0
  const needle = '<' + tag
  for (;;) {
    const found = text.indexOf(needle, at)
    if (found === -1) return count
    // Only a real tag, not a prefix of a longer name.
    const next = text.charCodeAt(found + needle.length)
    if (next === 32 || next === 47 || next === 62 || next === 9 || next === 10 || next === 13) count++
    at = found + needle.length
  }
}

/**
 * Scans one `.model` file.
 *
 * `onProgress` reports 0..1 across the objects found, so a caller driving this
 * from a worker can show real movement instead of a bar that waits for the
 * whole file.
 */
export function scanModelXml(text: string, onProgress?: (fraction: number) => void): ScannedModel {
  const objects: ScannedObject[] = []

  const objectPattern = /<object\b([^>]*)>([\s\S]*?)<\/object\s*>/g
  // Objects with no children close themselves and carry no geometry; the
  // pattern above skips them, which is correct - there is nothing to read.
  const allObjects = [...text.matchAll(objectPattern)]

  allObjects.forEach(([, rawAttributes, body], index) => {
    const id = attribute(rawAttributes, 'id')
    if (!id) return

    let vertices = new Float32Array(0)
    let triangles = new Uint32Array(0)

    const verticesBlock = /<vertices\s*>([\s\S]*?)<\/vertices\s*>/.exec(body)
    if (verticesBlock) {
      const block = verticesBlock[1]
      vertices = new Float32Array(countTags(block, 'vertex') * 3)
      let write = 0
      for (const match of block.matchAll(/<vertex\b([^>]*)>/g)) {
        const raw = match[1]
        vertices[write] = Number(attribute(raw, 'x') ?? 0)
        vertices[write + 1] = Number(attribute(raw, 'y') ?? 0)
        vertices[write + 2] = Number(attribute(raw, 'z') ?? 0)
        write += 3
      }
    }

    const trianglesBlock = /<triangles\s*>([\s\S]*?)<\/triangles\s*>/.exec(body)
    if (trianglesBlock) {
      const block = trianglesBlock[1]
      triangles = new Uint32Array(countTags(block, 'triangle') * 3)
      let write = 0
      for (const match of block.matchAll(/<triangle\b([^>]*)>/g)) {
        const raw = match[1]
        triangles[write] = Number(attribute(raw, 'v1') ?? 0)
        triangles[write + 1] = Number(attribute(raw, 'v2') ?? 0)
        triangles[write + 2] = Number(attribute(raw, 'v3') ?? 0)
        write += 3
      }
    }

    const components: ScannedObject['components'] = []
    const componentsBlock = /<components\s*>([\s\S]*?)<\/components\s*>/.exec(body)
    if (componentsBlock) {
      for (const match of componentsBlock[1].matchAll(/<component\b([^>]*)>/g)) {
        const raw = match[1]
        const objectid = attribute(raw, 'objectid')
        if (!objectid) continue
        components.push({
          // Namespaced first: production extensions write p:path, plain 3MF path.
          path: (attribute(raw, 'p:path') ?? attribute(raw, 'path') ?? '').replace(/^\/+/, ''),
          objectid,
          transform: parseTransform(attribute(raw, 'transform')),
        })
      }
    }

    objects.push({ id, vertices, triangles, components })
    onProgress?.((index + 1) / allObjects.length)
  })

  const build: ScannedBuildItem[] = []
  const buildBlock = /<build\b[^>]*>([\s\S]*?)<\/build\s*>/.exec(text)
  if (buildBlock) {
    for (const match of buildBlock[1].matchAll(/<item\b([^>]*)>/g)) {
      const raw = match[1]
      const objectid = attribute(raw, 'objectid')
      if (!objectid) continue
      build.push({ objectid, transform: parseTransform(attribute(raw, 'transform')) })
    }
  }

  return { objects, build }
}
