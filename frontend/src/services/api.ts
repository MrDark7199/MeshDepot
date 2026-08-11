import type {
  Collection, Design, DesignFile, DesignShare, Filters, PlatformAccount, QueueItem,
  DesignID, ShareLink, SyncJob, SyncState, Tag, UrlCheckResult, User, UserShareLink,
} from '../types'

const BASE = '/api/v1'

/** Hard ceiling for a single API call - without it a hung request keeps the
 *  triggering view in its loading state forever. */
const REQUEST_TIMEOUT_MS = 30000

/**
 * Makes an authenticated JSON API request and returns the parsed response.
 *
 * @param signal - optional caller-owned AbortSignal (e.g. from a view that may
 *   unmount mid-flight); it is combined with the built-in timeout.
 */
async function request<T = unknown>(method: string, path: string, body: unknown = null, signal?: AbortSignal): Promise<{ data: T; message?: string }> {
  const timeoutController = new AbortController()
  const timeoutId = setTimeout(() => timeoutController.abort(), REQUEST_TIMEOUT_MS)
  if (signal) {
    if (signal.aborted) timeoutController.abort()
    else signal.addEventListener('abort', () => timeoutController.abort(), { once: true })
  }
  const options: RequestInit = {
    method,
    credentials: 'include',
    headers: { 'Content-Type': 'application/json' },
    signal: timeoutController.signal,
  }
  // `!== null` and not a truthiness check: a deliberately falsy body (0, '',
  // false) is a valid payload and used to be dropped silently.
  if (body !== null) options.body = JSON.stringify(body)
  try {
    const response = await fetch(BASE + path, options)
    const responseData = await response.json().catch(() => ({}))
    if (!response.ok) {
      if (response.status === 401) window.dispatchEvent(new CustomEvent('auth:unauthorized'))
      throw new Error(responseData.message || responseData.error || `HTTP ${response.status}`)
    }
    return responseData
  } catch (failure) {
    if (failure instanceof DOMException && failure.name === 'AbortError') {
      throw new Error(signal?.aborted ? 'error.aborted' : 'error.timeout')
    }
    throw failure
  } finally {
    clearTimeout(timeoutId)
  }
}

/**
 * Posts a FormData body (file uploads) and returns the parsed response.
 *
 * Deliberately not routed through `request`: multipart bodies must not carry a
 * JSON content type (the browser sets the multipart boundary itself), and an
 * upload of a large model legitimately runs longer than REQUEST_TIMEOUT_MS.
 * Everything else is kept in step with `request` - most importantly the 401
 * event, without which a dead session shows up as a plain "upload failed".
 */
async function upload(path: string, formData: FormData): Promise<any> {
  const response = await fetch(BASE + path, { method: 'POST', credentials: 'include', body: formData })
  const responseData = await response.json().catch(() => ({}))
  if (!response.ok) {
    if (response.status === 401) window.dispatchEvent(new CustomEvent('auth:unauthorized'))
    throw new Error(responseData.error || responseData.message || 'Upload failed')
  }
  return responseData
}

/** Payload of GET /designs - one page of the library plus the total row count. */
export interface DesignPage {
  items: Design[]
  total: number
}

export const api = {
  // ── Auth ──────────────────────────────────────────────────────────────────
  login:       (credentials: { email: string; password: string; remember?: boolean }) => request('POST', '/auth/login', credentials),
  totpVerify:  (body: { pending_token: string; code: string; remember?: boolean }) => request('POST', '/auth/totp/verify', body),
  totpSetup:   () => request('POST', '/auth/totp/setup'),
  totpEnable:  (body: { code: string }) => request('POST', '/auth/totp/enable', body),
  totpDisable: (body: { password: string }) => request('POST', '/auth/totp/disable', body),
  logout:      () => request('POST', '/auth/logout'),
  me:          () => request<User>('GET',  '/auth/me'),

  // ── Designs ───────────────────────────────────────────────────────────────
  getDesigns: (searchQuery: string, filters: Partial<Filters> = {}, page = 1, perPage = 50, sort?: { field: string; dir: string }) => {
    const params = new URLSearchParams()
    if (searchQuery) params.set('search', searchQuery)
    if (filters.source_platform) params.set('source_platform', filters.source_platform)
    if (filters.tag_ids?.length)  params.set('tag_ids', filters.tag_ids.join(','))
    if (filters.shared_only)      params.set('shared_only', '1')
    if (filters.show_hidden)      params.set('show_hidden', '1')
    if (sort)                   { params.set('sort', sort.field); params.set('dir', sort.dir) }
    params.set('page', String(page))
    params.set('per_page', String(perPage))
    return request<DesignPage>('GET', `/designs?${params.toString()}`)
  },
  getDesign:     (designId: DesignID) => request<Design>('GET', `/designs/${designId}`),
  createDesign:  (data: Record<string, unknown>) => request<Design>('POST', '/designs', data),
  updateDesign:  (designId: DesignID, data: Record<string, unknown>) => request<Design>('PUT', `/designs/${designId}`, data),
  deleteDesign:  (designId: DesignID) => request('DELETE', `/designs/${designId}`),
  fetchCover:    (designId: DesignID) => request('POST', `/designs/${designId}/fetch-cover`),
  setDesignTags: (designId: DesignID, tagIds: number[]) => request('PUT', `/designs/${designId}/tags`, { tag_ids: tagIds }),
  syncDesign:    (designId: DesignID) => request('POST', `/designs/${designId}/sync`),
  syncAll:       () => request('POST', '/designs/sync-all'),
  getSyncStatus: () => request<SyncJob[]>('GET', '/designs/sync-status'),
  checkDuplicates: (designId: DesignID) => request('GET', `/designs/${designId}/duplicates`),
  checkUrl: (url: string) => request<UrlCheckResult>('GET', `/designs/check-url?url=${encodeURIComponent(url)}`),

  // ── Design Images ─────────────────────────────────────────────────────────
  uploadImage: (designId: DesignID, formData: FormData) => upload(`/designs/${designId}/images`, formData),
  deleteImage: (designId: DesignID, imageId: number) => request('DELETE', `/designs/${designId}/images/${imageId}`),
  setDesignImageCover: (designId: DesignID, imageId: number) => request('PUT', `/designs/${designId}/images/${imageId}/cover`),

  // ── Design Files ──────────────────────────────────────────────────────────
  getFiles:    (designId: DesignID) => request<DesignFile[]>('GET', `/designs/${designId}/files`),
  uploadFile:  (designId: DesignID, formData: FormData) => upload(`/designs/${designId}/files`, formData),
  addEntries:  (designId: DesignID, fileVersionId: number, formData: FormData) =>
    upload(`/designs/${designId}/files/${fileVersionId}/entries`, formData),
  deleteFile:  (designId: DesignID, fileVersionId: number) => request('DELETE', `/designs/${designId}/files/${fileVersionId}`),
  deleteFileEntry: (designId: DesignID, fileVersionId: number, entryId: number) =>
    request('DELETE', `/designs/${designId}/files/${fileVersionId}/entry/${entryId}`),
  downloadUrl: (designId: DesignID, fileVersionId: number) => `${BASE}/designs/${designId}/files/${fileVersionId}/download`,
  entryUrl:    (designId: DesignID, fileVersionId: number, entryId: number) =>
    `${BASE}/designs/${designId}/files/${fileVersionId}/entry/${entryId}`,
  // Reconstructed 3D mesh (binary STL) of a resin file (.pwmx).
  pwmxMeshUrl: (designId: DesignID, fileVersionId: number, entryId: number) =>
    `${BASE}/designs/${designId}/files/${fileVersionId}/entry/${entryId}/pwmx/mesh`,
  createDownloadToken: (designId: DesignID, fileVersionId: number, entryId: number) =>
    request('POST', `/designs/${designId}/files/${fileVersionId}/entry/${entryId}/token`),
  stlUrl:      (designId: DesignID, fileVersionId: number) => `${BASE}/designs/${designId}/files/${fileVersionId}/stl`,

  // ── Tags ─────────────────────────────────────────────────────────────────
  getTags:   () => request<Tag[]>('GET', '/tags'),
  searchTags: (q: string, limit = 20) => request<Tag[]>('GET', `/tags/search?q=${encodeURIComponent(q)}&limit=${limit}`),
  createTag: (data: { name: string; color: string }) => request<Tag>('POST', '/tags', data),
  deleteTag: (tagId: number) => request('DELETE', `/tags/${tagId}`),

  // ── Collections ───────────────────────────────────────────────────────────
  getCollections:      () => request<Collection[]>('GET', '/collections'),
  createCollection:    (data: Record<string, unknown>) => request<Collection>('POST', '/collections', data),
  updateCollection:    (collectionId: number, data: Record<string, unknown>) => request('PUT', `/collections/${collectionId}`, data),
  deleteCollection:    (collectionId: number) => request('DELETE', `/collections/${collectionId}`),
  getCollectionDesigns:(collectionId: number) => request<Design[]>('GET', `/collections/${collectionId}/designs`),
  getAddableDesigns:   (collectionId: number, search = '', page = 1, perPage = 40) =>
    request('GET', `/collections/${collectionId}/addable-designs?search=${encodeURIComponent(search)}&page=${page}&per_page=${perPage}`),
  addToCollection:     (collectionId: number, designIds: DesignID[]) => request('POST', `/collections/${collectionId}/designs`, { design_ids: designIds }),
  removeFromCollection:(collectionId: number, designId: DesignID) => request('DELETE', `/collections/${collectionId}/designs/${designId}`),
  getDesignCollections:(designId: DesignID) => request<Collection[]>('GET', `/designs/${designId}/collections`),

  // ── Share links (a design handed to someone without an account) ───────────
  getShareLinks:    (designId: DesignID) => request<ShareLink[]>('GET', `/designs/${designId}/links`),
  /** Every link of the account, whichever design it belongs to. */
  getUserShareLinks: (userId: string) => request<UserShareLink[]>('GET', `/users/${encodeURIComponent(userId)}/share-links`),
  createShareLink:  (designId: DesignID, expiresInHours: number) =>
    request<ShareLink>('POST', `/designs/${designId}/links`, { expires_in_hours: expiresInHours }),
  deleteShareLink:  (designId: DesignID, linkId: number) => request('DELETE', `/designs/${designId}/links/${linkId}`),
  /** The address to hand out - the page it opens needs no session. */
  shareLinkUrl:     (token: string) => `${window.location.origin}${window.location.pathname}?share=${encodeURIComponent(token)}`,

  // ── Shares ────────────────────────────────────────────────────────────────
  getShares:    (designId: DesignID) => request<DesignShare[]>('GET', `/designs/${designId}/shares`),
  shareDesign:  (designId: DesignID, emails: string[]) => request('POST', `/designs/${designId}/shares`, { emails }),
  unshareDesign:(designId: DesignID, shareId: number) => request('DELETE', `/designs/${designId}/shares/${shareId}`),

  // ── Users ─────────────────────────────────────────────────────────────────
  searchUsers:         (query: string) => request<User[]>('GET', `/users/search?q=${encodeURIComponent(query)}`),
  updateProfile:       (userId: string, data: Record<string, unknown>) => request<User>('PUT', `/users/${encodeURIComponent(userId)}/profile`, data),
  uploadAvatar:        (userId: string, file: File) => {
    const formData = new FormData(); formData.append('avatar', file)
    return upload(`/users/${encodeURIComponent(userId)}/avatar`, formData)
  },
  deleteAvatar:        (userId: string) => request('DELETE', `/users/${encodeURIComponent(userId)}/avatar`),
  changePassword:      (userId: string, data: Record<string, unknown>) => request('POST', `/users/${encodeURIComponent(userId)}/change-password`, data),
  forcePasswordChange: (userId: string, newPassword: string) => request('POST', `/users/${encodeURIComponent(userId)}/force-password`, { new_password: newPassword }),
  getUserStats:        (userId: string) => request('GET', `/users/${encodeURIComponent(userId)}/stats`),
  /** Cooldowns, queue depth and whether any platform account exists - what the sync buttons need. */
  getSyncState:        (userId: string) => request<SyncState>('GET', `/users/${encodeURIComponent(userId)}/sync-state`),

  // ── Download Queue ────────────────────────────────────────────────────────
  queueDownload:    (data: { source_url: string; platform?: string }) => request('POST', '/download', data),
  getQueue:         () => request<QueueItem[]>('GET', '/download/queue'),
  getQueueItem:     (queueId: number) => request<QueueItem>('GET', `/download/${queueId}`),
  retryDownload:    (queueId: number) => request('POST', `/download/${queueId}/retry`),
  cancelDownload:   (queueId: number) => request('DELETE', `/download/${queueId}`),
  dismissDownload:  (queueId: number) => request('DELETE', `/download/${queueId}/dismiss`),

  // ── Admin ─────────────────────────────────────────────────────────────────
  adminStats:         () => request('GET', '/admin/stats'),
  adminListUsers:     () => request<User[]>('GET', '/admin/users'),
  adminCreateUser:    (data: Record<string, unknown>) => request('POST', '/admin/users', data),
  adminUpdateUser:    (userId: string, data: Record<string, unknown>) => request('PUT', `/admin/users/${encodeURIComponent(userId)}`, data),
  adminDeleteUser:    (userId: string) => request('DELETE', `/admin/users/${encodeURIComponent(userId)}`),
  adminResetPassword: (userId: string, data: Record<string, unknown>) => request('POST', `/admin/users/${encodeURIComponent(userId)}/reset-password`, data),
  adminUserDetail:    (userId: string) => request('GET', `/admin/users/${encodeURIComponent(userId)}/detail`),
  adminHealth:        () => request('GET', '/admin/health'),
  adminGetSettings:    () => request('GET', '/admin/settings'),
  adminSaveSettings:   (data: Record<string, unknown>) => request('PUT', '/admin/settings', data),
  adminRunLibrarySync: () => request('POST', '/admin/library-sync/run'),
  adminTranslationBackfill: () => request('POST', '/admin/translations/backfill'),
  getPublicSettings:   () => request('GET', '/settings/public'),

  // ── Notifications ─────────────────────────────────────────────────────────
  getNotifications:   (userId: string) => request('GET',  `/users/${encodeURIComponent(userId)}/notifications`),
  markAllRead:        (userId: string) => request('POST',   `/users/${encodeURIComponent(userId)}/notifications/read-all`),
  deleteAllNotifications: (userId: string) => request('DELETE', `/users/${encodeURIComponent(userId)}/notifications`),
  deleteNotification: (userId: string, notifId: number) => request('DELETE', `/users/${encodeURIComponent(userId)}/notifications/${notifId}`),
  getNotifPrefs:      (userId: string) => request('GET',  `/users/${encodeURIComponent(userId)}/notification-prefs`),
  saveNotifPrefs:     (userId: string, data: Record<string, number | null>) => request('PUT', `/users/${encodeURIComponent(userId)}/notification-prefs`, data),

  // ── Covers ────────────────────────────────────────────────────────────────
  coverUrl: (relativePath: string) => `${BASE}/covers/${relativePath}`,

  // ── Platform Accounts ─────────────────────────────────────────────────────
  getPlatformAccounts:   (userId: string) => request<PlatformAccount[]>('GET', `/users/${encodeURIComponent(userId)}/platform-accounts`),
  savePlatformAccount:   (userId: string, data: Record<string, unknown>) => request('POST', `/users/${encodeURIComponent(userId)}/platform-accounts`, data),
  validatePlatformAccount: (userId: string, data: Record<string, unknown>) => request('POST', `/users/${encodeURIComponent(userId)}/platform-accounts/validate`, data),
  deletePlatformAccount: (userId: string, accountId: number) => request('DELETE', `/users/${encodeURIComponent(userId)}/platform-accounts/${accountId}`),
  syncPlatformLibrary:      (userId: string, platform: string) => request('POST', `/users/${encodeURIComponent(userId)}/platform-accounts/${encodeURIComponent(platform)}/sync`),
  syncAllPlatformLibraries: (userId: string) => request('POST', `/users/${encodeURIComponent(userId)}/platform-accounts/sync-all`),
}
