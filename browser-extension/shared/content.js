/**
 * Runs on every supported model page and puts the button there. platforms.js
 * knows the sites, bridge.js reaches into the page's world, panel.js is what the
 * visitor sees.
 */

// - The button --------------------------------

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
 * These sites are single-page applications, so moving to a design never reloads
 * the document. Polling the URL is far less to go wrong than hooking the history
 * API for a check this cheap.
 */
let lastSeenDesign = null
function synchroniseButton() {
  if (extensionGone()) {
    removeButton()
    removePanel()
    return
  }

  const current = meshdepotCurrentDesign()
  const key = meshdepotDesignKey(current)
  if (key === lastSeenDesign) return
  lastSeenDesign = key

  // Compared by design, not by address: these sites put their tabs in the URL,
  // so opening Files counted as leaving the page and tore down a running import.
  removeButton()
  closePanel()
  if (!current) return
  createButton()

  injectPageScript().catch(() => {})
}

// At once, before the site's own scripts run: the helper watches the site resolve
// its download links, and a link seen going past never has to be asked for.
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
