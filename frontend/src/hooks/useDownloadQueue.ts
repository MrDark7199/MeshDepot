import { createSignal, onCleanup } from 'solid-js'
import { createStore, reconcile } from 'solid-js/store'
import { api } from '../services/api'
import { errorKey } from '../utils/errorMessage'
import type { DownloadJob, QueueItem } from '../types'

/** Poll interval while at least one download is pending or running. */
const DOWNLOAD_POLL_MS = 2500

/**
 * Interval of the background check for queue entries this tab did not start.
 *
 * A library sync fills the queue from the server side - triggered in the
 * settings dialog, in another tab, or by the nightly scheduler - and the fast
 * loop above was only ever started by an import in this tab. Without this the
 * designs those downloads produced kept their placeholder image until the page
 * was reloaded by hand. Far slower than the fast loop, because it runs while
 * nothing is happening at all.
 */
const IDLE_WATCH_MS = 15000

const isActive = (job: DownloadJob) => job.status === 'pending' || job.status === 'downloading'
const isVisible = (job: DownloadJob) => isActive(job) || job.status === 'failed'

export interface DownloadQueueDeps {
  translate: (key: string, vars?: Record<string, string | number>) => string
  /** Translates a backend `error_msg` into a readable message. */
  translateError: (msg: string) => string
  showToast: (message: string, variant?: string) => void
  /** Called after downloads finished so the grid can pick up the new designs. */
  onDownloadsFinished: () => void
  /** Called once per bulk batch with its result counts (bell notification). */
}

export type DownloadQueueStore = ReturnType<typeof createDownloadQueueStore>

/**
 * Download-queue state: the panel list, the placeholder cards shown in the grid
 * and the on-demand polling loop that keeps both current.
 *
 * The loop only runs while downloads are actually active - failed jobs stay on
 * screen (with their retry button) but must not keep the timer alive, since the
 * queue API returns them for seven days.
 */
export function createDownloadQueueStore(deps: DownloadQueueDeps) {
  const [queue, setQueue] = createStore<QueueItem[]>([])
  const [jobs, setJobs] = createStore<DownloadJob[]>([])
  const [panelOpen, setPanelOpen] = createSignal(false)

  let pollTimer: ReturnType<typeof setInterval> | null = null

  const pendingItems = () => queue.filter(isActive)

  /**
   * Refreshes the panel list from the server. Also re-syncs the grid
   * placeholders, so a card can't get stuck (e.g. on "downloading") when a job
   * changes state server-side while the on-demand poll isn't running - jobs
   * re-queued or aborted in another tab, or after the poll already stopped.
   */
  const refresh = () => api.getQueue().then(response => {
    const data = response.data || []
    setQueue(reconcile(data, { key: 'id', merge: true }))
    // Preserve optimistic (negative-id) placeholders until the real server row
    // for the same source_url shows up, so a just-clicked card doesn't flicker away.
    const visible = data.filter(isVisible)
    const serverUrls = new Set(visible.map(job => job.source_url))
    const optimistic = jobs.filter(job => job.id < 0 && !serverUrls.has(job.source_url))
    setJobs(reconcile([...optimistic, ...visible], { key: 'id', merge: true }))
  }).catch(() => {})

  const stopPolling = () => {
    if (pollTimer !== null) { clearInterval(pollTimer); pollTimer = null }
  }

  /**
   * Starts the on-demand poll loop (no-op if already running). initialJobs are
   * optimistic placeholders shown until the first poll lands.
   */
  const startPolling = (initialJobs?: DownloadJob[]) => {
    if (pollTimer !== null) {
      if (initialJobs && initialJobs.length > 0) setJobs(reconcile([...jobs, ...initialJobs], { key: 'id', merge: true }))
      return
    }
    const handledJobIds = new Set<number>()

    const tick = async () => {
      try {
        const response = await api.getQueue()
        const current: QueueItem[] = response.data || []

        // Process each finished job exactly once
        let needsReload = false
        for (const job of current) {
          if ((job.status !== 'done' && job.status !== 'failed') || handledJobIds.has(job.id)) continue
          handledJobIds.add(job.id)
          if (job.status === 'done') {
            needsReload = true
            deps.showToast(deps.translate('toast_download_done'))
          }
        }

        // Add the finished design(s) without replacing the whole grid (keeps the
        // scroll position) and remove the placeholder at the same time.
        if (needsReload) deps.onDownloadsFinished()
        setJobs(reconcile(current.filter(isVisible), { key: 'id', merge: true }))

        // Stop only when no active jobs remain.
        const active = current.filter(isActive)
        const allHandled = current.every(job => handledJobIds.has(job.id) || isVisible(job))
        if (active.length === 0 && allHandled) {
          stopPolling()
          setJobs(reconcile(current.filter(job => job.status === 'failed'), { key: 'id', merge: true }))
        }
      } catch { /* network error - keep polling */ }
    }

    if (initialJobs && initialJobs.length > 0) setJobs(reconcile(initialJobs, { key: 'id', merge: true }))

    tick() // poll immediately, don't wait for the first interval
    pollTimer = setInterval(tick, DOWNLOAD_POLL_MS)
  }

  /** Restores placeholders for downloads that were still running after an F5. */
  const resumeFromServer = () => {
    api.getQueue().then(response => {
      const visible = (response.data || []).filter(isVisible)
      if (visible.length === 0) return
      setJobs(reconcile(visible, { key: 'id', merge: true }))
      startPolling()
    }).catch(() => {})
  }

  /**
   * Watches for queue entries this tab did not create (see {@link IDLE_WATCH_MS})
   * and hands them to the fast loop, which reports them and reloads the grid.
   */
  let watchTimer: ReturnType<typeof setInterval> | null = null
  const watchForServerJobs = () => {
    if (watchTimer !== null) return
    watchTimer = setInterval(async () => {
      if (pollTimer !== null) return // the fast loop already owns the queue
      try {
        const response = await api.getQueue()
        if ((response.data || []).some(isActive)) startPolling()
      } catch { /* network error - try again on the next tick */ }
    }, IDLE_WATCH_MS)
  }
  onCleanup(() => { if (watchTimer !== null) clearInterval(watchTimer) })

  const retry = async (queueId: number) => {
    try {
      await api.retryDownload(queueId)
      stopPolling()
      startPolling()
    } catch (failure: unknown) {
      const message = deps.translateError(errorKey(failure, ''))
      deps.showToast(message || deps.translate('toast_download_failed'), 'error')
    }
  }

  /** Retries every failed download in `failedJobs` at once. */
  const retryAll = async (failedJobs: DownloadJob[]) => {
    if (failedJobs.length === 0) return
    await Promise.allSettled(failedJobs.map(job => api.retryDownload(job.id)))
    stopPolling()
    startPolling()
    refresh()
  }

  const cancel = async (queueId: number) => {
    try {
      await api.cancelDownload(queueId)
      setJobs(reconcile(jobs.filter(job => job.id !== queueId)))
    } catch { /* ignore - the job may have finished just before the cancel */ }
  }

  const dismiss = async (queueId: number) => {
    try {
      await api.dismissDownload(queueId)
      setJobs(reconcile(jobs.filter(job => job.id !== queueId)))
    } catch { /* ignore */ }
  }

  onCleanup(stopPolling)

  return {
    queue,
    jobs,
    panelOpen,
    setPanelOpen,
    pendingItems,
    refresh,
    startPolling,
    stopPolling,
    resumeFromServer,
    watchForServerJobs,
    retry,
    retryAll,
    cancel,
    dismiss,
  }
}
