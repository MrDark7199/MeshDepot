/**
 * The bridge to the page's own world, loaded before panel.js and content.js.
 *
 * MakerWorld's API cannot be called from a content script: that request carries
 * the extension's principal, Firefox sends "Origin: moz-extension://…" with no
 * referrer, and MakerWorld answers 403.
 */

// page.js runs in the page and shares no scope with this side, so it keeps its
// own copies of these two. The pair has to agree.
const REQUEST_MARKER = 'meshdepot-importer'
const REPLY_MARKER = 'meshdepot-importer-page'

/** Null when the cookie is HttpOnly; the background script reads it then. */
function bearerTokenFromDocument() {
  for (const entry of document.cookie.split(';')) {
    const [name, ...rest] = entry.trim().split('=')
    if (name === 'token' && rest.length > 0) {
      return decodeURIComponent(rest.join('='))
    }
  }
  return null
}

async function bearerToken() {
  const fromDocument = bearerTokenFromDocument()
  if (fromDocument) return fromDocument
  const answer = await sendToExtension({ kind: 'read-token' })
  return answer && answer.token ? answer.token : null
}

/**
 * Puts page.js into the page's world and waits until it reports for duty.
 *
 * Only used to watch MakerWorld's own calls, which is a bonus: its CSP refuses
 * injected scripts, so the metadata is read off the page instead. The handshake
 * stays because a refusal is otherwise silent - the element loads, nothing runs,
 * and every later message goes unanswered.
 */
let pageScriptReady = null
function injectPageScript() {
  if (pageScriptReady) return pageScriptReady

  pageScriptReady = new Promise((resolve, reject) => {
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
let requestCounter = 0

/**
 * The page first, always: that reader works on every supported site and needs
 * nothing but the markup. A platform API is only asked afterwards, to improve on
 * what is already there, so a failing API costs the extra fields and nothing else.
 */
async function readMetadata(current) {
  const fromPage = meshdepotReadPageMetadata(current)
  if (!current.platform.useApi) return fromPage

  try {
    const token = await bearerToken()
    if (!token) throw new Error('no session token found')
    return mergeMetadata(fromPage, await readMakerworldMetadata(current.identifier, token))
  } catch (failure) {
    return fromPage
  }
}

/**
 * The API wins per field because when it answers it knows better, but a field it
 * left blank keeps the page's value. Pictures are joined: each source sees some
 * the other does not.
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


// - MakerWorld's API, asked with the page's own identity -----------

/**
 * A request carrying the page's principal rather than the extension's. Firefox
 * offers content.fetch() for exactly this; where it is missing the ordinary
 * fetch is tried, which is no worse than before.
 */
function pageFetch(path, headers) {
  const request = typeof content !== 'undefined' && content && typeof content.fetch === 'function'
    ? content.fetch
    : window.fetch
  return request.call(window, path, { credentials: 'include', headers: headers })
}

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
 * Tries the ways of authenticating in turn and keeps the one that works. Sending
 * no Authorization header is a real option: if the site authenticates by cookie,
 * an invented Bearer overrides it and produces "Please log in to download models".
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

  // Several field names are tried because the shape is MakerWorld's to change;
  // a missing one yields nothing and the gallery read off the page covers it.
  const pictures = []
  for (const field of ['designPictures', 'pictures', 'images', 'modelPictures', 'coverUrls']) {
    const value = meta.json[field]
    if (!Array.isArray(value)) continue
    for (const entry of value) {
      const url = typeof entry === 'string' ? entry : (entry && (entry.url || entry.picUrl || entry.imageUrl))
      if (typeof url === 'string' && url) pictures.push(url)
    }
  }

  // No source_url or platform here: the page reader derives an address that is
  // the same from every tab of a design.
  return {
    meta: {
      source_id: String(modelIdentifier),
      name: meta.json.title || meta.json.name || '',
      author: (meta.json.designCreator && meta.json.designCreator.name) || '',
      description: meta.json.summary || '',
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
