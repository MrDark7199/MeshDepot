/**
 * The import panel and the notice it leaves behind.
 *
 * Everything the visitor sees while an import is running: the instructions, the
 * list of captured files, and the result once it has been sent. The panel is
 * deliberately the only place that talks to the background about arming and
 * disarming, so a half-torn-down panel cannot leave a listener behind.
 */

// ── The result notice ────────────────────────────────────────────────────────

const TOAST_IDENTIFIER = 'meshdepot-import-toast'

/**
 * Reports the outcome above the button and gets out of the way by itself.
 *
 * The panel used to stay open with the answer in it, waiting to be closed - so
 * importing a second design meant dismissing the first one's receipt first, for
 * no reason. The panel now closes when the sending is done and says what
 * happened here instead.
 *
 * A failure stays twice as long: it has something to read, and something to act
 * on.
 */
function showToast(message, kind) {
  const existing = document.getElementById(TOAST_IDENTIFIER)
  if (existing) existing.remove()

  const toast = document.createElement('div')
  toast.id = TOAST_IDENTIFIER
  toast.textContent = message
  toast.style.cssText = [
    'position:fixed', 'right:18px', 'bottom:70px', 'z-index:2147483647',
    'max-width:320px', 'padding:11px 14px', 'border-radius:10px',
    'background:' + (kind === 'bad' ? '#5c2626' : '#1e4028'),
    'color:#f2f4f7', 'border:1px solid ' + (kind === 'bad' ? '#8a3b3b' : '#2f6b45'),
    'font-family:system-ui,-apple-system,sans-serif', 'font-size:12px', 'line-height:1.5',
    'box-shadow:0 8px 24px rgba(0,0,0,0.4)', 'cursor:pointer', 'white-space:pre-wrap',
  ].join(';')

  const dismiss = () => toast.remove()
  toast.addEventListener('click', dismiss)
  window.setTimeout(dismiss, kind === 'bad' ? 14000 : 7000)
  document.body.appendChild(toast)
}

const PANEL_IDENTIFIER = 'meshdepot-import-panel'

/**
 * How long to wait after the last captured download before sending.
 *
 * Long enough for a visitor to click the next plate, short enough that a single
 * file does not feel like it was forgotten.
 */
const SEND_DELAY_MILLISECONDS = 3000

/** How long a scheduled send waits for the design details before giving up. */
const METADATA_WAIT_MILLISECONDS = 30000
function removePanel() {
  const existing = document.getElementById(PANEL_IDENTIFIER)
  if (existing) existing.remove()
}

/**
 * Shuts the open panel down properly: timer stopped, listeners removed, the
 * background disarmed.
 *
 * Taking the panel out of the page is not enough, and that was the bug. An
 * import already scheduled kept its timer, its two capture listeners stayed
 * registered and the background stayed armed - so the invisible panel sent its
 * import a few seconds later, the visible one sent its own, and MeshDepot
 * received the same design twice.
 */
let activePanelShutdown = null
function closePanel() {
  if (activePanelShutdown) {
    const shutdown = activePanelShutdown
    activePanelShutdown = null
    shutdown()
    return
  }
  removePanel()
}

/**
 * The guided import.
 *
 * The visitor presses the site's own download button and the extension watches
 * what comes of it. That is deliberate rather than merely convenient: the
 * request is then part of a real click, with whatever headers, parameters and
 * captcha the site wants, and MakerWorld's rate limiting has nothing to object
 * to. Reconstructing the same call from the outside is what ran into 403 and
 * then 418.
 *
 * Two channels report a file, and both are needed. page.js reads the API
 * response, which knows which instance it belongs to; the background script sees
 * the download itself, which also catches the cases that never pass through
 * fetch. Duplicates are folded by URL.
 */
/**
 * What was read off the page, in one line.
 *
 * Shown because a gap here is otherwise silent: a design that arrives without a
 * creator or with a one-line description looks like MeshDepot lost something,
 * when in fact the page never offered it. Named before the import rather than
 * discovered afterwards.
 */
function describeMetadata(base) {
  const meta = base.meta || {}
  const parts = []
  parts.push(meta.name ? 'name' : 'no name')
  parts.push(meta.author ? 'creator' : 'no creator')
  const description = (meta.description || '').length
  parts.push(description > 200 ? 'description' : description > 0 ? 'short description' : 'no description')
  const images = (meta.images || []).length || (meta.cover_url ? 1 : 0)
  parts.push(images === 1 ? '1 picture' : images + ' pictures')
  if ((meta.tags || []).length) parts.push(meta.tags.length + ' tags')
  return 'Read from the page: ' + parts.join(' · ')
}

/** What the visitor has to do next, in the words of the site they are on. */
function describeNextStep(current, base, alreadyFound) {
  if (typeof current.platform.fetchExtras === 'function') {
    return alreadyFound > 0
      ? current.platform.label + ' listed ' + alreadyFound + (alreadyFound === 1 ? ' file' : ' files')
        + ' - sending them to MeshDepot. Nothing to download here.'
      : current.platform.label + ' listed no files for this design.'
  }
  // The count is only known when MakerWorld's API answered; without it the
  // sentence simply does not promise a number.
  if (current.platform.key === 'makerworld' && typeof base.instance_count === 'number') {
    const plates = base.instance_count === 1 ? '1 file' : base.instance_count + ' files'
    return 'Now press "Download 3MF" on the page for the files you want (' + plates
      + ' available). Solve the captcha if MakerWorld asks. Everything you download appears below.'
  }
  return 'Now press the download button on ' + current.platform.label
    + ' for the files you want. Everything you download appears below, and the import '
    + 'is sent by itself once you stop.'
}

function openPanel(current) {
  // Never two at once: each has its own listeners and its own pending send.
  closePanel()

  const panel = document.createElement('div')
  panel.id = PANEL_IDENTIFIER
  panel.style.cssText = [
    'position:fixed', 'right:18px', 'bottom:18px', 'z-index:2147483647',
    'width:320px', 'padding:14px 16px', 'border-radius:12px',
    'background:#1d2229', 'color:#f2f4f7', 'box-shadow:0 10px 30px rgba(0,0,0,0.45)',
    'font-family:system-ui,-apple-system,sans-serif', 'font-size:13px', 'line-height:1.5',
  ].join(';')

  const title = document.createElement('div')
  title.textContent = 'Import to MeshDepot'
  title.style.cssText = 'font-weight:700;font-size:14px;margin-bottom:8px'

  const instruction = document.createElement('div')
  instruction.style.cssText = 'margin-bottom:10px;color:#c8cfd8'
  instruction.textContent = 'Reading the design…'

  const metaLine = document.createElement('div')
  metaLine.style.cssText = 'margin-bottom:10px;font-size:11px;color:#98a2ad;line-height:1.5'

  const list = document.createElement('div')
  list.style.cssText = 'margin-bottom:12px;max-height:150px;overflow:auto;font-size:12px'

  const actions = document.createElement('div')
  actions.style.cssText = 'display:flex;gap:8px'

  const startButton = document.createElement('button')
  startButton.textContent = 'Start import'
  startButton.style.cssText = [
    'flex:1', 'padding:8px', 'border:none', 'border-radius:8px', 'background:#457b9d',
    'color:#fff', 'font-size:13px', 'font-weight:600', 'cursor:pointer',
  ].join(';')

  const closeButton = document.createElement('button')
  closeButton.textContent = 'Cancel'
  closeButton.style.cssText = [
    'padding:8px 12px', 'border:1px solid #3a424c', 'border-radius:8px',
    'background:transparent', 'color:#c8cfd8', 'font-size:13px', 'cursor:pointer',
  ].join(';')

  actions.appendChild(startButton)
  actions.appendChild(closeButton)
  panel.appendChild(title)
  panel.appendChild(instruction)
  panel.appendChild(metaLine)
  panel.appendChild(list)
  panel.appendChild(actions)
  document.body.appendChild(panel)

  // Keyed by URL: the same file reported by both channels must count once.
  const captured = new Map()
  let payloadBase = null

  const redrawList = () => {
    list.textContent = ''
    for (const file of captured.values()) {
      const row = document.createElement('div')
      // Said out loud when the local download could not be stopped: the file is
      // then in the Downloads folder after all, and finding that out by accident
      // later is worse than a word here.
      row.textContent = (file.cancelled === false ? '✓ ' : '✓ ') + file.name
        + (file.cancelled === false ? '  (also saved locally)' : '')
      row.style.cssText = 'color:' + (file.cancelled === false ? '#e0c169' : '#8fd694')
        + ';white-space:nowrap;overflow:hidden;text-overflow:ellipsis'
      list.appendChild(row)
    }
  }

  /**
   * Records one captured file.
   *
   * Both channels report the same download, so entries are folded by URL. Which
   * name survives is not arbitrary: the API channel fires first but only knows
   * the instance number, while the download itself carries the name MakerWorld
   * actually gave the file. That one wins whenever it arrives, because it is the
   * name the file will keep in the library.
   */
  const note = (url, name, authoritative, cancelled) => {
    if (!url) return
    const existing = captured.get(url)
    if (existing && !(authoritative && !existing.authoritative)) return
    captured.set(url, {
      name: name || 'file.3mf', url: url,
      authoritative: !!authoritative,
      // Only meaningful from the download channel; the API channel never starts
      // one, so undefined there means "nothing to cancel", not "failed".
      cancelled: cancelled,
    })
    redrawList()
    scheduleSend()
  }

  /**
   * Takes the download links the page is already showing.
   *
   * Where a platform offers them - Thingiverse does, one per file - there is
   * nothing to press: the links go straight to the server, no download runs in
   * the browser, and nothing can be built into a blob nobody can read.
   */
  async function collectExtras(design) {
    if (typeof design.platform.fetchExtras !== 'function') return 0
    const extras = await design.platform.fetchExtras(design.identifier)

    for (const file of extras.files || []) note(file.url, file.name || 'file', false)

    // What the platform states beats what the page shows, field by field, and
    // only where it actually says something. The page's author tag names the
    // site itself and its Open Graph picture is the site's house image - both
    // look like answers and are not.
    if (payloadBase && extras.meta) {
      for (const field of ['name', 'author', 'description', 'license']) {
        const value = extras.meta[field]
        if (typeof value === 'string' && value.trim() !== '') payloadBase.meta[field] = value
      }
      if (Array.isArray(extras.meta.tags) && extras.meta.tags.length > 0) {
        payloadBase.meta.tags = extras.meta.tags
      }
    }
    if (payloadBase && Array.isArray(extras.images) && extras.images.length > 0) {
      payloadBase.meta.images = mergeImages(extras.images, payloadBase.meta.images)
      payloadBase.meta.cover_url = payloadBase.meta.images[0] || ''
    }
    return (extras.files || []).length
  }

  // Channel one: the API response, seen by page.js.
  function pageListener(event) {
    if (event.source !== window) return
    const message = event.data
    if (!message || message.source !== REPLY_MARKER || message.kind !== 'captured') return
    note(message.url, 'instance ' + message.instanceId + '.3mf', false)
  }
  window.addEventListener('message', pageListener)

  // Channel two: the download itself, seen by the background script.
  function runtimeListener(message) {
    if (!message) return
    if (message.kind === 'download-captured') note(message.url, message.name, true, message.cancelled)
  }

  listenToExtension(runtimeListener)

  const shutDown = () => {
    if (sendTimer !== null) window.clearTimeout(sendTimer)
    sent = true
    window.removeEventListener('message', pageListener)
    stopListeningToExtension(runtimeListener)
    sendToExtension({ kind: 'disarm' })
    removePanel()
  }
  activePanelShutdown = shutDown

  closeButton.addEventListener('click', () => {
    activePanelShutdown = null
    shutDown()
  })

  /**
   * Sends everything captured so far, once the clicking has stopped.
   *
   * There is no send button: a download is the visitor saying they want this
   * file, and asking them to confirm it again is a click for nothing. The short
   * delay is what makes that safe - a model with five plates is five downloads
   * in a row, and each one restarts the timer, so they arrive as one import
   * rather than as one design and four duplicates.
   */
  let sendTimer = null
  let sent = false
  // Set when reading the design failed, so a scheduled send stops waiting for
  // something that is never coming. The reason is kept with it: a generic
  // "could not be read" replaced the actual message and hid what went wrong.
  let metadataFailed = false
  let metadataFailure = ''
  let waitingSince = Date.now()

  const scheduleSend = () => {
    if (sent) return
    if (sendTimer !== null) window.clearTimeout(sendTimer)
    // No count in the wording. The list below names every file as it arrives,
    // which is accurate; a number here was only ever a guess at how many more
    // are still coming.
    instruction.style.color = '#c8cfd8'
    instruction.textContent = 'Sending shortly - download another file to include it.'
    sendTimer = window.setTimeout(send, SEND_DELAY_MILLISECONDS)
  }

  async function send() {
    if (sent || captured.size === 0) return
    // The metadata call may still be in flight on a slow connection; without it
    // the design would arrive nameless. Bounded, though: when reading the design
    // fails outright the wait would otherwise never end, and the panel sat on
    // "Waiting for the design details…" for as long as it was left open.
    if (payloadBase === null) {
      // Reading falls back to the page and should always yield something, so
      // this is a net rather than an expected path.
      if (metadataFailed) {
        instruction.style.color = '#f08a8a'
        instruction.textContent = 'Nothing was sent - the design could not be read: ' + metadataFailure
        sent = true
        return
      }
      if (Date.now() - waitingSince > METADATA_WAIT_MILLISECONDS) {
        instruction.style.color = '#f08a8a'
        instruction.textContent = 'The design details did not arrive in time, so nothing was sent.'
        sent = true
        return
      }
      instruction.textContent = 'Waiting for the design details…'
      sendTimer = window.setTimeout(send, 500)
      return
    }
    sent = true
    instruction.style.color = '#c8cfd8'
    instruction.textContent = 'Sending to MeshDepot…'

    const payload = Object.assign({}, payloadBase, {
      files: Array.from(captured.values()).map(file => ({ name: file.name, url: file.url })),
    })
    let message = ''
    let kind = 'good'
    try {
      const answer = await sendToExtension({ kind: 'import', payload: payload })
      kind = answer && answer.ok ? 'good' : 'bad'
      message = answer && answer.ok
        ? (answer.message || 'Imported into MeshDepot.')
        // No answer at all means the extension was reloaded under this page.
        : 'Failed: ' + ((answer && answer.error)
          || (extensionGone() ? 'the extension was reloaded - reload this page and try again'
            : 'unknown error'))
    } catch (failure) {
      kind = 'bad'
      message = 'Failed: ' + failure.message
    }

    // Panel first, notice second: the button underneath is free again at once,
    // so a second design can be imported without dismissing the first receipt.
    closePanel()
    showToast(message, kind)
  }

  /**
   * Everything the import does, once it has been asked for.
   *
   * Nothing above this line touches the page beyond drawing the panel: the
   * button is easy to hit by accident on a page full of controls, and until this
   * runs no request has gone out, nothing is armed and no design is read.
   */
  function beginImport() {
    startButton.remove()
    instruction.textContent = 'Starting…'

    // Armed before the instruction is shown, so a very quick click is not missed.
    sendToExtension({ kind: 'arm' })

    readMetadata(current)
      .then(base => {
        payloadBase = base
        metaLine.textContent = describeMetadata(base)
        redrawList()

        if (typeof current.platform.fetchExtras !== 'function') {
          instruction.textContent = describeNextStep(current, base, 0)
          return
        }
        instruction.textContent = 'Asking ' + current.platform.label + ' for the file list…'
        // Caught here rather than falling through to the metadata handler below:
        // the design was read perfectly well, it is the file list that failed, and
        // saying otherwise sends someone looking in the wrong place.
        return collectExtras(current).then(count => {
          instruction.textContent = describeNextStep(current, base, count)
          metaLine.textContent = describeMetadata(payloadBase)
          redrawList()
        }).catch(failure => {
          instruction.style.color = '#f08a8a'
          instruction.textContent = 'The file list could not be fetched: ' + failure.message
        })
      })
      .catch(failure => {
        metadataFailed = true
        metadataFailure = failure.message
        instruction.style.color = '#f08a8a'
        instruction.textContent = 'Could not read the design: ' + failure.message
      })
  }

  // The question, and nothing else has happened yet. The button sits on a page
  // full of the site's own controls and is easy to hit by accident.
  const designName = currentDesignName()
  instruction.textContent = designName
    ? 'Import "' + designName + '" into MeshDepot?'
    : 'Import this design into MeshDepot?'
  startButton.addEventListener('click', beginImport)
}




/** The design's name as the page states it, for the question in the panel. */
function currentDesignName() {
  const heading = document.querySelector('h1')
  const fromHeading = heading ? (heading.textContent || '').trim() : ''
  if (fromHeading && fromHeading.length <= 80) return fromHeading
  const fromMeta = meshdepotMeta(['og:title', 'twitter:title'])
  return fromMeta.length <= 80 ? fromMeta : fromMeta.slice(0, 77) + '…'
}
