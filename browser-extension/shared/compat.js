/**
 * One name for the extension APIs in every browser: Firefox has `browser`,
 * Chrome has `chrome`, and in Manifest V3 both return promises. Assigned rather
 * than declared, because in Firefox the name already exists.
 */
if (typeof globalThis.browser === 'undefined' && typeof globalThis.chrome !== 'undefined') {
  globalThis.browser = globalThis.chrome
}

/**
 * Reloading the extension leaves the scripts already injected into open tabs
 * running with nothing behind them; Chrome drops runtime.id and every later call
 * throws into the page's console. Such a script cannot repair itself, so the
 * helpers below fail quietly and get out of the way.
 */
function extensionGone() {
  try {
    return !browser || !browser.runtime || !browser.runtime.id
  } catch (failure) {
    return true
  }
}

async function sendToExtension(message) {
  if (extensionGone()) return null
  try {
    return await browser.runtime.sendMessage(message)
  } catch (failure) {
    return null
  }
}

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
