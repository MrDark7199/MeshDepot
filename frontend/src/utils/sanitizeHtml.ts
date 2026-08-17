// HTML sanitization for untrusted, server-stored content (platform design
// descriptions). The previous approach was a chain of regex replacements on the
// raw string, which is fundamentally bypassable: the on-event stripper missed
// unquoted and single-quoted handlers (e.g. an img tag with an onerror handler),
// the script strip missed svg-wrapped and broken/nested tags, and a
// "javascript:" scheme could be obfuscated with embedded control characters or
// entities. A stored description therefore let one user run script in another
// user's session (stored XSS).
//
// Instead we parse the input into a real DOM and rebuild it, keeping only an
// allowlist of tags and attributes. Anything not on the list is dropped. This is
// dependency-free and runs only in the browser (the app is a client-side SPA;
// there is no SSR), so DOMParser is always available.

// Tags that may survive sanitization. Unknown-but-harmless tags are unwrapped
// (children kept); dangerous tags are dropped with their content (see below).
const ALLOWED_TAGS = new Set([
  'a', 'b', 'strong', 'i', 'em', 'u', 's', 'br', 'p', 'span', 'div',
  'ul', 'ol', 'li', 'blockquote', 'code', 'pre',
  'h1', 'h2', 'h3', 'h4', 'h5', 'h6', 'img', 'hr', 'table', 'thead',
  'tbody', 'tr', 'td', 'th',
])

// Tags whose entire subtree is discarded (never valid display content).
const DROP_WITH_CONTENT = new Set([
  'script', 'style', 'iframe', 'object', 'embed', 'link', 'meta',
  'head', 'title', 'svg', 'math',
])

// Per-tag attribute allowlist. Attributes not listed are stripped; this removes
// every on* event handler and style/srcset-style vectors by omission.
const ALLOWED_ATTRS: Record<string, Set<string>> = {
  a: new Set(['href']),
  img: new Set(['src', 'alt']),
}

// Hosts whose images may be loaded directly. A design description is untrusted
// third-party content, so an arbitrary <img src> is a tracking pixel: it reports
// every viewer's IP, user agent and view time to whoever wrote the description.
// Images from the platform the design came from are what users actually want to
// see, so those stay; everything else is turned into a plain link.
const IMAGE_HOST_ALLOWLIST = [
  'printables.com', 'thingiverse.com', 'makerworld.com', 'cults3d.com',
  'myminifactory.com', 'thangs.com',
]

/** True if the image URL points at (a subdomain of) an allowlisted platform host. */
function isAllowedImageHost(url: string): boolean {
  let host: string
  try {
    host = new URL(url, window.location.origin).hostname.toLowerCase()
  } catch {
    return false
  }
  if (host === window.location.hostname.toLowerCase()) return true
  return IMAGE_HOST_ALLOWLIST.some(allowed => host === allowed || host.endsWith('.' + allowed))
}

// Removes whitespace and C0 control characters (code points <= 0x20) from a URL
// before scheme-checking, so control characters cannot be used to smuggle a
// disallowed scheme (e.g. a tab inside "javascript:") past the filter. Done via
// char codes to avoid an unverifiable control-character regex literal.
function stripControlChars(value: string): string {
  let result = ''
  for (let index = 0; index < value.length; index++) {
    if (value.charCodeAt(index) > 0x20) result += value[index]
  }
  return result
}

/** True if a URL is safe to keep as an href/src (http(s), or site-relative). */
function isSafeUrl(value: string, allowRelative: boolean): boolean {
  const trimmed = value.trim()
  if (trimmed === '') return false
  const cleaned = stripControlChars(trimmed).toLowerCase()
  if (cleaned.startsWith('http://') || cleaned.startsWith('https://')) return true
  if (allowRelative && (trimmed.startsWith('/') || trimmed.startsWith('#'))) return true
  // Anything carrying a scheme we did not explicitly allow (javascript:, data:,
  // vbscript:, ...) is rejected. Schemeless relative refs are allowed only when
  // allowRelative is set.
  return allowRelative && !/^[a-z][a-z0-9+.-]*:/i.test(cleaned)
}

/** Escapes text so it is rendered literally, never parsed as HTML. */
export function escapeHtml(value: string): string {
  return value
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;')
}

interface SanitizeOptions {
  /** Base URL to prefix onto site-relative links/images (platform host). */
  linkBase?: string
}

/**
 * Sanitizes an untrusted HTML fragment down to an allowlist of tags and
 * attributes and returns the result as a ready-to-insert DOM fragment. Links are
 * forced to open in a new tab with rel="noopener noreferrer"; site-relative
 * links/images are rewritten against linkBase when given.
 *
 * Returning nodes rather than a string is deliberate: serializing the rebuilt
 * tree back to HTML and letting the caller re-parse it is the classic mXSS
 * setup, where the second parse produces a different tree than the one that was
 * checked. Callers attach the fragment directly instead.
 */
export function sanitizeHtml(html: string, options: SanitizeOptions = {}): DocumentFragment {
  const base = options.linkBase ?? ''
  const doc = new DOMParser().parseFromString(html, 'text/html')

  const clean = (node: Node): Node[] => {
    if (node.nodeType === Node.TEXT_NODE) {
      return [node.cloneNode(false)]
    }
    if (node.nodeType !== Node.ELEMENT_NODE) {
      return []
    }
    const element = node as Element
    const tag = element.tagName.toLowerCase()

    if (DROP_WITH_CONTENT.has(tag)) return []

    const cleanChildren: Node[] = []
    element.childNodes.forEach(child => {
      cleanChildren.push(...clean(child))
    })

    if (!ALLOWED_TAGS.has(tag)) {
      // Unknown but harmless tag (e.g. font, center): unwrap, keep children.
      return cleanChildren
    }

    const output = document.createElement(tag)
    const allowedAttrs = ALLOWED_ATTRS[tag]
    if (allowedAttrs) {
      for (const attr of Array.from(element.attributes)) {
        const name = attr.name.toLowerCase()
        if (!allowedAttrs.has(name)) continue
        let value = attr.value
        if (name === 'href' || name === 'src') {
          if (!isSafeUrl(value, true)) continue
          // Rewrite site-relative URLs onto the platform base.
          if (base && value.startsWith('/')) value = base + value
        }
        if (tag === 'img' && name === 'src' && !isAllowedImageHost(value)) {
          // Off-platform image: do not fetch it, offer it as a link instead.
          const link = document.createElement('a')
          link.setAttribute('href', value)
          link.setAttribute('target', '_blank')
          link.setAttribute('rel', 'noopener noreferrer')
          link.textContent = element.getAttribute('alt') || value
          return [link]
        }
        output.setAttribute(name, value)
      }
    }
    if (tag === 'a') {
      output.setAttribute('target', '_blank')
      output.setAttribute('rel', 'noopener noreferrer')
    }
    if (tag === 'img') {
      // Allowlisted host, but still third-party content: do not leak the
      // referrer, and never block rendering on it.
      output.setAttribute('referrerpolicy', 'no-referrer')
      output.setAttribute('loading', 'lazy')
    }
    cleanChildren.forEach(child => output.appendChild(child))
    return [output]
  }

  const fragment = document.createDocumentFragment()
  doc.body.childNodes.forEach(child => {
    clean(child).forEach(node => fragment.appendChild(node))
  })
  return fragment
}
