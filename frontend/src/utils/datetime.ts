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
 *
 * On top of that the account may pick a notation of its own. That preference is
 * module state rather than an argument, because it has to reach every one of
 * the several dozen call sites, and threading it through each of them is how
 * half of them would end up not getting it.
 */

/** Maps a UI language to the locale used for formatting. */
const LOCALES: Record<string, string> = { de: 'de-DE', en: 'en-GB' }

/**
 * The notations offered in the account settings.
 *
 * The empty pattern is the default and means "whatever the display language
 * uses" - the behaviour everybody had before this preference existed, and the
 * only value that changes when the language changes.
 *
 * Kept in step with validDateFormats in backend/internal/api/users.go.
 */
export const DATE_FORMATS = ['', 'DD.MM.YYYY', 'DD/MM/YYYY', 'MM/DD/YYYY', 'YYYY-MM-DD', 'D MMM YYYY', 'MMM D, YYYY'] as const

export type DateFormat = (typeof DATE_FORMATS)[number]

/** The notations offered in the dropdown - every one but the default. */
export const SELECTABLE_DATE_FORMATS = DATE_FORMATS.filter(pattern => pattern !== '')

/**
 * The pattern a display language renders on its own.
 *
 * The settings form preselects this for an account that has never chosen one,
 * so the dropdown can show notations and nothing else: an entry reading "follow
 * the language" would render exactly the same date as the fixed entry below it
 * and leave the reader wondering what the difference is.
 */
export function languageDatePattern(lang: string): string {
  return lang === 'de' ? 'DD.MM.YYYY' : 'DD/MM/YYYY'
}

/**
 * The account's chosen notation, '' until one is applied.
 *
 * Set from the user record on login and on every reload, next to the custom
 * CSS, so it is in place before the first date is painted.
 */
let currentFormat: string = ''

/** Applies the account's notation. An unknown value falls back to the default. */
export function setDateFormat(format: string | null | undefined): void {
  currentFormat = (DATE_FORMATS as readonly string[]).includes(format || '') ? (format || '') : ''
}

/** The notation in force, for the settings form to preselect. */
export function dateFormat(): string {
  return currentFormat
}

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

const pad = (value: number) => String(value).padStart(2, '0')

/** The short month name in the UI language, e.g. `Jul` or `Jan.`. */
function shortMonth(date: Date, lang: string): string {
  return date.toLocaleDateString(LOCALES[lang] ?? 'en-GB', { month: 'short' })
}

/**
 * Writes a date in the given notation.
 *
 * The patterns are rendered by hand rather than mapped onto Intl options: a
 * chosen notation has to look the same in both languages, and Intl would
 * reorder `MM/DD/YYYY` into the German order the moment the interface is
 * switched to German.
 */
function applyPattern(date: Date, pattern: string, lang: string): string {
  const day = date.getDate()
  const month = date.getMonth() + 1
  const year = date.getFullYear()
  switch (pattern) {
    case 'DD.MM.YYYY':  return `${pad(day)}.${pad(month)}.${year}`
    case 'DD/MM/YYYY':  return `${pad(day)}/${pad(month)}/${year}`
    case 'MM/DD/YYYY':  return `${pad(month)}/${pad(day)}/${year}`
    case 'YYYY-MM-DD':  return `${year}-${pad(month)}-${pad(day)}`
    case 'D MMM YYYY':  return `${day} ${shortMonth(date, lang)} ${year}`
    case 'MMM D, YYYY': return `${shortMonth(date, lang)} ${day}, ${year}`
    default:
      return date.toLocaleDateString(LOCALES[lang] ?? 'en-GB', { day: '2-digit', month: '2-digit', year: 'numeric' })
  }
}

/** The time of day, always 24-hour. Both interface languages write it that way. */
function applyTime(date: Date): string {
  return `${pad(date.getHours())}:${pad(date.getMinutes())}`
}

/** Date and time, e.g. `28.07.2026, 14:34` in German. */
export function formatDateTime(value: string | null | undefined, lang: string): string {
  const date = parseServerDate(value)
  if (!date) return '-'
  if (currentFormat === '') {
    return date.toLocaleString(LOCALES[lang] ?? 'en-GB', {
      day: '2-digit', month: '2-digit', year: 'numeric', hour: '2-digit', minute: '2-digit',
    })
  }
  return `${applyPattern(date, currentFormat, lang)}, ${applyTime(date)}`
}

/** Date only, e.g. `28.07.2026` in German. */
export function formatDate(value: string | null | undefined, lang: string): string {
  const date = parseServerDate(value)
  if (!date) return '-'
  return applyPattern(date, currentFormat, lang)
}

/**
 * A sample date in one notation, for the dropdown in the account settings.
 *
 * A fixed day is used, and one whose parts cannot be mistaken for one another:
 * with the 3rd of April every entry in the list would read `03/04` or `04/03`
 * and the reader would still not know which way round it is.
 */
export function formatDateExample(pattern: string, lang: string): string {
  return applyPattern(new Date(2026, 10, 25), pattern, lang)
}
