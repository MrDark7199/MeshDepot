import { on } from 'solid-js'
import type { DesignID, SyncStep } from '../types'

export const GRADIENTS = [
  ['#1a1a2e','#16213e','#0f3460'],['#2d1b33','#11998e','#38ef7d'],
  ['#0f0c29','#302b63','#24243e'],['#1f1c2c','#928dab'],
  ['#0f2027','#203a43','#2c5364'],['#141e30','#243b55'],['#200122','#6f0000'],
]

// - Scale: 25% larger than previous baseline -----------------

/**
 * Opens a design's detail view in a new browser tab/window via the
 * ?design=<id> deep link (used by middle-click on cards and rows).
 */
export const openDesignInNewWindow = (id: DesignID) =>
  window.open(`${window.location.pathname}?design=${encodeURIComponent(id)}`, '_blank', 'noopener')

/**
 * Picks one of the placeholder gradients for a design that has no cover.
 *
 * It used to be `id % GRADIENTS.length`, which the sequential rowid made an
 * even spread. The public id is a hex string, so its first characters stand in
 * for the number: what matters is only that the same design always gets the
 * same colour.
 */

export const gradientFor = (id: DesignID) =>
  GRADIENTS[parseInt(id.slice(0, 6), 16) % GRADIENTS.length] ?? GRADIENTS[0]

/**
 * Thumbnail card for a single design in the main grid.
 * Shows the cover image (or a gradient fallback), platform badge, rating stars,
 * shared/source-gone indicators, up to three tag chips, and an optional
 * sync-status overlay during a Sync-All operation.
 * Hovering reveals quick-action buttons (View, Sync).
 */
// Turns the running sync phase (current_step) into a label; null when no phase
// is known, which leaves the display on "syncing… %".

export function syncStepLabel(translate: (k: any, p?: any) => string, s?: SyncStep): string | null {
  if (!s || !s.step) return null
  const label = translate(`sync_step_${s.step}` as any)
  return s.tot > 0 ? `${label} (${s.cur}/${s.tot})` : label
}
