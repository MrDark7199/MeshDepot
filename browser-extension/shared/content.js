/**
 * Runs on every supported model page: puts the button there and keeps it in step
 * with the page.
 *
 * The parts around it: platforms.js knows the sites, bridge.js reaches into the
 * page's world, panel.js is what the visitor sees.
 */

// ── The button ───────────────────────────────────────────────────────────────

const BUTTON_IDENTIFIER = 'meshdepot-import-button'

function removeButton() {
  const existing = document.getElementById(BUTTON_IDENTIFIER)
  if (existing) existing.remove()
}

function createButton() {
  const button = document.createElement('button')
  button.id = BUTTON_IDENTIFIER
  button.textContent = 'Import to MeshDepot'
  button.style.cssText = [
    'position:fixed', 'right:18px', 'bottom:18px', 'z-index:2147483646',
    'padding:10px 16px', 'border:none', 'border-radius:10px',
    'background:#457b9d', 'color:#fff', 'font-size:14px', 'font-weight:600',
    'cursor:pointer', 'box-shadow:0 4px 14px rgba(0,0,0,0.3)',
    'font-family:system-ui,-apple-system,sans-serif',
  ].join(';')

  button.addEventListener('click', () => {
    const current = meshdepotCurrentDesign()
    if (current) openPanel(current)
  })
  document.body.appendChild(button)
}

/**
 * Keeps the button in step with the page.
 *
 * MakerWorld is a single-page application: moving from the model list to a
 * design never reloads the document, so a one-off check at startup would put
 * the button on the wrong pages and miss the right ones. The URL is polled
 * rather than hooked into the history API, which is a great deal less to go
 * wrong for a check this cheap.
 */
let lastSeenDesign = null
function synchroniseButton() {
  // Reloading the extension leaves this script running with nothing behind it.
  // It cannot recover - the tab has to be reloaded - so it takes its own button
  // off the page rather than leaving one that does nothing.
  if (extensionGone()) {
    removeButton()
    removePanel()
    return
  }

  const current = meshdepotCurrentDesign()
  const key = meshdepotDesignKey(current)
  if (key === lastSeenDesign) return
  lastSeenDesign = key

  // Compared by design, not by address. These sites put their tabs in the URL,
  // so opening Files counted as leaving the page: the panel vanished mid-import,
  // and the import it had already scheduled went out unseen a few seconds later.
  // Switching tabs is exactly what someone does on the way to the download.
  removeButton()
  closePanel()
  if (!current) return
  createButton()

  // On every platform now, and as early as possible. The helper does two things
  // only the page itself can: it sees the site resolve its own download links,
  // and it holds on to a blob the page is about to revoke. Both are needed before
  // the visitor presses anything.
  // Failure is expected on a site that refuses injected scripts, and costs
  // nothing: the helper only adds an earlier way of catching a download.
  injectPageScript().catch(() => {})
}

// The helper goes in at once, before the site's own scripts run: it watches the
// site resolve its own download links, and a link seen going past never has to be
// asked for. Waiting for the button click would inject it long after those calls
// are done.
const startingDesign = meshdepotCurrentDesign()
if (startingDesign) {
  injectPageScript().catch(() => {})
}

// The button needs a body to hang on, which at document_start does not exist yet.
function startButtonSynchronisation() {
  synchroniseButton()
  window.setInterval(synchroniseButton, 800)
}
if (document.body) {
  startButtonSynchronisation()
} else {
  document.addEventListener('DOMContentLoaded', startButtonSynchronisation, { once: true })
}
