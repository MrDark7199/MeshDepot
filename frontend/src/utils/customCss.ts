/** The id of the style element the account's own stylesheet lives in. */
const STYLE_ELEMENT_ID = 'meshdepot-custom-css'

/**
 * Puts the account's stylesheet into the document, replacing whatever was there.
 *
 * It used to be written only when the appearance form was saved, which meant it
 * was gone after the next reload while the text was still sitting in the form -
 * the setting looked kept and behaved as if it were not. It now comes with the
 * session user, so the same call runs on login and on every page load.
 *
 * Note for anyone writing one: the theme variables are set as inline styles on
 * <html>, and a stylesheet rule only wins over those with !important.
 */
export function applyCustomCss(css: string) {
  let element = document.getElementById(STYLE_ELEMENT_ID) as HTMLStyleElement | null
  if (!css) {
    element?.remove()
    return
  }
  if (!element) {
    element = document.createElement('style')
    element.id = STYLE_ELEMENT_ID
    document.head.appendChild(element)
  }
  element.textContent = css
}
