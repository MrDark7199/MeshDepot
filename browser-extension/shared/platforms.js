/**
 * The sites this extension works on, and how to read a design from each.
 *
 * Only MakerWorld needs anything of its own. Its download links are produced by
 * an API call that can be read from the page, and its metadata is better taken
 * from the same API than from the markup. Everywhere else the generic reader
 * below is enough - and the part that actually matters, catching the download,
 * is the browser's own downloads API and knows nothing about platforms at all.
 *
 * Loaded into the page's world alongside page.js, so both can use it.
 */

const MESHDEPOT_PLATFORMS = [
  {
    key: 'makerworld',
    label: 'MakerWorld',
    hostPattern: /(^|\.)makerworld\.com$/i,
    // /models/123456 - the id is what the API is addressed by.
    pathPattern: /\/models\/(\d+)/,
    // The site's own API answers with title, author, tags, cover and the list of
    // instances; richer and steadier than anything scraped from the markup.
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
    // Thingiverse needs its file list fetched, and with a token of the site's
    // own.
    //
    // "Download all files" builds the archive in the browser and hands out a
    // blob: address - which the server cannot fetch, a content script cannot
    // read, and the page revokes moments later. The file names in the markup
    // carry no address at all; they are rendered from data the page fetched.
    //
    // That data is behind /api/v2/things/<id>/complete, which answers 401 to a
    // bare request and 200 with a guest token from /api/v2/auth/view - free for
    // the asking, no account involved. Inside it, zip_data.files names every file
    // with a direct CDN address that needs no token at all.
    //
    // So the browser asks and the server fetches, each doing the part only it
    // can: the server is challenged by Cloudflare on those endpoints, and the
    // browser has no reason to download files whose addresses are public.
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

      // The pictures come from the same answer, and they are the design's own -
      // the page's Open Graph tag names Thingiverse's house image, which is
      // branding rather than a photo of the model.
      const named = list => (Array.isArray(list) ? list : [])
        .filter(entry => entry && typeof entry.url === 'string' && entry.url)
        .map(entry => ({ url: entry.url, name: (entry.name || '').trim() }))

      const creator = complete.creator || {}
      const tags = Array.isArray(complete.tags) ? complete.tags : []

      return {
        files: named(zipData.files),
        images: named(zipData.images).map(image => image.url),
        // Taken from the same answer for the same reason as the pictures: the
        // page's author meta tag names Thingiverse itself, not whoever made the
        // design.
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
 * The match patterns for every supported site, as one list.
 *
 * Firefox treats host permissions in a Manifest V3 extension as optional: until
 * they are granted the content scripts do not run at all, and the button never
 * appears. So the extension has to be able to ask for them itself, and both the
 * page and the background need the same list to do that.
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
 * schema.org data, which every one of these sites emits in some form.
 *
 * Preferred over CSS selectors because it is a contract the site keeps for
 * search engines: a redesign moves the markup around, the structured data
 * survives it. Selectors written against a particular page break the week
 * somebody changes a class name.
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
 * The design's address, the same one from every tab of it.
 *
 * Derived from the path rather than taken from the canonical link, because these
 * sites number their tabs into the URL: opening Files on Printables makes it
 * /model/123-name/files, and the canonical link follows. MeshDepot recognises a
 * design it already has by exactly this string, so a description tab and a files
 * tab reporting two different addresses import the same model twice - which is
 * what they did.
 *
 * The path is cut after the segment holding the id, so everything below it -
 * files, comments, remixes, makes - collapses onto the design itself.
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
 * Every picture on the page that looks like one of the model's own.
 *
 * Three sources, because no single one is complete. Open Graph often carries
 * only the cover; structured data sometimes has the gallery; and what is
 * actually on screen has all of it but needs a size threshold, since a page is
 * also full of avatars, icons and badges.
 *
 * Order matters: the cover comes first, because MeshDepot makes the first
 * picture the design's cover.
 */
function meshdepotReadImages(structured) {
  const found = []
  const seen = new Set()
  // Pictures that belong to the site rather than to the design: its logo, its
  // Open Graph house image, favicons, promo banners, placeholder thumbnails. A
  // design imported with the platform's own branding as its cover looks wrong in
  // a library and tells nobody what the model is.
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

  // Rendered size rather than the attributes: a lazy-loaded gallery often has no
  // width in the markup, and an avatar that is 300px in the file is still drawn
  // at 40 and should not count as a photo of the model.
  for (const image of document.querySelectorAll('img')) {
    const box = image.getBoundingClientRect()
    if (Math.min(box.width, box.height) < 160) continue
    add(image.currentSrc || image.src)
  }
  return found.slice(0, 24)
}

/**
 * The description as a reader sees it, not the one-line summary.
 *
 * og:description is written for search results: a single truncated line. Taking
 * it means importing a design whose description stops mid-sentence.
 *
 * The trick is that the short version is almost always the beginning of the long
 * one. So the short text becomes a probe: find where it appears in the page, and
 * the description is the block around it. That needs no class names and no
 * knowledge of any particular site, which is what the previous attempt depended
 * on - and Printables does not name its containers the way that assumed.
 */
function meshdepotReadDescription(fallback) {
  const flatten = text => (text || '').replace(/\s+/g, ' ').trim()
  const short = flatten(fallback).replace(/[.…\s]+$/, '')
  const best = meshdepotDescriptionAroundProbe(short) || meshdepotDescriptionByName()

  // Only when it genuinely beats the summary. A heuristic that misfires should
  // cost the long version, never the short one.
  if (best && flatten(best).length > short.length) return meshdepotDropLeadingHeading(best, short)
  return fallback || ''
}

/**
 * Drops a heading the block carried in front of the text.
 *
 * The container found is usually the tab body, and the tab body starts with the
 * word "Description". Harmless but wrong: it is a label on the page, not the
 * first word of what the designer wrote. Removed only when what follows really
 * is the description, so nothing can be cut off by accident.
 */
function meshdepotDropLeadingHeading(text, short) {
  const probe = short.slice(0, 40)
  if (probe.length < 20) return text

  // Cut at the description's own first words rather than at a line break. A
  // heading may or may not bring a newline with it - that depends on how the
  // page lays it out - and a rule that needs one works in the browser and not in
  // a test, or the other way round.
  const at = String(text).indexOf(probe)
  if (at > 0 && at <= 60) return String(text).slice(at)
  return text
}

/**
 * The text of an element, including anything it is currently hiding.
 *
 * innerText is what a reader sees, and that turned out to be the trap: a long
 * description behind a "show more" is in the document but not rendered, so
 * innerText returns the visible first paragraph and nothing else. textContent
 * has it all.
 *
 * The price is that textContent puts no breaks between blocks, so the paragraphs
 * are rebuilt here from the elements themselves. That keeps a description
 * readable rather than delivering it as one run-on wall.
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
 * Finds the block of text the summary was taken from, then widens it.
 *
 * The summary is the *beginning* of the description, and that is the whole
 * lever: the block being looked for starts with it, while a layout wrapper
 * around it does not. On Printables the wrapper opens with breadcrumbs, the
 * title, the author card and a Follow button - some 110 characters before the
 * sentence - and climbing into it produced exactly that mess, while still
 * missing the paragraphs further down.
 *
 * So the climb continues only while the probe stays near the front. A heading
 * like "Description" inside the block is tolerated; a page header is not.
 */
function meshdepotDescriptionAroundProbe(short) {
  const flatten = text => (text || '').replace(/\s+/g, ' ').trim()
  const probe = short.slice(0, 60)
  if (probe.length < 20) return ''

  // How much may stand before the description begins. Enough for a heading,
  // far short of a navigation trail.
  const MAXIMUM_PREAMBLE = 60
  // textContent, not innerText: see meshdepotBlockText above. A collapsed
  // description is invisible to innerText, which is how the first paragraph and
  // the surrounding buttons ended up looking like the whole thing.
  const startsWithProbe = element => {
    const offset = flatten(element.textContent).indexOf(probe)
    return offset >= 0 && offset <= MAXIMUM_PREAMBLE
  }

  const candidates = []
  for (const element of document.body.querySelectorAll('p, div, section, article, span, pre, li')) {
    if (startsWithProbe(element)) candidates.push(element)
  }
  // The deepest ones: every ancestor matches as well, and the paragraph itself is
  // where a climb should start.
  const anchors = candidates.filter(element =>
    !candidates.some(other => other !== element && element.contains(other)))

  const isForeign = element => element.querySelector(
    'nav, footer, header, form, [class*="omment"], [class*="elated"], [class*="emix"], [class*="idebar"], '
    + '[class*="ction-bar"], [class*="ctions"], [class*="tats"], [class*="oolbar"]')

  // Every anchor is climbed, not just the first: a page can carry the sentence
  // twice - once as a summary at the top, once at the head of the real
  // description - and the first one in document order is the wrong one.
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
 * Who made it.
 *
 * Structured data first, then the usual meta tags, then the link to the
 * creator's profile - and from that link's address, not its text. The text
 * around such a link carries whatever the site puts there: on Printables it read
 * "6 mantisrobot @mantisrobot", the follower count and the handle included. The
 * address says mantisrobot and nothing else.
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
 * Reduces whatever was found to a name.
 *
 * A profile link often wraps a whole card, so its text arrives as the name with
 * the site's furniture around it - Printables gave "6 mantisrobot @mantisrobot",
 * the follower count and the handle included, on one line as often as on three.
 *
 * An @handle in there is the surest thing present, so it wins. Otherwise the
 * counters are dropped and what remains is the name.
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
 * Everything MeshDepot needs about the design on this page.
 *
 * Structured data first, Open Graph second, the document title last. Tags are
 * sent as they come: decoding escaped entities and folding the casing are
 * MeshDepot's job, which already does both on every other import path.
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
      // Deliberately no source_id: MeshDepot derives it from the URL, per
      // platform, with code that is already right. A second opinion from here
      // could only disagree - and on one platform it initially did.
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

