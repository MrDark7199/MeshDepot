import { PLATFORM_LABELS } from '../constants/platforms'

export const AUTH_ERROR_PREFIXES = [
  'error.platform_credentials_required:',
  'error.thingiverse_restricted:', 'error.thingiverse_no_token:', 'error.thingiverse_auth_failed:',
  'error.makerworld_no_token:', 'error.makerworld_auth_failed:',
  'error.myminifactory_no_token:', 'error.myminifactory_auth_failed:', 'error.myminifactory_restricted:',
  'error.cults3d_restricted:', 'error.cults3d_no_token:', 'error.cults3d_auth_failed:',
  'error.printables_no_token:', 'error.printables_auth_failed:',
]

export const isAuthErrorMessage = (msg?: string) => !!msg && AUTH_ERROR_PREFIXES.some(p => msg.startsWith(p))

/** Translates a backend error_msg string. Tries the error code as an i18n key first,
 *  falls back to the human-readable part after the first colon. */

export const makeTranslateError = (translate: (k: string, p?: any) => string) => (msg?: string): string => {
  if (!msg) return ''
  const idx = msg.indexOf(':')
  const code = idx >= 0 ? msg.slice(0, idx) : msg
  const rest = idx >= 0 ? msg.slice(idx + 1).trim() : ''
  // The part after the first colon is either a parameter (e.g. platform name for
  // key-based errors) or a ready-made human message (when no i18n key matches).
  // A platform arrives as its internal key; the label table gives it its name.
  const t = translate(code, { platform: PLATFORM_LABELS[rest] || rest })
  if (t !== code) return t
  return rest || msg
}
