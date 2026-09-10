import { createSignal, onCleanup } from 'solid-js'
import { api } from '../services/api'
import type { DesignID, SyncJob, SyncStatus, SyncStep } from '../types'

/** Poll interval of the sync-status endpoint while at least one job is running. */
const SYNC_POLL_MS = 2500
/** How long a finished job keeps its status overlay on the card before it fades. */
const SYNC_OVERLAY_LINGER_MS = 2000

export interface SyncProgressDeps {
  /** Called once per finished job - used to push a bell notification. */
  onJobFinished: (job: SyncJob) => void
  /** Called after the last job finished, if anything actually changed. */
  onAllFinished: () => void
}

export type SyncProgressStore = ReturnType<typeof createSyncProgressStore>

function statusOf(job: SyncJob): SyncStatus {
  if (job.status === 'pending') return 'queued'
  if (job.status === 'running') return 'syncing'
  if (job.status === 'done')    return 'updated'
  return 'error'
}

/**
 * Per-design progress of running update checks (status, percentage and phase),
 * plus the polling loop that keeps them current.
 *
 * The loop is started on demand (after triggering a sync, or on page load when
 * jobs survived an F5) and stops itself as soon as the server queue is empty.
 */
export function createSyncProgressStore(deps: SyncProgressDeps) {
  const [statusMap, setStatusMap] = createSignal<Record<DesignID, SyncStatus>>({})
  const [progressMap, setProgressMap] = createSignal<Record<DesignID, number>>({})
  const [stepMap, setStepMap] = createSignal<Record<DesignID, SyncStep>>({})
  const [errorMap, setErrorMap] = createSignal<Record<DesignID, string>>({})
  /** Bumped whenever a job finishes; the design detail view re-reads its data on change. */
  const [tick, setTick] = createSignal(0)

  let pollTimer: ReturnType<typeof setInterval> | null = null

  const stop = () => {
    if (pollTimer !== null) { clearInterval(pollTimer); pollTimer = null }
  }

  const start = () => {
    if (pollTimer !== null) return
    // job IDs (sync_queue.id) already handled - never re-process or re-show the overlay
    const handledJobIds = new Set<number>()
    let needsReload = false

    pollTimer = setInterval(async () => {
      try {
        const res = await api.getSyncStatus()
        const jobs: SyncJob[] = res.data || []

        if (jobs.length === 0) {
          stop()
          // Failures are kept: the server drops a finished job from this list
          // after a while, and the reason would go with it.
          setStatusMap(prev => {
            const kept: Record<DesignID, SyncStatus> = {}
            for (const [designId, status] of Object.entries(prev)) {
              if (status === 'error') kept[designId] = status
            }
            return kept
          })
          setStepMap({})
          if (needsReload) deps.onAllFinished()
          return
        }

        // Only update active (not-yet-handled) entries
        setStatusMap(prev => {
          const next = { ...prev }
          let changed = false
          for (const job of jobs) {
            if (handledJobIds.has(job.id)) continue  // already finished - don't re-set
            const syncStatus = statusOf(job)
            if (next[job.design_id] !== syncStatus) { next[job.design_id] = syncStatus; changed = true }
          }
          return changed ? next : prev
        })
        setProgressMap(prev => {
          const next = { ...prev }
          for (const job of jobs) {
            if (job.status === 'running' && typeof job.progress === 'number') {
              next[job.design_id] = job.progress
            }
          }
          return next
        })
        setStepMap(prev => {
          const next = { ...prev }
          for (const job of jobs) {
            if (handledJobIds.has(job.id)) continue
            if (job.status === 'running' && job.current_step) {
              next[job.design_id] = {
                step: String(job.current_step),
                cur: Number(job.step_current) || 0,
                tot: Number(job.step_total) || 0,
              }
            }
          }
          return next
        })

        // Process each finished job exactly once
        for (const job of jobs) {
          if ((job.status !== 'done' && job.status !== 'failed') || handledJobIds.has(job.id)) continue
          handledJobIds.add(job.id)
          needsReload = true
          setTick(value => value + 1)
          if (job.status === 'failed') {
            setErrorMap(prev => ({ ...prev, [job.design_id]: job.error_msg || '' }))
          }
          deps.onJobFinished(job)
          // Remove the overlay after a brief flash - and never re-add it
          // (handledJobIds guards the setters above).
          const designId = job.design_id
          setStepMap(prev => {
            if (!(designId in prev)) return prev
            const next = { ...prev }
            delete next[designId]
            return next
          })
          // Only the green tick is transient; a failure waits for the next attempt.
          if (job.status !== 'failed') {
            setTimeout(() => {
              setStatusMap(prev => {
                if (!(designId in prev)) return prev
                const next = { ...prev }
                delete next[designId]
                return next
              })
            }, SYNC_OVERLAY_LINGER_MS)
          }
        }
      } catch { /* network error - keep polling */ }
    }, SYNC_POLL_MS)
  }

  /** Shows the "queued" overlay for the given designs before the first poll lands. */
  const markQueued = (designIds: DesignID[], replace = false) => {
    setErrorMap(prev => {
      const next = { ...prev }
      designIds.forEach(id => { delete next[id] })
      return next
    })
    setStatusMap(prev => {
      const next: Record<DesignID, SyncStatus> = replace ? {} : { ...prev }
      designIds.forEach(id => { next[id] = 'queued' })
      return next
    })
  }

  /** Restores overlays for jobs that were still running when the page reloaded. */
  const resumeFromServer = () => {
    api.getSyncStatus().then(res => {
      const jobs: SyncJob[] = res.data || []
      const running = jobs.filter(job => job.status === 'pending' || job.status === 'running')
      if (running.length === 0) return
      const map: Record<DesignID, SyncStatus> = {}
      running.forEach(job => { map[job.design_id] = statusOf(job) })
      setStatusMap(map)
      start()
    }).catch(() => {})
  }

  onCleanup(stop)

  return { statusMap, progressMap, stepMap, errorMap, tick, start, stop, markQueued, resumeFromServer }
}
