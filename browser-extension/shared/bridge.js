/**
 * The bridge to the page's own world.
 *
 * MakerWorld's API cannot be called from a content script: that request carries
 * the extension's principal, Firefox sends "Origin: moz-extension://…" with no
 * referrer, and MakerWorld answers 403. page.js is injected into the page itself
 * and makes the call from there; everything needed to reach it and to bring the
 * answer back safely lives here.
 *
 * Loaded before panel.js and content.js, which use it.
 */

// The two markers on every message between this world and page.js. page.js keeps
// its own copies - it runs in the page, which shares no scope with this side -
// and the pair has to agree.
const REQUEST_MARKER = 'meshdepot-importer'
const REPLY_MARKER = 'meshdepot-importer-page'
// the manifest loads into this content script before this file.

/**
 * The bearer token the site itself uses.
 *
 * Read from document.cookie when the cookie is visible to scripts; when it is
 * marked HttpOnly this returns null and the background script reads it through
 * the cookies API instead. Both paths are needed: which one applies is
 * MakerWorld's decision, not ours, and it has changed before.
 */
function bearerTokenFromDocument() {
  for (const entry of document.cookie.split(';')) {
    const [name, ...rest] = entry.trim().split('=')
    if (name === 'token' && rest.length > 0) {
      return decodeURIComponent(rest.join('='))
    }
  }
  return null
}

/** Asks the background script for the token when the page cannot see it.
 *  MakerWorld only - nothing else here needs a credential of the site's. */
async function bearerToken() {
  const fromDocument = bearerTokenFromDocument()
  if (fromDocument) return fromDocument
  const answer = await sendToExtension({ kind: 'read-token' })
  return answer && answer.token ? answer.token : null
}

/**
 * Puts page.js into the page's world and waits until it reports for duty.
 *
 * Only for watching MakerWorld's own calls now, which is a bonus rather than a
 * requirement: a download link seen going past is one that never has to be asked
 * for. The metadata no longer depends on it - see pageFetch below - because
 * MakerWorld's Content-Security-Policy refuses injected scripts, and an import
 * that hangs on a helper the page will not run is worse than one that simply
 * asks for what it needs.
 *
 * The handshake stays: a refusal is silent otherwise - the element loads, nothing
 * runs, and every later message goes unanswered.
 */
let pageScriptReady = null
function injectPageScript() {
  if (pageScriptReady) return pageScriptReady

  pageScriptReady = new Promise((resolve, reject) => {
    // Injected as the page's own script, so the manifest has to declare it web
    // accessible - which it does for MakerWorld only, the one site whose API is
    // read from here.

    const timeout = window.setTimeout(() => {
      window.removeEventListener('message', listener)
      reject(new Error('page.js did not start. The page may be blocking injected '
        + 'scripts (check the page console for a CSP error).'))
    }, 8000)

    function listener(event) {
      if (event.source !== window) return
      const message = event.data
      if (!message || message.source !== REPLY_MARKER || message.kind !== 'ready') return
      window.clearTimeout(timeout)
      window.removeEventListener('message', listener)
      resolve()
    }
    window.addEventListener('message', listener)

    const element = document.createElement('script')
    if (extensionGone()) {
      reject(new Error('the extension was reloaded - reload this page and try again'))
      return
    }
    element.src = browser.runtime.getURL('page.js')
    element.addEventListener('error', () => {
      window.clearTimeout(timeout)
      window.removeEventListener('message', listener)
      reject(new Error('page.js could not be loaded at all.'))
    })
    element.addEventListener('load', () => element.remove())
    ;(document.head || document.documentElement).appendChild(element)
  })
  return pageScriptReady
}

/** Numbers the exchanges with page.js so a reply cannot be mistaken for another. */
/** Numbers the exchanges with page.js so a reply cannot be mistaken for another. */
let requestCounter = 0

/**
 * Reads the design.
 *
 * The page first, always: that reader works on every supported site and needs
 * nothing but the markup in front of it. Only then is a platform API asked, and
 * only to improve on what is already there.
 *
 * The order is deliberate and was learned the hard way. MakerWorld used to be
 * read through its API alone, and every obstacle in front of that API - a
 * content script's principal earning a 403, a Content-Security-Policy refusing
 * the injected helper that worked around it - stopped the import outright, while
 * the page itself sat there with a title, an author and a gallery on it.
 *
 * Now a failing API costs the extra fields and nothing else.
 */
async function readMetadata(current) {
  const fromPage = meshdepotReadPageMetadata(current)
  if (!current.platform.useApi) return fromPage

  try {
    const token = await bearerToken()
    if (!token) throw new Error('no session token found')
    return mergeMetadata(fromPage, await readMakerworldMetadata(current.identifier, token))
  } catch (failure) {
    // The page's reading stands. That is the point of doing it first.
    return fromPage
  }
}

/**
 * Joins what the page gave with what the API added.
 *
 * The API wins per field, because when it answers it knows better - proper tags
 * rather than whatever the markup lists, the creator's name rather than a link's
 * text. Empty is not "better", though: a field the API left blank keeps the
 * page's value. Pictures are joined rather than replaced, since each source sees
 * some the other does not.
 */
function mergeMetadata(fromPage, fromApi) {
  const merged = {
    source_url: fromPage.source_url,
    platform: fromPage.platform,
    meta: Object.assign({}, fromPage.meta),
    instance_count: fromApi.instance_count,
  }
  for (const field of ['source_id', 'name', 'author', 'description', 'license', 'cover_url']) {
    const value = fromApi.meta[field]
    if (typeof value === 'string' && value.trim() !== '') merged.meta[field] = value
  }
  if (Array.isArray(fromApi.meta.tags) && fromApi.meta.tags.length > 0) {
    merged.meta.tags = fromApi.meta.tags
  }
  merged.meta.images = mergeImages(fromApi.meta.images, fromPage.meta.images)
  merged.meta.cover_url = merged.meta.cover_url || merged.meta.images[0] || ''
  return merged
}

/** Joins picture lists, cover first, without duplicates. */
function mergeImages(fromApi, fromPage) {
  const seen = new Set()
  const merged = []
  for (const url of [...(fromApi || []), ...(fromPage || [])]) {
    if (!url || seen.has(url)) continue
    seen.add(url)
    merged.push(url)
  }
  return merged.slice(0, 24)
}

/**
 * What was read off the page, in one line.
 *
 * Shown because a gap here is otherwise silent: a design that arrives without a
 * creator or with a one-line description looks like MeshDepot lost something,
 * when in fact the page never offered it. Named before the import rather than
 * discovered afterwards.
 */


// ── MakerWorld's API, asked with the page's own identity ─────────────────────

/**
 * A request carrying the page's principal rather than the extension's.
 *
 * This is the whole difficulty with MakerWorld in one function. A plain fetch
 * from a content script goes out as the extension: Firefox sends
 * "Origin: moz-extension://…" with no referrer, and MakerWorld answers 403. The
 * first answer to that was to inject a script into the page and call from there
 * - which worked until MakerWorld's Content-Security-Policy started refusing
 * injected scripts, leaving the panel waiting for a helper that never started.
 *
 * Firefox offers content.fetch() for exactly this: the same request, issued with
 * the page's principal, no injection involved. Where it is missing the ordinary
 * fetch is tried, which is better than nothing and no worse than before.
 */
function pageFetch(path, headers) {
  const request = typeof content !== 'undefined' && content && typeof content.fetch === 'function'
    ? content.fetch
    : window.fetch
  return request.call(window, path, { credentials: 'include', headers: headers })
}

/** One call against MakerWorld's API. authorization may be null - see below. */
async function makerworldApi(path, authorization) {
  const headers = { 'Accept': 'application/json', 'X-Requested-With': 'XMLHttpRequest' }
  if (authorization) headers['Authorization'] = authorization

  const response = await pageFetch(path, headers)
  const text = await response.text()
  let parsed = null
  try {
    parsed = JSON.parse(text)
  } catch (failure) {
    parsed = null
  }
  return { status: response.status, json: parsed, text: text }
}

/**
 * Tries the ways of authenticating in turn and keeps the one that works.
 *
 * Which one is right is a question about MakerWorld, not about this code, so it
 * is answered by asking. Sending no Authorization header at all is a real option
 * and not an oversight: if the site authenticates by cookie, an invented Bearer
 * overrides that and is precisely what produces "Please log in to download
 * models".
 */
let makerworldCredential
async function makerworldApiAuthenticated(path, cookieToken) {
  const ladder = [
    { name: 'cookies alone, no Authorization header', value: null },
    { name: 'a Bearer built from the token cookie', value: cookieToken ? 'Bearer ' + cookieToken : null },
  ]
  const candidates = makerworldCredential !== undefined
    ? [{ name: 'remembered', value: makerworldCredential }]
    : ladder

  let last = null
  for (const candidate of candidates) {
    const answer = await makerworldApi(path, candidate.value)
    if (answer.status === 200) {
      makerworldCredential = candidate.value
      return answer
    }
    last = answer
    // A captcha is not an authentication problem; asking again only digs deeper.
    if (answer.status === 418) return answer
  }
  return last
}

/** The design's own details, without touching the captcha-gated download endpoint. */
async function readMakerworldMetadata(modelIdentifier, token) {
  const meta = await makerworldApiAuthenticated(
    '/api/v1/design-service/design/' + modelIdentifier, token)
  if (!meta || meta.status !== 200 || !meta.json) {
    throw new Error('MakerWorld answered HTTP ' + ((meta && meta.status) || '?') + ' for the design.')
  }

  const categories = Array.isArray(meta.json.categories) ? meta.json.categories : []
  const instances = Array.isArray(meta.json.instances) ? meta.json.instances : []

  // Whatever picture lists the answer happens to carry. Several field names are
  // tried because the shape is MakerWorld's to change, and a missing one simply
  // yields nothing - the gallery read off the page covers the rest either way.
  const pictures = []
  for (const field of ['designPictures', 'pictures', 'images', 'modelPictures', 'coverUrls']) {
    const value = meta.json[field]
    if (!Array.isArray(value)) continue
    for (const entry of value) {
      const url = typeof entry === 'string' ? entry : (entry && (entry.url || entry.picUrl || entry.imageUrl))
      if (typeof url === 'string' && url) pictures.push(url)
    }
  }

  // No source_url or platform here: those come from the page reader, which
  // derives an address that is the same from every tab of a design.
  return {
    meta: {
      source_id: String(modelIdentifier),
      name: meta.json.title || meta.json.name || '',
      author: (meta.json.designCreator && meta.json.designCreator.name) || '',
      description: meta.json.summary || '',
      // Sent as they come. Decoding escaped entities and folding the casing are
      // MeshDepot's job, which already does both on every other import path.
      tags: (Array.isArray(meta.json.tags) ? meta.json.tags : [])
        .concat(categories.map(category => category.name))
        .filter(Boolean),
      license: meta.json.license || '',
      cover_url: meta.json.coverUrl || pictures[0] || '',
      images: [meta.json.coverUrl || '', ...pictures].filter(Boolean),
    },
    instance_count: instances.length,
  }
}
