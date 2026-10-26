import { useI18n } from '../i18n/index'
import { For, Show } from 'solid-js'
import type { QueueItem } from '../types'
import { queueLabel } from '../utils/queueStatus'
import { isAuthErrorMessage, makeTranslateError } from '../utils/authError'

export function QueuePanel(props: {queue: QueueItem[]; onClose: () => void; onCancel: (id: number) => void; onRetry: (id: number) => void; onRetryAll: () => void; onDismiss: (id: number) => void; onOpenSettings?: () => void}) {
  const { translate } = useI18n()
  const translateError = makeTranslateError(translate)
  const failedCount = () => props.queue.filter(q => q.status === 'failed').length
  return (
    <Show when={props.queue.length > 0}>
      <div style={{ position:'fixed', bottom:'26px', right:'26px', 'z-index':'200', background:'var(--bg2)', border:'1px solid var(--border)', 'border-radius':'18px', padding:'18px', width:'380px', 'box-shadow':'0 8px 30px rgba(0,0,0,0.3)', 'max-height':'70vh', overflow:'auto' }}>
        <div style={{ display:'flex', 'align-items':'center', 'justify-content':'space-between', gap:'10px', 'margin-bottom':'13px' }}>
          <span style={{ 'font-family':"'DM Sans',sans-serif", 'font-weight':'700', 'font-size':'15px', color:'var(--text)' }}>{translate('queue_title')}</span>
          <Show when={failedCount() > 1}>
            <button onClick={props.onRetryAll}
              style={{ 'flex-shrink':'0', background:'var(--accent)', color:'#fff', border:'none', 'border-radius':'7px', padding:'5px 11px', 'font-size':'12px', 'font-weight':'600', cursor:'pointer', 'font-family':"'DM Sans',sans-serif" }}>
              {translate('download_retry_all')} ({failedCount()})
            </button>
          </Show>
        </div>
        <div style={{ display:'flex', 'flex-direction':'column', gap:'8px' }}>
          <For each={props.queue}>{item => (
            <div style={{ background:item.status==='failed'?'rgba(239,68,68,0.08)':'var(--bg3)', 'border-radius':'10px', padding:'11px 13px', border:item.status==='failed'?'1px solid rgba(239,68,68,0.3)':'1px solid transparent' }}>
              <div style={{ display:'flex', 'align-items':'center', 'justify-content':'space-between' }}>
                <div style={{ 'min-width':'0', flex:'1' }}>
                  <div style={{ display:'flex', 'align-items':'center', gap:'7px', 'font-family':"'DM Sans',sans-serif", 'font-size':'13px', color:'var(--text2)', 'font-weight':'500', overflow:'hidden', 'text-overflow':'ellipsis', 'white-space':'nowrap' }}>
                    {/* An update concerns a design already in the library, so it is
                        named after it; an import has only its address so far. */}
                    <Show when={item.kind === 'update'}>
                      <span style={{ 'flex-shrink':'0', 'font-family':"'DM Mono',monospace", 'font-size':'10px', 'font-weight':'700', color:'var(--accent-light)', background:'rgba(69,123,157,0.18)', 'border-radius':'5px', padding:'1px 6px', 'text-transform':'uppercase', 'letter-spacing':'0.04em' }}>
                        {translate('queue_kind_update')}
                      </span>
                    </Show>
                    <Show when={item.kind === 'update' && item.design_name}>
                      <span style={{ overflow:'hidden', 'text-overflow':'ellipsis' }}>{item.design_name}</span>
                    </Show>
                    <Show when={item.kind !== 'update' || !item.design_name}>
                    <Show when={item.source_url} fallback={<span>-</span>}>
                      <a href={item.source_url} target="_blank" rel="noopener noreferrer"
                        style={{ color:'inherit', 'text-decoration':'none' }}
                        onMouseOver={e => (e.currentTarget.style.textDecoration='underline')}
                        onMouseOut={e => (e.currentTarget.style.textDecoration='none')}>
                        {item.source_url?.replace(/^https?:\/\/(www\.)?/, '').slice(0, 50) || '-'}
                      </a>
                    </Show>
                    </Show>
                  </div>
                  <div style={{ 'font-family':"'DM Mono',monospace", 'font-size':'11px', color:item.status==='failed'?'var(--danger)':item.status==='running'?'var(--accent-light)':'var(--muted)', 'margin-top':'2px' }}>
                    {item.status === 'failed'
                      ? translate('download_status_failed')
                      : item.status === 'running'
                        ? (() => {
                            const step = item.current_step
                            if (!step) return translate(item.kind === 'update' ? 'queue_status_updating' : 'download_status_downloading')
                            const label = translate(`download_step_${step}` as any)
                            return item.step_current != null && item.step_total != null && (item.step_total as number) > 0
                              ? `${label} (${item.step_current}/${item.step_total})`
                              : label
                          })()
                        : queueLabel(item, translate)}
                  </div>
                </div>
                <Show when={item.status === 'pending' || item.status === 'running'}>
                  <div style={{ display:'flex', 'align-items':'center', gap:'8px', 'flex-shrink':'0', 'margin-left':'11px' }}>
                    <div style={{ width:'17px', height:'17px', border:'2px solid var(--border)', 'border-top':'2px solid var(--accent)', 'border-radius':'50%', 'animation':'spin 0.8s linear infinite' }} />
                    <button onClick={() => props.onCancel(item.id)}
                      title={translate('download_cancel')}
                      style={{ width:'22px', height:'22px', background:'var(--danger-bg)', border:'1px solid var(--danger-border)', 'border-radius':'7px', color:'var(--danger)', 'font-size':'13px', cursor:'pointer', display:'flex', 'align-items':'center', 'justify-content':'center', 'line-height':'1', 'flex-shrink':'0' }}>
                      ✕
                    </button>
                  </div>
                </Show>
              </div>
              <Show when={item.status === 'failed'}>
                <Show when={(item as any).error_msg}>
                  <div style={{ 'font-size':'11px', color:'var(--danger)', 'margin-top':'6px', 'line-height':'1.4', 'word-break':'break-word' }}>
                    {translateError((item as any).error_msg || '')}
                  </div>
                </Show>
                <Show when={isAuthErrorMessage((item as any).error_msg) && props.onOpenSettings}>
                  <button onClick={props.onOpenSettings}
                    style={{ width:'100%', background:'rgba(250,104,49,0.1)', border:'1px solid rgba(250,104,49,0.3)', 'border-radius':'6px', padding:'4px 0', 'font-size':'11px', 'font-weight':'600', color:'#fa6831', cursor:'pointer', 'font-family':"'DM Sans',sans-serif", 'margin-top':'7px' }}>
                    {translate('credentials_open_settings')}
                  </button>
                </Show>
                <div style={{ display:'flex', gap:'7px', 'margin-top':'9px' }}>
                  <button onClick={() => props.onRetry(item.id)} style={{ flex:'1', background:'var(--accent)', color:'#fff', border:'none', 'border-radius':'7px', padding:'5px 0', 'font-size':'12px', 'font-weight':'600', cursor:'pointer', 'font-family':"'DM Sans',sans-serif" }}>
                    {translate('download_retry')}
                  </button>
                  <button onClick={() => props.onDismiss(item.id)} style={{ flex:'1', background:'transparent', color:'var(--muted)', border:'1px solid var(--border)', 'border-radius':'7px', padding:'5px 0', 'font-size':'12px', cursor:'pointer', 'font-family':"'DM Sans',sans-serif" }}>
                    {translate('download_dismiss')}
                  </button>
                </div>
              </Show>
            </div>
          )}</For>
        </div>
      </div>
    </Show>
  )
}
