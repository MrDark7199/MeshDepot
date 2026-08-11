# MeshDepot - Architecture

## Overview

MeshDepot is a multi-container application deployed via Docker Compose. It follows a classic three-tier architecture: a SPA frontend, a PHP REST API backend, and a MariaDB database - with Redis for sessions and async job state.

```
Browser
  │
  ▼
nginx (frontend) :3100
  │  serves static SPA assets
  │  proxies /api/* → nginx (backend) :3101
  ▼
nginx (backend) :3101
  │  fastcgi_pass → php-fpm :9000
  ▼
php-fpm (PHP 8.3)
  │
  ├── MariaDB 11      (persistent data)
  ├── Redis 7         (sessions, short-lived tokens)
  ├── playwright :3200 (browser automation - Cults3D downloads)
  └── tor :9050/9051  (Tor proxy - MakerWorld auto-login)
```

Background jobs (download queue, sync) run in a separate `cronjob` container on a schedule.

---

## Containers

| Container | Image | Role |
|---|---|---|
| `meshdepot_frontend` | nginx:alpine + Vite build | Serves the SPA; proxies `/api/*` to backend |
| `meshdepot_backend` | nginx:alpine | Reverse proxy for PHP-FPM |
| `meshdepot_php` | php:8.3-fpm-alpine | Executes PHP request handlers |
| `meshdepot_mariadb` | mariadb:11 | Relational database |
| `meshdepot_redis` | redis:7-alpine | Sessions and temporary tokens |
| `meshdepot_cronjob` | alpine + PHP CLI | Download queue worker and sync worker |
| `meshdepot_playwright` | Node.js + Playwright | Browser automation for Cults3D file capture |
| `meshdepot_tor` | osminogin/tor-simple | Tor SOCKS5 proxy for MakerWorld auto-login |

---

## Frontend

**Tech:** SolidJS · TypeScript · Vite · Babylon.js

### Entry point

`frontend/src/App.tsx` is the application root. It contains:
- `LoginPage` guard - redirects unauthenticated users
- `MainApp` - main shell: navigation, design grid, collections, pagination
- `CollectionsPage` - collection browser with filter support
- `DesignCard` - individual design card with cover, tags, and action menu
- `Toast` - global notification overlay

### Routing

Client-side routing is not used. The app is a single-page application with view state managed via SolidJS signals (`createSignal`). Navigation between views is handled by conditional rendering.

### State management

SolidJS fine-grained reactivity is used throughout:
- `createSignal` for local and shared state (designs, tags, search query, page, etc.)
- `createEffect` for reactive side-effects (search debounce, filter changes)
- No global state library - auth state is provided via `AuthContext` (SolidJS context)

### Pagination

`GET /api/designs` supports `page` and `per_page` query parameters. The response format is `{ items: [...], total: N }`. The frontend holds `page`, `perPage`, and `totalDesigns` signals. Changing the search query or active filters resets the page to 1.

### Notifications

Notifications are fetched from the API and deduplicated by title client-side. The unread count is derived from the deduplicated list, not a separate counter. Local (transient toast) notifications are merged into the same list.

### Internationalization

`frontend/src/i18n/index.tsx` provides an `I18nProvider` context. Components call `useI18n()` to get the `translate(key, vars?)` function. Translations are stored in `en.ts` and `de.ts`. The active language is persisted in `localStorage` and synced to the user record on change.

### Theming

`frontend/src/ThemeContext.tsx` applies CSS custom properties to `:root` based on the selected theme (dark / light / system) and accent color. Users can also supply custom CSS which is injected via a `<style>` tag.

### 3D Viewer

`frontend/src/components/StlViewer.tsx` loads and renders STL (binary + ASCII), OBJ, and 3MF files directly in the browser using Babylon.js. Parsing is done in the main thread without web workers; files larger than ~50 MB may be slow to load.

---

## Backend

**Tech:** PHP 8.3 · Custom router · PDO (MariaDB) · PHP Redis extension

### Entry point

`backend/public/index.php` is the single entry point for all API requests. It:
1. Applies CORS middleware
2. Starts the PHP session
3. Routes requests to the appropriate controller method based on HTTP method and path pattern
4. Catches `HttpException` and unhandled exceptions; returns structured JSON error responses

### Controllers

Each controller class handles one resource:

| Controller | Responsibility |
|---|---|
| `Auth` | Login, logout, session management, TOTP setup/verify/disable |
| `Design` | CRUD for designs, search, pagination, sync, cover fetch, duplicate check |
| `DesignFile` | File upload, versioning, per-entry download, 3D preview streaming, slicer token |
| `DesignImage` | Additional image upload and deletion per design |
| `Download` | Queue a URL for download, SSE progress streaming, retry/cancel/dismiss |
| `Collection` | Collections CRUD and design membership |
| `Tag` | Tag CRUD |
| `Share` | Design sharing between users |
| `Image` | Cover image serving |
| `User` | Own profile, password change, avatar, language, notification preferences |
| `UserAdmin` | Admin-only: user list, create, update, delete, reset password, stats, health |
| `UserPlatform` | Per-user platform credential storage (token, username, password - all encrypted) |
| `Notification` | In-app notifications: list, mark-read, delete, preferences |
| `Sync` | Per-design and bulk sync-all triggers |

### Helpers

| Class | Purpose |
|---|---|
| `Request` | Wraps `$_SERVER`, `php://input`; parses JSON body; provides `query()` and `sessionUser()` |
| `Response` | Writes JSON responses with HTTP status codes |
| `HttpException` | Thrown to produce structured error responses; caught in `index.php` |
| `Crypto` | AES-256-CBC encrypt/decrypt; per-user key derived from `APP_KEY` via HMAC-SHA256 |
| `Logger` | Leveled logging (`none` / `error` / `warning` / `info`) via `error_log()` |

### Middleware

| Class | Purpose |
|---|---|
| `CorsMiddleware` | Sets CORS headers; handles preflight OPTIONS requests |
| `AuthMiddleware` | Validates session; resolves `user_id` from `$_SESSION` |

### Platform implementations (`src/Platforms/`)

Each platform has a dedicated download class with a `download(string $url, int $userId): array` method.

| Class | Platform | Auth method |
|---|---|---|
| `ThingiverseDownload` | Thingiverse | Bearer token (App Token) |
| `PrintablesDownload` | Printables | Email + password auto-login; bearer token cached 25 days |
| `MakerworldDownload` | MakerWorld (Bambu) | Email + password auto-login via Tor; TOTP supported; token cached 85 days |
| `ThangsDownload` | Thangs | Email + password auto-login |
| `Cults3dDownload` | Cults3D | Playwright browser automation for file capture |
| `MyMiniFactoryDownload` | MyMiniFactory | API key |
| `PlatformHelpers` | - | Shared: cURL, image download, Playwright proxy, Tor circuit, TOTP generator |
| `BlobStore` | - | Content-addressed file storage: stores files by SHA-256 hash under `{STL_PATH}/blobs/{hash[0:2]}/{hash[2:4]}/{hash}`; identical content is stored exactly once across all designs and versions |

All platform credentials are decrypted at download time using `Crypto::decrypt()`. Auto-refreshed tokens (e.g. Printables, MakerWorld bearer tokens) are re-encrypted before being written back to the database.

### Workers (`src/Workers/`)

| Class | Trigger | Purpose |
|---|---|---|
| `DownloadWorker` | `cronjob` container (every 30 s) | Picks up `pending` download queue jobs (up to 3 at a time); calls the correct platform download class; updates DB; creates notifications; auto-resets stuck jobs after 15 min (max 3 retries) |
| `PlatformDownload` | `bin/PlatformDownload.php` CLI | Single-job download path used by SSE streaming (called per job) |
| `SyncWorker` | `bin/SyncDesigns.php` CLI | Picks up `pending` entries from `sync_queue`; spawns `SyncBackground.php` for each job (up to 5 at a time) |
| `SyncBackground` | `bin/SyncBackground.php` CLI | Processes one `sync_queue` entry: re-downloads files, deduplicates via `BlobStore`, creates a new version if content changed |
| `QueueWorker` | `bin/QueueWorker.php` CLI | Internal helper shared by the download worker loop |

### Logging

The `App\Helpers\Logger` class provides leveled logging across all backend code. The active level is set via the `LOG_LEVEL` environment variable:

| Level | What is logged |
|---|---|
| `none` | Nothing |
| `error` | Unhandled exceptions and hard failures only *(recommended for production)* |
| `warning` | Errors + soft failures: expired tokens, empty downloads, failed login attempts |
| `info` | Full trace of all download steps - useful for debugging import problems |

Web requests log via `error_log()` → PHP-FPM stderr → `docker logs meshdepot_php`.
CLI scripts log to STDERR → `docker logs meshdepot_cronjob`.

```bash
# Download queue and sync logs
docker logs meshdepot_cronjob --follow

# API errors
docker logs meshdepot_php --follow

# Playwright / Cults3D automation
docker logs meshdepot_playwright --follow
```

---

## Sessions

Sessions use PHP's native session mechanism with Redis as the session handler (`session.save_handler = redis`). On login, a secure `HttpOnly; SameSite=Strict` cookie is set. `AuthMiddleware` validates that the session exists and contains a valid `user_id`.

Short-lived tokens (slicer URL scheme, TOTP pending) are stored directly in Redis with explicit TTLs:

| Key pattern | TTL | Purpose |
|---|---|---|
| `totp_pending:{token}` | 300 s | Pending TOTP login |
| `totp_setup:{userId}` | 600 s | TOTP setup confirmation |
| `slicer_token:{token}` | 60 s | Single-use slicer file access |
| `login_attempts:{ip}` | 900 s | Failed login rate limiting (max 10 attempts) |

---

## Download Queue

Designs imported from external platforms are downloaded asynchronously:

1. `POST /api/download` creates a `download_queue` record with `status = pending`
2. The `cronjob` container runs the download worker every 30 seconds
3. The worker picks up pending jobs (up to 3 per run), calls the platform download class, saves files and metadata, marks the job `done`, and creates a notification
4. Jobs stuck in `downloading` for more than 15 minutes are reset to `pending` (up to 3 retries total), then marked `failed`
5. Live progress during a single import is streamed to the frontend via Server-Sent Events (`GET /api/download/stream`)

---

## Database Schema

### Tables

| Table | Purpose |
|---|---|
| `users` | User accounts: name, email, bcrypt hash, admin flag, language, TOTP secret (encrypted), avatar path |
| `tags` | User-scoped tags with name and color |
| `designs` | Design metadata: name, description, author, source URL, platform, source ID, cover path, rating, notes |
| `design_tags` | Many-to-many: designs ↔ tags |
| `design_files` | File versions per design: path, total size, file count, `is_current` flag, version string, notes |
| `design_file_entries` | Individual files within a version: filename, path, size, SHA-256 hash, relative path, blob hash |
| `design_images` | Additional images per design: path, sort order |
| `platform_accounts` | Per-user platform credentials: token, username, password, TOTP secret - all AES-256 encrypted; `auto_library_sync` flag controls whether the platform is included in scheduled library syncs |
| `download_queue` | Async download jobs: source URL, platform, status, retry count, error message, progress |
| `sync_queue` | Async design sync jobs: design ID, user ID, status (`pending`/`running`/`done`/`failed`), progress, error message, done timestamp |
| `file_blobs` | Registry of all content-addressed blobs: SHA-256 hash and size; one row per unique file content |
| `collections` | User-scoped named collections |
| `design_collections` | Many-to-many: collections ↔ designs |
| `design_shares` | Sharing records: which design is shared with which user by which owner |
| `design_sync_log` | Log of sync events per design (platform, status, timestamp) |
| `notifications` | In-app notification records per user |
| `notification_prefs` | Per-user notification preferences (sync_update, download_failed, design_shared, storage_80) |
| `app_settings` | Global key/value application settings (library sync hour, per-platform download cooldowns) |

### Relationships

```
users 1──n designs 1──n design_files 1──n design_file_entries
      1──n tags    n──m designs       (design_tags)
      1──n collections n──m designs   (design_collections)
      1──n platform_accounts
      1──n download_queue
      1──n sync_queue
      1──n notifications
      1──n notification_prefs
designs 1──n design_images
designs 1──n design_sync_log
designs n──m users (design_shares)
design_file_entries n──1 file_blobs (via blob_hash)
```

---

## File Storage

Files are stored on the host filesystem and bind-mounted into the containers:

| Variable | Default host path | Container path | Contents |
|---|---|---|---|
| `STL_PATH` | `./data/stl` | `/data/stl` | Design files, blobs, and temp dirs |
| `COVERS_PATH` | `./data/covers` | `/data/covers` | Cover images: `{userId}/cover_*.{ext}` |
| `THUMBNAILS_PATH` | `./data/thumbnails` | `/data/thumbnails` | Thumbnails (auto-generated) |

Under `/data/stl`:
- `{userId}/{designId}/{version}/` - legacy version directories (logical label only; actual data is in blobs)
- `blobs/{hash[0:2]}/{hash[2:4]}/{hash}` - content-addressed blob store; each unique file is stored once
- `tmp/{userId}/dl_{uniqid}/` - temporary download directories; cleaned up after each job

---

## Security

- Passwords hashed with PHP `password_hash()` (bcrypt, cost 10)
- Platform credentials (tokens, usernames, passwords) encrypted with AES-256-CBC; per-user key derived from `APP_KEY` via HMAC-SHA256
- Session IDs managed by PHP; stored in Redis; `HttpOnly; SameSite=Strict` cookie
- All API routes except `/api/auth/login` require a valid session
- Admin-only routes check the `admin` flag on the user record
- CORS restricted to the configured frontend origin
- Rate limiting on login: 10 failed attempts per IP per 15-minute window → 429
- Credential check before queuing a download: if a platform requires credentials that are missing, the request is rejected immediately with a descriptive error
