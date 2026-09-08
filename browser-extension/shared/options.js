/**
 * The extension's own page: connection state on the front, settings behind a
 * button.
 *
 * It opens in a tab rather than a popup panel, and not by preference. Firefox
 * asks about host permission in a prompt of its own; a popup closes the moment
 * that prompt takes focus, which cancelled the request before anyone could see
 * it and made Save appear to do nothing.
 */

/**
 * Where this page is showing.
 *
 * As the toolbar panel it is a popup, and a popup cannot ask for a permission:
 * Firefox draws its prompt at the top of the window, behind the panel, and the
 * panel closes the moment focus moves - the request is cancelled before anyone
 * has seen it. So that one step, and only that one, happens in a tab.
 */
const runningInTab = new URLSearchParams(window.location.search).get('view') === 'tab'
if (runningInTab) document.body.classList.add('in-tab')

const mainView = document.getElementById('mainView')
const settingsView = document.getElementById('settingsView')
const instanceField = document.getElementById('instanceUrl')
const keyField = document.getElementById('apiKey')
const cancelDownloadField = document.getElementById('cancelDownload')
const addToCollectionField = document.getElementById('addToCollection')
const statusBox = document.getElementById('status')
const dot = document.getElementById('dot')
const siteDot = document.getElementById('siteDot')
const siteText = document.getElementById('siteText')
const siteDetail = document.getElementById('siteDetail')
const grantSitesButton = document.getElementById('grantSites')
const diagnosticsBox = document.getElementById('diagnostics')
const stateText = document.getElementById('stateText')
const stateDetail = document.getElementById('stateDetail')

/** How often the connection is re-checked while this page is open. */
const CHECK_INTERVAL_MILLISECONDS = 20000

function report(kind, text) {
  statusBox.className = kind
  statusBox.textContent = text
  statusBox.style.display = 'block'
  // Brought into view. The panel is only as tall as the browser allows, and a
  // message that appears below the fold reads as nothing having happened -
  // which is the one thing a Save button must never do.
  statusBox.scrollIntoView({ block: 'nearest' })
}

function showState(kind, text, detail) {
  dot.className = 'dot ' + kind
  stateText.textContent = text
  stateDetail.textContent = detail || ''
}

async function settings() {
  const stored = await browser.storage.local.get(['instanceUrl', 'apiKey', 'cancelDownload', 'addToCollection'])
  return {
    instanceUrl: (stored.instanceUrl || '').replace(/\/+$/, ''),
    apiKey: stored.apiKey || '',
    cancelDownload: stored.cancelDownload !== false,
    // Off unless switched on: filing designs somewhere the member did not ask
    // for is a change to their library, not a convenience.
    addToCollection: stored.addToCollection === true,
  }
}

// ── Connection ───────────────────────────────────────────────────────────────

/**
 * Asks MeshDepot whether it is there and whether the key is any good.
 *
 * Both questions in one call, deliberately: a page that reports "connected"
 * because the host answered would still fail the first import on a revoked key,
 * and the person would have no idea why.
 */
/**
 * The state currently on screen, so a repeat check can leave it alone.
 *
 * Without this the twenty-second poll flipped the line to "Checking…" and back
 * to "Connected" every time - motion that says nothing, on a panel someone is
 * reading. The display now changes only when the answer does.
 */
let shownState = null

function applyState(kind, text, detail) {
  const key = kind + '|' + text + '|' + (detail || '')
  if (key === shownState) return
  shownState = key
  showState(kind, text, detail)
}

async function checkConnection(options) {
  const announce = !(options && options.quiet)
  const configuration = await settings()

  if (!configuration.instanceUrl) {
    applyState('bad', 'Not configured', 'No MeshDepot address set. Open Settings to add one.')
    return
  }
  if (!configuration.apiKey) {
    applyState('bad', 'No API key',
      'Create one in MeshDepot under Account settings → API keys, then add it in Settings.')
    return
  }

  // Only the first look says "Checking…". A background poll waits and reports the
  // outcome, which is the only part worth showing.
  if (announce && shownState === null) applyState('busy', 'Checking…', configuration.instanceUrl)
  const answer = await browser.runtime.sendMessage({ kind: 'check-connection' })

  if (answer && answer.ok) {
    // The version is checked but not shown: when it fits there is nothing to say,
    // and a number on screen that never changes is one more thing to read past.
    applyState('good', 'Connected',
      configuration.instanceUrl + (answer.user ? ' · signed in as ' + answer.user : ''))
  } else {
    applyState('bad', 'Not connected', (answer && answer.error) || 'Unknown error.')
  }
}

/**
 * Asks Firefox for a set of origins, from wherever this page happens to be.
 *
 * Tried in the toolbar panel first, because one click is the right number. The
 * prompt is drawn at the top of the window rather than next to the panel, which
 * is awkward but workable - so it is announced first, and the caller is told
 * where to look.
 *
 * It can still fail: the panel closes when focus moves, and a request cancelled
 * that way comes back as an error or a plain false. Only then is the tab
 * offered, where the prompt is somewhere obvious.
 */
async function requestOrigins(origins, what) {
  if (!runningInTab) {
    report('info', 'Firefox is asking whether this extension may ' + what
      + '. The prompt appears at the top of the window - choose Allow. '
      + 'If you cannot see it, use the button below.')
    await new Promise(resolve => window.setTimeout(resolve, 50))
  }
  try {
    return { granted: await browser.permissions.request({ origins: origins }) }
  } catch (failure) {
    return { granted: false, failed: true }
  }
}

/** Opens the page in a tab so a prompt the panel could not show has room. */
async function openGrantTab(what) {
  await browser.tabs.create({ url: browser.runtime.getURL('options.html?view=tab&' + what + '=1') })
  window.close()
}

/**
 * Whether the extension may run on the model sites at all.
 *
 * Firefox treats host permissions in a Manifest V3 extension as optional. Until
 * they are granted no content script runs, so the Import button simply never
 * appears - with nothing on screen to explain why. Hence a state of its own,
 * next to the connection.
 */
async function checkSiteAccess() {
  const granted = await browser.permissions.contains({ origins: MESHDEPOT_SITE_ORIGINS })
  if (granted) {
    siteDot.className = 'dot good'
    siteText.textContent = 'Site access granted'
    siteDetail.textContent = 'MakerWorld, Printables, Thingiverse, MyMiniFactory.'
    grantSitesButton.style.display = 'none'
  } else {
    siteDot.className = 'dot bad'
    siteText.textContent = 'Site access missing'
    siteDetail.textContent = 'Firefox has not allowed this extension to run on the model sites yet, '
      + 'so the Import button will not appear on them.'
    grantSitesButton.style.display = 'block'
  }
  return granted
}

grantSitesButton.addEventListener('click', async () => {
  const outcome = await requestOrigins(MESHDEPOT_SITE_ORIGINS, 'run on the model sites')
  const granted = await checkSiteAccess()

  if (granted) {
    siteDetail.textContent = 'Granted. Reload any model page you already have open and the '
      + 'Import button appears.'
    return
  }
  if (outcome.failed && !runningInTab) {
    // The panel could not hold the prompt open. One button, and it says so.
    siteDetail.textContent = 'Firefox could not show the prompt here.'
    grantSitesButton.textContent = 'Open a tab to allow it'
    grantSitesButton.onclick = () => openGrantTab('grantSites')
    return
  }
  siteDetail.textContent = 'Declined. Without this the extension cannot run on those sites, '
    + 'and the Import button will not appear.'
})

/**
 * What the extension last saw of the download machinery.
 *
 * Shown here because the alternative is sending someone into the console in
 * about:debugging - two clicks off the beaten path, a different console from the
 * one F12 opens, and it has to be watching before the thing happens. When an
 * import waits for a download that never arrives, this line says which of the
 * three possible reasons it was.
 */
async function showDiagnostics() {
  const stored = await browser.storage.local.get('diagnostics')
  const diagnostics = stored.diagnostics || {}

  if (diagnostics.downloadsApi === 'missing') {
    diagnosticsBox.textContent = 'Firefox has not granted this extension access to downloads, '
      + 'so a started download cannot be picked up at all.'
    return
  }
  const parts = []
  parts.push(diagnostics.downloadsApi === 'available' ? 'Watching for downloads.' : 'Download access unclear.')
  parts.push(diagnostics.lastDownload
    ? 'Last one: ' + diagnostics.lastDownload + '.'
    : 'None seen yet.')
  diagnosticsBox.textContent = parts.join(' ')
}

// ── Views ────────────────────────────────────────────────────────────────────

document.getElementById('openSettings').addEventListener('click', async () => {
  const configuration = await settings()
  instanceField.value = configuration.instanceUrl
  keyField.value = configuration.apiKey
  cancelDownloadField.checked = configuration.cancelDownload
  addToCollectionField.checked = configuration.addToCollection
  // Cleared only when the panel is opened by hand; the permission tab puts its
  // own message here straight afterwards and must not lose it.
  if (!runningInTab) statusBox.style.display = 'none'
  mainView.classList.add('hidden')
  settingsView.classList.remove('hidden')
})

document.getElementById('closeSettings').addEventListener('click', () => {
  settingsView.classList.add('hidden')
  mainView.classList.remove('hidden')
  checkConnection({ quiet: true })
})

// ── Saving ───────────────────────────────────────────────────────────────────

/**
 * Makes sure the extension may reach the configured instance.
 *
 * Firefox's prompt is easy to miss - it appears at the top of the window, and
 * until it is answered nothing else happens - so it is announced beforehand and
 * both answers are reported afterwards.
 */
async function ensurePermission(instanceUrl) {
  // A match pattern may not carry a port, so "http://localhost:9000/*" would be
  // rejected as invalid rather than merely not match. Permission therefore
  // covers every port on that host.
  const address = new URL(instanceUrl)
  const origin = address.protocol + '//' + address.hostname + '/*'

  if (await browser.permissions.contains({ origins: [origin] })) {
    return { granted: true, origin: origin }
  }
  const outcome = await requestOrigins([origin], 'contact ' + address.hostname)
  return { granted: outcome.granted, failed: outcome.failed, origin: origin }
}

document.getElementById('save').addEventListener('click', async () => {
  const instanceUrl = instanceField.value.trim().replace(/\/+$/, '')
  const apiKey = keyField.value.trim()

  if (!instanceUrl) {
    report('bad', 'Enter the address of your MeshDepot instance, for example http://localhost:9000.')
    return
  }
  let outcome
  try {
    outcome = await ensurePermission(instanceUrl)
  } catch (failure) {
    report('bad', 'That is not a valid address. Use something like http://localhost:9000 '
      + '- with the scheme, without a path.')
    return
  }

  // Written before any prompt, so nothing typed here is lost either way.
  await browser.storage.local.set({
    instanceUrl: instanceUrl,
    apiKey: apiKey,
    cancelDownload: cancelDownloadField.checked,
    addToCollection: addToCollectionField.checked,
  })

  if (!outcome.granted && outcome.failed && !runningInTab) {
    report('bad', 'Saved, but Firefox could not show the permission prompt in this panel. '
      + 'Press Save again in the tab that opens.')
    await openGrantTab('grant')
    return
  }
  if (!outcome.granted) {
    report('bad', 'Permission for ' + outcome.origin + ' was declined. Saved anyway, but the '
      + 'extension cannot reach your instance until it is allowed. Press Save again to be asked once more.')
    return
  }

  // Every message below leads with what the button did. The connection check
  // that follows is useful, but it is not what was asked for by pressing Save -
  // reporting only its result read as though the settings had not been stored.
  if (!apiKey) {
    report('bad', 'Settings saved. There is no API key yet, so imports will fail - '
      + 'create one in MeshDepot under Account settings → API keys.')
    return
  }
  report('info', 'Settings saved. Checking the connection…')
  const answer = await browser.runtime.sendMessage({ kind: 'check-connection' })
  if (answer && answer.ok) {
    report('good', 'Settings saved. Connected to ' + instanceUrl
      + (answer.user ? ' as ' + answer.user + '.' : '.'))
  } else {
    report('bad', 'Settings saved, but MeshDepot did not answer: '
      + ((answer && answer.error) || 'unknown error'))
  }
})

// ── Start ────────────────────────────────────────────────────────────────────

// Opened as the permission tab: show the settings, already filled in, and ask for
// the one click that is still needed.
//
// Not clicked automatically, though it would be tempting: permissions.request()
// only works from a real user gesture, and a scripted click is refused outright.
// So the person presses Save, and this time the prompt is somewhere they can see
// it.
if (new URLSearchParams(window.location.search).get('grantSites') === '1') {
  report('info', 'Press the button below. Firefox will ask whether this extension may run on '
    + 'the model sites; the prompt appears at the top of this window. It could not ask inside '
    + 'the toolbar panel, which is why this tab opened.')
}

if (new URLSearchParams(window.location.search).get('grant') === '1') {
  document.getElementById('openSettings').click()
  report('info', 'Almost there - press Save below. Firefox will then ask whether this '
    + 'extension may contact your MeshDepot instance, and the prompt appears at the top '
    + 'of this window. It could not ask inside the toolbar panel, which is why this tab opened.')
}

checkSiteAccess()
checkConnection()
showDiagnostics()
window.setInterval(() => {
  // Only while the main view is showing: re-checking behind the settings form
  // would move the state under someone who is in the middle of editing it.
  if (!mainView.classList.contains('hidden')) {
    checkConnection({ quiet: true })
    checkSiteAccess()
    showDiagnostics()
  }
}, CHECK_INTERVAL_MILLISECONDS)
