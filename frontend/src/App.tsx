import { createSignal, createEffect, createMemo, on, onCleanup, onMount, Show, For } from 'solid-js'
import { useAuth } from './services/AuthContext'
import { useI18n } from './i18n/index'
import LoginPage from './pages/LoginPage'
import SharePage from './pages/SharePage'
import { AddDesignModal } from './components/DesignModals'
import { AccountSettingsModal, ForcePasswordChangeModal } from './components/AccountSettings'
import { ServerSettingsModal } from './components/ServerSettings'
import { DesignPage } from './components/DesignPage'
import { api } from './services/api'
import type { Design, DesignID, Tag, QueueBlock, DownloadJob, Filters, Collection } from './types'
import { createNotificationsStore } from './hooks/useNotifications'
import { createSyncProgressStore } from './hooks/useSyncProgress'
import { createDownloadQueueStore } from './hooks/useDownloadQueue'
import { displayName } from './utils/designText'
import { errorKey } from './utils/errorMessage'
import { formatDateTime } from './utils/datetime'
import { navBar, type NavBarDeps } from './app/navBar'
import { gridMain, type GridMainDeps } from './app/gridMain'
import { CollectionsPage } from './components/CollectionsPage'
import { ConfirmDiscardModal, ConfirmModal } from './components/ConfirmModals'
import { FilterModal } from './components/FilterModal'
import { QueuePanel } from './components/QueuePanel'
import { Toast } from './components/Toast'
import { globalStyles } from './styles/globalStyles'
import { makeTranslateError } from './utils/authError'

/** Base interval of the single background-poll ticker (see the onMount loop in MainApp). */
const POLL_TICK_MS = 2000

/** Possible top-level views. */
type AppView = 'grid' | 'design' | 'collections'

/**
 * Root authenticated application shell.
 *
 * Manages the global navigation state (grid / design detail / collections),
 * the sticky navigation bar with search/filter/sync-all controls and all
 * top-level modals (AddDesign, SyncModal, FilterModal, AccountSettings,
 * ServerSettings).
 *
 * The three self-contained background areas live in their own stores and are
 * only wired up here: {@link createNotificationsStore} (bell),
 * {@link createSyncProgressStore} (update checks) and
 * {@link createDownloadQueueStore} (downloads).
 *
 * Data flow:
 * - Designs, tags, collections, queue, and notifications are loaded on mount.
 * - Search and filter changes trigger debounced/immediate design reloads.
 * - One ticker refreshes the queue (6 s), notifications (8 s) and the grid
 *   (10 s); it pauses while the tab is hidden.
 */
function MainApp() {
  const {user, updateUser, logout} = useAuth()
  const {translate, lang, translateDesigns} = useI18n()
  const [queueBlocks, setQueueBlocks] = createSignal<QueueBlock[]>([])
  const loadQueueBlocks = () => api.getQueueBlocks().then((r: any) => setQueueBlocks(r.data || [])).catch(() => {})

  onMount(() => {
    const handler = () => logout()
    window.addEventListener('auth:unauthorized', handler, { once: true })
    onCleanup(() => window.removeEventListener('auth:unauthorized', handler))
  })

  // Deep link: ?design=<public id> opens the detail view directly (middle-click
  // → new window). The id is opaque, so it is taken as given and the server
  // decides whether it names anything.
  const deepLinkDesignId = new URLSearchParams(window.location.search).get('design') || ''
  const [view, setView] = createSignal<AppView>(deepLinkDesignId ? 'design' : 'grid')
  const [openDesignId, setOpenDesignId] = createSignal<DesignID | null>(deepLinkDesignId || null)
  const [openInEditMode, setOpenInEditMode] = createSignal(false)
  const [openCollectionId, setOpenCollectionId] = createSignal<number | null>(null)
  const [designs, setDesigns] = createSignal<Design[]>([])
  const [loading, setLoading] = createSignal(true)
  const [search, setSearch] = createSignal('')
  const [showUser, setShowUser] = createSignal(false)
  const [showAdd, setShowAdd] = createSignal(false)
  const [showAccountSettings, setShowAccountSettings] = createSignal(false)
  const [accountSettingsTab, setAccountSettingsTab] = createSignal('account')
  const [showServerSettings, setShowServerSettings] = createSignal(false)
  const [toast, setToast] = createSignal('')
  const [toastVariant, setToastVariant] = createSignal('success')
  const [showFilters, setShowFilters] = createSignal(false)
  const [filters, setFilters] = createSignal<Filters>({source_platform:'', tag_ids:[], shared_only:false, show_hidden:false})
  const [allTags, setAllTags] = createSignal<Tag[]>([])
  const [allCollections, setAllCollections] = createSignal<Collection[]>([])
  const [showSyncAllConfirm, setShowSyncAllConfirm] = createSignal(false)
  const [pendingDelete, setPendingDelete] = createSignal<Design | null>(null)
  // Off by default; see the same signal in DesignPage for why.
  const [excludeFromSync, setExcludeFromSync] = createSignal(false)
  const [page, setPage] = createSignal(1)
  const [hasPlatformAccount, setHasPlatformAccount] = createSignal(false)

  // The three self-contained areas - notifications, sync progress, download
  // queue - live in their own stores; MainApp only wires them together. Their
  // dependencies are handed in as closures because the functions they call
  // (showToast, prependNewDesigns, …) are only defined further down.
  const notifs = createNotificationsStore({
    userId: () => user()?.id,
    translate,
  })
  // Platform queue blocks/pauses surface as error entries in the notification
  // bell instead of a permanent top-right banner. One notification per newly
  // seen block; a block that clears is forgotten so a later re-block notifies
  // again, and its bell entry is withdrawn - see below.
  const notifiedBlockKeys = new Set<string>()
  // Title of the bell entry each platform currently has, so it can be taken back
  // once that platform runs again. Keyed by platform rather than by block key:
  // the key carries the block's expiry, which changes on a re-block.
  const blockNotificationTitles = new Map<string, string>()
  const blockKey = (block: QueueBlock) => `${block.platform}|${block.blocked ? (block.until || 'blocked') : 'paused'}`
  // Written through the shared formatter so it follows the account's notation
  // like every other date, instead of the DD/MM/YYYY it used to hard-code.
  const formatBlockUntil = (iso?: string) => (iso ? formatDateTime(iso, lang()) : '')
  createEffect(() => {
    const active = queueBlocks()
    const activeKeys = new Set(active.map(blockKey))
    const activePlatforms = new Set(active.map(block => block.platform))
    for (const block of active) {
      const key = blockKey(block)
      if (notifiedBlockKeys.has(key)) continue
      notifiedBlockKeys.add(key)
      const platformName = block.platform.charAt(0).toUpperCase() + block.platform.slice(1)
      const title = translate('queue_block_notif_title', { platform: platformName })
      const body = block.blocked
        ? translate('queue_block_alert_blocked', { until: formatBlockUntil(block.until) })
        : translate('queue_block_alert_paused')
      blockNotificationTitles.set(block.platform, title)
      notifs.pushError(title, body)
    }
    for (const key of [...notifiedBlockKeys]) {
      if (!activeKeys.has(key)) notifiedBlockKeys.delete(key)
    }
    // A platform that no longer appears in the active list is running again -
    // an admin lifted the pause or the automatic block expired. Its bell entry
    // says downloads are paused, which is now false, so it goes. This runs in
    // every user's tab on the next poll, which is what makes the message
    // disappear for everyone rather than only for the admin.
    for (const [platform, title] of [...blockNotificationTitles]) {
      if (activePlatforms.has(platform)) continue
      notifs.removeLocalByTitle(title)
      blockNotificationTitles.delete(platform)
    }
  })
  const sync = createSyncProgressStore({
    onJobFinished: job => {
      const name = job.design_name || String(job.design_id)
      if (job.status === 'failed') notifs.pushSyncError(name, job.error_msg || undefined)
      else notifs.pushSyncDone(name)
    },
    onAllFinished: () => reloadDesignsSilent(), // silent merge - keeps scroll position
  })
  const downloads = createDownloadQueueStore({
    translate,
    translateError: makeTranslateError(translate),
    showToast: (message, variant) => showToast(message, variant),
    onDownloadsFinished: () => prependNewDesigns(),
  })
  const [perPage, setPerPage] = createSignal(50)
  const [totalDesigns, setTotalDesigns] = createSignal(0)
  const [viewMode, setViewMode] = createSignal<'grid'|'list'>(
    (localStorage.getItem('stlv_view_mode') as 'grid'|'list') || 'grid'
  )
  type SortField = 'name' | 'platform' | 'updated_at'
  const [sortField, setSortField] = createSignal<SortField>('updated_at')
  const [sortDir, setSortDir] = createSignal<'asc'|'desc'>('desc')
  const toggleSort = (field: SortField) => {
    if (sortField() === field) { setSortDir(d => d === 'asc' ? 'desc' : 'asc') }
    else { setSortField(field); setSortDir('asc') }
    // Sorting happens on the server and across the whole library, before the
    // pagination - so reload from page 1, or only the current page is reordered.
    setPage(1); loadDesigns(search(), filters(), 1)
  }
  // The server already sorts the page globally; this only passes it through.
  const sortedDesigns = createMemo(() => designs())

  // Hide placeholder cards whose design already appears in designs() -
  // prevents a race where loadDesigns() resolves before the polling tick
  // clears the queue entry (e.g. navigating back while a download finishes).
  const visibleDownloadJobs = createMemo(() => {
    const loaded = new Set(designs().map(d => d.source_url).filter(Boolean))
    return downloads.jobs.filter(job => !job.source_url || !loaded.has(job.source_url))
  })

  let toastTimer: ReturnType<typeof setTimeout>
  let searchTimer: ReturnType<typeof setTimeout>
  // Last search term actually sent to the backend, so the debounce can skip a
  // run that would repeat the query already on screen.
  let loadedSearch = ''
  let userDropdownRef: HTMLDivElement | undefined

  /**
   * Displays a toast notification for 3.5 seconds.
   */
  const showToast = (message: string, variant = 'success') => {
    setToast(message); setToastVariant(variant)
    clearTimeout(toastTimer)
    toastTimer = setTimeout(() => setToast(''), 3500)
  }

  createEffect(() => {
    if (!showUser()) return
    const handler = (e: MouseEvent) => {
      if (userDropdownRef && !userDropdownRef.contains(e.target as Node)) setShowUser(false)
    }
    document.addEventListener('mousedown', handler)
    onCleanup(() => document.removeEventListener('mousedown', handler))
  })

  /**
   * Fetches designs from the API using the given search query and filters.
   * Logs out the user automatically if a 401 Unauthorized error is returned.
   */
  const loadDesigns = async (q = '', f?: Filters, p?: number, pp?: number) => {
    setLoading(true)
    const activePage = p  ?? page()
    const activePerPage = pp ?? perPage()
    try {
      const response = await api.getDesigns(q, f ?? filters(), activePage, activePerPage, { field: sortField(), dir: sortDir() })
      setDesigns(response.data?.items || [])
      setTotalDesigns(response.data?.total ?? 0)
    } catch { } finally { setLoading(false) }
  }

  /**
   * Fetches designs newer than the current max id and prepends them, keeping the
   * existing items' object identity so the <For> reuses their DOM. This avoids the
   * full-list replace of loadDesigns(), which would recreate every card and reset
   * the scroll position. Only acts on the first grid page; elsewhere it no-ops.
   */
  const prependNewDesigns = async () => {
    if (view() !== 'grid') return
    // Prepending new designs by id is only correct under the default sort
    // (newest first). Under any other - by platform, say - a high id does not
    // belong on page 1, and prepending would put it at the top regardless. Then
    // reload the current page silently, in its correct global order, instead.
    if (sortField() !== 'updated_at' || sortDir() !== 'desc') { reloadDesignsSilent(); return }
    // Off the first page: don't touch the visible list, but keep the page count
    // accurate so a freshly downloaded design doesn't make a new page appear out of
    // nowhere when the user later navigates back to page 1.
    if (page() !== 1) { refreshDesignCount(); return }
    // Page 1: re-fetch in the correct global order and merge by id, which keeps
    // DOM identity and the scroll position. Prepending would be wrong under the
    // default updated_at sort - a freshly inserted design with an older
    // updated_at (a manually added entry, say) has a high id but must not jump
    // to the top.
    reloadDesignsSilent()
  }

  /** Refreshes only the total design count (for pagination) without altering the visible page. */
  const refreshDesignCount = async () => {
    try {
      const res = await api.getDesigns(search(), filters(), 1, 1)
      if (typeof res.data?.total === 'number') setTotalDesigns(res.data.total)
    } catch {}
  }

  /**
   * Re-fetches the current grid page and merges by id WITHOUT toggling the loading
   * spinner: unchanged designs keep their object identity so the <For> reuses their
   * DOM and the scroll position is preserved. Used for background reloads (e.g. after
   * a sync finishes) that may update existing items in place.
   */
  const reloadDesignsSilent = async () => {
    try {
      const response = await api.getDesigns(search(), filters(), page(), perPage(), { field: sortField(), dir: sortDir() })
      const incoming = response.data?.items || []
      setDesigns(prev => {
        const byId = new Map(prev.map(d => [d.id, d]))
        return incoming.map(item => {
          const existing = byId.get(item.id)
          return existing && JSON.stringify(existing) === JSON.stringify(item) ? existing : item
        })
      })
      setTotalDesigns(response.data?.total ?? 0)
    } catch {}
  }
  const loadTags = () => api.getTags().then((r: any) => setAllTags(r.data || [])).catch(() => {})
  const loadCollections = () => api.getCollections().then((r: any) => setAllCollections(r.data || [])).catch(() => {})
  // Checking all designs re-downloads each one from its source platform, so
  // without a platform account the run would only fail its way through the
  // queue. The nav button is therefore not offered at all - the same condition
  // the sync tab in the account settings uses. Read once here and again after
  // the settings close, which is the only place an account can be added.
  const loadPlatformAccountState = () => api.getSyncState(user()!.id)
    .then(response => setHasPlatformAccount(!!response.data?.has_accounts))
    .catch(() => {})
  loadDesigns(); loadTags(); loadCollections(); downloads.refresh(); notifs.load(true); loadPlatformAccountState(); loadQueueBlocks()

  // Debounced search. on(..., { defer: true }) instead of reading search()
  // inside the body: the previous version guarded with a `prevSearch`
  // variable and returned early, which skipped the onCleanup registration -
  // it only worked because the previous run's cleanup happened to fire.
  createEffect(on(search, searchTerm => {
    clearTimeout(searchTimer)
    searchTimer = setTimeout(() => {
      if (searchTerm === loadedSearch) return
      loadedSearch = searchTerm
      setPage(1); loadDesigns(searchTerm, undefined, 1)
    }, 350)
    onCleanup(() => clearTimeout(searchTimer))
  }, { defer: true }))

  createEffect(on(() => JSON.stringify(filters()), () => {
    setPage(1); loadDesigns(search(), filters(), 1)
  }, { defer: true }))

  // One ticker for all background refreshes instead of three intervals of
  // 6/8/10 s. Those ran as createEffect bodies that read no signal at all -
  // onMount in disguise - and hammered a backend with a single DB connection
  // with roughly one request per second per open tab. The loop now idles
  // while the tab is hidden and catches up when it becomes visible again.
  onMount(() => {
    const pollJobs = [
      { everyMs: 6000,  lastRun: Date.now(), run: downloads.refresh },
      { everyMs: 8000,  lastRun: Date.now(), run: () => notifs.load(false) },
      { everyMs: 10000, lastRun: Date.now(), run: prependNewDesigns },
      { everyMs: 20000, lastRun: Date.now(), run: loadQueueBlocks },
    ]
    const tick = () => {
      if (document.hidden) return
      const now = Date.now()
      for (const job of pollJobs) {
        if (now - job.lastRun < job.everyMs) continue
        job.lastRun = now
        job.run()
      }
    }
    const ticker = setInterval(tick, POLL_TICK_MS)
    const onVisibilityChange = () => { if (!document.hidden) tick() }
    document.addEventListener('visibilitychange', onVisibilityChange)
    onCleanup(() => {
      clearInterval(ticker)
      document.removeEventListener('visibilitychange', onVisibilityChange)
    })
  })

  const hasActive = () => filters().source_platform || filters().tag_ids?.length || filters().shared_only || filters().show_hidden
  const pendingItems = downloads.pendingItems

  /**
   * Navigates to the design detail view and reflects it in the URL
   * (?design=<id>), so a reload (F5) stays on the design instead of
   * falling back to the grid.
   */
  const openDesignById = (id: DesignID, editMode = false) => {
    setOpenInEditMode(editMode); setOpenDesignId(id); setView('design'); window.scrollTo(0, 0)
    if (new URLSearchParams(window.location.search).get('design') !== String(id)) {
      history.pushState({ view: 'design', designId: id }, '', `${window.location.pathname}?design=${id}`)
    }
  }
  /** Navigates to the design detail view for the given design. */
  const openDesign = (design: Design) => openDesignById(design.id)

  /**
   * Navigates to the collections view and pushes a history entry. The view is
   * encoded in history.state (not the URL, which stays clean) so the popstate
   * handler can restore the collections view on Back - otherwise a clean URL is
   * indistinguishable from the grid and Back would always fall back to the grid.
   */
  const openCollections = (id: number | null = null) => {
    if (id != null) setOpenCollectionId(id)
    setView('collections'); window.scrollTo(0, 0)
    history.pushState({ view: 'collections', collectionId: id }, '', window.location.pathname)
  }

  /**
   * Navigates to the grid ("home") and keeps history.state in sync with the
   * displayed view. Without this, going home from the collections view (or a
   * design) would leave a stale history entry, so opening a design and pressing
   * Back would return to that stale view (e.g. collections) instead of the grid.
   */
  const goToGrid = () => {
    setView('grid'); loadDesigns(search())
    if ((history.state as { view?: string } | null)?.view !== 'grid') {
      history.replaceState({ view: 'grid' }, '', window.location.pathname)
    }
  }

  // Browser back/forward: restore the view from history.state (falling back to
  // the ?design= URL parameter for deep links / older entries without state).
  onMount(() => {
    const onPop = (e: PopStateEvent) => {
      const st = e.state as { view?: string; designId?: DesignID; collectionId?: number } | null
      const urlId = new URLSearchParams(window.location.search).get('design') || ''
      if (st?.view === 'collections') {
        setOpenInEditMode(false)
        if (st.collectionId != null) setOpenCollectionId(st.collectionId)
        setView('collections'); window.scrollTo(0, 0)
      } else if (st?.view === 'design' || urlId) {
        setOpenInEditMode(false); setOpenDesignId(st?.designId ?? urlId); setView('design'); window.scrollTo(0, 0)
      } else if (view() !== 'grid') {
        setView('grid'); loadDesigns(search()); loadTags()
      }
    }
    window.addEventListener('popstate', onPop)
    onCleanup(() => window.removeEventListener('popstate', onPop))
  })
  /** Prompts for confirmation, then deletes the design via the API. */
  const deleteDesign = (design: Design) => {
    // Preselected: a design deleted on purpose is one the user does not want
    // back, and the sync would otherwise hand it over again on its next run.
    setExcludeFromSync(!!design.source_url)
    setPendingDelete(design)
  }

  /** Performs the deletion once the ConfirmModal has been acknowledged. */
  const confirmDeleteDesign = async (design: Design) => {
    setPendingDelete(null)
    try {
      await api.deleteDesign(design.id, excludeFromSync() && !!design.source_url)
      showToast(translate('toast_design_deleted_named', { name: displayName(design, lang(), translateDesigns()) }))
      loadDesigns(search())
    } catch { showToast(translate('toast_delete_failed'), 'error') }
  }

  const syncOne = async (design: Design) => {
    try {
      await api.syncDesign(design.id)
      sync.markQueued([design.id])
      sync.start()
      showToast(translate('toast_sync_started'))
    } catch { showToast(translate('toast_sync_failed'), 'error') }
  }

  /** Queues all syncable designs on the server and starts polling for status. */
  const syncAll = async () => {
    const toSync = designs().filter(d => d.source_url && !d.is_shared)
    if (toSync.length === 0) { showToast(translate('toast_no_syncable_designs')); return }

    try {
      await api.syncAll()
      // Mark all as queued immediately so the UI shows something right away
      sync.markQueued(toSync.map(d => d.id), true)
      sync.start()
    } catch { showToast(translate('toast_sync_failed'), 'error') }
  }

  // On page load, restore overlays/placeholders for work that survived an F5.
  sync.resumeFromServer()
  downloads.resumeFromServer()
  // A library sync fills the download queue from the server side, so the grid
  // has to notice entries this tab never started - otherwise their designs keep
  // the placeholder image until the page is reloaded by hand.
  downloads.watchForServerJobs()

  const setUserDropdownRef = (element: HTMLDivElement) => { userDropdownRef = element }

  const navBarDeps: NavBarDeps = {
    translate, user, logout, search, setSearch, hasActive, viewMode, setViewMode,
    visibleDownloadJobs, hasPlatformAccount, showUser, setShowUser, setShowAdd, setShowFilters,
    setShowSyncAllConfirm, setShowAccountSettings, setShowServerSettings, openCollections,
    goToGrid, downloads, notifs, setUserDropdownRef,
  }

  const gridMainDeps: GridMainDeps = {
    translate, loading, designs, sortedDesigns, visibleDownloadJobs, viewMode, sortField,
    sortDir, toggleSort, page, perPage, totalDesigns, setPage, loadDesigns, search, filters,
    openDesign, syncOne, setAccountSettingsTab, setShowAccountSettings, downloads, sync,
  }

  const commonModals = () => (
    <>
      <ConfirmDiscardModal />
      <Show when={showAccountSettings()}>
        <AccountSettingsModal user={user()!} onClose={() => { setShowAccountSettings(false); loadPlatformAccountState() }} showToast={showToast} onUserUpdate={updateUser} initialTab={accountSettingsTab()} />
      </Show>
      <Show when={showServerSettings()}>
        <ServerSettingsModal user={user()!} onClose={() => setShowServerSettings(false)} showToast={showToast} />
      </Show>
      <Show when={showSyncAllConfirm()}>
        <ConfirmModal
          title={translate('sync_all_confirm_title')}
          body={translate('sync_all_confirm_body')}
          hint={translate('sync_all_confirm_hint')}
          confirmLabel={translate('sync_all_confirm_start')}
          onClose={() => setShowSyncAllConfirm(false)}
          onConfirm={() => { setShowSyncAllConfirm(false); syncAll() }} />
      </Show>
      <Show when={pendingDelete()}>
        {design => (
          <ConfirmModal
            title={translate('btn_delete_design')}
            body={translate('confirm_delete_card', { name: displayName(design(), lang(), translateDesigns()) })}
            confirmLabel={translate('btn_confirm_delete')}
            danger
            checkboxLabel={design().source_url ? translate('delete_exclude_from_sync') : undefined}
            checkboxChecked={excludeFromSync()}
            onCheckboxChange={setExcludeFromSync}
            onClose={() => setPendingDelete(null)}
            onConfirm={() => confirmDeleteDesign(design())} />
        )}
      </Show>
    </>
  )

  // - Single reactive return - all views rendered with Show for SolidJS reactivity -
  return (
    <div style={{ 'min-height': '100vh', background: 'var(--bg)' }}>
      <style>{globalStyles}</style>
      <Toast message={toast()} variant={toastVariant()} />

      {/* Design detail page */}
      <Show when={view() === 'design' && !!openDesignId()}>
        {navBar(navBarDeps, true)}
        <DesignPage designId={openDesignId()!}
          initialEditMode={openInEditMode()}
          onBack={() => {
            // If this design was opened via an in-app push (from grid or a
            // collection), go back through history so we return to wherever we
            // came from. popstate then restores that view.
            if ((history.state as { view?: string } | null)?.view === 'design') { history.back(); return }
            // Deep link / direct load (no in-app history): fall back to the grid.
            setOpenInEditMode(false); setView('grid'); loadDesigns(search()); loadTags()
            if (window.location.search) history.replaceState(null, '', window.location.pathname)
          }}
          showToast={showToast} allTags={allTags()} allCollections={allCollections()}
          onTagsChanged={() => { loadTags(); loadDesigns(search()) }}
          onCollectionsChanged={loadCollections}
          onOpenCollection={(id) => openCollections(id)}
          isReadOnly={!!(designs().find(x => x.id === openDesignId())?.is_shared)}
          onSync={async (id, name) => {
            try {
              await api.syncDesign(id)
              sync.markQueued([id])
              sync.start()
              showToast(translate('toast_sync_started'))
            } catch (failure) {
              showToast(makeTranslateError(translate)(errorKey(failure)) || translate('toast_sync_failed'), 'error')
            }
          }}
          syncTick={sync.tick()}
          syncStatus={sync.statusMap()[openDesignId()!]}
          syncProgress={sync.progressMap()[openDesignId()!]}
          syncStep={sync.stepMap()[openDesignId()!]}
          syncError={sync.errorMap()[openDesignId()!]} />
        {commonModals()}
      </Show>

      {/* Collections view */}
      <Show when={view() === 'collections'}>
        {navBar(navBarDeps, true)}
        <CollectionsPage collections={allCollections()} onBack={() => history.back()} showToast={showToast}
          onChanged={loadCollections} onOpenDesign={openDesignById}
          initialCollectionId={openCollectionId()} onInitialCollectionHandled={() => setOpenCollectionId(null)} />
        {commonModals()}
      </Show>

      {/* Main grid */}
      <Show when={view() === 'grid'}>
        {navBar(navBarDeps)}
        {gridMain(gridMainDeps)}

        <Show when={showAdd()}>
          <AddDesignModal onClose={() => setShowAdd(false)} onSaved={async (designId) => { await loadDesigns(search()); loadTags(); if (designId) openDesignById(designId, true) }} showToast={showToast} onOpenAccountSettings={(tab?: string) => { setShowAdd(false); setAccountSettingsTab(tab || 'account'); setShowAccountSettings(true) }}
            onQueued={(url, platform) => {
              showToast(translate('toast_download_queued'))
              const placeholder: DownloadJob = { id: -Date.now(), status: 'pending', source_url: url, platform }
              downloads.startPolling([placeholder])
            }}
            />
        </Show>
        <Show when={showFilters()}>
          <FilterModal filters={filters()} setFilters={setFilters} allTags={allTags()} onClose={() => setShowFilters(false)} perPage={perPage()} onPerPageChange={pp => { setPerPage(pp); setPage(1); loadDesigns(search(), filters(), 1, pp) }} />
        </Show>
        <Show when={downloads.panelOpen()}>
          <QueuePanel queue={downloads.queue} onClose={() => downloads.setPanelOpen(false)} onCancel={downloads.cancel} onRetry={downloads.retry} onRetryAll={() => downloads.retryAll(visibleDownloadJobs().filter(job => job.status === 'failed'))} onDismiss={downloads.dismiss} onOpenSettings={() => { setAccountSettingsTab('platforms'); setShowAccountSettings(true) }} />
        </Show>
        {commonModals()}
      </Show>
    </div>
  )
}

export default function App() {
  const {user, updateUser, loading} = useAuth()
  // A share link is answered before anything asks for a session: whoever opens
  // it has no account, and the token in the URL is the whole authorisation.
  const shareToken = new URLSearchParams(window.location.search).get('share') || ''
  if (shareToken) return <SharePage token={shareToken} />
  return (
    <Show when={!loading()} fallback={null}>
      <Show when={user()} fallback={<LoginPage />}>
        {/* Nothing of the app is mounted while a password change is pending. The
            server answers every call with error.password_change_required until
            it is done, so the views behind the modal fetched nothing - and they
            never retried afterwards, which left a deep link to a design showing
            an empty page with only its back button. Mounting after the change
            lets them load the way they always do. */}
        <Show when={!user()!.must_change_password} fallback={
          <div style={{ 'min-height': '100vh', background: 'var(--bg)' }}>
            <style>{globalStyles}</style>
            <ForcePasswordChangeModal user={user()!}
              onChanged={() => updateUser({ ...user()!, must_change_password: false })} />
          </div>
        }>
          <MainApp />
        </Show>
      </Show>
    </Show>
  )
}
