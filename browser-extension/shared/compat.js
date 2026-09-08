/**
 * One name for the extension APIs, in every browser.
 *
 * Firefox provides `browser` with promises; Chrome and Chromium provide
 * `chrome`, which in Manifest V3 also returns promises. The two are close enough
 * that the rest of this extension can be written once - it only needs the name
 * to exist.
 *
 * Assigned rather than declared: in Firefox `browser` is already there, and a
 * second declaration in the same scope would be an error.
 */
if (typeof globalThis.browser === 'undefined' && typeof globalThis.chrome !== 'undefined') {
  globalThis.browser = globalThis.chrome
}

/**
 * Whether this content script still belongs to a living extension.
 *
 * Reloading the extension - which happens constantly while developing, and once
 * per update afterwards - leaves the scripts already injected into open tabs
 * running with nothing behind them. Chrome then drops runtime.id, and every
 * later call throws "Extension context invalidated", repeatedly and in the
 * page's own console.
 *
 * The old script cannot repair itself; the tab has to be reloaded. What it can
 * do is fail quietly and get out of the way.
 */
function extensionGone() {
  try {
    return !browser || !browser.runtime || !browser.runtime.id
  } catch (failure) {
    return true
  }
}

/** sendMessage that answers null instead of throwing once the context is gone. */
async function sendToExtension(message) {
  if (extensionGone()) return null
  try {
    return await browser.runtime.sendMessage(message)
  } catch (failure) {
    return null
  }
}

/** Adds and removes a runtime listener, both no-ops once the context is gone. */
function listenToExtension(listener) {
  if (extensionGone()) return
  try {
    browser.runtime.onMessage.addListener(listener)
  } catch (failure) {
    // Nothing to listen to any more.
  }
}

function stopListeningToExtension(listener) {
  if (extensionGone()) return
  try {
    browser.runtime.onMessage.removeListener(listener)
  } catch (failure) {
    // As above.
  }
}
