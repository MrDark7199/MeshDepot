/**
 * Helpers for controls that navigate inside the app but should still behave
 * like links to the browser.
 *
 * A `<div onClick>` or `<button onClick>` cannot be middle-clicked into a new
 * tab: middle click does not fire `click` at all, and Ctrl/Cmd-click has no
 * meaning without an `href`. Rendering an `<a href>` and cancelling only the
 * plain left click gives the browser back everything it normally does with a
 * link, while the app keeps handling in-page navigation itself.
 */

/** The app's own URL without the ?design= deep link, i.e. the grid. */
export const gridHref = () => window.location.pathname

/**
 * True when the browser should be left to handle this click itself: middle or
 * right button, or any modifier that means "open somewhere else". The caller
 * returns early on true instead of calling preventDefault.
 */
export function browserHandlesClick(event: MouseEvent): boolean {
  return event.button !== 0 || event.ctrlKey || event.metaKey || event.shiftKey || event.altKey
}
