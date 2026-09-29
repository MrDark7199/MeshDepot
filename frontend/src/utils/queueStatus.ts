import type { QueueItem } from '../types'

/**
 * What a waiting job says about itself. One that is resting behind a paused
 * platform, or on its second attempt, otherwise looks exactly like one just
 * queued - which reads as stuck.
 */
export function queueLabel(
  job: Partial<Pick<QueueItem, 'retry_count' | 'max_retries' | 'queue_blocked' | 'queue_paused'>>,
  translate: (key: any) => string,
): string {
  if (job.queue_blocked) return translate('download_status_auto_paused')
  if (job.queue_paused) return translate('download_status_paused')
  const attempt = (job.retry_count ?? 0) + 1
  const limit = job.max_retries ?? 0
  if (attempt > 1 && limit > 0) {
    return `${translate('download_status_queued')} (${translate('download_try')} ${attempt}/${limit})`
  }
  return translate('download_status_queued')
}
