import { createSignal } from 'solid-js'
import { api } from '../services/api'
import type { AppNotification } from '../types'

export interface NotificationsDeps {
  /** Currently logged-in user; `undefined` while the session is not (yet) known. */
  userId: () => string | undefined
  translate: (key: string, vars?: Record<string, string | number>) => string
}

export type NotificationsStore = ReturnType<typeof createNotificationsStore>

/** Drops entries whose title was already seen - the server sends one row per
 *  sync run, so repeated failures of the same design would otherwise pile up. */
function deduplicateByTitle(items: AppNotification[]): AppNotification[] {
  const seen = new Set<string>()
  return items.filter(notification => {
    if (seen.has(notification.title)) return false
    seen.add(notification.title)
    return true
  })
}

/**
 * Bell-dropdown state: the notification list, the unread badges (split into info
 * and error) and the local (client-side) notifications that sync, bulk downloads
 * and platform queue blocks push while running.
 *
 * Local entries live only in this tab and carry a negative id, so they can be
 * merged with server rows without colliding. Each notification carries a `level`
 * (info | error) so the list and the bell badge can tell the two apart.
 */
export function createNotificationsStore(deps: NotificationsDeps) {
  const [notifications, setNotifications] = createSignal<AppNotification[]>([])
  const [panelOpen, setPanelOpen] = createSignal(false)
  let localNotifId = -1

  /**
   * Fetches the server-side notifications. full = `true` replaces the list
   * (initial load), `false` merges the server rows into the locally pushed ones
   * (polling tick). A server row's `type` decides its level: anything that reads
   * as an error/failure becomes an error, everything else info.
   */
  const load = (full = false) => {
    const userId = deps.userId()
    if (!userId) return
    api.getNotifications(userId).then((response: any) => {
      const incoming: AppNotification[] = (response.data?.items || []).map((row: any) => ({
        ...row,
        level: /error|fail/i.test(String(row.type ?? '')) ? 'error' : 'info',
      }))
      if (full) {
        setNotifications(deduplicateByTitle(incoming))
      } else {
        setNotifications(prev => {
          const locals = prev.filter(n => n.local)
          return deduplicateByTitle([...locals, ...incoming])
        })
      }
    }).catch(() => {})
  }

  /** Adds a notification, or refreshes the timestamp of an existing same-title one. */
  const upsert = (notification: AppNotification) => {
    setNotifications(prev => {
      const index = prev.findIndex(n => n.title === notification.title)
      if (index !== -1) {
        const next = [...prev]
        next[index] = { ...next[index], created_at: notification.created_at, read_at: null, level: notification.level }
        return next
      }
      return [notification, ...prev]
    })
  }

  const pushLocal = (title: string, body: string, level: 'info' | 'error' = 'info') => upsert({
    id: localNotifId--,
    local: true,
    title,
    body,
    level,
    created_at: new Date().toISOString(),
    read_at: null,
  })

  /** Pushes an error notification (queue blocks, generic failures). */
  const pushError = (title: string, body: string) => pushLocal(title, body, 'error')

  const pushSyncError = (name: string, body?: string) =>
    pushLocal(
      deps.translate('sync_error_notif_title').replace('{name}', name),
      body || deps.translate('sync_error_notif_body'),
      'error',
    )

  const pushSyncDone = (name: string) =>
    pushLocal(
      deps.translate('sync_done_notif_title').replace('{name}', name),
      deps.translate('sync_done_notif_body'),
      'info',
    )

  /** Clears the list locally and on the server. */
  const clearAll = () => {
    setNotifications([])
    const userId = deps.userId()
    if (userId) api.deleteAllNotifications(userId).catch(() => {})
  }

  /** Removes one entry - local ones client-side only, server rows via the API. */
  const remove = (notification: AppNotification) => {
    if (notification.local) {
      setNotifications(prev => prev.filter(n => n.id !== notification.id))
      return
    }
    const userId = deps.userId()
    if (userId) api.deleteNotification(userId, notification.id).then(() => load()).catch(() => {})
  }

  // Unread counts split by level, derived from the list so they can never drift
  // out of sync with it. A count of 0 is left for the bell to hide.
  const unreadError = () => notifications().filter(n => !n.read_at && n.level === 'error').length
  const unreadInfo = () => notifications().filter(n => !n.read_at && (n.level ?? 'info') !== 'error').length
  const unreadCount = () => notifications().filter(n => !n.read_at).length

  return {
    notifications,
    unreadCount,
    unreadInfo,
    unreadError,
    panelOpen,
    setPanelOpen,
    load,
    pushSyncError,
    pushSyncDone,
    pushError,
    clearAll,
    remove,
  }
}
