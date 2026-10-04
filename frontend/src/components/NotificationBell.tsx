import { createEffect, onCleanup, For, Show } from 'solid-js'
import type { NotificationsStore } from '../hooks/useNotifications'
import { useI18n } from '../i18n/index'
import { formatDateTime } from '../utils/datetime'

/**
 * Bell button with unread badge and the dropdown panel listing the
 * notifications. All state lives in the passed-in store; this component only
 * renders it and closes the panel on an outside click.
 */
export function NotificationBell(props: {
  store: NotificationsStore
  translate: (key: string, vars?: Record<string, string | number>) => string
}) {
  const { lang } = useI18n()
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
        style={{ background:'var(--surface)', border:'1px solid var(--border)', 'border-radius':'11px', width:'44px', height:'44px', display:'flex', 'align-items':'center', 'justify-content':'center', cursor:'pointer', position:'relative', color:'var(--muted)' }}>
        <svg width="19" height="19" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <path d="M18 8A6 6 0 0 0 6 8c0 7-3 9-3 9h18s-3-2-3-9"/><path d="M13.73 21a2 2 0 0 1-3.46 0"/>
        </svg>
        {/* Two badges - errors (red) and info (accent), each hidden at zero. */}
        <div style={{ position:'absolute', top:'2px', right:'2px', display:'flex', gap:'2px' }}>
          <Show when={props.store.unreadError() > 0}>
            <span style={{ 'min-width':'16px', height:'16px', padding:'0 3px', background:'var(--danger)', 'border-radius':'8px', display:'flex', 'align-items':'center', 'justify-content':'center', 'font-family':"'DM Mono',monospace", 'font-size':'9px', 'font-weight':'700', color:'#fff', 'line-height':'1', 'box-sizing':'border-box' }}>
              {props.store.unreadError() > 9 ? '9+' : props.store.unreadError()}
            </span>
          </Show>
          <Show when={props.store.unreadInfo() > 0}>
            <span style={{ 'min-width':'16px', height:'16px', padding:'0 3px', background:'var(--accent)', 'border-radius':'8px', display:'flex', 'align-items':'center', 'justify-content':'center', 'font-family':"'DM Mono',monospace", 'font-size':'9px', 'font-weight':'700', color:'#fff', 'line-height':'1', 'box-sizing':'border-box' }}>
              {props.store.unreadInfo() > 9 ? '9+' : props.store.unreadInfo()}
            </span>
          </Show>
        </div>
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
              <div style={{ padding:'10px 16px', 'border-bottom':'1px solid var(--border)', 'border-left': notification.level === 'error' ? '3px solid var(--danger)' : '3px solid transparent', background: notification.read_at ? 'transparent' : (notification.level === 'error' ? 'var(--danger-bg)' : 'rgba(69,123,157,0.06)'), display:'flex', gap:'10px', 'align-items':'flex-start' }}>
                <Show when={!notification.read_at} fallback={<div style={{ width:'6px', 'flex-shrink':'0' }} />}>
                  <div style={{ width:'6px', height:'6px', 'border-radius':'50%', background: notification.level === 'error' ? 'var(--danger)' : 'var(--accent)', 'margin-top':'5px', 'flex-shrink':'0' }} />
                </Show>
                <div style={{ flex:'1', 'min-width':'0' }}>
                  <div style={{ 'font-family':"'DM Sans',sans-serif", 'font-size':'12px', 'font-weight':'600', color:'var(--text)', 'word-break':'break-word' }}>{notification.title}</div>
                  <Show when={notification.body}>
                    <div style={{ 'font-family':"'DM Sans',sans-serif", 'font-size':'11px', color:'var(--muted)', 'margin-top':'2px', 'word-break':'break-word' }}>{notification.body}</div>
                  </Show>
                  <div style={{ 'font-family':"'DM Mono',monospace", 'font-size':'10px', color:'var(--muted)', 'margin-top':'3px' }}>{formatDateTime(notification.created_at, lang())}</div>
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
