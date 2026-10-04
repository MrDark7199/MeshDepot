import { onMount } from 'solid-js'

/** The height of the bar at the top of every view. One value, because the bar
 *  changes contents between the grid, a design and the edit screen - and a bar
 *  that also changes height makes the page jump on the way over. */
export const NAV_H = '70px'

/** The left and right margin of that bar, and of the page under it. Shared for
 *  the same reason as the height: the logo is in the same place on every view,
 *  rather than sliding sideways on the way between them. */
export const PAGE_X = '40px'

export const CARD_W = '280px'

export const CARD_H = '344px'
