# MeshDepot browser extension

Imports the design you are looking at into your own MeshDepot instance, using
the session you already have in the browser.

Works on **MakerWorld, Printables, Thingiverse and MyMiniFactory**.

MeshDepot supports Thangs and Cults3D as well, but the extension deliberately
does not: they were only ever in here because the capture mechanism is
platform-independent, not because anybody asked, and a site the extension claims
to work on is a site somebody has to keep working.

No account credentials for any of those sites are stored on the MeshDepot server
on this path — no password, no TOTP seed, no token. The download link is created
by the site for your own signed-in session and handed to MeshDepot as a finished
result, so any two-factor method works, including the emailed codes that the
server-side login cannot automate.

Firefox, Chrome and Chromium. Chrome and Chromium share one build — Chromium is
the base Chrome is made from, and for an extension there is nothing between them.

## Layout

    shared/     every script and the settings page - the source of truth
    build.sh    assembles the two loadable directories from it
    firefox/    generated
    chrome/     generated - load this in Chromium too

Edit `shared/`, then run `./build.sh`. The two builds differ in exactly two
places, and both are in the manifest: Firefox runs a list of background scripts
as an event page, Chrome runs a single file as a service worker (`sw.js`, which
pulls in the same scripts). Everything else is one codebase — `compat.js` gives
`chrome` the name `browser` so the rest never has to ask which browser it is in.

## Load it locally

**Firefox** — `about:debugging#/runtime/this-firefox` → **Load Temporary
Add-on…** → `firefox/manifest.json`. It stays until Firefox is closed.

**Chrome / Chromium** — `chrome://extensions` → **Developer mode** → **Load
unpacked** → the `chrome/` directory. It stays until removed.

After changing anything: run `./build.sh`, press **Reload** on the extension's
entry, then reload the model tab. The extension reload alone is enough for the
background; the content scripts are injected when the page loads, so their old
copies keep running until the tab is reloaded too. Reloading both every time is
the habit that saves the confusion.

One difference worth knowing at setup: Firefox treats host permissions in a
Manifest V3 extension as optional and asks separately, which is what the "Site
access" card on the extension's page is for. Chrome grants them at install, so
that card is usually green from the start.

## Set it up

1. Click the extension's toolbar button. The top card says whether it may run on
   the model sites at all — Firefox treats those permissions as optional in a
   Manifest V3 extension, and **until they are granted no content script runs, so
   the Import button never appears**. Press **Allow access to the model sites**
   and confirm — Firefox draws that prompt at the top of the window rather than
   next to the panel, so look up there. Then reload any model page you already
   had open.
2. In MeshDepot: **Account settings → API keys → Create**. Pick how long it
   should last — 90 days by default, because the key's plaintext lives in this
   browser's profile, a file on disk, and one that never expires turns a copied
   profile into a permanent way in. The key is shown once; only its hash is
   stored.

   MeshDepot refuses an API key over an unencrypted connection from outside its
   own network: the key travels in a header, so plaintext hands it to everyone on
   the way. Over `localhost` or from the same LAN it is accepted as before.
3. Press **Settings** in the panel, enter the address of
   your instance (`http://localhost:9000`) and the key, and save.

   Firefox asks once whether the extension may contact that host; the prompt
   again appears at the top of the window. Should the panel close before you can
   answer — it does that when focus moves — the extension offers a tab where the
   prompt has room. That is a fallback, not the normal path.
4. The panel then shows whether it is connected, re-checked every twenty
   seconds. "Connected" means the host answered *and* the key was
   accepted — a reachable host alone would still fail the first import.

## Try it

1. Sign in to the site and open any model page.
2. A blue **Import to MeshDepot** button appears bottom right. It only shows on
   model pages and follows in-page navigation.
3. Click it. A panel opens and asks whether to import this design. Nothing has
   happened yet at that point — the button sits among the site's own controls and
   is easy to hit by accident, so no request goes out and nothing is read until
   you press **Start import**.
4. Then, on platforms that need it, press the site's own download button — solve
   the captcha if one appears. On Thingiverse there is nothing to press: it lists
   its files itself.
5. Every file is ticked off as it arrives, and the import is sent by itself
   three seconds after the last one. Download several files in a row and they
   arrive as one design.

A platform lists everything the designer uploaded - the models, and beside them a
brochure or a photo. The server keeps the printable ones (`stl`, `3mf`, `obj`,
`step`, `stp`) and drops the rest, the same rule MeshDepot's own Thingiverse
importer follows, so both ways of importing a design produce the same thing. When
nothing looks printable the whole list is kept, so an unusually named design
still arrives.

The rule is applied twice, and the second time is the one that matters for
Printables: its download is a single archive whose contents are only known once
it has been opened - and it carries a generated PDF beside the models.

An archive is unpacked on the server, so a Printables or Thingiverse download
becomes the files it contains rather than one archive in the library. A `.3mf` is
left alone — it is a ZIP too, but it is a model.

The two are told apart by **content, not by filename**: a 3MF carries its model
under `3D/`, and an archive of parts does not. That matters because a filename is
not always there — Chrome does not know one when it reports a download, and
guessing had a Printables archive filed as a single model.

The browser's own download is removed again: MeshDepot fetches the file from the
link, so a second copy on your disk is not what you asked for by importing. It
flashes up in the downloads list for a moment before it goes. There is a switch
for keeping it.

## How the parts fit together

Five content scripts, loaded in this order into one shared scope:

- `compat.js` — makes `browser` mean the extension API in every browser.
- `platforms.js` — which sites count as model pages, how to read a design from
  one, and the stable address of a design regardless of which tab is open.
- `bridge.js` — everything touching the page's own world: injecting `page.js`,
  reading the session token, requesting metadata and copying the answer safely
  back across the boundary.
- `panel.js` — what the visitor sees: the panel, the captured-file list, the
  result notice. The only place that arms and disarms the background.
- `content.js` — the button and the page lifecycle.

Plus, outside that scope:

- `page.js` — MakerWorld only, and optional. It watches the site resolve its own
  download links, so a link seen going past never has to be asked for. It used to
  make the API calls as well, but MakerWorld's Content-Security-Policy refuses
  injected scripts; those calls now go through `content.fetch()` in `bridge.js`,
  which carries the page's principal without any injection. If the injection is
  refused, nothing breaks — that capture channel is simply absent.
- `background.js` — watches for the download, removes the local copy, and talks
  to MeshDepot.

**The boundary between `page.js` and the rest is not a formality.** Firefox keeps
the two worlds apart, and an object that arrived from the page cannot have a
content script's own objects written into it — doing so raises "Not allowed to
define cross-origin object as property on [Object] XrayWrapper". Anything coming
back from `page.js` is therefore copied into this side first.

Catching the download is the platform-independent half, and it is the half that
matters: `downloads.onCreated` reports whatever the visitor started, on any site.
Only *reading* the design differs between platforms.

## Why it works this way

The extension does not resolve download links on its own. It did at first, and
that path ran into a 403 (`Please log in to download models` — the credential
could not be reconstructed from a cookie) and then, once that was solved, into
HTTP 418: MakerWorld's GeeTest wall, which a real signed-in Firefox meets just
as readily as the server does. The wall is about pace and reputation, not about
detecting automation.

Letting the visitor press the site's own button removes the problem instead of
fighting it. The request is then part of a genuine click, with whatever headers,
parameters and captcha MakerWorld wants, and the extension only reads the answer.

## Settings

Behind the **Settings** button in the toolbar panel:

- **MeshDepot address** — e.g. `http://localhost:9000`. A match pattern cannot
  carry a port, so the permission covers every port on that host. `localhost` is
  the easy case: browsers treat it as trustworthy, so plain `http` raises no
  objection there.
- **API key** — from Account settings in MeshDepot.
- **Remove the file from the Downloads folder** — on by default.
- **Put imports in a collection** — off by default. When on, every imported
  design is also filed under *Manual Website Import* in MeshDepot, created on
  first use. The name belongs to the server, not to this extension: a client that
  could choose it could fill somebody's library with collections.

The page follows the browser's light or dark setting.

## What it sends

The gallery is read the same way on every platform — Open Graph tags, structured
data, and the pictures actually rendered on the page, merged and deduplicated.
On MakerWorld whatever its API names is merged in as well. Neither source is
complete on its own.

```json
{
  "source_url": "https://www.printables.com/model/123456-thing",
  "platform": "printables",
  "meta": { "source_id": "123456", "name": "…", "author": "…",
            "description": "…", "tags": ["…"], "license": "…",
            "cover_url": "https://…" },
  "files": [ { "instance_id": "789", "name": "Plate 1.3mf", "url": "https://…" } ]
}
```

`source_url` is the model page, so MeshDepot's existing duplicate check
(`user_id` + `source_url`) keeps working unchanged.

Tags go out exactly as MakerWorld returns them. Decoding escaped entities and
folding the casing stay on the server, where every other import path already
does it.

## Where the metadata comes from

The page is read first on every platform, including MakerWorld, and a platform
API is asked afterwards only to improve on that. The order matters: reading
MakerWorld through its API alone meant every obstacle in front of that API
stopped the import outright, while the page sat there with a title, an author and
a gallery on it. A failing API now costs the extra fields and nothing else.


Both halves contribute, because neither is complete on its own.

The extension reads the page: structured data, Open Graph, and what is rendered.
That works until a site loads its description only when the reader asks for it —
Printables does exactly that, so the page genuinely holds one summary line and
nothing more, however carefully it is read.

So the server asks the platform. Printables answers a public model over its own
GraphQL API **without any credentials**, and the full description, the creator,
the tags and the gallery come back. Only gaps are filled; the one exception is
the description, where the longer text wins — that exception is the whole reason
this exists, since what the page offered was not empty but a first sentence.

Nothing on this path logs in or reads a stored credential. A platform that will
not answer anonymously simply contributes nothing and the page's reading stands,
which is the case for every platform but Printables today.

## Version check

The extension and the server are updated separately: this lives in a browser and
updates itself, MeshDepot is self-hosted and gets updated when its operator gets
round to it. So both name the contract they speak, and the panel compares them
before it reports "Connected" — on every check and on every save. Nothing is
shown while they agree; the version only ever appears in the message that says
which side is out of date.

- `IMPORT_API_VERSION` in `background.js`
- `BrowserImportAPIVersion` / `BrowserImportAPIMinVersion` in the server's
  `internal/api/browserimport.go`

Currently **1** on both sides. `GET /api/v1/version` answers without a key on
purpose — an extension whose key this server never issued still has to be able to
tell "your MeshDepot is too old" from "wrong key". One route answers for the
whole server rather than one per feature, and a server so old that it does not
exist answers 404, which is read the same way.

Both directions are reported with the advice that fits: a server too old is the
operator's job, an extension too old is the reader's own.

## Thingiverse takes a different route

Its "Download all files" builds the archive **in the browser** — that is the
countdown — and hands out a `blob:` address. Every way to those bytes is shut:
the server cannot fetch such an address, a content script cannot read it (wrong
principal), `content.fetch` does not exist in current Firefox, and Thingiverse
blocks injected scripts, so a helper in the page cannot hold the blob either. The
file names in the markup carry no address at all — the list is rendered from data
the page fetched.

So the extension fetches that data instead:

1. `GET /api/v2/auth/view` — a guest token, free for the asking, no account
   involved.
2. `GET /api/v2/things/<id>/complete` with that token. Without it the endpoint
   answers 401.
3. That answer carries the whole design: `zip_data.files` names every file with a
   **direct CDN address** needing no token, `zip_data.images` the real gallery,
   and `creator`, `tags`, `license` and `description_html` the rest.

Taking the metadata from there is not a nicety. The page's `author` meta tag
names *Thingiverse*, and its Open Graph picture is the site's house image — both
look like answers and are not, so a design imported from the markup arrived with
the platform's branding as its cover and "Thingiverse.com" as its designer.

The server then fetches those addresses. It cannot do steps 1 and 2 itself —
Cloudflare challenges it there — and the browser has no reason to do step 3,
since the addresses are public. Each side does the part only it can, and no
download runs in the browser at all.

The server paces those fetches: Thingiverse answers 429 to a handful of requests
in quick succession, and a design with ten files would otherwise fetch two and be
turned away for the rest.

## When a download address is only a doorway

MyMiniFactory's `/download/<id>` is tied to the session that asked for it and
answers **403** to this server, however public the design. But it is not the real
address: it redirects to a presigned S3 link that anyone may fetch for the next
four hours, and that one carries the filename in
`response-content-disposition` too.

So the extension passes on the address the download **ended** at, not the one it
started from - `finalUrl`, looked up once more when the browser has not settled
it yet. The server then fetches the same link the browser would have.

Fetching the file in the extension and uploading the bytes was tried first and
does not work: the S3 host sends no CORS headers, so neither the background nor a
content script may read the response. `POST /api/v1/imports/browser/upload`
remains for a platform that genuinely hands out no fetchable address.

## Rate limiting

MakerWorld answers HTTP 418 (GeeTest) when download links are resolved in quick
succession. A real, signed-in browser is **not** exempt — the wall is about pace
and reputation, not about detecting automation. That is worth knowing, because
the server-side sidecar was built on the assumption that a convincing browser
would get through.

The extension therefore avoids the wall rather than trying to climb it.

**The quiet path — download the files yourself first.** Every response the site
produces for its own download button passes through the extension's hook and is
kept. Click MakerWorld's download for the plates you want, then press *Import to
MeshDepot*: those links are already in hand, no request is made for them, and no
rate limit applies. This is the recommended way for a model with many plates.

**The active path.** Anything not captured is resolved by the extension: roughly
two seconds apart, one patient retry after fifteen seconds, and the working
credential remembered for the tab so no attempt is wasted. It works, but a model
with many plates takes a while and several designs in a row can still hit the
wall. It clears on its own after a few minutes.

The payload reports the split as `captured_count` and `requested_count`.

## Known limits

- Download links are presigned and live for minutes. The server has to fetch
  them promptly rather than queue them behind a long backlog.
- Links resolved in the browser *can* be fetched from elsewhere: tested from a
  second address over Tor, the same URL served the same bytes. The signature
  covers the path alone, which is why the server can do the downloading and no
  file has to travel through the browser.
- The flip side of that: for its five minutes such a link is a complete
  substitute for your session on the platform. It is kept out of the server log
  for that reason.
- A design imported this way has no platform account behind it, so the nightly
  library sync cannot check it for updates.
