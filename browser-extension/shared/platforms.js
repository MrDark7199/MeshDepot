/**
 * The sites this extension works on, and how to read a design from each. Only
 * MakerWorld needs anything of its own; catching the download is the browser's
 * job and knows nothing about platforms.
 */

const MESHDEPOT_PLATFORMS = [
  {
    key: 'makerworld',
    label: 'MakerWorld',
    hostPattern: /(^|\.)makerworld\.com$/i,
    pathPattern: /\/models\/(\d+)/,
    useApi: true,
  },
  {
    key: 'printables',
    label: 'Printables',
    hostPattern: /(^|\.)printables\.com$/i,
    pathPattern: /\/model\/(\d+)/,
  },
  {
    key: 'thingiverse',
    label: 'Thingiverse',
    hostPattern: /(^|\.)thingiverse\.com$/i,
    pathPattern: /\/thing:(\d+)/,
    // "Download all files" builds the archive in the browser behind a blob:
    // address nobody else can read. /api/v2/things/<id>/complete lists every
    // file with a public CDN address instead; it needs a guest token from
    // /api/v2/auth/view, which is free for the asking. The browser fetches it
    // because Cloudflare challenges the server on those endpoints.
    fetchExtras: async identifier => {
      const guest = await fetch('/api/v2/auth/view', { credentials: 'include' })
      if (!guest.ok) {
        throw new Error('Thingiverse refused a guest token (HTTP ' + guest.status + ').')
      }
      const token = (await guest.json()).access
      if (typeof token !== 'string' || !token) {
        throw new Error('Thingiverse sent no guest token.')
      }

      const response = await fetch('/api/v2/things/' + identifier + '/complete', {
        credentials: 'include',
        headers: { 'Authorization': 'Bearer ' + token },
      })
      if (!response.ok) {
        throw new Error('Thingiverse answered HTTP ' + response.status + ' for the design.')
      }
      const complete = await response.json()
      const zipData = (complete && complete.zip_data) || {}
      if (!Array.isArray(zipData.files)) throw new Error('Thingiverse listed no files for this design.')

      // Pictures and creator come from the same answer: the page's Open Graph
      // tag and author meta name Thingiverse itself, not the design.
      const named = list => (Array.isArray(list) ? list : [])
        .filter(entry => entry && typeof entry.url === 'string' && entry.url)
        .map(entry => ({ url: entry.url, name: (entry.name || '').trim() }))

      const creator = complete.creator || {}
      const tags = Array.isArray(complete.tags) ? complete.tags : []

      return {
        files: named(zipData.files),
        images: named(zipData.images).map(image => image.url),
        meta: {
          name: (complete.name || '').trim(),
          author: (creator.name || '').trim(),
          description: complete.description_html || complete.details || '',
          license: (complete.license || '').trim(),
          tags: tags.map(tag => (tag && tag.name) || '').filter(Boolean),
        },
      }
    },
  },
  {
    key: 'myminifactory',
    label: 'MyMiniFactory',
    hostPattern: /(^|\.)myminifactory\.com$/i,
    pathPattern: /\/object\/([^/?#]+)/,
  },
]

/**
 * Firefox treats host permissions in MV3 as optional, so the extension has to
 * ask for them itself - and both sides need the same list to do that.
 */
const MESHDEPOT_SITE_ORIGINS = [
  'https://makerworld.com/*',
  'https://*.makerworld.com/*',
  'https://www.printables.com/*',
  'https://printables.com/*',
  'https://www.thingiverse.com/*',
  'https://thingiverse.com/*',
  'https://www.myminifactory.com/*',
  'https://myminifactory.com/*',
]

/** The platform and design id of the page currently open, or null. */
function meshdepotCurrentDesign() {
  for (const platform of MESHDEPOT_PLATFORMS) {
    if (!platform.hostPattern.test(window.location.hostname)) continue
    const match = platform.pathPattern.exec(window.location.pathname)
    if (match) return { platform: platform, identifier: match[1] }
  }
  return null
}

/** The first meta tag with the given property or name. */
function meshdepotMeta(names) {
  for (const name of names) {
    const element = document.querySelector('meta[property="' + name + '"], meta[name="' + name + '"]')
    if (element && element.content && element.content.trim()) return element.content.trim()
  }
  return ''
}

/**
 * schema.org data, which every one of these sites emits. Preferred over CSS
 * selectors: it is a contract the site keeps for search engines, and it survives
 * the redesigns that break selectors.
 */
function meshdepotStructuredData() {
  const found = {}
  for (const element of document.querySelectorAll('script[type="application/ld+json"]')) {
    let parsed
    try {
      parsed = JSON.parse(element.textContent)
    } catch (failure) {
      continue
    }
    const candidates = Array.isArray(parsed) ? parsed : [parsed, ...(parsed['@graph'] || [])]
    for (const entry of candidates) {
      if (!entry || typeof entry !== 'object') continue
      if (!found.name && typeof entry.name === 'string') found.name = entry.name
      if (!found.description && typeof entry.description === 'string') found.description = entry.description
      if (!found.author && (entry.author || entry.creator)) {
        const raw = entry.author || entry.creator
        const author = Array.isArray(raw) ? raw[0] : raw
        if (author && typeof author === 'object' && typeof author.name === 'string') found.author = author.name
        else if (typeof author === 'string') found.author = author
      }
      if (!found.license && typeof entry.license === 'string') found.license = entry.license
      if (!found.tags && entry.keywords) {
        found.tags = Array.isArray(entry.keywords)
          ? entry.keywords
          : String(entry.keywords).split(',').map(tag => tag.trim())
      }
      if (entry.image) {
        const list = Array.isArray(entry.image) ? entry.image : [entry.image]
        found.images = (found.images || []).concat(list.map(image =>
          typeof image === 'string' ? image : (image && image.url) || ''))
        if (!found.image) found.image = found.images.find(Boolean) || ''
      }
    }
  }
  return found
}

/**
 * The design's address, the same from every tab of it. The canonical link is not
 * usable: these sites put their tabs in the URL, and MeshDepot recognises a
 * design it already has by exactly this string - so /files and the description
 * tab imported the same model twice. Cut after the segment holding the id.
 */
function meshdepotDesignUrl(current) {
  const path = window.location.pathname
  const match = current.platform.pathPattern.exec(path)
  if (!match) return window.location.origin + path

  const endOfMatch = match.index + match[0].length
  const nextSlash = path.indexOf('/', endOfMatch)
  const kept = nextSlash === -1 ? path : path.slice(0, nextSlash)
  return window.location.origin + kept
}

/** Identifies the design being viewed, so a tab change is not mistaken for a new one. */
function meshdepotDesignKey(current) {
  return current ? current.platform.key + ':' + current.identifier : ''
}

/**
 * Three sources, because none is complete on its own. Order matters: MeshDepot
 * makes the first picture the design's cover.
 */
function meshdepotReadImages(structured) {
  const found = []
  const seen = new Set()
  // Pictures belonging to the site rather than the design - a library full of
  // platform logos as covers tells nobody what the models are.
  const siteFurniture = /\/site\/img\/|opengraph|favicon|\/logo|promo-|\/default\/|_next\/static\/media\//i

  const add = value => {
    if (typeof value !== 'string') return
    let url = value.trim()
    if (!url || url.startsWith('data:') || siteFurniture.test(url)) return
    try {
      url = new URL(url, window.location.href).href
    } catch (failure) {
      return
    }
    if (seen.has(url)) return
    seen.add(url)
    found.push(url)
  }

  for (const element of document.querySelectorAll('meta[property="og:image"], meta[name="og:image"]')) {
    add(element.content)
  }
  if (Array.isArray(structured.images)) structured.images.forEach(add)
  else add(structured.image)

  // Rendered size, not the attributes: a lazy gallery states no width, and an
  // avatar drawn at 40px is not a photo of the model whatever its file says.
  for (const image of document.querySelectorAll('img')) {
    const box = image.getBoundingClientRect()
    if (Math.min(box.width, box.height) < 160) continue
    add(image.currentSrc || image.src)
  }
  return found.slice(0, 24)
}

/**
 * og:description is a single truncated line written for search results, but it
 * is also the beginning of the real description - so it serves as a probe: find
 * where it appears in the page and take the block around it. That needs no class
 * names, which is what the previous attempt got wrong on Printables.
 */
function meshdepotReadDescription(fallback) {
  const flatten = text => (text || '').replace(/\s+/g, ' ').trim()
  const short = flatten(fallback).replace(/[.…\s]+$/, '')
  const best = meshdepotDescriptionAroundProbe(short) || meshdepotDescriptionByName()

  // Only when it genuinely beats the summary: a misfire must cost the long
  // version, never the short one.
  if (best && flatten(best).length > short.length) return meshdepotDropLeadingHeading(best, short)
  return fallback || ''
}

/**
 * The block found is usually the tab body, which opens with the word
 * "Description" - a label on the page, not the designer's first word.
 */
function meshdepotDropLeadingHeading(text, short) {
  const probe = short.slice(0, 40)
  if (probe.length < 20) return text

  // Cut at the description's own first words, not at a line break: whether a
  // heading brings one depends on the page's layout.
  const at = String(text).indexOf(probe)
  if (at > 0 && at <= 60) return String(text).slice(at)
  return text
}

/**
 * textContent rather than innerText: a description behind a "show more" is in
 * the document but not rendered, and innerText returns only the visible first
 * paragraph. The price is that paragraphs have to be rebuilt from the elements.
 */
function meshdepotBlockText(element) {
  const blocks = element.querySelectorAll('p, li, h1, h2, h3, h4, h5, h6, blockquote, pre, br')
  if (blocks.length === 0) return (element.textContent || '').trim()

  const pieces = []
  for (const block of blocks) {
    // Nested blocks would be emitted twice, once inside their parent.
    if (block.tagName !== 'BR' && block.querySelector('p, li, h1, h2, h3, h4, h5, h6, blockquote, pre')) continue
    const text = (block.textContent || '').replace(/\s+/g, ' ').trim()
    if (text) pieces.push(text)
  }
  return pieces.length ? pieces.join('\n\n') : (element.textContent || '').trim()
}

/**
 * Finds the block the summary was taken from, then widens it. The block starts
 * with the probe while a layout wrapper around it does not - on Printables that
 * wrapper opens with breadcrumbs, title and a Follow button. So the climb
 * continues only while the probe stays near the front.
 */
function meshdepotDescriptionAroundProbe(short) {
  const flatten = text => (text || '').replace(/\s+/g, ' ').trim()
  const probe = short.slice(0, 60)
  if (probe.length < 20) return ''

  // Enough for a heading, far short of a navigation trail.
  const MAXIMUM_PREAMBLE = 60
  const startsWithProbe = element => {
    const offset = flatten(element.textContent).indexOf(probe)
    return offset >= 0 && offset <= MAXIMUM_PREAMBLE
  }

  const candidates = []
  for (const element of document.body.querySelectorAll('p, div, section, article, span, pre, li')) {
    if (startsWithProbe(element)) candidates.push(element)
  }
  // The deepest ones: every ancestor matches too, and the climb starts here.
  const anchors = candidates.filter(element =>
    !candidates.some(other => other !== element && element.contains(other)))

  const isForeign = element => element.querySelector(
    'nav, footer, header, form, [class*="omment"], [class*="elated"], [class*="emix"], [class*="idebar"], '
    + '[class*="ction-bar"], [class*="ctions"], [class*="tats"], [class*="oolbar"]')

  // Every anchor, not just the first: a page carries the sentence twice - as a
  // summary at the top and at the head of the real description.
  let best = ''
  for (const anchor of anchors) {
    let widest = anchor
    let current = anchor.parentElement
    for (let step = 0; step < 8 && current && current !== document.body; step += 1) {
      const text = flatten(current.textContent)
      if (text.length > 20000 || isForeign(current) || !startsWithProbe(current)) break
      if (text.length > flatten(widest.textContent).length) widest = current
      current = current.parentElement
    }
    const text = meshdepotBlockText(widest)
    if (flatten(text).length > flatten(best).length) best = text
  }
  return best
}

/** The older route: a container that says in its own name what it holds. */
function meshdepotDescriptionByName() {
  let best = ''
  for (const element of document.querySelectorAll('[class*="escription"], [id*="escription"], [data-testid*="escription"]')) {
    if (element.querySelector('[class*="escription"], [id*="escription"]')) continue
    const text = meshdepotBlockText(element)
    if (text.length > best.length && text.length < 20000) best = text
  }
  return best
}

/**
 * Structured data, then meta tags, then the profile link - by its address, not
 * its text: on Printables that text read "6 mantisrobot @mantisrobot".
 */
function meshdepotReadAuthor(structured) {
  if (structured.author) return meshdepotCleanAuthor(structured.author)
  const fromMeta = meshdepotMeta(['author', 'article:author', 'og:author', 'twitter:creator'])
  if (fromMeta && !fromMeta.startsWith('@')) return meshdepotCleanAuthor(fromMeta)

  const handlePatterns = [
    /\/@([^/?#]+)/,            // printables.com/@name
    /\/social\/([^/?#]+)/,     // myminifactory.com/social/name
    /\/users\/([^/?#]+)/,      // a shape several sites use
  ]
  for (const link of document.querySelectorAll('a[href]')) {
    const href = link.getAttribute('href') || ''
    for (const pattern of handlePatterns) {
      const match = pattern.exec(href)
      if (match && match[1] && match[1].length <= 60) return decodeURIComponent(match[1])
    }
  }

  // Last resort: the text of a link marked as the author, tidied up.
  for (const link of document.querySelectorAll('[rel="author"]')) {
    const cleaned = meshdepotCleanAuthor(link.innerText)
    if (cleaned) return cleaned
  }
  return fromMeta ? meshdepotCleanAuthor(fromMeta) : ''
}

/**
 * A profile link often wraps a whole card, so its text arrives with the site's
 * furniture around it. An @handle is the surest thing present and wins;
 * otherwise the counters are dropped.
 */
function meshdepotCleanAuthor(value) {
  const tokens = String(value || '').split(/[\s\n]+/).map(token => token.trim()).filter(Boolean)

  const handle = tokens.find(token => token.startsWith('@') && token.length > 1)
  if (handle) return handle.slice(1).slice(0, 60)

  // "6", "1.2k", "3,400", "12M" - a count of something, not a person.
  const isCounter = token => /^[\d.,]+[kmKM]?$/.test(token)
  const words = tokens.filter(token => !isCounter(token))
  return words.join(' ').slice(0, 60)
}

/**
 * Everything MeshDepot needs about the design on this page: structured data
 * first, Open Graph second, the document title last.
 */
function meshdepotReadPageMetadata(current) {
  const structured = meshdepotStructuredData()
  const name = structured.name || meshdepotMeta(['og:title', 'twitter:title'])
    || document.title.replace(/\s*[|\-–]\s*[^|\-–]*$/, '').trim()

  const shortDescription = structured.description
    || meshdepotMeta(['og:description', 'description', 'twitter:description']) || ''
  const images = meshdepotReadImages(structured)

  return {
    source_url: meshdepotDesignUrl(current),
    platform: current.platform.key,
    meta: {
      // No source_id: MeshDepot derives it from the URL per platform, and a
      // second opinion from here could only disagree.
      name: name || '',
      author: meshdepotReadAuthor(structured),
      description: meshdepotReadDescription(shortDescription),
      tags: (structured.tags || []).filter(Boolean),
      license: structured.license || '',
      cover_url: images[0] || '',
      images: images,
    },
  }
}

