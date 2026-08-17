/**
 * Extracts the message an API rejection carries.
 *
 * The backend returns errors as i18n keys (`error.internal`, `validation.…`),
 * and `api.request` rethrows them as `Error(key)`. Callers therefore have to
 * pass the result through `translate()` - otherwise the raw key ends up on
 * screen, which is exactly what used to happen in ServerSettings.tsx.
 */
export function errorKey(failure: unknown, fallback = 'error.server'): string {
  if (failure instanceof Error && failure.message) return failure.message
  if (typeof failure === 'string' && failure) return failure
  return fallback
}
