/**
 * Formatting of the timestamps the API returns.
 *
 * Two things used to go wrong at every call site:
 *
 * 1. The backend stores SQLite's CURRENT_TIMESTAMP, i.e. `2026-07-28 12:34:56`
 *    in UTC and without a zone marker. `new Date()` reads such a string as
 *    *local* time, so the value was shown two hours off in Germany.
 * 2. `toLocaleString(undefined, …)` formats in the browser locale, not in the
 *    language the user picked in the app - a German UI would render an
 *    American date next to its German label.
 *
 * Both are fixed here once: parse as UTC, format in the UI language.
 */

/** Maps a UI language to the locale used for formatting. */
const LOCALES: Record<string, string> = { de: 'de-DE', en: 'en-GB' }

/**
 * Parses an API timestamp. Naive SQL timestamps (`YYYY-MM-DD HH:MM:SS`, no
 * zone) are UTC, so the marker is added before parsing; ISO strings that
 * already carry a zone are left alone. Returns null for empty/unparsable input.
 */
export function parseServerDate(value: string | null | undefined): Date | null {
  if (!value) return null
  let text = value.trim()
  if (/^\d{4}-\d{2}-\d{2} \d{2}:\d{2}(:\d{2})?$/.test(text)) text = text.replace(' ', 'T') + 'Z'
  const date = new Date(text)
  return isNaN(date.getTime()) ? null : date
}

/** Date and time, e.g. `28.07.2026, 14:34` in German. */
export function formatDateTime(value: string | null | undefined, lang: string): string {
  const date = parseServerDate(value)
  if (!date) return '-'
  return date.toLocaleString(LOCALES[lang] ?? 'en-GB', {
    day: '2-digit', month: '2-digit', year: 'numeric', hour: '2-digit', minute: '2-digit',
  })
}

/** Date only, e.g. `28.07.2026` in German. */
export function formatDate(value: string | null | undefined, lang: string): string {
  const date = parseServerDate(value)
  if (!date) return '-'
  return date.toLocaleDateString(LOCALES[lang] ?? 'en-GB', { day: '2-digit', month: '2-digit', year: 'numeric' })
}
