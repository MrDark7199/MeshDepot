import { sanitizeHtml } from './sanitizeHtml'
import { PLATFORM_BASES } from '../constants/platforms'

/**
 * Rendering of a stored design description, shared by the detail view and the
 * public share page.
 *
 * Both pieces have to travel together: the fragment builder turns a plain-text
 * description into markup (line breaks, links), and the stylesheet gives that
 * markup - and the platform HTML that arrives for an imported design - its
 * spacing back. The global reset zeroes every margin, so without these rules a
 * description collapses into one unbroken block of text.
 */

export const descriptionCss = `
  .design-description img { max-width: 100%; height: auto; border-radius: 8px; }
  .design-description a { color: var(--accent-light); text-decoration: underline; }
  .design-description p { margin: 0 0 10px; }
  .design-description ul, .design-description ol { margin: 0 0 10px; padding-left: 20px; }
  .design-description li { margin-bottom: 4px; }
  .design-description strong, .design-description b { font-weight: 600; }
  .design-description h1, .design-description h2, .design-description h3,
  .design-description h4, .design-description h5, .design-description h6 { margin: 16px 0 8px; line-height: 1.3; }
  .design-description blockquote { margin: 0 0 10px; padding-left: 12px; border-left: 3px solid var(--border2); }
  .design-description code, .design-description pre { font-family: 'DM Mono', monospace; font-size: 0.92em; }
  .design-description pre { margin: 0 0 10px; white-space: pre-wrap; }
  .design-description hr { margin: 14px 0; border: none; border-top: 1px solid var(--border); }
  .design-description .design-video-link { display: inline-block; margin: 0 0 10px; padding: 9px 14px;
    background: var(--surface); border: 1px solid var(--border2); border-radius: 9px;
    color: var(--text2); text-decoration: none; font-weight: 600; }
  .design-description .design-video-link:hover { border-color: var(--accent); color: var(--accent-light); }
`

/**
 * Builds the description's DOM. Markup goes through the sanitizer as it is;
 * plain text is decoded, escaped and given its line breaks and links first,
 * because a description without any tags would otherwise render as a single
 * paragraph.
 */
export function buildDescriptionFragment(raw: string, platform?: string | null): DocumentFragment {
  if (/<[a-z][^>]*>/i.test(raw)) {
    return sanitizeHtml(raw, { linkBase: PLATFORM_BASES[platform ?? 'manual'] ?? '' })
  }
  const decoded = raw
    .replace(/&nbsp;/g, ' ')
    .replace(/&amp;/g, '&')
    .replace(/&lt;/g, '<')
    .replace(/&gt;/g, '>')
    .replace(/&quot;/g, '"')
    .replace(/&#39;/g, "'")
  const escaped = decoded
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/\n/g, '<br>')
    .replace(/ {2,}/g, '<br>')
  const linked = escaped.replace(
    /(https?:\/\/[^\s<>"]+)/g,
    '<a href="$1" target="_blank" rel="noopener noreferrer">$1</a>',
  )
  return sanitizeHtml(linked)
}
