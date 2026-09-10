/** Mirrors validHostname in backend/internal/api/designs.go. */
export const isResolvableHost = (host: string): boolean => {
  if (!host) return false
  // Bracketed IPv6 and plain IPv4 are addresses, not names.
  if (host.startsWith('[') || /^\d{1,3}(\.\d{1,3}){3}$/.test(host)) return true
  const labels = host.replace(/\.$/, '').split('.')
  if (labels.length < 2) return false
  if (!labels.every(label => label.length <= 63 && /^[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?$/.test(label))) return false
  return /^[a-zA-Z]{2,}$/.test(labels[labels.length - 1])
}

/**
 * The source url is optional; a filled one has to be an address that leads
 * somewhere, because it becomes the outgoing link and the sync's starting point.
 *
 * "http://afeefafefefaffefef" parses and has a host, which is why it used to
 * pass - but there is no such site. A registrable name is required instead.
 * The scheme may be left out: nobody types "https://" in front of an address
 * they copied, and refusing it for that reason is a rule the user has to learn
 * from an error message. Mirrors normalizeSourceURL in the backend.
 */
export const normalizeSourceUrl = (value: string): string | null => {
  const raw = (value || '').trim()
  if (!raw) return ''
  try {
    const parsed = new URL(raw.includes('://') ? raw : 'https://' + raw)
    if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') return null
    return isResolvableHost(parsed.hostname) ? parsed.toString() : null
  } catch { return null }
}
