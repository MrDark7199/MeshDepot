import { createEffect, onCleanup, For, Show } from 'solid-js'
import type { NotificationsStore } from '../hooks/useNotifications'

/** Formats an ISO timestamp as `DD.MM.YYYY HH:MM` in the browser's timezone. */
function formatTimestamp(iso: string): string {
  const date = new Date(iso)
  const pad = (value: number) => String(value).padStart(2, '0')
  return `${pad(date.getDate())}.${pad(date.getMonth() + 1)}.${date.getFullYear()} ${pad(date.getHours())}:${pad(date.getMinutes())}`
}

/**
 * Bell button with unread badge and the dropdown panel listing the
 * notifications. All state lives in the passed-in store; this component only
 * renders it and closes the panel on an outside click.
 */
export function NotificationBell(props: {
  store: NotificationsStore
  translate: (key: string, vars?: Record<string, string | number>) => string
}) {
  let panelRef: HTMLDivElement | undefined

  createEffect(() => {
    if (!props.store.panelOpen()) return
    const handler = (mouseEvent: MouseEvent) => {
      if (panelRef && !panelRef.contains(mouseEvent.target as Node)) props.store.setPanelOpen(false)
    }
    document.addEventListener('mousedown', handler)
    onCleanup(() => document.removeEventListener('mousedown', handler))
  })

  return (
    <div ref={panelRef} style={{ position:'relative' }}>
      <button onClick={() => props.store.setPanelOpen(isVisible => !isVisible)}
        style={{ background:'var(--surface)', border:'1px solid var(--border)', 'border-radius':'11px', width:'42px', height:'42px', display:'flex', 'align-items':'center', 'justify-content':'center', cursor:'pointer', position:'relative', color:'var(--muted)' }}>
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <path d="M18 8A6 6 0 0 0 6 8c0 7-3 9-3 9h18s-3-2-3-9"/><path d="M13.73 21a2 2 0 0 1-3.46 0"/>
        </svg>
        <Show when={props.store.unreadCount() > 0}>
          <span style={{ position:'absolute', top:'5px', right:'5px', width:'16px', height:'16px', background:'var(--accent)', 'border-radius':'50%', display:'flex', 'align-items':'center', 'justify-content':'center', 'font-family':"'DM Mono',monospace", 'font-size':'9px', 'font-weight':'700', color:'#fff', 'line-height':'1' }}>
            {props.store.unreadCount() > 9 ? '9+' : props.store.unreadCount()}
          </span>
        </Show>
      </button>
      <Show when={props.store.panelOpen()}>
        <div class="stlv-notif-panel" style={{ position:'absolute', top:'calc(100% + 10px)', right:'0', 'z-index':'1100', background:'var(--bg)', 'border-radius':'16px', border:'1px solid var(--border)', 'box-shadow':'0 20px 60px rgba(0,0,0,0.4)', width:'320px', overflow:'hidden' }}>
          <div style={{ display:'flex', 'align-items':'center', 'justify-content':'space-between', padding:'12px 16px', 'border-bottom':'1px solid var(--border)' }}>
            <span style={{ 'font-family':"'DM Sans',sans-serif", 'font-weight':'700', 'font-size':'13px', color:'var(--text)' }}>{props.translate('notif_title')}</span>
            <Show when={props.store.notifications().length > 0}>
              <button onClick={() => props.store.clearAll()}
                style={{ background:'none', border:'none', 'font-family':"'DM Sans',sans-serif", 'font-size':'11px', color:'var(--accent)', cursor:'pointer', padding:'0' }}>
                {props.translate('notif_mark_all_read')}
              </button>
            </Show>
          </div>
          <div style={{ 'max-height':'360px', 'overflow-y':'auto' }}>
            <Show when={props.store.notifications().length === 0}>
              <div style={{ padding:'20px 16px', 'text-align':'center', 'font-family':"'DM Sans',sans-serif", 'font-size':'13px', color:'var(--muted)' }}>{props.translate('notif_empty')}</div>
            </Show>
            <For each={props.store.notifications()}>{notification => (
              <div style={{ padding:'10px 16px', 'border-bottom':'1px solid var(--border)', background: notification.read_at ? 'transparent' : 'rgba(69,123,157,0.06)', display:'flex', gap:'10px', 'align-items':'flex-start' }}>
                <Show when={!notification.read_at} fallback={<div style={{ width:'6px', 'flex-shrink':'0' }} />}>
                  <div style={{ width:'6px', height:'6px', 'border-radius':'50%', background:'var(--accent)', 'margin-top':'5px', 'flex-shrink':'0' }} />
                </Show>
                <div style={{ flex:'1', 'min-width':'0' }}>
                  <div style={{ 'font-family':"'DM Sans',sans-serif", 'font-size':'12px', 'font-weight':'600', color:'var(--text)', 'word-break':'break-word' }}>{notification.title}</div>
                  <Show when={notification.body}>
                    <div style={{ 'font-family':"'DM Sans',sans-serif", 'font-size':'11px', color:'var(--muted)', 'margin-top':'2px', 'word-break':'break-word' }}>{notification.body}</div>
                  </Show>
                  <div style={{ 'font-family':"'DM Mono',monospace", 'font-size':'10px', color:'var(--muted)', 'margin-top':'3px' }}>{formatTimestamp(notification.created_at)}</div>
                </div>
                <button
                  onClick={() => props.store.remove(notification)}
                  style={{ background:'none', border:'none', color:'var(--muted)', cursor:'pointer', 'font-size':'16px', 'line-height':'1', padding:'0 2px', 'flex-shrink':'0', opacity:'0.6' }}
                  title={props.translate('btn_remove')}>×</button>
              </div>
            )}</For>
          </div>
        </div>
      </Show>
    </div>
  )
}
