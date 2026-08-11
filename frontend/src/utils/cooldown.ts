import { createSignal, onCleanup } from 'solid-js'

/** How long a manual sync blocks the next one (api.manualSyncCooldown). */
export const MANUAL_SYNC_COOLDOWN_SECONDS = 600

/**
 * Countdown for the manual sync cooldown.
 *
 * The server refuses a second "sync now" within {@link MANUAL_SYNC_COOLDOWN_SECONDS},
 * so the button has to stop offering a call that only comes back as an error -
 * a burst of triggered syncs is what gets a platform account blocked. The ticker
 * only runs while a cooldown is pending, so an idle page stays idle.
 */
export function createCooldown() {
  const [remaining, setRemaining] = createSignal(0)
  let ticker: ReturnType<typeof setInterval> | undefined
  const stop = () => { if (ticker !== undefined) { clearInterval(ticker); ticker = undefined } }
  onCleanup(stop)
  const start = (seconds: number) => {
    if (seconds <= 0) return
    setRemaining(seconds)
    if (ticker !== undefined) return
    ticker = setInterval(() => setRemaining(left => {
      if (left <= 1) { stop(); return 0 }
      return left - 1
    }), 1000)
  }
  /**
   * Starts the countdown and remembers when it runs out, so reopening the
   * dialog restores it instead of offering a button the server refuses. The
   * deadline is per key (the account), because the cooldown is too.
   */
  const startPersisted = (key: string, seconds: number) => {
    localStorage.setItem(deadlineKey(key), String(Date.now() + seconds * 1000))
    start(seconds)
  }

  /** Picks the countdown up again after a reload; returns the seconds left. */
  const resume = (key: string) => {
    const stored = Number(localStorage.getItem(deadlineKey(key)) || 0)
    const left = Math.ceil((stored - Date.now()) / 1000)
    if (left <= 0) {
      localStorage.removeItem(deadlineKey(key))
      return 0
    }
    start(left)
    return left
  }

  return {
    remaining,
    /** Whole minutes left, for the "available again in {min} min" hint. */
    minutes: () => Math.ceil(remaining() / 60),
    start,
    startPersisted,
    resume,
  }
}

const deadlineKey = (key: string) => `meshdepot_sync_cooldown_${key}`
