/**
 * Reads the MakerWorld session cookie and forwards a collected design to the
 * configured MeshDepot instance.
 *
 * Two things have to happen outside the page, which is why this file exists at
 * all. A cookie marked HttpOnly is invisible to the content script but readable
 * here through the cookies API. And the request to MeshDepot is cross-site: the
 * session cookie of the instance is SameSite=Strict and would not be sent, so
 * the extension authenticates with an API key of its own instead.
 */

/** Reads the instance settings the options page stores. */
async function settings() {
  const stored = await browser.storage.local.get(['instanceUrl', 'apiKey', 'cancelDownload', 'addToCollection'])
  return {
    instanceUrl: (stored.instanceUrl || '').replace(/\/+$/, ''),
    apiKey: stored.apiKey || '',
    // On by default: the server fetches the file from the link, so a second copy
    // on this machine is not what the visitor asked for by pressing import.
    cancelDownload: stored.cancelDownload !== false,
    // Off unless switched on: filing designs somewhere the member did not ask for
    // is a change to their library, not a convenience.
    addToCollection: stored.addToCollection === true,
  }
}

/** The bearer token from the makerworld.com cookie jar, or null. */
async function makerworldToken() {
  try {
    const cookie = await browser.cookies.get({ url: 'https://makerworld.com/', name: 'token' })
    return cookie ? cookie.value : null
  } catch (failure) {
    return null
  }
}

/**
 * Sends one collected design to MeshDepot.
 *
 * In dry-run mode nothing leaves the browser: the payload is written to the
 * extension console and reported as a success. That is what makes the whole
 * flow testable before the receiving endpoint exists.
 */
async function sendToMeshDepot(payload) {
  const configuration = await settings()

  if (!configuration.instanceUrl || !configuration.apiKey) {
    return { ok: false, error: 'MeshDepot is not set up yet. Open the extension and press Settings.' }
  }

  try {
    const response = await fetch(configuration.instanceUrl + '/api/v1/imports/browser', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'Authorization': 'Bearer ' + configuration.apiKey,
      },
      body: JSON.stringify(Object.assign({}, payload, {
        add_to_collection: configuration.addToCollection,
      })),
    })
    const text = await response.text()
    let parsed = null
    try {
      parsed = JSON.parse(text)
    } catch (failure) {
      parsed = null
    }

    if (!response.ok) {
      // MeshDepot answers with an i18n key, and for these routes a sentence
      // after it. readError takes the sentence - the bare key was what someone
      // saw as "error.browser_import_no_files", which tells nobody anything.
      if (response.status === 409) {
        return { ok: false, error: 'This design is already in your library.' }
      }
      if (response.status === 401) {
        return { ok: false, error: 'MeshDepot rejected the API key. Check it in the options.' }
      }
      if (response.status === 403) {
        return { ok: false, error: 'MeshDepot refuses an API key over an unencrypted connection from '
          + 'outside its own network. Reach it over https, or from the same network as the server.' }
      }
      if (response.status === 404) {
        return { ok: false, error: 'This MeshDepot does not know the browser import. Update MeshDepot.' }
      }
      // The server was not allowed to fetch the files. MyMiniFactory is the case
      // that showed it: its download addresses answer 403 to anyone but the
      // browser that asked for them. So the browser fetches them - it has the
      // session - and sends the bytes instead of the links.
      return { ok: false, error: readError(parsed, response, text) }
    }

    const data = (parsed && parsed.data) || {}
    let message = 'Imported ' + (data.file_count || 0)
      + ((data.file_count === 1) ? ' file.' : ' files.')
    if (data.collection) {
      message += ' Filed under "' + data.collection + '".'
    }
    if (data.skipped_count) {
      // Named rather than glossed over: a link that expired means a plate is
      // missing from the design, and finding that out later is worse.
      message += ' ' + data.skipped_count + ' link(s) had already expired.'
    }
    return { ok: true, message: message }
  } catch (failure) {
    // A self-hosted instance is the normal case here, so an unreachable host or
    // a certificate the browser refuses is worth naming rather than hiding.
    return { ok: false, error: 'Could not reach MeshDepot: ' + failure.message }
  }
}

// ── Watching for the download the visitor starts ─────────────────────────────

// Said once when this script loads. An event page is started and stopped as the
// browser sees fit, and "no log line appeared" has two very different causes:
// the download was never reported, or nothing was listening. This tells them
// apart, and names the permission that decides it.
/**
 * What the extension last saw of the download machinery.
 *
 * Kept in storage and shown on the extension's own page rather than only logged.
 * A console in about:debugging is the wrong place to send someone: it is two
 * clicks off the beaten path, has to be open *before* the thing happens, and is
 * a different console from the one F12 gives. The answer belongs where the
 * question is asked.
 */
const DIAGNOSTICS_KEY = 'diagnostics'

async function noteDiagnostics(fields) {
  const stored = await browser.storage.local.get(DIAGNOSTICS_KEY)
  await browser.storage.local.set({
    [DIAGNOSTICS_KEY]: Object.assign({}, stored[DIAGNOSTICS_KEY] || {}, fields),
  })
}

noteDiagnostics({
  downloadsApi: typeof browser.downloads === 'undefined' ? 'missing'
    : (typeof browser.downloads.onCreated === 'undefined' ? 'incomplete' : 'available'),
})


/**
 * The tab whose import is waiting for a download.
 *
 * Kept in storage rather than in a variable, and that is not fussiness. This is
 * an event page: the browser unloads it whenever it has nothing to do, and a
 * module-level variable goes with it. The download then wakes the page up again,
 * finds nothing armed, and lets the file through - which is exactly the symptom
 * that led here.
 *
 * Only one tab at a time, and only while the panel is open. A listener armed
 * permanently would report every unrelated download the browser makes.
 */
const ARMED_TAB_KEY = 'armedTabIdentifier'

async function armedTab() {
  const stored = await browser.storage.local.get(ARMED_TAB_KEY)
  const identifier = stored[ARMED_TAB_KEY]
  return typeof identifier === 'number' ? identifier : null
}

async function setArmedTab(identifier) {
  if (identifier === null) await browser.storage.local.remove(ARMED_TAB_KEY)
  else await browser.storage.local.set({ [ARMED_TAB_KEY]: identifier })
}

/**
 * Reports a started download to the waiting tab.
 *
 * The URL is what matters: it is the presigned CDN link MakerWorld just issued
 * for this visitor's click, and MeshDepot can fetch it directly. blob: and data:
 * downloads are ignored - they exist only inside this browser and mean nothing
 * to the server. Those are covered by the other capture channel, which reads the
 * API response instead.
 */
/**
 * The address a download ends at, and what it is called there.
 *
 * Both come from one lookup, because both have the same problem: when a download
 * is first reported, Chrome has neither followed its redirects nor settled its
 * name.
 *
 * The address matters because a site's own is often only a doorway.
 * MyMiniFactory's /download/<id> is tied to the session that asked and refuses
 * this server, while the presigned S3 address it redirects to may be fetched by
 * anyone for the next four hours.
 *
 * The name matters because that doorway is a number: "231269" is what the panel
 * listed, and what the design would have been filed under had the CDN not stated
 * something better.
 */
async function settleDownload(item) {
  let url = item.finalUrl && item.finalUrl !== item.url ? item.finalUrl : ''
  let filename = baseName(item.filename)

  if (!url || !filename) {
    try {
      await new Promise(resolve => setTimeout(resolve, 400))
      const found = await browser.downloads.search({ id: item.id })
      const settled = found && found[0]
      if (settled) {
        url = url || settled.finalUrl || settled.url || ''
        filename = filename || baseName(settled.filename)
      }
    } catch (failure) {
      // No search, or the download is already gone: what is in hand stands.
    }
  }

  url = url || item.url || ''
  return { url: url, name: filename || nameFromAddress(url) }
}

/** The last segment of a path the browser reported, without its directories. */
function baseName(value) {
  return (value || '').split(/[\\/]/).pop() || ''
}

/**
 * The filename an address states.
 *
 * A presigned link usually carries one in response-content-disposition, and that
 * is the name the platform means. The path segment is the fallback, and on some
 * sites it is a number and nothing else.
 */
function nameFromAddress(rawUrl) {
  try {
    const address = new URL(rawUrl)
    const disposition = address.searchParams.get('response-content-disposition') || ''
    // filename*=utf-8''name.zip wins over filename=name.zip: it is the form that
    // survives non-ASCII.
    const encoded = /filename\*=(?:utf-8'')?([^;]+)/i.exec(disposition)
    const plain = /filename="?([^";]+)/i.exec(disposition)
    const stated = (encoded && encoded[1]) || (plain && plain[1])
    if (stated) return decodeURIComponent(stated.trim())
    return decodeURIComponent(baseName(address.pathname))
  } catch (failure) {
    return ''
  }
}


browser.downloads.onCreated.addListener(async item => {
  const tabIdentifier = await armedTab()
  const settled = await settleDownload(item)
  const url = settled.url
  // Noted before any of the conditions below, and on purpose: when a download
  // does not reach an import, the question is which of them turned it away. The
  // extension's own page shows the answer under "Downloads".
  const seenAt = new Date().toLocaleTimeString()

  if (tabIdentifier === null) {
    await noteDiagnostics({ lastDownload: seenAt + ' - seen, but no import was waiting' })
    return
  }
  // A blob: address exists only inside this browser, and there is no way to
  // those bytes: the server cannot fetch such an address, a content script
  // cannot read it, content.fetch does not exist in current Firefox, and the
  // sites that produce them refuse injected scripts. Said plainly rather than
  // treated as a failure - on Thingiverse, the one platform that does this, the
  // files were taken from the page's own links before any download started.
  if (/^blob:/i.test(url)) {
    await noteDiagnostics({ lastDownload: seenAt + ' - built in the browser, not usable' })
    return
  }
  if (!/^https?:/i.test(url)) {
    await noteDiagnostics({ lastDownload: seenAt + ' - seen, but its address is unusable (' + url.slice(0, 24) + '…)' })
    return
  }
  await noteDiagnostics({ lastDownload: seenAt + ' - captured' })

  const configuration = await settings()
  let cancelled = false
  if (configuration.cancelDownload) {
    // The point of the import is that the server fetches the file. Letting the
    // browser pull the same bytes to disk as well is waste the visitor did not
    // ask for - and the setting is there for whoever disagrees.
    try {
      await browser.downloads.cancel(item.id)
      await browser.downloads.erase({ id: item.id })
      cancelled = true
    } catch (failure) {
      // Already finished, or not cancellable. The panel says so on the file's
      // own row, which is where somebody would look.
    }
  }

  const downloadName = settled.name
  try {
    await browser.tabs.sendMessage(tabIdentifier, {
      kind: 'download-captured', url: url, name: downloadName, cancelled: cancelled,
    })
  } catch (failure) {
    // The tab is gone, or its content script is not listening.
    await noteDiagnostics({ lastDownload: seenAt + ' - captured, but the waiting tab did not answer' })
    await setArmedTab(null)
  }
})

// A tab that goes away takes its import with it. Without this the stored id
// would outlive the panel - and after a browser restart it would point at
// whatever tab happens to get that number next.
browser.tabs.onRemoved.addListener(async identifier => {
  if (await armedTab() === identifier) await setArmedTab(null)
})

// Opens the settings in a tab rather than a popup panel. A popup closes as soon
// as focus leaves it, and Firefox's permission prompt does exactly that - the
// request was cancelled before anyone could see it, and "Save" appeared to do
// nothing at all.
/**
 * Turns MeshDepot's answer into something worth reading.
 *
 * Errors arrive as an i18n key, optionally followed by a sentence:
 * "error.browser_import_no_files:None of the files could be downloaded…". The
 * sentence is what a person needs; the bare key is what they were shown before.
 */
function readError(parsed, response, text) {
  const raw = parsed && parsed.error ? String(parsed.error) : ''
  const colon = raw.indexOf(':')
  if (colon > 0 && colon < raw.length - 1) return raw.slice(colon + 1).trim()
  if (raw) return raw
  return 'MeshDepot answered HTTP ' + response.status + ': ' + text.slice(0, 160)
}

/**
 * The import contract this extension speaks.
 *
 * The two halves are updated separately - this one lives in a browser and
 * updates itself, MeshDepot is self-hosted and gets updated when its operator
 * gets round to it. Without comparing versions, that gap turns up as an import
 * that fails with nothing useful to say.
 *
 * Raise this together with BrowserImportAPIVersion in the server's
 * internal/api/browserimport.go.
 */
const IMPORT_API_VERSION = 1

/**
 * Compares this extension's contract with what the server offers.
 *
 * Both directions matter, and they need different advice: a server too old is
 * the operator's job, an extension too old is the reader's own. Saying only
 * "incompatible" would leave them guessing which.
 */
function versionVerdict(serverVersion, serverMinVersion) {
  if (typeof serverVersion !== 'number') {
    // No version at all: a MeshDepot from before this endpoint existed.
    return { ok: false, error: 'This MeshDepot is too old for the extension - it does not know '
      + 'the browser import yet. Update MeshDepot.' }
  }
  if (serverVersion < IMPORT_API_VERSION) {
    return { ok: false, error: 'This MeshDepot speaks import version ' + serverVersion
      + ', the extension needs ' + IMPORT_API_VERSION + '. Update MeshDepot.' }
  }
  if (typeof serverMinVersion === 'number' && serverMinVersion > IMPORT_API_VERSION) {
    return { ok: false, error: 'This MeshDepot needs import version ' + serverMinVersion
      + ' and the extension speaks ' + IMPORT_API_VERSION + '. Update the extension.' }
  }
  return { ok: true }
}

/**
 * Asks the instance whether it is reachable and the key still valid.
 *
 * Both in one call: a page that says "connected" because the host answered
 * would still fail the first import on a revoked key, and nobody would know why.
 * Reaching this route at all means the key passed MeshDepot's middleware.
 */
async function checkConnection() {
  const configuration = await settings()
  if (!configuration.instanceUrl) return { ok: false, error: 'No MeshDepot address configured.' }

  try {
    // The version first, and without the key: a server too old to know the
    // browser import at all would otherwise answer 404 to everything and be
    // reported as a bad key, sending someone off to fix the one thing that is
    // fine.
    const versionResponse = await fetch(configuration.instanceUrl + '/api/v1/version')
    if (versionResponse.status === 404) {
      return { ok: false, error: 'This MeshDepot is too old for the extension - it does not know '
        + 'the browser import yet. Update MeshDepot.' }
    }
    const versionBody = versionResponse.ok ? await versionResponse.json().catch(() => null) : null
    // One route answers for the whole server, so the entry for this client is
    // picked out of it rather than the route being specific to importing.
    const offered = (versionBody && versionBody.data && versionBody.data.browser_import) || {}
    const verdict = versionVerdict(offered.version, offered.min_version)
    if (!verdict.ok) return verdict

    if (!configuration.apiKey) return { ok: false, error: 'No API key configured.' }
    const response = await fetch(configuration.instanceUrl + '/api/v1/imports/browser', {
      method: 'GET',
      headers: { 'Authorization': 'Bearer ' + configuration.apiKey },
    })
    if (response.status === 401) {
      return { ok: false, error: 'MeshDepot rejected the API key. Create a new one and paste it in Settings. '
        + 'A key also stops working once it expires.' }
    }
    if (response.status === 403) {
      return { ok: false, error: 'This connection is not encrypted, and MeshDepot only accepts an API key '
        + 'over https or from its own network.' }
    }
    if (!response.ok) {
      return { ok: false, error: 'MeshDepot answered HTTP ' + response.status + '.' }
    }
    const parsed = await response.json().catch(() => null)
    return { ok: true, user: (parsed && parsed.data && parsed.data.user) || '' }
  } catch (failure) {
    // A self-hosted instance is the normal case, so an unreachable host, a
    // refused certificate or a missing host permission all land here and are
    // worth naming rather than hiding behind "offline".
    return { ok: false, error: 'Could not reach MeshDepot: ' + failure.message }
  }
}

/**
 * Registered in the form both browsers understand.
 *
 * Firefox lets a listener return a promise and answers with what it resolves to.
 * Chrome does not: there the answer goes through sendResponse, and the listener
 * has to return true to say one is coming. The second form works in Firefox as
 * well, so it is the one used.
 */
browser.runtime.onMessage.addListener((message, sender, sendResponse) => {
  handleMessage(message, sender).then(sendResponse)
  return true
})

async function handleMessage(message, sender) {
  if (!message || typeof message.kind !== 'string') return { ok: false, error: 'malformed message' }
  switch (message.kind) {
    case 'read-token':
      return { token: await makerworldToken() }
    case 'arm': {
      const identifier = sender && sender.tab ? sender.tab.id : null
      await setArmedTab(identifier)
      await noteDiagnostics({ armedTab: identifier === null ? 'not armed' : 'tab ' + identifier })
      return { ok: identifier !== null }
    }
    case 'disarm':
      await setArmedTab(null)
      return { ok: true }
    case 'check-connection':
      return checkConnection()
    case 'import':
      return sendToMeshDepot(message.payload)

    default:
      return { ok: false, error: 'unknown message kind' }
  }
}
