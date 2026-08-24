const express    = require('express')
const crypto     = require('crypto')
const { firefox } = require('playwright')

const app  = express()
app.use(express.json())

// Derived from PLAYWRIGHT_URL - the same variable the Go app uses to find us -
// so the two ends cannot disagree about host or port. The value has no default
// here either: it is set once in backend/Dockerfile, and a fallback would just
// be a second place to keep in sync.
const LOOPBACK = '127.0.0.1'

function fatal(message) {
    console.error('[playwright] ' + message)
    process.exit(1)
}

function listenTarget() {
    const raw = (process.env.PLAYWRIGHT_URL || '').trim()
    if (!raw) fatal('PLAYWRIGHT_URL is not set - it comes from backend/Dockerfile; do not override it with an empty value')

    let parsed
    try {
        parsed = new URL(raw)
    } catch {
        fatal('PLAYWRIGHT_URL is not a URL: ' + raw)
    }

    const host = parsed.hostname.replace(/^\[|\]$/g, '')
    const port = parseInt(parsed.port, 10)
    if (!host || !port) fatal('PLAYWRIGHT_URL needs an explicit host and port, got: ' + raw)

    // Pointing the app at a resolver elsewhere is allowed; binding a remote host
    // is not, and would leave the entrypoint respawning us on EADDRNOTAVAIL
    // forever. Loopback is also the only sensible interface: this service takes
    // credentials and hands back session cookies.
    const isLocal = ['localhost', '0.0.0.0', '::', '::1'].includes(host) || /^127\./.test(host)
    if (!isLocal) {
        console.warn('[playwright] PLAYWRIGHT_URL points at ' + host + ', not this container - listening on ' + LOOPBACK + ':' + port + ' instead')
        return { host: LOOPBACK, port }
    }
    return { host, port }
}

const { host: HOST, port: PORT } = listenTarget()

// Shared-secret authentication. Every request must carry
// `Authorization: Bearer <token>`; the compare is constant-time.
//
// This fails CLOSED: without PLAYWRIGHT_TOKEN the service refuses to start.
// It takes e-mail + password in the request body, navigates arbitrary URLs and
// hands back files and the full cookie jar (cf_clearance, _session_id) in the
// clear - an unauthenticated instance is an open browser proxy holding someone
// else's credentials for anyone on the same Docker network.
const PLAYWRIGHT_TOKEN = process.env.PLAYWRIGHT_TOKEN || ''
if (!PLAYWRIGHT_TOKEN) {
    console.error('[playwright] FATAL: PLAYWRIGHT_TOKEN is not set.')
    console.error('[playwright] The sidecar accepts credentials and returns cookies, so it refuses to run without authentication.')
    console.error('[playwright] Generate one (e.g. `openssl rand -hex 32`) and set the same value on this container and on the app container.')
    process.exit(1)
}
app.use((req, res, next) => {
    const header = req.get('authorization') || ''
    const prefix = 'Bearer '
    const provided = header.startsWith(prefix) ? header.slice(prefix.length) : ''
    const a = Buffer.from(provided)
    const b = Buffer.from(PLAYWRIGHT_TOKEN)
    if (a.length !== b.length || !crypto.timingSafeEqual(a, b)) {
        return res.status(401).json({ error: 'unauthorized' })
    }
    next()
})

// ── Concurrency guard ────────────────────────────────────────────────────────
// Every request launches its own Firefox (partly headful under Xvfb), so N
// parallel calls mean N browser processes. Without a limit a handful of requests
// is enough to exhaust the container's memory. Requests beyond the limit wait in
// a queue; if they wait too long they are rejected instead of piling up, and a
// request that runs too long is aborted so one stuck browser can't block the slot.
const MAX_CONCURRENCY   = Math.max(1, parseInt(process.env.PLAYWRIGHT_MAX_CONCURRENCY || '2', 10) || 2)
const QUEUE_TIMEOUT_MS  = Math.max(1000, parseInt(process.env.PLAYWRIGHT_QUEUE_TIMEOUT_MS || '60000', 10) || 60000)
const REQUEST_TIMEOUT_MS = Math.max(1000, parseInt(process.env.PLAYWRIGHT_REQUEST_TIMEOUT_MS || '300000', 10) || 300000)

let running = 0
const waiting = []

function acquireSlot() {
    if (running < MAX_CONCURRENCY) { running++; return Promise.resolve() }
    return new Promise((resolve, reject) => {
        const entry = { resolve, reject }
        entry.timer = setTimeout(() => {
            const index = waiting.indexOf(entry)
            if (index !== -1) waiting.splice(index, 1)
            reject(new Error('busy'))
        }, QUEUE_TIMEOUT_MS)
        waiting.push(entry)
    })
}

function releaseSlot() {
    const next = waiting.shift()
    if (next) { clearTimeout(next.timer); next.resolve(); return }
    running = Math.max(0, running - 1)
}

/**
 * Wraps a route handler so that at most MAX_CONCURRENCY of them run at a time
 * and none of them runs longer than REQUEST_TIMEOUT_MS.
 */
function limited(handler) {
    return async (req, res) => {
        try {
            await acquireSlot()
        } catch {
            console.warn('[playwright] queue full - rejecting ' + req.path)
            return res.status(503).json({ error: 'sidecar busy - try again later' })
        }
        let timer
        try {
            await Promise.race([
                handler(req, res),
                new Promise((_, reject) => { timer = setTimeout(() => reject(new Error('request timeout')), REQUEST_TIMEOUT_MS) }),
            ])
        } catch (err) {
            console.error('[playwright] ' + req.path + ': ' + err.message)
            // A timed-out handler keeps running in the background; closing its
            // browser here is what actually frees the memory (and usually makes
            // the handler fail fast on its next Playwright call).
            if (res.locals.browser) await res.locals.browser.close().catch(() => {})
            if (!res.headersSent) res.status(504).json({ error: err.message })
        } finally {
            clearTimeout(timer)
            releaseSlot()
        }
    }
}

/**
 * Logs a page into Cults3D. Shared by both Cults3D endpoints, which each carried
 * their own copy of this sequence - every timing tweak had to be made twice, and
 * a login that works for the collection sync but not for the download is exactly
 * the kind of bug that costs an afternoon.
 *
 * `tag` only labels the log line. Throws when the sign-in page is still showing
 * afterwards, which is how Cults3D signals a rejected login.
 */
async function cults3dLogin(page, email, password, tag) {
    await page.goto('https://cults3d.com/en/users/sign-in', { waitUntil: 'domcontentloaded', timeout: 45000 })
    await page.waitForTimeout(2000)
    // Quantcast consent overlay - it covers the form and swallows the clicks.
    await page.evaluate(() => {
        document.getElementById('qc-cmp2-container')?.remove()
        document.getElementById('qc-cmp2-ui')?.remove()
    })
    await page.waitForSelector('input[name="user[email]"]', { timeout: 45000 })
    await page.fill('input[name="user[email]"]', email)
    await page.fill('input[name="user[password]"]', password)
    await page.waitForTimeout(800)
    // form.submit() bypasses the disabled-button state on Cults3D
    await page.evaluate(() => { const f = document.querySelector('form'); if (f) f.submit() })
    await page.waitForTimeout(4000)
    await page.waitForLoadState('networkidle', { timeout: 10000 }).catch(() => {})
    if (page.url().includes('sign-in')) {
        throw new Error('Login failed - check email/password in settings')
    }
    console.log('[playwright] ' + tag + ': login successful')
}

/**
 * POST /resolve/myminifactory
 * Body: { urls: ["https://www.myminifactory.com/download/{id}?archive_id=...&key=...", ...] }
 * Returns: { resolved: [ { url, s3: "<presigned S3 url>" | null } ] }
 *
 * MMF's /download endpoint is Cloudflare-gated (blocks headless Chromium AND Go's
 * net/http TLS fingerprint → 403 "Just a moment"), but it only returns a 302 to a
 * presigned S3 URL (dl4.myminifactory.com - no Cloudflare). Firefox passes CF, so
 * we navigate each download URL, capture the 302 Location, and hand the S3 URLs
 * back. The caller then downloads directly from S3 (fast, no CF, no file transfer
 * through the browser).
 */
app.post('/resolve/myminifactory', limited(async (req, res) => {
    const urls = (req.body && Array.isArray(req.body.urls)) ? req.body.urls : null
    if (!urls || urls.length === 0) {
        return res.status(400).json({ error: 'urls (non-empty array) required' })
    }

    let browser
    try {
        // Firefox bypasses the Cloudflare bot check that blocks headless Chromium on MMF.
        browser = await firefox.launch({ headless: true })
        // Registered so the request-timeout guard can close it if this handler hangs.
        res.locals.browser = browser
        const context = await browser.newContext({
            userAgent: 'Mozilla/5.0 (Windows NT 10.0; Win64; rv:122.0) Gecko/20100101 Firefox/122.0',
            viewport: { width: 1280, height: 800 },
        })
        const page = await context.newPage()

        // Capture the 302 Location of any /download/ response (= the presigned S3 URL).
        let pendingLoc = null
        context.on('response', resp => {
            const u = resp.url()
            if (u.includes('/download/') && resp.status() >= 300 && resp.status() < 400) {
                const loc = resp.headers()['location'] || resp.headers()['Location']
                if (loc) pendingLoc = loc
            }
        })

        // Warm-up: establish a Cloudflare clearance cookie for the domain.
        await page.goto('https://www.myminifactory.com/', { waitUntil: 'domcontentloaded', timeout: 30000 }).catch(() => {})
        await page.waitForTimeout(1500)

        const resolved = []
        for (const dlUrl of urls) {
            pendingLoc = null
            // Navigating yields 302 → S3, then a download starts and aborts the
            // navigation (goto rejects); we only need the captured 302 Location.
            await page.goto(dlUrl, { waitUntil: 'commit', timeout: 30000 }).catch(() => {})
            await page.waitForTimeout(1200)
            if (pendingLoc) console.log('[playwright] mmf-resolve: ' + dlUrl + ' -> ok')
            else console.warn('[playwright] mmf-resolve: ' + dlUrl + ' -> no download URL captured')
            resolved.push({ url: dlUrl, s3: pendingLoc })
        }

        await browser.close()
        browser = null
        return res.json({ resolved })
    } catch (err) {
        if (browser) await browser.close().catch(() => {})
        console.error('[playwright] mmf-resolve error: ' + err.message)
        return res.status(500).json({ error: err.message })
    }
}))

/**
 * POST /collections/cults3d
 * Body: { email, password }
 * Returns: { collections: [ { id, name, urls: ["https://cults3d.com/en/3d-model/...", ...] } ] }
 *
 * Cults3D's pages are Cloudflare-gated and block headless Chromium (and Go's
 * net/http) with the "Just a moment" JS challenge. Firefox passes that challenge,
 * so we log in, open the (English-forced) collections overview and walk each
 * collection (paginated) to collect the model links.
 */
app.post('/collections/cults3d', limited(async (req, res) => {
    const email = req.body && req.body.email
    const password = req.body && req.body.password
    if (!email || !password) {
        return res.status(400).json({ error: 'email + password required' })
    }
    let browser
    try {
        // Headful Firefox under Xvfb: Cloudflare challenges headless Firefox on
        // Cults3D, but a headed browser (real display via xvfb-run) passes.
        browser = await firefox.launch({ headless: false })
        // Registered so the request-timeout guard can close it if this handler hangs.
        res.locals.browser = browser
        const context = await browser.newContext({
            userAgent: 'Mozilla/5.0 (Windows NT 10.0; Win64; rv:122.0) Gecko/20100101 Firefox/122.0',
            viewport: { width: 1280, height: 800 },
        })
        const page = await context.newPage()

        await cults3dLogin(page, email, password, 'cults3d-collections')

        // Navigate + return model hrefs, or null if the page errored (e.g. 406 = past last page).
        const getModels = async (u) => {
            const status = await page.goto(u, { waitUntil: 'domcontentloaded', timeout: 30000 })
                .then(r => (r ? r.status() : 0)).catch(() => 0)
            if (!status || status >= 400) return null
            await page.waitForTimeout(1200)
            return page.evaluate(() =>
                [...document.querySelectorAll('a[href*="/3d-model/"]')].map(a => a.getAttribute('href')))
        }

        // Collections overview (English → /en/3d-model links).
        await page.goto('https://cults3d.com/en/design-collections', { waitUntil: 'domcontentloaded', timeout: 30000 })
        await page.waitForTimeout(1500)
        const refs = await page.evaluate(() => {
            const out = []
            const seen = new Set()
            for (const a of document.querySelectorAll('a[href*="/design-collections/"]')) {
                const href = a.getAttribute('href') || ''
                const m = href.match(/^\/en\/design-collections\/([^/?#]+)\/([^/?#]+)$/)
                if (!m) continue
                const user = m[1], slug = m[2]
                if (slug === 'edit' || slug === 'bearbeiten' || user === 'tips' || user === 'new') continue
                if (seen.has(m[0])) continue
                seen.add(m[0])
                out.push({ user, slug, url: 'https://cults3d.com' + m[0] })
            }
            return out
        })

        const collections = []
        for (const ref of refs) {
            const urls = []
            const seen = new Set()
            let name = ref.slug
            for (let p = 1; p <= 50; p++) {
                const found = await getModels(ref.url + '?page=' + p)
                if (!found) break
                if (p === 1) {
                    name = await page.evaluate(() => {
                        const h = document.querySelector('h1')
                        return (h && h.innerText ? h.innerText.trim() : '')
                    }).catch(() => '') || name
                }
                let added = 0
                for (let h of found) {
                    if (!h) continue
                    if (h.charAt(0) === '/') h = 'https://cults3d.com' + h
                    if (!seen.has(h)) { seen.add(h); urls.push(h); added++ }
                }
                if (added === 0) break
            }
            collections.push({ id: ref.user + '/' + ref.slug, name, urls })
        }

        await browser.close()
        browser = null
        return res.json({ collections })
    } catch (err) {
        if (browser) await browser.close().catch(() => {})
        console.error('[playwright] cults3d-collections error: ' + err.message)
        return res.status(500).json({ error: err.message })
    }
}))

/**
 * POST /download/cults3d-url
 * Body: { email, password, urls: ["https://cults3d.com/en/downloads/{id}?creation=...", ...] }
 * Returns: { files: [{ name, data (base64) }] } or { error }
 *
 * API-driven download: the caller already resolved the order download URLs via the
 * Cults3D GraphQL API (orders.lines.downloadUrl), so we skip the name-your-price /
 * cart order-flow entirely. Firefox logs in (passes Cloudflare) and navigates each
 * download URL; files are captured from responses and browser download events.
 */
app.post('/download/cults3d-url', limited(async (req, res) => {
    const { email, password } = req.body || {}
    const urls = (req.body && Array.isArray(req.body.urls)) ? req.body.urls : null
    if (!email || !password || !urls || urls.length === 0) {
        return res.status(400).json({ error: 'email, password and urls[] required' })
    }
    let browser
    try {
        // Headful Firefox under Xvfb - passes Cloudflare where headless is blocked.
        browser = await firefox.launch({ headless: false })
        // Registered so the request-timeout guard can close it if this handler hangs.
        res.locals.browser = browser
        const context = await browser.newContext({
            userAgent: 'Mozilla/5.0 (Windows NT 10.0; Win64; rv:122.0) Gecko/20100101 Firefox/122.0',
            viewport: { width: 1280, height: 800 },
            acceptDownloads: true,
        })

        const fs = require('fs')
        const files = []
        const seen = new Set()

        const captureResponse = (response) => {
            try {
                const u = response.url()
                if (!u.includes('cults3d.com') && !u.includes('cdn') && !u.includes('amazonaws') && !u.includes('fbi.')) return
                const ct = response.headers()['content-type'] || ''
                const cd = response.headers()['content-disposition'] || ''
                const isFile = (/attachment/i.test(cd) && !/\.txt/i.test(cd)) ||
                               /octet-stream|model\/stl|application\/zip/i.test(ct) ||
                               /\.(stl|3mf|obj|step|zip)(\?|$)/i.test(u)
                if (!isFile) return
                response.body().then(buf => {
                    const fn = (cd.match(/filename\*?=(?:UTF-8''|["']?)([^"';\n]+)/i) || [])[1]
                            || u.split('/').pop().split('?')[0] || 'file.stl'
                    const name = decodeURIComponent(fn.replace(/['"]/g, '').trim())
                    const key = name + ':' + buf.length
                    if (seen.has(key)) return
                    seen.add(key)
                    files.push({ name, data: buf.toString('base64') })
                    console.log('[playwright] cults3d-url: captured ' + name + ' (' + buf.length + ' bytes)')
                }).catch(() => {})
            } catch (_) {}
        }
        const captureDownload = async (download) => {
            try {
                const p = await download.path()
                const buf = fs.readFileSync(p)
                const name = download.suggestedFilename() || 'file.stl'
                const key = name + ':' + buf.length
                if (seen.has(key)) return
                seen.add(key)
                files.push({ name, data: buf.toString('base64') })
                console.log('[playwright] cults3d-url: download event ' + name + ' (' + buf.length + ' bytes)')
            } catch (e) { console.error('[playwright] cults3d-url: dl error ' + e.message) }
        }
        context.on('response', captureResponse)
        context.on('page', np => np.on('download', captureDownload))

        const page = await context.newPage()
        page.on('download', captureDownload)

        // Full cached cookie jar ("name=val; name=val") incl. cf_clearance +
        // _session_id. Reusing cf_clearance (same Firefox build/UA/IP) lets us skip
        // Cloudflare's JS challenge, and _session_id skips the login - so a cached
        // jar means most downloads touch neither CF nor the login form.
        const cookieJar = (req.body && typeof req.body.cookieJar === 'string') ? req.body.cookieJar : ''

        const doLogin = () => cults3dLogin(page, email, password, 'cults3d-url')

        if (cookieJar) {
            const cks = cookieJar.split(';').map(s => s.trim()).filter(Boolean).map(pair => {
                const i = pair.indexOf('=')
                return i > 0 ? { name: pair.slice(0, i), value: pair.slice(i + 1), url: 'https://cults3d.com' } : null
            }).filter(Boolean)
            if (cks.length) await context.addCookies(cks).catch(() => {})
        }

        // Wait for a Cloudflare "Just a moment" interstitial on the current page to
        // clear (Firefox solves the JS challenge). The /en/downloads endpoints are
        // JS-challenged even when logged in, so the file only starts after it clears.
        const waitCfClear = async () => {
            for (let i = 0; i < 30; i++) {
                const t = await page.title().catch(() => '')
                if (!/just a moment/i.test(t)) return
                await page.waitForTimeout(1000)
            }
        }

        let loggedIn = false
        for (const u of urls) {
            try {
                const before = files.length
                await page.goto(u, { waitUntil: 'domcontentloaded', timeout: 90000 }).catch(() => null)
                if (!loggedIn && (page.url().includes('sign-in') || page.url().includes('log-in-choice'))) {
                    await doLogin()
                    loggedIn = true
                    await page.goto(u, { waitUntil: 'domcontentloaded', timeout: 90000 }).catch(() => null)
                }
                await waitCfClear()
                // Give the download (response- or event-delivered) time to arrive,
                // polling so large files aren't cut off.
                for (let i = 0; i < 20 && files.length === before; i++) {
                    await page.waitForTimeout(1000)
                }
                await page.waitForTimeout(3000)
            } catch (e) { console.error('[playwright] cults3d-url: nav error ' + e.message) }
        }
        await page.waitForTimeout(2000)

        // Return the full cookie jar (cf_clearance + _session_id + …) so the caller
        // can cache it and skip both Cloudflare and login on subsequent downloads.
        let outJar = ''
        try {
            const cks = await context.cookies('https://cults3d.com')
            outJar = cks.filter(c => ['cf_clearance', '_session_id', 'cf_bm', '__cf_bm'].includes(c.name))
                        .map(c => c.name + '=' + c.value).join('; ')
        } catch (_) {}

        await browser.close(); browser = null
        return res.json({ files, cookieJar: outJar })
    } catch (err) {
        if (browser) await browser.close().catch(() => {})
        console.error('[playwright] cults3d-url error: ' + err.message)
        return res.status(500).json({ error: err.message })
    }
}))

// ── Warm MakerWorld Firefox ──────────────────────────────────────────────────
// The other endpoints launch a browser per request and throw it away. MakerWorld
// is the exception: its GeeTest cookies are domain-wide and are handed out on the
// first visit, so a browser that stays open resolves many downloads off one visit
// - whereas a fresh browser per job means a fresh first visit per job, which is
// what reads as bulk scraping and trips the anti-bot.
//
// Because the point is to SHARE those cookies, all callers use one context and
// one page, and requests are serialised onto it rather than run in parallel.
const MAKERWORLD_IDLE_MS = Math.max(60000, parseInt(process.env.MAKERWORLD_IDLE_MS || '900000', 10) || 900000)

const MAKERWORLD_USER_AGENT = 'Mozilla/5.0 (Windows NT 10.0; Win64; rv:122.0) Gecko/20100101 Firefox/122.0'

let makerworldBrowser  = null
let makerworldContext  = null
let makerworldPage     = null
let makerworldLastUsed = 0
let makerworldReaper   = null

// Serialises access to the single shared page. The concurrency guard allows
// MAX_CONCURRENCY handlers at once, and two of them driving the same page would
// interleave navigations.
let makerworldChain = Promise.resolve()

function makerworldSerial(task) {
    const run = makerworldChain.then(task, task)
    // Keep the chain alive after a failed task, or every later call inherits the
    // rejection and never runs.
    makerworldChain = run.catch(() => {})
    return run
}

/**
 * Closes the warm instance and clears the module state. Safe to call twice, and
 * safe to call on an already-dead browser.
 */
async function makerworldDispose() {
    const browser = makerworldBrowser
    makerworldBrowser = null
    makerworldContext = null
    makerworldPage    = null
    if (browser) {
        await browser.close().catch(() => {})
        console.log('[playwright] makerworld: warm browser closed')
    }
}

function makerworldStartReaper() {
    if (makerworldReaper) return
    makerworldReaper = setInterval(() => {
        if (!makerworldBrowser) return
        if (Date.now() - makerworldLastUsed < MAKERWORLD_IDLE_MS) return
        console.log('[playwright] makerworld: warm browser idle - closing')
        makerworldSerial(() => makerworldDispose())
    }, 60000)
    // Do not hold the process open just for the reaper.
    makerworldReaper.unref()
}

/**
 * True when the page is a Cloudflare bot-verification wall rather than
 * MakerWorld. Ported from the Go warm session it used to live in. This only
 * DETECTS the wall - it is never solved or clicked through.
 */
async function makerworldIsBotWall(page) {
    try {
        return await page.evaluate(() => {
            const title = (document.title || '').toLowerCase()
            return title.includes('just a moment') ||
                   title.includes('security verification') ||
                   title.includes('attention required') ||
                   !!document.querySelector('iframe[src*="challenges.cloudflare.com"]')
        })
    } catch (_) {
        return false
    }
}

/**
 * Waits a randomised moment. GeeTest scores how quickly a page goes from loaded
 * to acting on it, and a constant delay is itself a pattern - so the wait has a
 * floor plus jitter rather than one fixed value.
 */
function makerworldHumanPause(page, minimumMs, jitterMs) {
    return page.waitForTimeout(minimumMs + Math.floor(Math.random() * jitterMs))
}

/**
 * Returns the warm page, launching Firefox and doing the one warm-up visit if
 * there is none. Caller must be inside makerworldSerial().
 */
async function makerworldEnsurePage() {
    if (makerworldPage && !makerworldPage.isClosed()) return makerworldPage

    await makerworldDispose()
    // Headless: unlike the Cloudflare-gated sites in this file, GeeTest let a
    // headless request through in testing, and this instance is long-lived - so
    // it keeps holding whatever a headful Firefox costs for as long as it runs.
    makerworldBrowser = await firefox.launch({
        headless: true,
        firefoxUserPrefs: {
            // Firefox exposes navigator.webdriver from this pref; turning it off
            // removes the marker at the engine, not from a page script that a
            // fingerprinter can notice has been tampered with.
            'dom.webdriver.enabled': false,
            // No proxy, pinned rather than assumed. Nothing sets one today, but
            // Playwright picks up HTTP_PROXY/ALL_PROXY from the environment, and
            // this container also runs Tor - MakerWorld must not end up behind an
            // exit node, whose address pool is exactly what GeeTest scores badly.
            // 0 = direct connection, and it beats any env-derived setting.
            'network.proxy.type': 0,
        },
    })
    makerworldContext = await makerworldBrowser.newContext({
        userAgent: MAKERWORLD_USER_AGENT,
        viewport: { width: 1280, height: 800 },
        locale: 'en-US',
    })
    makerworldPage = await makerworldContext.newPage()

    // One visit to the site (not to a model) is enough for GeeTest to hand out
    // its domain-wide cookies; every later resolve reuses them.
    await makerworldPage.goto('https://makerworld.com/en', { waitUntil: 'domcontentloaded', timeout: 60000 })
    await makerworldHumanPause(makerworldPage, 2000, 1500)

    if (await makerworldIsBotWall(makerworldPage)) {
        await makerworldDispose()
        throw Object.assign(new Error('cloudflare bot-check'), { botWall: true })
    }
    console.log('[playwright] makerworld: warm browser established')

    makerworldStartReaper()
    return makerworldPage
}

/**
 * POST /resolve-makerworld
 * Body: { modelID, instanceID, token }
 * Returns: { status, body, url } or { error }
 *
 * MakerWorld's f3mf endpoint is GeeTest-gated: called from outside a browser it
 * answers HTTP 418. Called from the page context of a browser that already holds
 * the GeeTest cookies it answers with the presigned CDN URL, which the caller can
 * then stream directly (no file transfer through this sidecar).
 *
 * The token is the Bambu bearer token the Go app got from the API login. It is
 * sent as a header on the fetch, so the browser itself stays signed out and one
 * warm instance can serve every user.
 *
 * `status` and `body` are passed through verbatim so the caller keeps its own
 * 418/captcha handling; `url` is the parsed convenience field and is null when
 * the body was not JSON or carried no url.
 */
app.post('/resolve-makerworld', limited(async (req, res) => {
    const { modelID, instanceID, token } = req.body || {}
    if (!modelID || !instanceID || !token) {
        return res.status(400).json({ error: 'modelID, instanceID and token required' })
    }
    // Both ids go into a URL path. They are numeric everywhere they are produced,
    // so rejecting anything else is free and keeps a crafted id from pointing the
    // warm, cookie-holding browser at some other endpoint.
    if (!/^\d+$/.test(String(modelID)) || !/^\d+$/.test(String(instanceID))) {
        return res.status(400).json({ error: 'modelID and instanceID must be numeric' })
    }

    // A handler that hits the request timeout disposes the warm instance rather
    // than closing a browser the module still points at.
    res.locals.browser = { close: () => makerworldDispose() }

    const apiPath = '/api/v1/design-service/instance/' + instanceID + '/f3mf?type=download&fileType='
    const modelURL = 'https://makerworld.com/en/models/' + modelID

    // Runs one pass on the warm page. Returns null when the page looked dead, so
    // the caller can re-establish and try once more.
    const attempt = async () => {
        let page
        try {
            page = await makerworldEnsurePage()
            makerworldLastUsed = Date.now()

            // Visit the model page first: the fetch then goes out from the same
            // origin and referer a visitor's would, instead of from a bare tab.
            await page.goto(modelURL, { waitUntil: 'domcontentloaded', timeout: 45000 })
            // GeeTest finishes setting its challenge cookies in the background,
            // after domcontentloaded. Firing the fetch before that is what earns
            // the 418, so this wait is deliberately longer than a cosmetic one.
            await makerworldHumanPause(page, 2500, 1500)
            // A scroll event from real input, so the page has seen the visitor do
            // something before the download call goes out.
            await page.mouse.wheel(0, 300)
            await makerworldHumanPause(page, 400, 400)

            if (await makerworldIsBotWall(page)) {
                await makerworldDispose()
                throw Object.assign(new Error('cloudflare bot-check'), { botWall: true })
            }
        } catch (err) {
            if (err && err.botWall) throw err
            console.warn('[playwright] makerworld: session unusable (' + err.message + ')')
            return null
        }

        return page.evaluate(async ({ api, bearer }) => {
            try {
                const response = await fetch(api, {
                    headers: {
                        'Authorization': 'Bearer ' + bearer,
                        'Accept': 'application/json',
                        'X-Requested-With': 'XMLHttpRequest',
                        // Sec-Fetch-* are forbidden header names: fetch() drops
                        // them and the browser sets its own. Same-origin from the
                        // model page it sends exactly these three values anyway,
                        // so they are stated here for the record, not for effect.
                        'Sec-Fetch-Dest': 'empty',
                        'Sec-Fetch-Mode': 'cors',
                        'Sec-Fetch-Site': 'same-origin',
                    },
                })
                return { status: response.status, body: await response.text() }
            } catch (e) {
                return { status: -1, body: String(e) }
            }
        }, { api: apiPath, bearer: token })
    }

    return makerworldSerial(async () => {
        try {
            let result = await attempt()
            if (result === null) {
                await makerworldDispose()
                result = await attempt()
            }
            if (result === null) {
                return res.status(502).json({ error: 'could not establish a MakerWorld browser session' })
            }

            let url = null
            try {
                const parsed = JSON.parse(result.body)
                if (parsed && typeof parsed.url === 'string' && parsed.url) url = parsed.url
            } catch (_) {}

            if (url) {
                console.log('[playwright] makerworld: model=' + modelID + ' instance=' + instanceID + ' -> ok')
            } else {
                // Never the body: it can carry the captcha payload and, on some
                // errors, the token that was sent.
                console.warn('[playwright] makerworld: model=' + modelID + ' instance=' + instanceID +
                             ' -> no url (HTTP ' + result.status + ')')
            }
            return res.json({ status: result.status, body: result.body, url })
        } catch (err) {
            if (err && err.botWall) {
                console.warn('[playwright] makerworld: STOP - Cloudflare bot-check served instead of MakerWorld')
                await makerworldDispose()
                if (!res.headersSent) {
                    return res.json({ status: 403, body: '', url: null, botWall: true })
                }
                return
            }
            console.error('[playwright] makerworld error: ' + err.message)
            await makerworldDispose()
            if (!res.headersSent) return res.status(500).json({ error: err.message })
        }
    })
}))

app.get('/health', (_, res) => res.json({ ok: true }))

app.listen(PORT, HOST, () => console.log('[playwright] Server listening on ' + HOST + ':' + PORT))
