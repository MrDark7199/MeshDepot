# MeshDepot - API Reference

All endpoints are prefixed with `/api`. The frontend nginx proxies `/api/*` to the backend transparently, so browser requests use the same origin as the UI.

Authentication is via PHP session cookie set on login. All endpoints except `POST /api/auth/login` and `GET /api/health` require a valid session.

All responses are JSON. Successful responses follow `{ "data": ... }` or `{ "data": ..., "message": "..." }`. Error responses follow `{ "error": "error.key" }`.

---

---

## Authentication

### POST /api/auth/login
Log in with email/username and password.

**Request body:**
```json
{ "identifier": "admin", "password": "secret", "remember": false }
```

**Response:** `200 OK` - sets `meshdepot_sess` cookie
```json
{ "id": 1, "name": "Admin", "email": "admin@example.com", "admin": true, "language": "en" }
```

If TOTP is enabled, the response contains `{ "pending_token": "..." }` instead of setting a session. Pass the `pending_token` + `code` to `/api/auth/totp/verify` to complete login.

---

### POST /api/auth/logout
Invalidates the current session.

**Response:** `204 No Content`

---

### GET /api/auth/me
Returns the currently authenticated user.

**Response:** `200 OK`
```json
{ "id": 1, "name": "Admin", "email": "admin@example.com", "admin": true, "avatar_url": null, "language": "en" }
```

---

### POST /api/auth/totp/verify
Complete a login that requires TOTP.

**Request body:**
```json
{ "pending_token": "...", "code": "123456" }
```

**Response:** `200 OK` - sets `meshdepot_sess` cookie

---

### POST /api/auth/totp/setup
Generate a new TOTP secret and QR code URI for the authenticated user.

**Response:** `200 OK`
```json
{ "secret": "BASE32SECRET", "otpauth_uri": "otpauth://totp/..." }
```

---

### POST /api/auth/totp/enable
Confirm and activate TOTP using a valid code from the authenticator app.

**Request body:** `{ "code": "123456" }`

---

### POST /api/auth/totp/disable
Disable TOTP for the authenticated user.

**Request body:** `{ "code": "123456" }`

---

## Designs

### GET /api/designs
Returns designs belonging to the authenticated user (and designs shared with them), paginated.

**Query parameters:**
- `search` - search string (matches name, description, author)
- `source_platform` - filter by platform slug (e.g. `thingiverse`, `printables`)
- `tag_ids` - comma-separated tag IDs to filter by
- `shared_only` - `1` to return only designs shared with the user
- `since_id` - return only designs with `id` greater than this value (used for polling new entries without a full reload)
- `page` - page number (default: `1`)
- `per_page` - results per page (default: `50`, max: `1000`)

**Response:** `200 OK`
```json
{
  "items": [ { "id": 1, "name": "...", "tags": [ ... ], ... } ],
  "total": 42
}
```

---

### GET /api/designs/check-url
Check whether a source URL already exists in the user's library.

**Query parameters:**
- `url` - the URL to check

**Response:** `200 OK`
```json
{ "exists": true, "design": { "id": 5, "name": "My Model" } }
```
or
```json
{ "exists": false, "design": null }
```

---

### GET /api/designs/sync-status
Returns the current sync job status (running / idle).

---

### POST /api/designs/sync-all
Trigger an update check for all syncable designs belonging to the authenticated user.

---

### POST /api/designs
Create a new design manually (without a source URL).

**Request body:**
```json
{ "name": "My Design", "source_platform": "manual" }
```

**Response:** `200 OK` - created design object

---

### GET /api/designs/{id}
Returns a single design by ID.

---

### PUT /api/designs/{id}
Update design metadata.

**Request body:** partial design fields - `name`, `description`, `category`, `license`, `author`, `source_url`, `notes`, `rating`, `print_time_minutes`

---

### DELETE /api/designs/{id}
Delete a design and all associated files, images, and queue entries.

---

### PUT /api/designs/{id}/tags
Replace the tag set for a design.

**Request body:** `{ "tag_ids": [1, 3, 7] }`

---

### POST /api/designs/{id}/sync
Queue a single design for a background sync. Creates a `sync_queue` entry with `status = pending`. Returns `already_queued` if a pending or running job for the design already exists.

**Response:** `200 OK`
```json
{ "design_id": 42, "status": "queued" }
```

---

### GET /api/designs/{id}/sync/stream
Server-Sent Events stream for a live, foreground sync of a single design (re-downloads immediately in the request process). Use this for the interactive "sync now" button; the background worker uses `sync_queue` entries.

**Events emitted:**
- `step` - `{ type: "step", step: "connect", label: "Connecting to …" }`
- `step` - `{ type: "step", step: "store", label: "New file 1/3: …", current: 1, total: 3 }`
- `step` - `{ type: "step", step: "skip", label: "Unchanged: …", skipped: true }`
- `done` - `{ type: "done", design_id: 42, version: "2.0", file_count: 3, changed: true }`
- `done` - `{ type: "done", design_id: 42, changed: false }` (when all files are identical)
- `error` - `{ type: "error", message: "..." }`

---

### POST /api/designs/{id}/fetch-cover
Fetch or re-fetch the cover image from the source platform.

---

### GET /api/designs/{id}/duplicates
Returns designs in the user's library that share the same source platform and source ID.

---

### GET /api/designs/{id}/collections
Returns collections that contain this design.

---

## Design Files

### GET /api/designs/{id}/files
Returns all file versions for a design.

---

### POST /api/designs/{id}/files
Upload a new file version (multipart/form-data).

**Form fields:**
- `file` - the file (ZIP, STL, OBJ, 3MF)
- `version` - version string (e.g. `"1.1"`)
- `notes` - optional notes

---

### DELETE /api/designs/{designId}/files/{fileId}
Delete a specific file version.

---

### GET /api/designs/{designId}/files/{fileId}/download
Download the full version archive as an octet stream.

---

### GET /api/designs/{designId}/files/{fileId}/entry/{entryId}
Download a single file entry from within a version.

---

### GET /api/designs/{designId}/files/{fileId}/stl
Stream a file for in-browser 3D preview (used by the Babylon.js viewer).

---

### POST /api/designs/{designId}/files/{fileId}/slicer-token
Generate a short-lived (60 s) token for opening the file in a local slicer application.

---

### GET /api/files/token/{token}
Serve a file using a slicer token (no session required; token is single-use, 60 s TTL).

---

## Design Images

### POST /api/designs/{id}/images
Upload an additional image for a design (multipart/form-data, field: `file`).

---

### DELETE /api/designs/{id}/images/{imageId}
Delete a specific design image.

---

## Download Queue

### POST /api/download
Queue a design for download from a platform URL.

**Request body:**
```json
{ "source_url": "https://www.thingiverse.com/thing:12345" }
```

**Response:** `201 Created`
```json
{ "queue_id": 7, "platform": "thingiverse", "status": "pending" }
```

Returns `422` if the platform requires credentials that are not configured.

---

### GET /api/download/stream
Server-Sent Events endpoint. Streams live progress for an active download.

**Events emitted:**
- `step` - `{ type: "step", step: "connect", label: "Connecting to Thingiverse…" }`
- `done` - `{ type: "done", design_id: 42 }`
- `error` - `{ type: "error", message: "..." }`

---

### GET /api/download/queue
Returns all download queue entries for the authenticated user.

---

### GET /api/download/{id}
Returns the status of a single queue entry.

---

### POST /api/download/{id}/retry
Reset a failed download queue entry to `pending` so the worker retries it.

---

### DELETE /api/download/{id}
Cancel and remove a `pending` or `downloading` queue entry.

---

### DELETE /api/download/{id}/dismiss
Dismiss a `done` or `failed` queue entry (removes it from the list).

---

## Collections

### GET /api/collections
Returns all collections for the authenticated user.

---

### POST /api/collections
Create a new collection.

**Request body:** `{ "name": "My Collection" }`

---

### PUT /api/collections/{id}
Rename a collection.

**Request body:** `{ "name": "New Name" }`

---

### DELETE /api/collections/{id}
Delete a collection (does not delete the designs inside).

---

### GET /api/collections/{id}/designs
Returns all designs in a collection.

---

### POST /api/collections/{id}/designs
Add a design to a collection.

**Request body:** `{ "design_id": 42 }`

---

### DELETE /api/collections/{id}/designs/{designId}
Remove a design from a collection.

---

## Tags

### GET /api/tags
Returns all tags for the authenticated user.

---

### POST /api/tags
Create a new tag.

**Request body:** `{ "name": "Functional", "color": "#457b9d" }`

---

### PATCH /api/tags/{id}
Update a tag name or color.

**Request body:** `{ "name": "Updated", "color": "#e63946" }`

---

### DELETE /api/tags/{id}
Delete a tag (removes it from all designs).

---

## Sharing

### GET /api/designs/{id}/shares
Returns all sharing records for a design (who has access).

---

### POST /api/designs/{id}/shares
Share a design with one or more users by email.

**Request body:** `{ "emails": ["user@example.com"] }`

---

### DELETE /api/designs/{id}/shares/{shareId}
Revoke access for a specific share.

---

## Notifications

### GET /api/users/{id}/notifications
Returns all notifications for the specified user.

---

### POST /api/users/{id}/notifications/mark-all-read
Mark all notifications for the user as read.

---

### DELETE /api/users/{id}/notifications
Delete all notifications for the user.

---

### DELETE /api/users/{id}/notifications/{notificationId}
Delete a single notification.

---

### GET /api/users/{id}/notification-prefs
Returns notification preferences for the user.

---

### PUT /api/users/{id}/notification-prefs
Update notification preferences.

**Request body:**
```json
{
  "sync_update": 1,
  "download_failed": 1,
  "design_shared": 1,
  "storage_80": 0,
  "sync_min_age_days": 7
}
```

---

## Images

### POST /api/covers/{designId}
Upload a cover image for a design (multipart/form-data, field: `file`).

---

### GET /api/covers/{filename}
Serve a cover image file.

---

## Account (current user)

### PUT /api/users/{id}/profile
Update own profile (name, email, language).

---

### POST /api/users/{id}/change-password
Change own password.

**Request body:** `{ "current_password": "old", "new_password": "new" }`

---

### GET /api/users/{id}/avatar
Serve the avatar image for the user.

---

### POST /api/users/{id}/avatar
Upload a profile avatar (multipart/form-data, field: `file`).

---

### DELETE /api/users/{id}/avatar
Remove the profile avatar.

---

### GET /api/users/{id}/stats
Returns storage and design statistics for the user.

---

### GET /api/users/{id}/platform-accounts
Returns platform credentials for the specified user. Tokens and usernames are decrypted for display; passwords are replaced by a `has_password: true/false` flag.

---

### POST /api/users/{id}/platform-accounts
Save or update a platform credential. All values are stored AES-256 encrypted. Omitting a field (or sending `password = "***"`) preserves the existing value - no need to re-send unchanged credentials.

**Request body:**
```json
{ "platform": "thingiverse", "token": "abc123", "auto_library_sync": true }
```
or
```json
{ "platform": "printables", "username": "user@example.com", "password": "secret", "auto_library_sync": true }
```
or (MakerWorld with TOTP):
```json
{ "platform": "makerworld", "username": "user@example.com", "password": "secret", "totp_secret": "BASE32SECRET" }
```

`auto_library_sync` (boolean, default `true`) - whether this account is included in scheduled library syncs.

---

### DELETE /api/users/{id}/platform-accounts/{accountId}
Remove a platform credential.

---

### POST /api/users/{id}/platform-accounts/sync-all
Trigger an immediate library sync for all `auto_library_sync`-enabled accounts of the user. Runs `LibrarySync.php` in the background; returns before the sync completes.

**Response:** `200 OK`
```json
{ "message": "Sync started" }
```

---

### POST /api/users/{id}/platform-accounts/{platform}/sync
Trigger an immediate library sync for a single platform account (e.g. `printables`). Runs `LibrarySync.php --platform={platform}` in the background.

**Response:** `200 OK`
```json
{ "message": "Sync started" }
```

---

## Admin (admin only)

All `/api/admin` endpoints require `admin: true` on the authenticated user.

### GET /api/admin/users
Returns all users with stats (design count, storage used).

---

### POST /api/admin/users
Create a new user.

**Request body:** `{ "name": "Jane", "email": "jane@example.com", "password": "secret", "admin": false }`

---

### PUT /api/admin/users/{id}
Update user profile (name, email, admin flag, active state).

---

### DELETE /api/admin/users/{id}
Permanently delete a user and all their data.

---

### POST /api/admin/users/{id}/force-password
Reset a user's password (admin action, no current password required).

**Request body:** `{ "password": "newpass" }`

---

### GET /api/admin/users/{id}/detail
Returns detailed information about a user.

---

### GET /api/admin/stats
Returns server-wide statistics (total users, designs, files, storage, platform breakdown).

---

### GET /api/admin/health
Returns system health status (database, Redis, disk space, PHP version).

---

### GET /api/admin/settings
Returns all application settings as a key/value map.

**Response:** `200 OK`
```json
{
  "library_sync_hour": 3,
  "download_cooldown_default": 0,
  "download_cooldown_printables": 0
}
```

---

### PUT /api/admin/settings
Update one or more application settings. Unknown keys are silently ignored; integer values are clamped to their allowed range.

**Request body:** partial key/value map, e.g.:
```json
{ "library_sync_hour": 2, "download_cooldown_makerworld": 60 }
```

Settable keys:

| Key | Type | Range | Description |
|---|---|---|---|
| `library_sync_hour` | int | 0–23 | Hour of day (UTC) when the scheduled library sync runs |
| `download_cooldown_default` | int | 0–3600 | Default delay (s) between downloads |
| `download_cooldown_printables` | int | 0–3600 | Per-platform override for Printables |
| `download_cooldown_thingiverse` | int | 0–3600 | Per-platform override for Thingiverse |
| `download_cooldown_makerworld` | int | 0–3600 | Per-platform override for MakerWorld |
| `download_cooldown_thangs` | int | 0–3600 | Per-platform override for Thangs |
| `download_cooldown_cults3d` | int | 0–3600 | Per-platform override for Cults3D |
| `download_cooldown_myminifactory` | int | 0–3600 | Per-platform override for MyMiniFactory |

---

### POST /api/admin/library-sync/run
Trigger an immediate library sync for all users (sets the `library_sync_force` flag; the cronjob picks it up). Returns before the sync completes.

**Response:** `200 OK`
```json
{ "message": "Library sync started" }
```

---

### GET /api/health
Public health check (no session required).

---

## Settings (authenticated)

### GET /api/settings/public
Returns public application settings accessible to all authenticated users. Currently exposes `library_sync_hour` so the UI can display the scheduled sync time.

**Response:** `200 OK`
```json
{ "library_sync_hour": 3 }
```

---

## Users (shared)

### GET /api/users/search
Search users by name or email (used for sharing suggestions).

---

## Error Responses

All errors return a JSON body with a machine-readable key:

```json
{ "error": "error.invalid_credentials" }
```

Error keys are translated by the frontend. Common keys:

| Key | Meaning |
|---|---|
| `error.unauthorized` | No valid session |
| `error.not_found` | Resource not found |
| `error.invalid_credentials` | Wrong email or password |
| `error.invalid_email` | Email format invalid |
| `error.wrong_password` | Current password incorrect |
| `error.url_required` | URL field is empty |
| `error.unsupported_platform` | URL does not match a supported platform |
| `error.platform_credentials_required:{platform}` | Platform requires credentials that are not configured |
| `error.internal` | Unexpected server error |
| `error.myminifactory_no_token` | MyMiniFactory API key missing |
| `error.myminifactory_restricted` | Model requires purchase or subscription |
| `error.thingiverse_restricted` | Thingiverse App Token required |
| `error.cults3d_restricted` | Cults3D credentials required for file download |
