/**
 * Runs in the page's own world and watches MakerWorld resolve its own download
 * links.
 *
 * That is all it does now. It used to make the API calls as well, because a
 * request from a content script carries the extension's principal and earns a
 * 403 - but MakerWorld's Content-Security-Policy refuses injected scripts, so
 * an import that depended on this one hung waiting for a helper the page would
 * never run. Those calls moved to bridge.js, which asks with the page's
 * principal through content.fetch() and needs no injection at all.
 *
 * What is left is worth keeping and costs nothing when it fails: when the
 * visitor presses download, the answer passes through these hooks and the link
 * is kept. A link seen going past is one that never has to be asked for, and
 * therefore one that can never meet the captcha wall.
 *
 * Communication runs over window.postMessage. Every message is checked for
 * origin and a marker, because the page and anything else on it can post here
 * too.
 */

(function () {
  const REPLY_MARKER = 'meshdepot-importer-page'

  /**
   * The Authorization header MakerWorld's own scripts use, once one has been
   * seen.
   *
   * Reconstructing that credential from a cookie was the first attempt and it
   * earned a 403 with "Please log in to download models": whatever the site
   * authenticates with, it is not simply the cookie named "token". Rather than
   * guess again, the page's own calls are read as they go out - fetch and
   * XMLHttpRequest are both wrapped, because which one the site uses is its
   * business and may change.
   *
   * This only ever observes. Nothing is redirected, blocked or altered.
   */
  let capturedAuthorization = null

  /**
   * Download links the site produced for itself, by instance id.
   *
   * This is the quiet way to get them. MakerWorld answers HTTP 418 when links
   * are resolved in quick succession, and a signed-in browser is not exempt -
   * the wall is about pace, not about who is asking. So the best request is the
   * one never made: when the visitor clicks the site's own download button, the
   * answer passes through this hook and is kept. Importing then costs nothing at
   * all.
   */
  const capturedDownloads = new Map()
  const INSTANCE_PATH_PATTERN = /\/design-service\/instance\/(\d+)\/f3mf/

  /** Remembers a download link from a response body the site asked for. */
  function noteDownloadResponse(url, bodyText) {
    const match = INSTANCE_PATH_PATTERN.exec(String(url))
    if (!match) return
    try {
      const parsed = JSON.parse(bodyText)
      const downloadUrl = parsed && (parsed.url || parsed.downloadUrl)
      if (!downloadUrl) return
      capturedDownloads.set(match[1], downloadUrl)
      // Announced as it happens, so the panel can tick the file off while the
      // visitor is still on the page rather than only at the end.
      reply({ kind: 'captured', instanceId: match[1], url: downloadUrl })
    } catch (failure) {
      // Not JSON, or not the shape expected - nothing to keep.
    }
  }

  const originalFetch = window.fetch
  window.fetch = function (input, options) {
    try {
      const source = (options && options.headers)
        || (typeof Request !== 'undefined' && input instanceof Request ? input.headers : null)
      if (source) {
        const header = new Headers(source).get('authorization')
        if (header) capturedAuthorization = header
      }
    } catch (failure) {
      // Reading the headers must never break the site's own request.
    }
    const request = originalFetch.apply(this, arguments)
    // The clone is read separately so the site's own handling is untouched: a
    // body can only be consumed once, and this must not be the one that does it.
    request.then(response => {
      try {
        const url = response.url || (typeof input === 'string' ? input : '')
        if (INSTANCE_PATH_PATTERN.test(String(url))) {
          response.clone().text().then(text => noteDownloadResponse(url, text)).catch(() => {})
        }
      } catch (failure) {
        // Never let observation disturb the page.
      }
    }).catch(() => {})
    return request
  }

  const originalOpen = XMLHttpRequest.prototype.open
  XMLHttpRequest.prototype.open = function (method, url) {
    try {
      if (INSTANCE_PATH_PATTERN.test(String(url))) {
        this.addEventListener('load', () => {
          try {
            noteDownloadResponse(url, this.responseText)
          } catch (failure) {
            // responseType may not be text; nothing to do.
          }
        })
      }
    } catch (failure) {
      // As above - observation must not break the request.
    }
    return originalOpen.apply(this, arguments)
  }

  const originalSetRequestHeader = XMLHttpRequest.prototype.setRequestHeader
  XMLHttpRequest.prototype.setRequestHeader = function (name, value) {
    try {
      if (String(name).toLowerCase() === 'authorization' && value) {
        capturedAuthorization = String(value)
      }
    } catch (failure) {
      // As above.
    }
    return originalSetRequestHeader.apply(this, arguments)
  }

  function reply(message) {
    window.postMessage(Object.assign({ source: REPLY_MARKER }, message), window.location.origin)
  }

  /**
   * Blobs the page has handed out addresses for, newest last.
   *
   * Thingiverse builds its archive in the browser and downloads it from a blob:
   * address - which the extension cannot read: a plain fetch carries the wrong
   * principal, content.fetch does not exist in this Firefox, and the page tends
   * to revoke the address the moment its download has started. By then there is
   * nothing left to read for anyone.
   *
   * So the object itself is kept here, at the one moment it is certainly alive.
   * A revoked address no longer matters: the bytes are held, not the link to
   * them.
   *
   * Bounded to a handful, because holding archives alive costs exactly as much
   * memory as they are big.
   */
  const heldBlobs = new Map()
  const MAXIMUM_HELD = 4

  const originalCreateObjectURL = URL.createObjectURL
  URL.createObjectURL = function (object) {
    const url = originalCreateObjectURL.apply(this, arguments)
    try {
      if (object instanceof Blob) {
        heldBlobs.set(url, object)
        while (heldBlobs.size > MAXIMUM_HELD) heldBlobs.delete(heldBlobs.keys().next().value)
      }
    } catch (failure) {
      // Observation must never break the page's own download.
    }
    return url
  }

  window.addEventListener('message', event => {
    if (event.source !== window) return
    const request = event.data
    if (!request || request.source !== 'meshdepot-importer' || request.kind !== 'get-blob') return

    const blob = heldBlobs.get(request.url)
    // The blob travels by structured clone, so what arrives on the other side is
    // that side's own object - no boundary left to trip over.
    reply({ kind: 'blob', url: request.url, blob: blob || null })
  })

  reply({ kind: 'ready' })

})()
