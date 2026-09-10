/**
 * Runs in the page's own world and watches MakerWorld resolve its own download
 * links. A link seen going past never has to be asked for, and therefore never
 * meets the captcha wall that guards asking.
 *
 * Communication runs over window.postMessage, checked for origin and marker
 * because the page can post here too. Everything below only observes.
 */

(function () {
  const REPLY_MARKER = 'meshdepot-importer-page'

  /**
   * The Authorization header the site's own scripts use. Rebuilding it from the
   * cookie earned "Please log in to download models", so it is read as it goes
   * out instead - from fetch and XHR both, since which one the site uses may change.
   */
  let capturedAuthorization = null

  /** Download links the site produced for itself, by instance id. */
  const capturedDownloads = new Map()
  const INSTANCE_PATH_PATTERN = /\/design-service\/instance\/(\d+)\/f3mf/

  function noteDownloadResponse(url, bodyText) {
    const match = INSTANCE_PATH_PATTERN.exec(String(url))
    if (!match) return
    try {
      const parsed = JSON.parse(bodyText)
      const downloadUrl = parsed && (parsed.url || parsed.downloadUrl)
      if (!downloadUrl) return
      capturedDownloads.set(match[1], downloadUrl)
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
    // A clone is read, because a body can only be consumed once and this must
    // not be what consumes it.
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
      // As above.
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
   * Blobs the page has handed out addresses for. Thingiverse builds its archive
   * in the browser and revokes the blob: address as its download starts, leaving
   * nothing to read - so the object itself is held at the one moment it is
   * certainly alive. Bounded, because that costs as much memory as the archive is big.
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
