/**
 * Runs outside the page: reads the HttpOnly MakerWorld cookie, watches downloads,
 * and talks to MeshDepot with an API key (the instance's own session cookie is
 * SameSite=Strict and would not be sent from here).
 */

async function settings() {
  const stored = await browser.storage.local.get(['instanceUrl', 'apiKey', 'cancelDownload', 'addToCollection'])
  return {
    instanceUrl: (stored.instanceUrl || '').replace(/\/+$/, ''),
    apiKey: stored.apiKey || '',
    cancelDownload: stored.cancelDownload !== false,
    addToCollection: stored.addToCollection === true,
  }
}

async function makerworldToken() {
  try {
    const cookie = await browser.cookies.get({ url: 'https://makerworld.com/', name: 'token' })
    return cookie ? cookie.value : null
  } catch (failure) {
    return null
  }
}

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
      return { ok: false, error: readError(parsed, response, text) }
    }

    const data = (parsed && parsed.data) || {}
    let message = 'Imported ' + (data.file_count || 0)
      + ((data.file_count === 1) ? ' file.' : ' files.')
    if (data.collection) {
      message += ' Filed under "' + data.collection + '".'
    }
    if (data.skipped_count) {
      message += ' ' + data.skipped_count + ' link(s) had already expired.'
    }
    return { ok: true, message: message }
  } catch (failure) {
    return { ok: false, error: 'Could not reach MeshDepot: ' + failure.message }
  }
}

// - Watching for the download the visitor starts ---------------

/** What the extension last saw of the download machinery, shown on its own page. */
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
 * The tab whose import is waiting for a download. In storage rather than a
 * variable: this is an event page, and the browser unloads it between events.
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
 * Where a download ends and what it is called there. A site's own address is
 * often only a doorway tied to this session, while the address it redirects to
 * is the one MeshDepot can fetch - and it states a better name than "231269".
 * Chrome has followed neither when the download is first reported.
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

function baseName(value) {
  return (value || '').split(/[\\/]/).pop() || ''
}

/** The filename a presigned link states in response-content-disposition. */
function nameFromAddress(rawUrl) {
  try {
    const address = new URL(rawUrl)
    const disposition = address.searchParams.get('response-content-disposition') || ''
    // filename*=utf-8''name.zip wins: it is the form that survives non-ASCII.
    const encoded = /filename\*=(?:utf-8'')?([^;]+)/i.exec(disposition)
    const plain = /filename="?([^";]+)/i.exec(disposition)
    const stated = (encoded && encoded[1]) || (plain && plain[1])
    if (stated) return decodeURIComponent(stated.trim())
    return decodeURIComponent(baseName(address.pathname))
  } catch (failure) {
    return ''
  }
}


/** Cancels the download, or deletes the file when it already finished. */
async function stopDownload(identifier) {
  try {
    await browser.downloads.cancel(identifier)
    return true
  } catch (failure) {
    try {
      await browser.downloads.removeFile(identifier)
      return true
    } catch (removeFailure) {
      return false
    }
  }
}

browser.downloads.onCreated.addListener(async item => {
  const tabIdentifier = await armedTab()
  const seenAt = new Date().toLocaleTimeString()

  if (tabIdentifier === null) {
    await noteDiagnostics({ lastDownload: seenAt + ' - seen, but no import was waiting' })
    return
  }

  // Judged before anything is stopped: what the server cannot fetch afterwards
  // has to stay in the browser. A blob: address exists only here.
  const startingUrl = item.finalUrl || item.url || ''
  if (/^blob:/i.test(startingUrl)) {
    await noteDiagnostics({ lastDownload: seenAt + ' - built in the browser, not usable' })
    return
  }
  if (!/^https?:/i.test(startingUrl)) {
    await noteDiagnostics({ lastDownload: seenAt + ' - seen, but its address is unusable (' + startingUrl.slice(0, 24) + '…)' })
    return
  }

  // Stopped before the address is resolved: a small file finishes in less time
  // than that takes, and a late cancel leaves it on the disk.
  const configuration = await settings()
  const cancelled = configuration.cancelDownload ? await stopDownload(item.id) : false

  const settled = await settleDownload(item)
  const url = settled.url
  await noteDiagnostics({ lastDownload: seenAt + ' - captured' })
  if (cancelled) {
    try {
      await browser.downloads.erase({ id: item.id })
    } catch (failure) {
      // The list entry stays; the file is gone either way.
    }
  }

  const downloadName = settled.name
  try {
    await browser.tabs.sendMessage(tabIdentifier, {
      kind: 'download-captured', url: url, name: downloadName, cancelled: cancelled,
    })
  } catch (failure) {
    await noteDiagnostics({ lastDownload: seenAt + ' - captured, but the waiting tab did not answer' })
    await setArmedTab(null)
  }
})

browser.tabs.onRemoved.addListener(async identifier => {
  if (await armedTab() === identifier) await setArmedTab(null)
})

/**
 * MeshDepot sends an i18n key optionally followed by a sentence:
 * "error.browser_import_no_files:None of the files could be downloaded…".
 * The sentence is the part worth showing.
 */
function readError(parsed, response, text) {
  const raw = parsed && parsed.error ? String(parsed.error) : ''
  const colon = raw.indexOf(':')
  if (colon > 0 && colon < raw.length - 1) return raw.slice(colon + 1).trim()
  if (raw) return raw
  return 'MeshDepot answered HTTP ' + response.status + ': ' + text.slice(0, 160)
}

/** Raise together with BrowserImportAPIVersion in the server's internal/api/browserimport.go. */
const IMPORT_API_VERSION = 1

/** Both directions need different advice: an old server is the operator's job, an old extension the reader's. */
function versionVerdict(serverVersion, serverMinVersion) {
  if (typeof serverVersion !== 'number') {
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

/** Reachability and key validity in one go, so "connected" cannot mean a revoked key. */
async function checkConnection() {
  const configuration = await settings()
  if (!configuration.instanceUrl) return { ok: false, error: 'No MeshDepot address configured.' }

  try {
    // The version first and without the key: a server too old to know the import
    // answers 404 to everything, which would be reported as a bad key.
    const versionResponse = await fetch(configuration.instanceUrl + '/api/v1/version')
    if (versionResponse.status === 404) {
      return { ok: false, error: 'This MeshDepot is too old for the extension - it does not know '
        + 'the browser import yet. Update MeshDepot.' }
    }
    const versionBody = versionResponse.ok ? await versionResponse.json().catch(() => null) : null
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
    return { ok: false, error: 'Could not reach MeshDepot: ' + failure.message }
  }
}

// Chrome answers through sendResponse and needs the listener to return true;
// Firefox accepts that form too.
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
