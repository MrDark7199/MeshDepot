import { CardStatusBar } from './CardStatusBar'
import { CARD_H, CARD_W } from '../constants/layout'
import { PLATFORM_COLORS, PLATFORM_LABELS } from '../constants/platforms'
import { useI18n } from '../i18n/index'
import { Show, on } from 'solid-js'
import type { DownloadJob } from '../types'
import { isAuthErrorMessage, makeTranslateError } from '../utils/authError'
import { GRADIENTS } from '../utils/designDisplay'

export function DownloadPlaceholderCard(props: { job: DownloadJob; onCancel: () => void; onRetry?: () => void; onDismiss?: () => void; onOpenSettings?: () => void }) {
  const { translate } = useI18n()
  const translateError = makeTranslateError(translate)
  const plat = props.job.platform || 'manual'
  const grad = GRADIENTS[0]
  const gradientString = `linear-gradient(135deg,${grad[0]},${grad[1]})`
  const isFailed = () => props.job.status === 'failed'
  const isActive = () => props.job.status === 'downloading'
  const stepLabel = () => {
    if (!isActive()) return translate('download_status_queued')
    const step = props.job.current_step as string | null | undefined
    if (!step) return translate('download_status_downloading')
    const label = translate(`download_step_${step}` as any)
    if (props.job.step_current != null && props.job.step_total != null && (props.job.step_total as number) > 0) {
      return `${label} (${props.job.step_current}/${props.job.step_total})`
    }
    return label
  }
  return (
    <div style={{ width:CARD_W, 'border-radius':'18px', overflow:'hidden', background:'var(--bg2)',
      'box-shadow':'0 4px 20px rgba(0,0,0,0.18)', 'flex-shrink':'0',
      border:`1px solid ${isFailed() ? 'var(--danger)' : 'var(--border)'}`, opacity:'0.9' }}>
      <div style={{ position:'relative', width:CARD_W, height:CARD_H, background:isFailed() ? 'linear-gradient(135deg,#7f1d1d,#450a0a)' : gradientString }}>
        <div style={{ position:'absolute', top:'13px', left:'13px', background:'rgba(0,0,0,0.72)', 'border-radius':'8px', padding:'4px 10px', 'font-size':'11px', 'font-weight':'700', color:'#fff', 'font-family':"'DM Mono',monospace", 'backdrop-filter':'blur(4px)' }}>
          {PLATFORM_LABELS[plat] || plat}
        </div>
        <div style={{ position:'absolute', inset:'0', display:'flex', 'align-items':'center', 'justify-content':'center' }}>
          {isFailed()
            ? <div style={{ 'font-size':'36px', color:'rgba(255,255,255,0.5)' }}>✕</div>
            : <div style={{ width:'38px', height:'38px', border:'3px solid rgba(255,255,255,0.25)', 'border-top-color':'#fff', 'border-radius':'50%', animation:'spin 0.9s linear infinite' }} />
          }
        </div>
        {/* Step overlay */}
        <Show when={!isFailed()}>
          <CardStatusBar kind={isActive() ? 'active' : 'queued'} label={stepLabel()} />
        </Show>
      </div>
      <div style={{ padding:'14px 16px 16px' }}>
        <div style={{ 'font-family':"'DM Sans',sans-serif", 'font-size':'13px', 'font-weight':'600', color: isFailed() ? 'var(--danger)' : 'var(--muted)', 'margin-bottom':'5px' }}>
          {isFailed() ? translate('download_status_failed') : isActive() ? translate('download_status_downloading') : translate('download_status_queued')}
        </div>
        <div style={{ 'font-family':"'DM Mono',monospace", 'font-size':'10px', color:'var(--muted)', 'white-space':'nowrap', overflow:'hidden', 'text-overflow':'ellipsis', 'margin-bottom': isFailed() ? '10px' : '0' }}>
          <Show when={props.job.source_url} fallback={<span>-</span>}>
            <a href={props.job.source_url} target="_blank" rel="noopener noreferrer"
              style={{ color:'inherit', 'text-decoration':'none' }}
              onMouseOver={e => (e.currentTarget.style.textDecoration='underline')}
              onMouseOut={e => (e.currentTarget.style.textDecoration='none')}>
              {props.job.source_url?.replace(/^https?:\/\/(www\.)?/, '')}
            </a>
          </Show>
        </div>
        <Show when={isFailed() && props.job.error_msg}>
          <div style={{ 'font-family':"'DM Sans',sans-serif", 'font-size':'11px', color:'var(--danger)', 'margin-bottom':'10px', 'word-break':'break-word', 'line-height':'1.4' }}>
            {translateError(props.job.error_msg)}
          </div>
        </Show>
        <Show when={isFailed() && isAuthErrorMessage(props.job.error_msg) && props.onOpenSettings}>
          <button onClick={props.onOpenSettings}
            style={{ width:'100%', background:'rgba(250,104,49,0.12)', border:'1px solid rgba(250,104,49,0.35)', 'border-radius':'7px', padding:'5px 0', 'font-size':'11px', 'font-weight':'600', color:'#fa6831', cursor:'pointer', 'font-family':"'DM Sans',sans-serif", 'margin-bottom':'8px' }}>
            {translate('credentials_open_settings')}
          </button>
        </Show>
        <Show when={isFailed()}>
          <div style={{ display:'flex', gap:'8px' }}>
            <button onClick={props.onRetry} style={{ flex:'1', background:'var(--accent)', color:'#fff', border:'none', 'border-radius':'8px', padding:'6px 0', 'font-size':'12px', 'font-weight':'600', cursor:'pointer', 'font-family':"'DM Sans',sans-serif" }}>
              {translate('download_retry')}
            </button>
            <button onClick={props.onDismiss} style={{ flex:'1', background:'var(--bg3)', color:'var(--muted)', border:'1px solid var(--border)', 'border-radius':'8px', padding:'6px 0', 'font-size':'12px', cursor:'pointer', 'font-family':"'DM Sans',sans-serif" }}>
              {translate('download_dismiss')}
            </button>
          </div>
        </Show>
      </div>
    </div>
  )
}

export function DownloadPlaceholderRow(props: { job: DownloadJob; onCancel: () => void; onRetry?: () => void; onDismiss?: () => void; onOpenSettings?: () => void }) {
  const { translate } = useI18n()
  const translateError = makeTranslateError(translate)
  const plat = props.job.platform || 'manual'
  const isFailed = () => props.job.status === 'failed'
  const isActive = () => props.job.status === 'downloading'
  const stepLabel = () => {
    if (!isActive()) return translate('download_status_queued')
    const step = props.job.current_step as string | null | undefined
    if (!step) return translate('download_status_downloading')
    const label = translate(`download_step_${step}` as any)
    if (props.job.step_current != null && props.job.step_total != null && (props.job.step_total as number) > 0) {
      return `${label} (${props.job.step_current}/${props.job.step_total})`
    }
    return label
  }
  return (
    <div style={{ display:'flex', 'align-items':'center', gap:'12px', padding:'10px 14px', background:'var(--bg2)',
      'border-radius':'12px', border:`1px solid ${isFailed() ? 'var(--danger)' : 'var(--border)'}`, 'flex-shrink':'0' }}>
      {/* Status icon */}
      <div style={{ width:'36px', height:'36px', 'flex-shrink':'0', 'border-radius':'8px',
        background: isFailed() ? 'rgba(239,68,68,0.15)' : 'var(--bg3)',
        display:'flex', 'align-items':'center', 'justify-content':'center' }}>
        <Show when={isFailed()} fallback={
          <Show when={isActive()} fallback={
            <div style={{ width:'14px', height:'14px', border:'2px solid #64748b', 'border-radius':'50%' }} />
          }>
            <div style={{ width:'16px', height:'16px', border:'2px solid #fff', 'border-top-color':'transparent', 'border-radius':'50%', animation:'spin 0.9s linear infinite' }} />
          </Show>
        }>
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="var(--danger)" stroke-width="2.5"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
        </Show>
      </div>
      {/* Platform + info */}
      <div style={{ flex:'1', 'min-width':'0' }}>
        <div style={{ display:'flex', 'align-items':'center', gap:'8px', 'margin-bottom':'2px' }}>
          <span style={{ 'font-size':'10px', 'font-weight':'700', color:'#fff', 'font-family':"'DM Mono',monospace",
            background: PLATFORM_COLORS[plat] ?? 'rgba(255,255,255,0.15)', 'border-radius':'5px', padding:'2px 7px' }}>
            {PLATFORM_LABELS[plat] || plat}
          </span>
          <span style={{ 'font-family':"'DM Sans',sans-serif", 'font-size':'12px', 'font-weight':'600',
            color: isFailed() ? 'var(--danger)' : 'var(--muted)' }}>
            {isFailed() ? translate('download_status_failed') : isActive() ? stepLabel() : translate('download_status_queued')}
          </span>
        </div>
        <div style={{ 'font-family':"'DM Mono',monospace", 'font-size':'11px', color:'var(--muted)', overflow:'hidden', 'text-overflow':'ellipsis', 'white-space':'nowrap' }}>
          <Show when={props.job.source_url} fallback={<span>-</span>}>
            <a href={props.job.source_url} target="_blank" rel="noopener noreferrer"
              style={{ color:'inherit', 'text-decoration':'none' }}
              onMouseOver={e => (e.currentTarget.style.textDecoration='underline')}
              onMouseOut={e => (e.currentTarget.style.textDecoration='none')}>
              {props.job.source_url?.replace(/^https?:\/\/(www\.)?/, '')}
            </a>
          </Show>
        </div>
        <Show when={isFailed() && props.job.error_msg}>
          <div style={{ 'font-size':'11px', color:'var(--danger)', 'margin-top':'2px', overflow:'hidden', 'text-overflow':'ellipsis', 'white-space':'nowrap' }}>
            {translateError(props.job.error_msg)}
          </div>
        </Show>
      </div>
      {/* Actions */}
      <div style={{ display:'flex', gap:'6px', 'flex-shrink':'0' }}>
        <Show when={isFailed() && isAuthErrorMessage(props.job.error_msg) && props.onOpenSettings}>
          <button onClick={props.onOpenSettings}
            style={{ padding:'5px 10px', 'border-radius':'7px', background:'rgba(250,104,49,0.12)', border:'1px solid rgba(250,104,49,0.35)', color:'#fa6831', 'font-size':'11px', 'font-weight':'600', cursor:'pointer', 'font-family':"'DM Sans',sans-serif", 'white-space':'nowrap' }}>
            {translate('credentials_open_settings')}
          </button>
        </Show>
        <Show when={isFailed()}>
          <button onClick={props.onRetry}
            style={{ padding:'5px 10px', 'border-radius':'7px', background:'var(--accent)', border:'none', color:'#fff', 'font-size':'11px', 'font-weight':'600', cursor:'pointer', 'font-family':"'DM Sans',sans-serif" }}>
            {translate('download_retry')}
          </button>
          <button onClick={props.onDismiss}
            style={{ padding:'5px 10px', 'border-radius':'7px', background:'var(--bg3)', border:'1px solid var(--border)', color:'var(--muted)', 'font-size':'11px', cursor:'pointer', 'font-family':"'DM Sans',sans-serif" }}>
            {translate('download_dismiss')}
          </button>
        </Show>
        <Show when={!isFailed()}>
          <button onClick={props.onCancel} title={translate('download_cancel')}
            style={{ width:'28px', height:'28px', background:'var(--bg3)', border:'1px solid var(--border)', 'border-radius':'50%', color:'var(--muted)', 'font-size':'16px', cursor:'pointer', display:'flex', 'align-items':'center', 'justify-content':'center', 'line-height':'1' }}>
            ×
          </button>
        </Show>
      </div>
    </div>
  )
}
