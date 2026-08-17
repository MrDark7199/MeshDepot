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
 * Bell-dropdown state: the notification list, the unread badge and the local
 * (client-side) notifications that sync and bulk downloads push while running.
 *
 * Local entries live only in this tab and carry a negative id, so they can be
 * merged with server rows without colliding.
 */
export function createNotificationsStore(deps: NotificationsDeps) {
  const [notifications, setNotifications] = createSignal<AppNotification[]>([])
  const [unreadCount, setUnreadCount] = createSignal(0)
  const [panelOpen, setPanelOpen] = createSignal(false)
  let localNotifId = -1

  /**
   * Fetches the server-side notifications. full = `true` replaces the list
   * (initial load), `false` merges the server rows into the locally pushed ones
   * (polling tick).
   */
  const load = (full = false) => {
    const userId = deps.userId()
    if (!userId) return
    api.getNotifications(userId).then((response: any) => {
      const incoming: AppNotification[] = response.data?.items || []
      if (full) {
        const deduped = deduplicateByTitle(incoming)
        setNotifications(deduped)
        setUnreadCount(deduped.filter(n => !n.read_at).length)
      } else {
        setNotifications(prev => {
          const locals = prev.filter(n => n.local)
          const deduped = deduplicateByTitle([...locals, ...incoming])
          setUnreadCount(deduped.filter(n => !n.read_at).length)
          return deduped
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
        next[index] = { ...next[index], created_at: notification.created_at, read_at: null }
        return next
      }
      setUnreadCount(count => count + 1)
      return [notification, ...prev]
    })
  }

  const pushLocal = (title: string, body: string) => upsert({
    id: localNotifId--,
    local: true,
    title,
    body,
    created_at: new Date().toISOString(),
    read_at: null,
  })

  const pushSyncError = (name: string, body?: string) =>
    pushLocal(
      deps.translate('sync_error_notif_title').replace('{name}', name),
      body || deps.translate('sync_error_notif_body'),
    )

  const pushSyncDone = (name: string) =>
    pushLocal(
      deps.translate('sync_done_notif_title').replace('{name}', name),
      deps.translate('sync_done_notif_body'),
    )

  /** Clears the list locally and on the server. */
  const clearAll = () => {
    setNotifications([])
    setUnreadCount(0)
    const userId = deps.userId()
    if (userId) api.deleteAllNotifications(userId).catch(() => {})
  }

  /** Removes one entry - local ones client-side only, server rows via the API. */
  const remove = (notification: AppNotification) => {
    if (notification.local) {
      setNotifications(prev => prev.filter(n => n.id !== notification.id))
      if (!notification.read_at) setUnreadCount(count => Math.max(0, count - 1))
      return
    }
    const userId = deps.userId()
    if (userId) api.deleteNotification(userId, notification.id).then(() => load()).catch(() => {})
  }

  return {
    notifications,
    unreadCount,
    panelOpen,
    setPanelOpen,
    load,
    pushSyncError,
    pushSyncDone,
    clearAll,
    remove,
  }
}
