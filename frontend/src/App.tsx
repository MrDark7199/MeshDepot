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
import { UserAvatar } from './components/UserAvatar'
import type { Design, DesignID, Tag, QueueItem, DownloadJob, Filters, Collection, SyncStatus, SyncStep } from './types'
import { createNotificationsStore } from './hooks/useNotifications'
import { createSyncProgressStore } from './hooks/useSyncProgress'
import { createDownloadQueueStore } from './hooks/useDownloadQueue'
import { NotificationBell } from './components/NotificationBell'
import { guardClose as guardCloseGlobal, pendingActionSignal, pendingMessageSignal, confirmDiscard, cancelDiscard } from './utils/unsavedChanges'
import { displayName, displayAuthor } from './utils/designText'
import { PLATFORM_COLORS, PLATFORM_LABELS, platformLabel } from './constants/platforms'

const GRADIENTS = [
  ['#1a1a2e','#16213e','#0f3460'],['#2d1b33','#11998e','#38ef7d'],
  ['#0f0c29','#302b63','#24243e'],['#1f1c2c','#928dab'],
  ['#0f2027','#203a43','#2c5364'],['#141e30','#243b55'],['#200122','#6f0000'],
]

// ── Scale: 25% larger than previous baseline ──────────────────────────────────
const NAV_H = '85px'
const CARD_W = '280px'
const CARD_H = '344px'

/** Base interval of the single background-poll ticker (see the onMount loop in MainApp). */
const POLL_TICK_MS = 2000

const globalStyles = `
  *, *::before, *::after { box-sizing: border-box; margin: 0; padding: 0; }
  html, body, #root { background: var(--bg); min-height: 100vh; font-size: 16px; -webkit-font-smoothing: antialiased; }
  ::-webkit-scrollbar { width: 8px; }
  ::-webkit-scrollbar-track { background: var(--bg2); }
  ::-webkit-scrollbar-thumb { background: var(--scrollbar); border-radius: 5px; }
  html[data-theme="dark"]  input, html[data-theme="dark"]  select, html[data-theme="dark"]  textarea { color-scheme: dark; }
  html[data-theme="light"] input, html[data-theme="light"] select, html[data-theme="light"] textarea { color-scheme: light; }
  /* 3D-viewer number fields sit in an always-dark panel; give the number a little air before the spinner. */
  .stlv-num::-webkit-inner-spin-button, .stlv-num::-webkit-outer-spin-button { margin-left: 7px; }
  @keyframes spin { to { transform: rotate(360deg); } }
  @keyframes toastIn { from { opacity: 0; transform: translateX(-50%) translateY(-12px); } to { opacity: 1; transform: translateX(-50%) translateY(0); } }

  /* ── Mobile ──────────────────────────────────────────────────────────────── */
  @media (max-width: 640px) {
    /* Nav: wrap to two rows (logo+buttons / search) */
    .stlv-nav { flex-wrap: wrap !important; height: auto !important; padding: 10px 14px !important; column-gap: 10px !important; row-gap: 0 !important; }
    .stlv-nav-logo { flex: 0 0 auto !important; }
    .stlv-nav-right { flex: 0 0 auto !important; margin-left: auto !important; gap: 8px !important; }
    /* Search row drops below logo row */
    .stlv-nav-center { flex: 0 0 100% !important; padding-bottom: 8px !important; }
    .stlv-nav-center .stlv-search { width: 100% !important; }
    /* Hide secondary nav actions on mobile */
    .stlv-nav-extra { display: none !important; }
    /* Grid */
    .stlv-main { padding: 16px 10px !important; }
    .stlv-grid { gap: 12px !important; }
    .stlv-card { width: calc(50vw - 19px) !important; min-width: 140px !important; }
    .stlv-card-cover { width: 100% !important; }
    /* Detail page: single column */
    .stlv-detail-grid { grid-template-columns: 1fr !important; }
    /* Breadcrumb: don't stick below a variable-height nav */
    .stlv-breadcrumb { position: static !important; top: auto !important; }
    /* Notification panel: respect screen edge */
    .stlv-notif-panel { width: min(320px, calc(100vw - 16px)) !important; right: -8px !important; }
    /* Modals */
    .stlv-modal { width: calc(100vw - 24px) !important; padding: 20px !important; }
    .stlv-modal-wide { width: 100vw !important; height: 100dvh !important; border-radius: 0 !important; }
  }
  @media (max-width: 400px) {
    .stlv-card { width: calc(100vw - 20px) !important; }
    .stlv-card-cover { height: 220px !important; }
  }
`

/**
 * Fixed-position toast notification displayed at the top centre of the screen.
 * Renders nothing when `message` is empty.
 */
function Toast(props: {message:string; variant:string}) {
  const isError = () => props.variant === 'error'
  return (
    <Show when={props.message}>
      <div style={{ position:'fixed', top:'34px', left:'50%', 'transform':'translateX(-50%)', background:isError()?'var(--danger-bg)':'var(--success-bg)', border:`1px solid ${isError()?'var(--danger-border)':'var(--success-border)'}`, 'border-radius':'14px', padding:'13px 32px', color:isError()?'var(--danger)':'var(--success)', 'font-family':"'DM Sans',sans-serif", 'font-size':'16px', 'font-weight':'600', 'z-index':'9999', 'box-shadow':'0 8px 30px rgba(0,0,0,0.4)', 'pointer-events':'none', 'white-space':'nowrap', 'animation':'toastIn 0.18s ease forwards' }}>
        {props.message}
      </div>
    </Show>
  )
}


function NavBtn(props: {children: any; onClick: () => void; title: string; active?: boolean}) {
  return (
    <button onClick={props.onClick} title={props.title}
      style={{ background:props.active?'rgba(69,123,157,0.2)':'var(--surface)', border:`1px solid ${props.active?'var(--accent)':'var(--border)'}`, 'border-radius':'11px', width:'42px', height:'42px', display:'flex', 'align-items':'center', 'justify-content':'center', cursor:'pointer', 'transition':'background 0.15s', 'flex-shrink':'0', color:'var(--muted)' }}>
      {props.children}
    </button>
  )
}

const AUTH_ERROR_PREFIXES = [
  'error.platform_credentials_required:',
  'error.thingiverse_restricted:', 'error.thingiverse_no_token:', 'error.thingiverse_auth_failed:',
  'error.makerworld_no_token:', 'error.makerworld_auth_failed:',
  'error.myminifactory_no_token:', 'error.myminifactory_auth_failed:', 'error.myminifactory_restricted:',
  'error.cults3d_restricted:', 'error.cults3d_no_token:', 'error.cults3d_auth_failed:',
  'error.printables_no_token:', 'error.printables_auth_failed:',
]
const isAuthErrorMessage = (msg?: string) => !!msg && AUTH_ERROR_PREFIXES.some(p => msg.startsWith(p))

/** Translates a backend error_msg string. Tries the error code as an i18n key first,
 *  falls back to the human-readable part after the first colon. */
const makeTranslateError = (translate: (k: string, p?: any) => string) => (msg?: string): string => {
  if (!msg) return ''
  const idx = msg.indexOf(':')
  const code = idx >= 0 ? msg.slice(0, idx) : msg
  const rest = idx >= 0 ? msg.slice(idx + 1).trim() : ''
  // The part after the first colon is either a parameter (e.g. platform name for
  // key-based errors) or a ready-made human message (when no i18n key matches).
  const t = translate(code, { platform: rest })
  if (t !== code) return t
  return rest || msg
}

type CardStatusKind = 'queued' | 'active' | 'updated' | 'no_change' | 'error'
function CardStatusBar(props: { kind: CardStatusKind; label: string }) {
  const mono = "'DM Mono',monospace"
  return (
    <div style={{ position:'absolute', bottom:'0', left:'0', right:'0', background:'rgba(0,0,0,0.72)', 'backdrop-filter':'blur(4px)', display:'flex', 'align-items':'center', 'justify-content':'center', gap:'7px', padding:'8px 12px' }}>
      <Show when={props.kind === 'queued'}>
        <div style={{ width:'13px', height:'13px', 'flex-shrink':'0', border:'2px solid #64748b', 'border-radius':'50%' }} />
        <span style={{ 'font-family':mono, 'font-size':'11px', color:'#94a3b8', 'font-weight':'600' }}>{props.label}</span>
      </Show>
      <Show when={props.kind === 'active'}>
        <div style={{ width:'13px', height:'13px', 'flex-shrink':'0', border:'2px solid rgba(255,255,255,0.3)', 'border-top-color':'#fff', 'border-radius':'50%', animation:'spin 0.7s linear infinite' }} />
        <span style={{ 'font-family':mono, 'font-size':'11px', color:'#fff', 'font-weight':'600' }}>{props.label}</span>
      </Show>
      <Show when={props.kind === 'updated'}>
        <div style={{ width:'13px', height:'13px', 'flex-shrink':'0', background:'#22c55e', 'border-radius':'50%', display:'flex', 'align-items':'center', 'justify-content':'center', 'font-size':'9px', color:'#fff' }}>✓</div>
        <span style={{ 'font-family':mono, 'font-size':'11px', color:'#22c55e', 'font-weight':'600' }}>{props.label}</span>
      </Show>
      <Show when={props.kind === 'no_change'}>
        <div style={{ width:'13px', height:'13px', 'flex-shrink':'0', background:'#64748b', 'border-radius':'50%', display:'flex', 'align-items':'center', 'justify-content':'center', 'font-size':'9px', color:'#fff' }}>✓</div>
        <span style={{ 'font-family':mono, 'font-size':'11px', color:'#94a3b8', 'font-weight':'600' }}>{props.label}</span>
      </Show>
      <Show when={props.kind === 'error'}>
        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="#ef4444" stroke-width="2.5"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
        <span style={{ 'font-family':mono, 'font-size':'11px', color:'#ef4444', 'font-weight':'600' }}>{props.label}</span>
      </Show>
    </div>
  )
}

function DownloadPlaceholderCard(props: { job: DownloadJob; onCancel: () => void; onRetry?: () => void; onDismiss?: () => void; onOpenSettings?: () => void }) {
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

function DownloadPlaceholderRow(props: { job: DownloadJob; onCancel: () => void; onRetry?: () => void; onDismiss?: () => void; onOpenSettings?: () => void }) {
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

/**
 * Opens a design's detail view in a new browser tab/window via the
 * ?design=<id> deep link (used by middle-click on cards and rows).
 */
const openDesignInNewWindow = (id: DesignID) =>
  window.open(`${window.location.pathname}?design=${encodeURIComponent(id)}`, '_blank', 'noopener')

/**
 * Picks one of the placeholder gradients for a design that has no cover.
 *
 * It used to be `id % GRADIENTS.length`, which the sequential rowid made an
 * even spread. The public id is a hex string, so its first characters stand in
 * for the number: what matters is only that the same design always gets the
 * same colour.
 */
const gradientFor = (id: DesignID) =>
  GRADIENTS[parseInt(id.slice(0, 6), 16) % GRADIENTS.length] ?? GRADIENTS[0]

/**
 * Thumbnail card for a single design in the main grid.
 * Shows the cover image (or a gradient fallback), platform badge, rating stars,
 * shared/source-gone indicators, up to three tag chips, and an optional
 * sync-status overlay during a Sync-All operation.
 * Hovering reveals quick-action buttons (View, Sync).
 */
// Turns the running sync phase (current_step) into a label; null when no phase
// is known, which leaves the display on "syncing… %".
function syncStepLabel(translate: (k: any, p?: any) => string, s?: SyncStep): string | null {
  if (!s || !s.step) return null
  const label = translate(`sync_step_${s.step}` as any)
  return s.tot > 0 ? `${label} (${s.cur}/${s.tot})` : label
}

function DesignCard(props: {design:Design; index:number; onOpen:(design:Design)=>void; onSync?:(design:Design)=>void; syncStatus?:SyncStatus; syncProgress?:number; syncStep?:SyncStep}) {
  const { translate, lang, translateDesigns } = useI18n()
  const { user } = useAuth()
  const authorName = () => displayAuthor(props.design, user()?.name)
  const [hovered, setHovered] = createSignal(false)
  const grad = GRADIENTS[props.index % GRADIENTS.length]
  const gradientString = grad.length === 3
    ? `linear-gradient(135deg,${grad[0]},${grad[1]},${grad[2]})`
    : `linear-gradient(135deg,${grad[0]},${grad[1]})`
  const coverUrl = () => props.design.cover_path ? api.coverUrl(props.design.cover_path) : null
  const plat = () => props.design.source_platform

  return (
    <div class="stlv-card"
      onMouseEnter={() => setHovered(true)}
      onMouseLeave={() => setHovered(false)}
      onClick={() => props.onOpen(props.design)}
      onMouseDown={e => { if (e.button === 1) e.preventDefault() }}
      onAuxClick={e => { if (e.button === 1) { e.preventDefault(); openDesignInNewWindow(props.design.id) } }}
      style={{ width:CARD_W, 'border-radius':'18px', overflow:'hidden', background:'var(--bg2)',
        'box-shadow': hovered() ? '0 20px 56px rgba(0,0,0,0.45)' : '0 4px 20px rgba(0,0,0,0.18)',
        'transform': hovered() ? 'translateY(-8px)' : 'none', 'transition':'all 0.22s ease', cursor:'pointer',
        'flex-shrink':'0', border: props.design.is_shared ? '1px solid var(--accent)' : '1px solid var(--border)' }}>

      {/* Cover */}
      <div class="stlv-card-cover" style={{ position:'relative', width:CARD_W, height:CARD_H, background:gradientString }}>
        <Show when={coverUrl()}>
          <img src={coverUrl()!} alt=""
            style={{ width:'100%', height:'100%', 'object-fit':'cover' }}
            onError={e => { (e.target as HTMLImageElement).style.display = 'none' }} />
        </Show>
        <Show when={!coverUrl()}>
          <div style={{ position:'absolute', inset:'0', display:'flex', 'align-items':'center', 'justify-content':'center', 'font-size':'72px', opacity:'0.18' }}>🖨️</div>
        </Show>

        {/* Shared badge */}
        <Show when={props.design.is_shared}>
          <div style={{ position:'absolute', top:'13px', left:'13px', background:'rgba(69,123,157,0.85)', 'border-radius':'8px', padding:'4px 10px', 'font-size':'11px', 'font-weight':'700', color:'#fff', 'font-family':"'DM Mono',monospace", 'backdrop-filter':'blur(4px)', display:'flex', 'align-items':'center', gap:'5px' }}>
            <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="#fff" stroke-width="2.5">
              <path d="M4 12v8a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2v-8"/>
              <polyline points="16 6 12 2 8 6"/><line x1="12" y1="2" x2="12" y2="15"/>
            </svg>
            shared
          </div>
        </Show>
        <Show when={!props.design.is_shared && plat()}>
          <div style={{ position:'absolute', top:'13px', left:'13px', background: PLATFORM_COLORS[plat()!] ?? 'rgba(0,0,0,0.72)', 'border-radius':'8px', padding:'4px 10px', 'font-size':'11px', 'font-weight':'700', color:'#fff', 'font-family':"'DM Mono',monospace", 'backdrop-filter':'blur(4px)' }}>
            {platformLabel(plat()!, translate)}
          </div>
        </Show>
        <Show when={props.design.is_hidden}>
          <div style={{ position:'absolute', top:'13px', right:'13px', background:'rgba(168,85,247,0.85)', 'border-radius':'8px', padding:'4px 10px', 'font-size':'11px', 'font-weight':'700', color:'#fff', 'font-family':"'DM Mono',monospace", 'backdrop-filter':'blur(4px)', display:'flex', 'align-items':'center', gap:'5px' }}>
            <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="#fff" stroke-width="2.5"><path d="M17.94 17.94A10.07 10.07 0 0 1 12 20c-7 0-11-8-11-8a18.45 18.45 0 0 1 5.06-5.94"/><path d="M9.9 4.24A9.12 9.12 0 0 1 12 4c7 0 11 8 11 8a18.5 18.5 0 0 1-2.16 3.19"/><line x1="1" y1="1" x2="23" y2="23"/></svg>
            hidden
          </div>
        </Show>
        <Show when={(props.design as any).source_deleted}>
          <div style={{ position:'absolute', top:'13px', right:'13px', background:'rgba(230,57,70,0.85)', 'border-radius':'8px', padding:'4px 10px', 'font-size':'11px', 'font-weight':'700', color:'#fff', 'font-family':"'DM Mono',monospace", 'backdrop-filter':'blur(4px)', display:'flex', 'align-items':'center', gap:'5px' }}>
            <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="#fff" stroke-width="2.5"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
            source gone
          </div>
        </Show>

        {/* Rating */}
        <Show when={(props.design.rating ?? 0) > 0}>
          <div style={{ position:'absolute', bottom:'13px', left:'13px', background:'rgba(0,0,0,0.65)', 'border-radius':'7px', padding:'3px 9px', 'font-size':'13px', color:'#f4a261', 'backdrop-filter':'blur(4px)' }}>
            {'★'.repeat(props.design.rating ?? 0)}{'☆'.repeat(5 - (props.design.rating ?? 0))}
          </div>
        </Show>

        {/* Sync-all status overlay */}
        <Show when={props.syncStatus}>
          <CardStatusBar
            kind={props.syncStatus === 'syncing' ? 'active' : props.syncStatus as CardStatusKind}
            label={
              props.syncStatus === 'queued' ? translate('sync_status_queued') :
              props.syncStatus === 'syncing' ? (syncStepLabel(translate, props.syncStep) ?? (props.syncProgress && props.syncProgress > 0 ? `${translate('sync_status_syncing')} ${props.syncProgress}%` : translate('sync_status_syncing'))) :
              props.syncStatus === 'updated' ? translate('sync_status_updated') :
              props.syncStatus === 'no_change' ? translate('sync_status_no_change') :
              translate('sync_status_error')
            }
          />
        </Show>

        {/* Action buttons on hover */}
        <div style={{ position:'absolute', top:'11px', right:'11px', display:'flex', gap:'6px', opacity:hovered()?'1':'0', 'transition':'opacity 0.2s' }}>
          <button onClick={e => { e.stopPropagation(); props.onOpen(props.design) }} title="View"
            style={{ background:'rgba(0,0,0,0.65)', border:'1px solid rgba(255,255,255,0.2)', 'border-radius':'9px', width:'36px', height:'36px', display:'flex', 'align-items':'center', 'justify-content':'center', cursor:'pointer', 'backdrop-filter':'blur(4px)' }}>
            <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="#fff" stroke-width="2.2">
              <path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z"/><circle cx="12" cy="12" r="3"/>
            </svg>
          </button>
        </div>
      </div>

      {/* Footer */}
      <div style={{ padding:'14px 16px 16px', 'border-top':`1px solid ${props.design.is_shared ? 'rgba(69,123,157,0.3)' : 'var(--border)'}` }}>
        <div style={{ 'font-family':"'DM Sans',sans-serif", 'font-size':'16px', 'font-weight':'600', color:'var(--text)', 'white-space':'nowrap', overflow:'hidden', 'text-overflow':'ellipsis' }}>
          {displayName(props.design, lang(), translateDesigns())}
        </div>
        <Show when={authorName()}>
          <div style={{ 'font-family':"'DM Mono',monospace", 'font-size':'12px', color:'var(--muted)', 'margin-top':'3px', overflow:'hidden', 'text-overflow':'ellipsis', 'white-space':'nowrap' }}>
            {translate('card_by')} {authorName()}
          </div>
        </Show>
        <Show when={props.design.is_shared && props.design.shared_by_name}>
          <div style={{ 'font-family':"'DM Mono',monospace", 'font-size':'11px', color:'var(--accent-light)', 'margin-top':'3px' }}>
            ↗ {props.design.shared_by_name}
          </div>
        </Show>
        <Show when={(props.design.tags?.length ?? 0) > 0}>
          <div style={{ display:'flex', 'flex-wrap':'wrap', gap:'5px', 'margin-top':'9px' }}>
            <For each={props.design.tags!.slice(0, 3)}>{tag =>
              <span style={{ 'font-size':'11px', padding:'3px 8px', 'border-radius':'6px', background:tag.color+'33', color:tag.color, 'font-family':"'DM Sans',sans-serif", 'font-weight':'600' }}>
                {tag.name}
              </span>
            }</For>
            <Show when={props.design.tags!.length > 3}>
              <span style={{ 'font-size':'11px', color:'var(--muted)', 'font-family':"'DM Mono',monospace" }}>
                +{props.design.tags!.length - 3}
              </span>
            </Show>
          </div>
        </Show>
      </div>
    </div>
  )
}

function DesignRow(props: {design:Design; index:number; onOpen:(design:Design)=>void; onSync?:(design:Design)=>void; syncStatus?:SyncStatus; syncProgress?:number; syncStep?:SyncStep}) {
  const { translate, lang, translateDesigns } = useI18n()
  const { user } = useAuth()
  const authorName = () => displayAuthor(props.design, user()?.name)
  const grad = GRADIENTS[props.index % GRADIENTS.length]
  const gradientString = grad.length === 3
    ? `linear-gradient(135deg,${grad[0]},${grad[1]},${grad[2]})`
    : `linear-gradient(135deg,${grad[0]},${grad[1]})`
  const coverUrl = () => props.design.cover_path ? api.coverUrl(props.design.cover_path) : null
  const plat = () => props.design.source_platform

  return (
    <div onClick={() => props.onOpen(props.design)}
      onMouseDown={e => { if (e.button === 1) e.preventDefault() }}
      onAuxClick={e => { if (e.button === 1) { e.preventDefault(); openDesignInNewWindow(props.design.id) } }}
      style={{ display:'flex', 'align-items':'center', gap:'12px', padding:'8px 12px', background:'var(--bg2)',
        'border-radius':'12px', border: props.design.is_shared ? '1px solid var(--accent)' : '1px solid var(--border)',
        cursor:'pointer', transition:'background 0.15s' }}
      onMouseEnter={e => (e.currentTarget as HTMLElement).style.background = 'var(--bg3)'}
      onMouseLeave={e => (e.currentTarget as HTMLElement).style.background = 'var(--bg2)'}>

      {/* Thumbnail */}
      <div style={{ width:'48px', height:'48px', 'flex-shrink':'0', 'border-radius':'8px', overflow:'hidden', background: coverUrl() ? 'var(--bg3)' : gradientString, position:'relative' }}>
        <Show when={coverUrl()}>
          <img src={coverUrl()!} alt="" style={{ width:'100%', height:'100%', 'object-fit':'cover', 'border-radius':'8px', display:'block' }}
            onError={e => { (e.target as HTMLImageElement).style.display='none' }} />
        </Show>
      </div>

      {/* Platform badge */}
      <Show when={plat()}>
        <span style={{ 'flex-shrink':'0', 'font-size':'10px', 'font-weight':'700', color:'#fff', 'font-family':"'DM Mono',monospace",
          background: props.design.is_shared ? 'rgba(69,123,157,0.85)' : (PLATFORM_COLORS[plat()!] ?? 'rgba(0,0,0,0.45)'),
          'border-radius':'5px', padding:'2px 7px' }}>
          {props.design.is_shared ? 'shared' : platformLabel(plat()!, translate)}
        </span>
      </Show>

      {/* Name + author */}
      <div style={{ flex:'1', 'min-width':'0' }}>
        <div style={{ 'font-family':"'DM Sans',sans-serif", 'font-size':'14px', 'font-weight':'600', color:'var(--text)', overflow:'hidden', 'text-overflow':'ellipsis', 'white-space':'nowrap' }}>
          {displayName(props.design, lang(), translateDesigns())}
        </div>
        <Show when={authorName()}>
          <div style={{ 'font-family':"'DM Mono',monospace", 'font-size':'11px', color:'var(--muted)', overflow:'hidden', 'text-overflow':'ellipsis', 'white-space':'nowrap' }}>
            {translate('card_by')} {authorName()}
          </div>
        </Show>
        <Show when={props.design.is_shared && props.design.shared_by_name}>
          <div style={{ 'font-family':"'DM Mono',monospace", 'font-size':'11px', color:'var(--accent-light)' }}>
            ↗ {props.design.shared_by_name}
          </div>
        </Show>
      </div>

      {/* Tags */}
      <Show when={(props.design.tags?.length ?? 0) > 0}>
        <div class="stlv-row-tags" style={{ display:'flex', gap:'4px', 'flex-shrink':'0' }}>
          <For each={props.design.tags!.slice(0, 3)}>{tag =>
            <span style={{ 'font-size':'10px', padding:'2px 7px', 'border-radius':'5px', background:tag.color+'33', color:tag.color, 'font-family':"'DM Sans',sans-serif", 'font-weight':'600', 'white-space':'nowrap' }}>
              {tag.name}
            </span>
          }</For>
          <Show when={props.design.tags!.length > 3}>
            <span style={{ 'font-size':'10px', color:'var(--muted)', 'font-family':"'DM Mono',monospace" }}>+{props.design.tags!.length - 3}</span>
          </Show>
        </div>
      </Show>

      {/* Rating */}
      <Show when={(props.design.rating ?? 0) > 0}>
        <span style={{ 'flex-shrink':'0', 'font-size':'12px', color:'#f4a261', 'white-space':'nowrap' }}>
          {'★'.repeat(props.design.rating ?? 0)}
        </span>
      </Show>

      {/* source gone */}
      <Show when={(props.design as any).source_deleted}>
        <span style={{ 'flex-shrink':'0', 'font-size':'10px', 'font-weight':'700', color:'#ef4444', 'font-family':"'DM Mono',monospace", 'white-space':'nowrap' }}>✕ source gone</span>
      </Show>

      {/* Sync status pill */}
      <Show when={props.syncStatus}>
        <div style={{ 'flex-shrink':'0', display:'flex', 'align-items':'center', gap:'5px' }}>
          <Show when={props.syncStatus === 'queued'}>
            <div style={{ width:'10px', height:'10px', border:'2px solid #64748b', 'border-radius':'50%' }} />
            <span style={{ 'font-family':"'DM Mono',monospace", 'font-size':'10px', color:'#94a3b8', 'font-weight':'600', 'white-space':'nowrap' }}>{translate('sync_status_queued')}</span>
          </Show>
          <Show when={props.syncStatus === 'syncing'}>
            <div style={{ width:'10px', height:'10px', border:'2px solid #fff', 'border-top-color':'transparent', 'border-radius':'50%', animation:'spin 0.7s linear infinite' }} />
            <span style={{ 'font-family':"'DM Mono',monospace", 'font-size':'10px', color:'#fff', 'font-weight':'600', 'white-space':'nowrap' }}>
              {syncStepLabel(translate, props.syncStep) ?? (props.syncProgress && props.syncProgress > 0 ? `${translate('sync_status_syncing')} ${props.syncProgress}%` : translate('sync_status_syncing'))}
            </span>
          </Show>
          <Show when={props.syncStatus === 'updated'}>
            <span style={{ 'font-size':'10px', color:'#22c55e', 'font-weight':'600', 'font-family':"'DM Mono',monospace", 'white-space':'nowrap' }}>✓ {translate('sync_status_updated')}</span>
          </Show>
          <Show when={props.syncStatus === 'no_change'}>
            <span style={{ 'font-size':'10px', color:'#94a3b8', 'font-family':"'DM Mono',monospace", 'white-space':'nowrap' }}>✓ {translate('sync_status_no_change')}</span>
          </Show>
          <Show when={props.syncStatus === 'error'}>
            <span style={{ 'font-size':'10px', color:'#ef4444', 'font-family':"'DM Mono',monospace", 'white-space':'nowrap' }}>✕ {translate('sync_status_error')}</span>
          </Show>
        </div>
      </Show>

      {/* Action buttons */}
      <div style={{ 'flex-shrink':'0', display:'flex', gap:'6px' }} onClick={e => e.stopPropagation()}>
        <button onClick={e => { e.stopPropagation(); props.onOpen(props.design) }} title={translate('btn_view') || 'View'}
          style={{ background:'var(--bg3)', border:'1px solid var(--border)', 'border-radius':'8px', width:'32px', height:'32px', display:'flex', 'align-items':'center', 'justify-content':'center', cursor:'pointer' }}>
          <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="var(--text2)" stroke-width="2.2">
            <path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z"/><circle cx="12" cy="12" r="3"/>
          </svg>
        </button>
      </div>
    </div>
  )
}

/**
 * Modal for adding designs to a collection. Lists the user's addable designs
 * (owned or shared, not yet in the collection) server-side with search + paging,
 * lets the user multi-select, and adds the chosen ones on confirm.
 */
function AddDesignsToCollectionModal(props: {
  collectionId: number
  collectionName: string
  onClose: () => void
  onAdded: () => void
  showToast: (m: string, v?: string) => void
}) {
  const { translate, lang, translateDesigns } = useI18n()
  const { user } = useAuth()
  const PER_PAGE = 40
  const [search, setSearch] = createSignal('')
  const [items, setItems] = createSignal<Design[]>([])
  const [total, setTotal] = createSignal(0)
  const [loading, setLoading] = createSignal(false)
  const [adding, setAdding] = createSignal(false)
  const [selected, setSelected] = createSignal<Set<DesignID>>(new Set())
  let pageLoaded = 0
  let searchTimer: ReturnType<typeof setTimeout>

  const load = async (page: number, query: string, append: boolean) => {
    setLoading(true)
    try {
      const res: any = await api.getAddableDesigns(props.collectionId, query, page, PER_PAGE)
      const incoming: Design[] = res.data?.items || []
      setItems(prev => append ? [...prev, ...incoming] : incoming)
      setTotal(res.data?.total ?? 0)
      pageLoaded = page
    } catch {} finally { setLoading(false) }
  }
  load(1, '', false)

  const onSearch = (q: string) => {
    setSearch(q)
    clearTimeout(searchTimer)
    searchTimer = setTimeout(() => load(1, q.trim(), false), 300)
  }

  const toggle = (id: DesignID) => setSelected(prev => {
    const next = new Set(prev)
    next.has(id) ? next.delete(id) : next.add(id)
    return next
  })

  const confirm = async () => {
    if (selected().size === 0) return
    setAdding(true)
    try {
      await api.addToCollection(props.collectionId, [...selected()])
      props.showToast(translate('col_add_done', { n: selected().size }))
      props.onAdded()
      props.onClose()
    } catch { props.showToast(translate('col_add_failed'), 'error') }
    finally { setAdding(false) }
  }

  const hasMore = () => items().length < total()
  const sans = "'DM Sans',sans-serif"
  const mono = "'DM Mono',monospace"

  return (
    <div onClick={props.onClose}
      style={{ position:'fixed', inset:'0', background:'rgba(0,0,0,0.8)', display:'flex', 'align-items':'center', 'justify-content':'center', 'z-index':'2000', 'backdrop-filter':'blur(6px)' }}>
      <div onClick={e => e.stopPropagation()}
        style={{ background:'var(--bg)', 'border-radius':'18px', width:'560px', 'max-width':'94vw', height:'min(86vh, 720px)', display:'flex', 'flex-direction':'column', overflow:'hidden', border:'1px solid var(--border)', 'box-shadow':'0 40px 100px rgba(0,0,0,0.5)' }}>
        {/* Header */}
        <div style={{ padding:'18px 22px', 'border-bottom':'1px solid var(--border)', display:'flex', 'align-items':'center', 'justify-content':'space-between', gap:'12px', 'flex-shrink':'0' }}>
          <span style={{ 'font-family':sans, 'font-weight':'700', 'font-size':'16px', color:'var(--text)', overflow:'hidden', 'text-overflow':'ellipsis', 'white-space':'nowrap' }}>
            {translate('col_add_title', { name: props.collectionName })}
          </span>
          <button onClick={props.onClose} style={{ background:'none', border:'none', color:'var(--muted)', cursor:'pointer', 'font-size':'22px', 'line-height':'1', 'flex-shrink':'0' }}>×</button>
        </div>
        {/* Search */}
        <div style={{ padding:'14px 22px 10px', 'flex-shrink':'0' }}>
          <div style={{ position:'relative' }}>
            <input value={search()} onInput={e => onSearch(e.currentTarget.value)} placeholder={translate('col_add_search')}
              style={{ width:'100%', background:'var(--input-bg)', border:'1px solid var(--border2)', 'border-radius':'10px', padding:`10px ${search() ? '38px' : '14px'} 10px 14px`, color:'var(--text)', 'font-family':sans, 'font-size':'14px', outline:'none', 'box-sizing':'border-box' }} />
            <Show when={search()}>
              <button onClick={() => { clearTimeout(searchTimer); setSearch(''); load(1, '', false) }}
                style={{ position:'absolute', right:'11px', top:'50%', transform:'translateY(-50%)', background:'none', border:'none', cursor:'pointer', display:'flex', 'align-items':'center', padding:'2px', color:'var(--muted)' }}>
                <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
              </button>
            </Show>
          </div>
        </div>
        {/* List */}
        <div style={{ flex:'1', 'overflow-y':'auto', padding:'0 22px' }}>
          <Show when={!loading() && items().length === 0}>
            <div style={{ color:'var(--muted)', 'font-family':mono, 'font-size':'14px', 'text-align':'center', padding:'40px 0' }}>{translate('col_add_empty')}</div>
          </Show>
          <For each={items()}>{design => {
            const isSel = () => selected().has(design.id)
            const cover = () => design.cover_path ? api.coverUrl(design.cover_path) : null
            const authorName = () => displayAuthor(design, user()?.name)
            return (
              <div onClick={() => toggle(design.id)}
                style={{ display:'flex', 'align-items':'center', gap:'12px', padding:'9px 10px', 'border-radius':'10px', cursor:'pointer', border:`1px solid ${isSel() ? 'var(--accent)' : 'transparent'}`, background:isSel() ? 'rgba(69,123,157,0.12)' : 'transparent', 'margin-bottom':'4px' }}>
                <div style={{ width:'42px', height:'42px', 'flex-shrink':'0', 'border-radius':'8px', overflow:'hidden', background:`linear-gradient(135deg,${gradientFor(design.id)[0]},${gradientFor(design.id)[1]})`, position:'relative' }}>
                  <Show when={cover()}>
                    <img src={cover()!} alt="" style={{ width:'100%', height:'100%', 'object-fit':'cover' }} onError={e => { (e.target as HTMLImageElement).style.display='none' }} />
                  </Show>
                </div>
                <div style={{ flex:'1', 'min-width':'0' }}>
                  <div style={{ 'font-family':sans, 'font-size':'14px', 'font-weight':'600', color:'var(--text)', overflow:'hidden', 'text-overflow':'ellipsis', 'white-space':'nowrap' }}>
                    {displayName(design, lang(), translateDesigns())}
                  </div>
                  <div style={{ display:'flex', 'align-items':'center', gap:'7px', 'margin-top':'2px' }}>
                    <Show when={design.source_platform}>
                      <span style={{ 'font-family':mono, 'font-size':'10px', 'font-weight':'700', color:'#fff', background:(PLATFORM_COLORS[design.source_platform!] ?? 'rgba(0,0,0,0.45)'), 'border-radius':'5px', padding:'1px 6px' }}>
                        {platformLabel(design.source_platform!, translate)}
                      </span>
                    </Show>
                    <Show when={design.is_shared}>
                      <span style={{ 'font-family':mono, 'font-size':'10px', 'font-weight':'700', color:'#fff', background:'rgba(69,123,157,0.85)', 'border-radius':'5px', padding:'1px 6px' }}>shared</span>
                    </Show>
                    <Show when={authorName()}>
                      <span style={{ 'font-family':mono, 'font-size':'11px', color:'var(--muted)', overflow:'hidden', 'text-overflow':'ellipsis', 'white-space':'nowrap' }}>{authorName()}</span>
                    </Show>
                  </div>
                </div>
                <div style={{ width:'22px', height:'22px', 'flex-shrink':'0', 'border-radius':'6px', border:`2px solid ${isSel() ? 'var(--accent)' : 'var(--border2)'}`, background:isSel() ? 'var(--accent)' : 'transparent', display:'flex', 'align-items':'center', 'justify-content':'center', color:'#fff', 'font-size':'13px' }}>
                  <Show when={isSel()}>✓</Show>
                </div>
              </div>
            )
          }}</For>
          <Show when={hasMore() && !loading()}>
            <button onClick={() => load(pageLoaded + 1, search().trim(), true)}
              style={{ width:'100%', background:'var(--surface)', border:'1px solid var(--border)', 'border-radius':'10px', padding:'9px 0', 'font-family':sans, 'font-size':'13px', color:'var(--text2)', cursor:'pointer', margin:'8px 0 14px' }}>
              {translate('col_add_load_more')}
            </button>
          </Show>
          <Show when={loading()}>
            <div style={{ color:'var(--muted)', 'font-family':mono, 'font-size':'13px', 'text-align':'center', padding:'14px 0' }}>{translate('label_loading')}</div>
          </Show>
        </div>
        {/* Footer */}
        <div style={{ padding:'14px 22px', 'border-top':'1px solid var(--border)', display:'flex', 'align-items':'center', 'justify-content':'space-between', gap:'12px', 'flex-shrink':'0' }}>
          <span style={{ 'font-family':mono, 'font-size':'12px', color:'var(--muted)' }}>{translate('col_add_total', { n: total() })}</span>
          <div style={{ display:'flex', gap:'9px' }}>
            <button onClick={props.onClose} style={{ background:'transparent', border:'1px solid var(--border)', 'border-radius':'9px', padding:'9px 16px', 'font-family':sans, 'font-size':'13px', color:'var(--text2)', cursor:'pointer' }}>{translate('btn_cancel')}</button>
            <button onClick={confirm} disabled={selected().size === 0 || adding()}
              style={{ background:selected().size === 0 ? 'var(--surface)' : 'var(--accent)', border:'none', 'border-radius':'9px', padding:'9px 18px', 'font-family':sans, 'font-size':'13px', 'font-weight':'600', color:selected().size === 0 ? 'var(--muted)' : '#fff', cursor:selected().size === 0 ? 'default' : 'pointer', opacity:adding() ? '0.7' : '1' }}>
              {adding() ? '…' : translate('col_add_confirm', { n: selected().size })}
            </button>
          </div>
        </div>
      </div>
    </div>
  )
}

/**
 * Collections management page - sidebar with collection list + create form,
 * and a content area showing the selected collection's designs with search
 * and filter controls.
 */
function CollectionsPage(props: {
  collections: Collection[]
  onBack: () => void
  showToast: (m: string, v?: string) => void
  onChanged: () => void
  onOpenDesign: (id: DesignID) => void
  initialCollectionId?: number | null
  onInitialCollectionHandled?: () => void
}) {
  const {translate} = useI18n()
  const [activeCol, setActiveCol] = createSignal<Collection | null>(null)
  const [colDesigns, setColDesigns] = createSignal<Design[]>([])
  const [showAddDesigns, setShowAddDesigns] = createSignal(false)
  const [loadingDesigns, setLoadingDesigns] = createSignal(false)
  const [newColName, setNewColName] = createSignal('')
  const [isCreating, setIsCreating] = createSignal(false)
  const [pendingDeleteCol, setPendingDeleteCol] = createSignal<Collection | null>(null)
  // Renaming the open collection, in place in the heading. Synced collections
  // are renamed the same way as own ones: the sync keeps the local name.
  const [renamingCol, setRenamingCol] = createSignal(false)
  const [renameDraft, setRenameDraft] = createSignal('')
  // Per-collection filters
  const [colSearch, setColSearch] = createSignal('')
  const [colFilters, setColFilters] = createSignal<Filters>({source_platform:'', tag_ids:[], shared_only:false, show_hidden:false})
  const [showColFilter, setShowColFilter] = createSignal(false)
  const [allTags, setAllTags] = createSignal<Tag[]>([])

  const sans = "'DM Sans',sans-serif"
  const mono = "'DM Mono',monospace"
  const inp: any = { width:'100%', background:'var(--input-bg)', border:'1px solid var(--border2)', 'border-radius':'10px', padding:'10px 14px', color:'var(--text)', 'font-family':sans, 'font-size':'14px', outline:'none', 'box-sizing':'border-box', 'margin-bottom':'11px' }

  // Load tags for filter
  createEffect(() => {
    api.getTags().then((r: any) => setAllTags(r.data || [])).catch(() => {})
  })

  const openCollection = async (col: Collection) => {
    setActiveCol(col); setLoadingDesigns(true); setColSearch(''); setColFilters({source_platform:'', tag_ids:[], shared_only:false, show_hidden:false})
    try { const response = await api.getCollectionDesigns(col.id) as any; setColDesigns(response.data || []) }
    catch {} finally { setLoadingDesigns(false) }
  }

  // Auto-open a specific collection when navigated from DesignPage
  createEffect(() => {
    const id = props.initialCollectionId
    if (!id) return
    const col = props.collections.find(c => c.id === id)
    if (col) { openCollection(col); props.onInitialCollectionHandled?.() }
  })

  /**
   * Returns the active collection's designs filtered by search query,
   * platform, tags, and shared-only flag.
   */
  const filteredColDesigns = () => {
    let designs = colDesigns()
    const query = colSearch().toLowerCase().trim()
    const filters = colFilters()
    if (query) designs = designs.filter(design => design.name.toLowerCase().includes(query) || (design.name_de || '').toLowerCase().includes(query) || (design.author || '').toLowerCase().includes(query))
    if (filters.source_platform) designs = designs.filter(design => design.source_platform === filters.source_platform)
    if (filters.tag_ids.length > 0) designs = designs.filter(design => filters.tag_ids.every(tid => design.tags?.some(tag => tag.id === tid)))
    if (filters.shared_only) designs = designs.filter(design => design.is_shared)
    if (!filters.show_hidden) designs = designs.filter(design => !design.is_hidden)
    return designs
  }

  const colHasActiveFilters = () => { const filters = colFilters(); return !!(filters.source_platform || filters.tag_ids.length || filters.shared_only || filters.show_hidden) }

  const createCollection = async () => {
    if (!newColName().trim()) return; setIsCreating(true)
    try { await api.createCollection({ name: newColName().trim() }); setNewColName(''); props.showToast(translate('toast_collection_created')); props.onChanged() }
    catch {} finally { setIsCreating(false) }
  }

  const deleteCollection = async (id: number) => {
    try { await api.deleteCollection(id); props.showToast(translate('toast_collection_deleted')); props.onChanged(); if (activeCol()?.id === id) setActiveCol(null) } catch {}
  }

  const startRename = () => { const col = activeCol(); if (!col) return; setRenameDraft(col.name); setRenamingCol(true) }

  /**
   * Writes the edited name. Closing the editor first makes this safe to call
   * twice: pressing Enter saves and unmounts the input, whose blur would
   * otherwise arrive right behind it and save a second time.
   */
  const commitRename = async () => {
    if (!renamingCol()) return
    setRenamingCol(false)
    const col = activeCol()
    const name = renameDraft().trim()
    if (!col || !name || name === col.name) return
    try {
      await api.updateCollection(col.id, { name })
      // The local copy drives the heading, so it is updated alongside the
      // reload: without it the heading falls back to the old name until the
      // collection list has come back.
      setActiveCol({ ...col, name })
      props.showToast(translate('toast_collection_renamed'))
      props.onChanged()
    } catch {}
  }

  return (
    <div style={{ 'min-height':'100vh', background:'var(--bg)', padding:'36px' }}>
      <div style={{ 'max-width':'1400px', margin:'0 auto' }}>
        <div style={{ display:'flex', 'align-items':'center', gap:'18px', 'margin-bottom':'34px' }}>
          <button onClick={props.onBack}
            style={{ background:'var(--surface)', border:'1px solid var(--border)', 'border-radius':'11px', padding:'9px 18px', color:'var(--text2)', cursor:'pointer', 'font-family':sans, 'font-size':'15px', 'font-weight':'600', display:'flex', 'align-items':'center', gap:'7px' }}>
            <svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><polyline points="15 18 9 12 15 6"/></svg>
            {translate('btn_back')}
          </button>
          <h1 style={{ 'font-family':sans, 'font-size':'26px', 'font-weight':'700', color:'var(--text)' }}>{translate('collection_title')}</h1>
        </div>

        <div style={{ display:'grid', 'grid-template-columns':'300px 1fr', gap:'26px' }}>
          {/* Sidebar */}
          <div>
            <div style={{ background:'var(--bg2)', border:'1px solid var(--border)', 'border-radius':'15px', padding:'18px', 'margin-bottom':'18px' }}>
              <input value={newColName()} onInput={e => setNewColName(e.currentTarget.value)}
                placeholder={translate('collection_name')}
                onKeyDown={(e: KeyboardEvent) => e.key === 'Enter' && createCollection()}
                style={inp} />
              <button onClick={createCollection} disabled={!newColName().trim() || isCreating()}
                style={{ width:'100%', padding:'10px', background:'var(--accent)', border:'none', 'border-radius':'10px', color:'#fff', 'font-family':sans, 'font-size':'14px', 'font-weight':'700', cursor:'pointer', opacity:(!newColName().trim() || isCreating()) ? '0.5' : '1' }}>
                {isCreating() ? '…' : `+ ${translate('collection_new')}`}
              </button>
            </div>

            <div style={{ display:'flex', 'flex-direction':'column', gap:'7px' }}>
              <For each={props.collections}>{col => (
                <div onClick={() => openCollection(col)}
                  style={{ display:'flex', 'align-items':'center', gap:'11px', padding:'13px 15px', 'border-radius':'13px', background:activeCol()?.id === col.id ? 'rgba(69,123,157,0.15)' : 'var(--bg2)', border:`1px solid ${activeCol()?.id === col.id ? 'var(--accent)' : 'var(--border)'}`, cursor:'pointer' }}>
                  <svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="#fff" stroke-width="2">
                    <path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z"/>
                  </svg>
                  <span style={{ flex:'1', 'font-family':sans, 'font-size':'14px', 'font-weight':'600', color:activeCol()?.id === col.id ? 'var(--accent-light)' : 'var(--text)' }}>{col.name}</span>
                  <span style={{ 'font-family':mono, 'font-size':'11px', color:'var(--muted)', background:'var(--bg3)', 'border-radius':'5px', padding:'2px 7px' }}>{col.design_count ?? 0}</span>
                  <button onClick={e => { e.stopPropagation(); setPendingDeleteCol(col) }}
                    style={{ background:'none', border:'none', color:'var(--muted)', cursor:'pointer', 'font-size':'17px', opacity:'0.5' }}>×</button>
                </div>
              )}</For>
              <Show when={props.collections.length === 0}>
                <div style={{ 'font-family':mono, 'font-size':'14px', color:'var(--muted)', 'text-align':'center', padding:'26px' }}>{translate('col_no_collections')}</div>
              </Show>
            </div>
          </div>

          {/* Content */}
          <div>
            <Show when={!activeCol()}>
              <div style={{ display:'flex', 'align-items':'center', 'justify-content':'center', height:'220px', color:'var(--muted)', 'font-family':mono, 'font-size':'15px' }}>{translate('col_select_hint')}</div>
            </Show>
            <Show when={activeCol()}>
              {/* Filter bar + result count on same line */}
              <div style={{ display:'flex', 'align-items':'center', gap:'11px', 'margin-bottom':'22px', 'flex-wrap':'wrap' }}>
                <Show when={renamingCol()} fallback={
                  <div style={{ display:'flex', 'align-items':'center', gap:'9px', 'flex-shrink':'0' }}>
                    <h2 onDblClick={startRename} title={translate('col_rename')}
                      style={{ 'font-family':sans, 'font-size':'20px', 'font-weight':'700', color:'var(--text)', cursor:'text' }}>{activeCol()!.name}</h2>
                    <button onClick={startRename} title={translate('col_rename')} aria-label={translate('col_rename')}
                      style={{ background:'none', border:'none', color:'var(--muted)', cursor:'pointer', padding:'2px', display:'flex', 'align-items':'center' }}>
                      <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                        <path d="M12 20h9"/><path d="M16.5 3.5a2.12 2.12 0 0 1 3 3L7 19l-4 1 1-4z"/>
                      </svg>
                    </button>
                    {/* Says where the collection came from - the name alone no
                        longer does once it has been renamed here. */}
                    <Show when={activeCol()!.source_platform}>
                      <span title={translate('col_synced_from', {platform: platformLabel(activeCol()!.source_platform!, translate)})}
                        style={{ 'font-family':mono, 'font-size':'10px', 'font-weight':'700', color:'#fff', background:(PLATFORM_COLORS[activeCol()!.source_platform!] ?? 'rgba(0,0,0,0.45)'), 'border-radius':'5px', padding:'2px 7px', cursor:'help' }}>
                        {platformLabel(activeCol()!.source_platform!, translate)}
                      </span>
                    </Show>
                  </div>
                }>
                  <input value={renameDraft()} onInput={e => setRenameDraft(e.currentTarget.value)}
                    ref={element => queueMicrotask(() => { element.focus(); element.select() })}
                    onBlur={commitRename}
                    onKeyDown={(e: KeyboardEvent) => {
                      if (e.key === 'Enter') commitRename()
                      if (e.key === 'Escape') setRenamingCol(false)
                    }}
                    style={{ background:'var(--input-bg)', border:'1px solid var(--accent)', 'border-radius':'10px', padding:'6px 12px', color:'var(--text)', 'font-family':sans, 'font-size':'20px', 'font-weight':'700', outline:'none', 'flex-shrink':'0', width:'320px', 'max-width':'100%' }} />
                </Show>
                <div style={{ flex:'1', position:'relative', 'min-width':'160px' }}>
                  <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="var(--muted)" stroke-width="2" style={{ position:'absolute', left:'11px', top:'50%', transform:'translateY(-50%)' }}>
                    <circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/>
                  </svg>
                  <input value={colSearch()} onInput={e => setColSearch(e.currentTarget.value)}
                    placeholder={translate('col_search_placeholder')}
                    style={{ width:'100%', background:'var(--input-bg)', border:'1px solid var(--border2)', 'border-radius':'10px', padding:'8px 12px 8px 34px', color:'var(--text)', 'font-family':sans, 'font-size':'13px', outline:'none', 'box-sizing':'border-box' }} />
                </div>
                <button onClick={() => setShowColFilter(currentValue => !currentValue)}
                  style={{ background: colHasActiveFilters() ? 'rgba(69,123,157,0.18)' : 'var(--input-bg)', border:`1px solid ${colHasActiveFilters() ? 'var(--accent)' : 'var(--border2)'}`, 'border-radius':'10px', padding:'8px 14px', color: colHasActiveFilters() ? 'var(--accent-light)' : 'var(--text)', 'font-family':sans, 'font-size':'13px', cursor:'pointer', display:'flex', 'align-items':'center', gap:'7px', 'flex-shrink':'0' }}>
                  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2"><line x1="4" y1="6" x2="20" y2="6"/><line x1="8" y1="12" x2="16" y2="12"/><line x1="11" y1="18" x2="13" y2="18"/></svg>
                  {translate('filter_title')}
                  <Show when={colHasActiveFilters()}>
                    <span style={{ background:'var(--accent)', color:'#fff', 'border-radius':'50%', width:'16px', height:'16px', 'font-size':'10px', display:'flex', 'align-items':'center', 'justify-content':'center', 'font-weight':'700' }}>
                      {(colFilters().source_platform ? 1 : 0) + colFilters().tag_ids.length + (colFilters().shared_only ? 1 : 0) + (colFilters().show_hidden ? 1 : 0)}
                    </span>
                  </Show>
                </button>
                <button onClick={() => setShowAddDesigns(true)}
                  style={{ background:'var(--accent)', border:'none', 'border-radius':'10px', padding:'8px 14px', color:'#fff', 'font-family':sans, 'font-size':'13px', 'font-weight':'600', cursor:'pointer', display:'flex', 'align-items':'center', gap:'7px', 'flex-shrink':'0' }}>
                  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4"><line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/></svg>
                  {translate('col_add_designs')}
                </button>
              </div>
              <Show when={showColFilter()}>
                <FilterModal filters={colFilters()} setFilters={setColFilters} allTags={allTags()} onClose={() => setShowColFilter(false)} />
              </Show>
              <Show when={showAddDesigns() && activeCol()}>
                <AddDesignsToCollectionModal
                  collectionId={activeCol()!.id}
                  collectionName={activeCol()!.name}
                  onClose={() => setShowAddDesigns(false)}
                  onAdded={() => { openCollection(activeCol()!); props.onChanged() }}
                  showToast={props.showToast} />
              </Show>
              {/* Result count inline with heading */}
              <span style={{ 'font-family':mono, 'font-size':'13px', color:'var(--muted)', 'margin-bottom':'10px', display:'block' }}>
                {translate(filteredColDesigns().length === 1 ? 'col_result' : 'col_results', {n: filteredColDesigns().length})}
              </span>

              <Show when={loadingDesigns()}><div style={{ color:'var(--muted)', 'font-family':mono }}>{translate('label_loading')}</div></Show>
              <Show when={!loadingDesigns() && filteredColDesigns().length === 0}>
                <div style={{ color:'var(--muted)', 'font-family':mono, 'font-size':'15px', 'text-align':'center', padding:'40px 0' }}>{colDesigns().length === 0 ? translate('collection_empty') : translate('col_no_matches')}</div>
              </Show>
              <div style={{ display:'flex', 'flex-wrap':'wrap', gap:'24px' }}>
                <For each={filteredColDesigns()}>{(colDesign, colIndex) =>
                  <DesignCard design={colDesign} index={colIndex()} onOpen={() => props.onOpenDesign(colDesign.id)} />
                }</For>
              </div>
            </Show>
          </div>
        </div>

        <Show when={pendingDeleteCol()}>
          <div style={{ position:'fixed', inset:'0', background:'rgba(0,0,0,0.65)', display:'flex', 'align-items':'center', 'justify-content':'center', 'z-index':'600', 'backdrop-filter':'blur(5px)' }}
            onClick={() => setPendingDeleteCol(null)}>
            <div onClick={e => e.stopPropagation()}
              style={{ background:'var(--bg2)', 'border-radius':'18px', padding:'28px 32px', width:'420px', border:'1px solid var(--border)', 'box-shadow':'0 24px 64px rgba(0,0,0,0.5)' }}>
              <div style={{ 'font-family':sans, 'font-size':'18px', 'font-weight':'700', color:'var(--text)', 'margin-bottom':'10px' }}>{translate('delete_collection_title')}</div>
              <div style={{ 'font-family':sans, 'font-size':'14px', color:'var(--text2)', 'margin-bottom':'24px', 'line-height':'1.6' }}>
                {translate('delete_collection_body', { name: pendingDeleteCol()!.name })}
              </div>
              <div style={{ display:'flex', gap:'11px', 'justify-content':'flex-end' }}>
                <button onClick={() => setPendingDeleteCol(null)}
                  style={{ padding:'9px 20px', background:'var(--surface)', border:'1px solid var(--border)', 'border-radius':'10px', color:'var(--muted)', 'font-size':'14px', cursor:'pointer', 'font-family':sans }}>
                  {translate('btn_cancel')}
                </button>
                <button onClick={() => { const col = pendingDeleteCol(); if (col) deleteCollection(col.id); setPendingDeleteCol(null) }}
                  style={{ padding:'9px 20px', background:'var(--danger-bg)', border:'1px solid var(--danger)', 'border-radius':'10px', color:'var(--danger)', 'font-size':'14px', 'font-weight':'700', cursor:'pointer', 'font-family':sans }}>
                  {translate('btn_confirm_delete')}
                </button>
              </div>
            </div>
          </div>
        </Show>
      </div>
    </div>
  )
}

function UserDropdown(props: { user: any; onLogout: () => void; onAccountSettings: () => void; onServerSettings: () => void }) {
  const { translate } = useI18n()
  const buttonStyle: any = { width:'100%', padding:'12px 17px', background:'var(--surface)', border:'1px solid var(--border)', 'border-radius':'11px', color:'var(--text2)', 'font-family':"'DM Sans',sans-serif", 'font-size':'15px', 'font-weight':'500', cursor:'pointer', display:'flex', 'align-items':'center', 'justify-content':'center', gap:'10px' }
  return (
    <div style={{ position:'absolute', top:'calc(100% + 16px)', right:'0', 'z-index':'1000', background:'var(--bg)', 'border-radius':'20px', border:'1px solid var(--border)', 'box-shadow':'0 24px 70px rgba(0,0,0,0.4)', overflow:'hidden', width:'290px' }}>
      <div style={{ background:'var(--bg3)', padding:'24px 22px 20px', 'border-bottom':'1px solid var(--border)' }}>
        <div style={{ display:'flex', 'flex-direction':'column', 'align-items':'center', gap:'8px' }}>
          <UserAvatar name={props.user.name || props.user.email || '?'} avatarUrl={props.user.avatar_url} size={72} fontSize={26} />
          <div style={{ 'font-family':"'DM Sans',sans-serif", 'font-weight':'700', color:'var(--text)', 'font-size':'17px', 'margin-top':'4px' }}>{props.user.name}</div>
          <Show when={props.user.email}>
            <div style={{ 'font-family':"'DM Mono',monospace", 'font-size':'13px', color:'var(--muted)' }}>{props.user.email}</div>
          </Show>
        </div>
        <div style={{ 'margin-top':'18px', display:'flex', 'flex-direction':'column', gap:'8px' }}>
          <button onClick={props.onAccountSettings} style={buttonStyle}>
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2">
              <path d="M20 21v-2a4 4 0 0 0-4-4H8a4 4 0 0 0-4 4v2"/><circle cx="12" cy="7" r="4"/>
            </svg>
            {translate('account_settings_title')}
          </button>
          <Show when={props.user.admin}>
            <button onClick={props.onServerSettings} style={buttonStyle}>
              <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2">
                <rect x="2" y="3" width="20" height="14" rx="2"/><line x1="8" y1="21" x2="16" y2="21"/><line x1="12" y1="17" x2="12" y2="21"/>
              </svg>
              {translate('admin_settings_title')}
            </button>
          </Show>
        </div>
      </div>
      <div style={{ background:'var(--bg3)', padding:'13px 22px' }}>
        <button onMouseDown={e => { e.preventDefault(); e.stopPropagation(); props.onLogout() }}
          style={{ width:'100%', padding:'12px 17px', background:'var(--danger-bg)', border:'1px solid var(--danger-border)', 'border-radius':'11px', color:'var(--danger)', 'font-family':"'DM Sans',sans-serif", 'font-size':'15px', 'font-weight':'600', cursor:'pointer', display:'flex', 'align-items':'center', 'justify-content':'center', gap:'10px' }}>
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2">
            <path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4"/>
            <polyline points="16 17 21 12 16 7"/><line x1="21" y1="12" x2="9" y2="12"/>
          </svg>
          {translate('btn_sign_out')}
        </button>
      </div>
    </div>
  )
}

function FilterModal(props: {filters: Filters; setFilters: (f: Filters) => void; allTags: Tag[]; onClose: () => void; perPage?: number; onPerPageChange?: (pp: number) => void}) {
  const { translate } = useI18n()
  const [local, setLocal] = createSignal({...props.filters})
  const [localPerPage, setLocalPerPage] = createSignal(props.perPage ?? 50)
  const lbl: any = { display:'block', 'font-family':"'DM Mono',monospace", 'font-size':'12px', color:'var(--muted)', 'text-transform':'uppercase', 'letter-spacing':'0.05em', 'margin-bottom':'8px' }
  const inp: any = { width:'100%', background:'var(--input-bg)', border:'1px solid var(--border2)', 'border-radius':'10px', padding:'10px 14px', color:'var(--text)', 'font-family':"'DM Sans',sans-serif", 'font-size':'14px', outline:'none' }
  // Tag filter as a select2-style combobox: matches are searched on the server
  // rather than by loading every tag up front. Picks are held as chips, and the
  // initial selection is resolved from the allTags list that is loaded anyway.
  const [selectedTags, setSelectedTags] = createSignal<Tag[]>(props.allTags.filter(t => props.filters.tag_ids?.includes(t.id)))
  const [tagQuery, setTagQuery] = createSignal('')
  const [tagResults, setTagResults] = createSignal<Tag[]>([])
  const [tagOpen, setTagOpen] = createSignal(false)
  const notSelected = (t: Tag) => !selectedTags().some(s => s.id === t.id)
  const runTagSearch = async (v: string) => {
    try { const r = await api.searchTags(v.trim()) as { data: Tag[] }; setTagResults(r.data || []) }
    catch { setTagResults([]) }
  }
  let tagSearchTimer: ReturnType<typeof setTimeout>
  const onTagInput = (v: string) => { setTagQuery(v); setTagOpen(true); clearTimeout(tagSearchTimer); tagSearchTimer = setTimeout(() => runTagSearch(v), 200) }
  const addTag = (t: Tag) => { if (notSelected(t)) setSelectedTags(ts => [...ts, t]); setTagQuery(''); setTagResults([]); setTagOpen(false) }
  const removeTag = (id: number) => setSelectedTags(ts => ts.filter(t => t.id !== id))
  const onTagKeyDown = (e: KeyboardEvent) => {
    if (e.key === 'Enter') { e.preventDefault(); const a = tagResults().filter(notSelected); if (a.length) addTag(a[0]) }
    else if (e.key === 'Escape') setTagOpen(false)
  }
  return (
    <div style={{ position:'fixed', inset:'0', background:'rgba(0,0,0,0.5)', display:'flex', 'align-items':'flex-start', 'justify-content':'center', 'z-index':'500', 'padding-top':'96px' }}
      onClick={props.onClose}>
      <div onClick={e => e.stopPropagation()}
        style={{ background:'var(--bg2)', 'border-radius':'20px', padding:'30px', width:'420px', border:'1px solid var(--border)', 'box-shadow':'0 20px 60px rgba(0,0,0,0.4)' }}>
        <div style={{ 'font-family':"'DM Sans',sans-serif", 'font-weight':'700', 'font-size':'18px', color:'var(--text)', 'margin-bottom':'22px' }}>{translate('filter_title')}</div>
        <div style={{ display:'flex', 'flex-direction':'column', gap:'20px' }}>
          <Show when={props.onPerPageChange}>
            <div>
              <label style={lbl}>Einträge pro Seite</label>
              <div style={{ display:'flex', gap:'8px', 'flex-wrap':'wrap' }}>
                <For each={[5, 50, 100, 500, 1000]}>{n => (
                  <button onClick={() => setLocalPerPage(n)}
                    style={{ padding:'7px 16px', 'border-radius':'9px', border:`1px solid ${localPerPage()===n?'var(--accent)':'var(--border)'}`, background:localPerPage()===n?'rgba(69,123,157,0.15)':'var(--surface)', color:localPerPage()===n?'var(--accent-light)':'var(--text2)', 'font-family':"'DM Mono',monospace", 'font-size':'13px', cursor:'pointer' }}>
                    {n}
                  </button>
                )}</For>
              </div>
            </div>
          </Show>
          <div>
            <label style={lbl}>{translate('filter_platform')}</label>
            <select value={local().source_platform} onChange={e => setLocal(l => ({...l, source_platform: e.currentTarget.value}))} style={inp}>
              <option value="">{translate('filter_all_platforms')}</option>
              <For each={Object.entries(PLATFORM_LABELS)}>{([k]) => <option value={k}>{platformLabel(k, translate)}</option>}</For>
            </select>
          </div>
          <div>
            <label style={lbl}>{translate('filter_visibility')}</label>
            <div style={{ display:'flex', gap:'8px', 'flex-wrap':'wrap' }}>
              <button onClick={() => setLocal(l => ({...l, shared_only: !l.shared_only}))}
                style={{ padding:'9px 17px', 'border-radius':'9px', border:`1px solid ${local().shared_only ? 'var(--accent)' : 'var(--border)'}`, background:local().shared_only ? 'rgba(69,123,157,0.15)' : 'var(--surface)', color:local().shared_only ? 'var(--accent-light)' : 'var(--muted)', 'font-size':'14px', cursor:'pointer', 'font-family':"'DM Sans',sans-serif", 'font-weight':'500' }}>
                {translate('filter_shared_only')}
              </button>
              <button onClick={() => setLocal(l => ({...l, show_hidden: !l.show_hidden}))}
                style={{ padding:'9px 17px', 'border-radius':'9px', border:`1px solid ${local().show_hidden ? 'var(--accent)' : 'var(--border)'}`, background:local().show_hidden ? 'rgba(69,123,157,0.15)' : 'var(--surface)', color:local().show_hidden ? 'var(--accent-light)' : 'var(--muted)', 'font-size':'14px', cursor:'pointer', 'font-family':"'DM Sans',sans-serif", 'font-weight':'500' }}>
                {translate('filter_show_hidden')}
              </button>
            </div>
          </div>
          <div>
            <label style={lbl}>{translate('filter_tags')}</label>
            {/* Ausgewählte Tags als entfernbare Chips */}
            <Show when={selectedTags().length > 0}>
              <div style={{ display:'flex', 'flex-wrap':'wrap', gap:'7px', 'margin-bottom':'8px' }}>
                <For each={selectedTags()}>{tag => (
                  <span style={{ display:'inline-flex', 'align-items':'center', gap:'6px', 'font-size':'13px', padding:'4px 6px 4px 11px', 'border-radius':'8px', background:tag.color+'33', color:tag.color, 'font-family':"'DM Sans',sans-serif", 'font-weight':'500' }}>
                    {tag.name}
                    <button onClick={() => removeTag(tag.id)} style={{ background:'none', border:'none', color:tag.color, cursor:'pointer', 'font-size':'15px', 'line-height':'1', padding:'0' }}>×</button>
                  </span>
                )}</For>
              </div>
            </Show>
            {/* Sucheingabe + Dropdown (serverseitige Suche) */}
            <div style={{ position:'relative' }}>
              <input value={tagQuery()} placeholder={translate('filter_tag_search_placeholder')}
                onInput={e => onTagInput(e.currentTarget.value)}
                onFocus={() => { setTagOpen(true); runTagSearch(tagQuery()) }}
                onClick={() => { if (!tagOpen()) { setTagOpen(true); runTagSearch(tagQuery()) } }}
                onBlur={() => setTimeout(() => setTagOpen(false), 150)}
                onKeyDown={onTagKeyDown}
                style={{ ...inp, 'font-size':'13px', padding:'8px 12px' }} />
              <Show when={tagOpen() && tagResults().filter(notSelected).length > 0}>
                <div style={{ position:'absolute', top:'calc(100% + 4px)', left:'0', right:'0', 'z-index':'40', background:'var(--bg2)', border:'1px solid var(--border)', 'border-radius':'10px', 'box-shadow':'0 12px 30px rgba(0,0,0,0.35)', 'max-height':'200px', 'overflow-y':'auto', padding:'5px' }}>
                  <For each={tagResults().filter(notSelected)}>{tag => (
                    <button onMouseDown={e => e.preventDefault()} onClick={() => addTag(tag)}
                      style={{ display:'flex', 'align-items':'center', gap:'8px', width:'100%', 'text-align':'left', padding:'7px 9px', background:'none', border:'none', 'border-radius':'7px', cursor:'pointer', 'font-family':"'DM Sans',sans-serif", 'font-size':'13px', color:'var(--text2)' }}>
                      <span style={{ width:'10px', height:'10px', 'border-radius':'3px', background:tag.color, 'flex-shrink':'0' }} />
                      {tag.name}
                    </button>
                  )}</For>
                </div>
              </Show>
            </div>
          </div>
        </div>
        <div style={{ display:'flex', gap:'11px', 'margin-top':'24px' }}>
          <button onClick={() => { props.setFilters({source_platform:'',tag_ids:[],shared_only:false,show_hidden:false}); setLocal({source_platform:'',tag_ids:[],shared_only:false,show_hidden:false}); setSelectedTags([]); props.onPerPageChange?.(50); setLocalPerPage(50); props.onClose() }}
            style={{ flex:'1', padding:'11px', background:'var(--surface)', border:'1px solid var(--border)', 'border-radius':'10px', color:'var(--muted)', cursor:'pointer', 'font-family':"'DM Sans',sans-serif", 'font-size':'14px' }}>
            {translate('filter_clear')}
          </button>
          <button onClick={() => { props.setFilters({ ...local(), tag_ids: selectedTags().map(t => t.id) }); props.onPerPageChange?.(localPerPage()); props.onClose() }}
            style={{ flex:'2', padding:'11px', background:'var(--accent)', border:'none', 'border-radius':'10px', color:'#fff', cursor:'pointer', 'font-family':"'DM Sans',sans-serif", 'font-size':'14px', 'font-weight':'600' }}>
            {translate('filter_apply')}
          </button>
        </div>
      </div>
    </div>
  )
}

function ConfirmDiscardModal() {
  const { translate } = useI18n()
  return (
    <Show when={!!pendingActionSignal()}>
      <div style={{ position:'fixed', inset:'0', background:'rgba(0,0,0,0.55)', display:'flex', 'align-items':'center', 'justify-content':'center', 'z-index':'9999' }}
        onClick={cancelDiscard}>
        <div onClick={(e: MouseEvent) => e.stopPropagation()}
          style={{ background:'var(--bg2)', 'border-radius':'20px', padding:'28px', width:'380px', 'max-width':'calc(100vw - 32px)', border:'1px solid var(--border)', 'box-shadow':'0 20px 60px rgba(0,0,0,0.45)' }}>
          <div style={{ 'font-family':"'DM Sans',sans-serif", 'font-weight':'700', 'font-size':'18px', color:'var(--text)', 'margin-bottom':'12px' }}>
            {translate('confirm_discard_title')}
          </div>
          <p style={{ 'font-family':"'DM Sans',sans-serif", 'font-size':'14px', color:'var(--text2)', margin:'0 0 24px', 'line-height':'1.55' }}>
            {pendingMessageSignal()}
          </p>
          <div style={{ display:'flex', gap:'10px', 'justify-content':'flex-end' }}>
            <button onClick={cancelDiscard}
              style={{ padding:'9px 20px', 'border-radius':'10px', border:'1px solid var(--border)', background:'var(--surface)', color:'var(--text2)', 'font-family':"'DM Sans',sans-serif", 'font-size':'14px', cursor:'pointer' }}>
              {translate('btn_cancel')}
            </button>
            <button onClick={confirmDiscard}
              style={{ padding:'9px 20px', 'border-radius':'10px', border:'1px solid var(--danger-border)', background:'var(--danger-bg)', color:'var(--danger)', 'font-family':"'DM Sans',sans-serif", 'font-size':'14px', 'font-weight':'600', cursor:'pointer' }}>
              {translate('btn_discard')}
            </button>
          </div>
        </div>
      </div>
    </Show>
  )
}

/**
 * Styled confirmation dialog. Replaces the native, blocking `confirm()` that
 * the card-delete action used to call - the app now has exactly one look for
 * "are you sure?", and the text goes through i18n like everything else.
 */
function ConfirmModal(props: { title: string; body: string; hint?: string; confirmLabel: string; danger?: boolean; onClose: () => void; onConfirm: () => void }) {
  const { translate } = useI18n()
  const cancelLabel = () => translate('btn_cancel')
  return (
    <div style={{ position:'fixed', inset:'0', background:'rgba(0,0,0,0.55)', display:'flex', 'align-items':'center', 'justify-content':'center', 'z-index':'500' }}
      onClick={props.onClose}>
      <div onClick={e => e.stopPropagation()}
        style={{ background:'var(--bg2)', 'border-radius':'20px', padding:'30px', width:'420px', 'max-width':'calc(100vw - 32px)', border:'1px solid var(--border)', 'box-shadow':'0 20px 60px rgba(0,0,0,0.45)' }}>
        <div style={{ 'font-family':"'DM Sans',sans-serif", 'font-weight':'700', 'font-size':'18px', color:'var(--text)', 'margin-bottom':'14px' }}>
          {props.title}
        </div>
        <p style={{ 'font-family':"'DM Sans',sans-serif", 'font-size':'14px', color:'var(--text2)', margin:'0 0 10px 0', 'line-height':'1.55' }}>
          {props.body}
        </p>
        <Show when={props.hint}>
          <p style={{ 'font-family':"'DM Sans',sans-serif", 'font-size':'13px', color:'var(--muted)', margin:'0 0 16px 0', 'line-height':'1.5' }}>
            {props.hint}
          </p>
        </Show>
        <div style={{ height:'10px' }} />
        <div style={{ display:'flex', gap:'10px', 'justify-content':'flex-end' }}>
          <button onClick={props.onClose}
            style={{ padding:'9px 20px', 'border-radius':'10px', border:'1px solid var(--border)', background:'var(--surface)', color:'var(--text2)', 'font-family':"'DM Sans',sans-serif", 'font-size':'14px', cursor:'pointer' }}>
            {cancelLabel()}
          </button>
          <button onClick={props.onConfirm}
            style={{ padding:'9px 20px', 'border-radius':'10px', border:'none', background: props.danger ? 'var(--danger)' : 'var(--accent)', color:'#fff', 'font-family':"'DM Sans',sans-serif", 'font-size':'14px', 'font-weight':'600', cursor:'pointer' }}>
            {props.confirmLabel}
          </button>
        </div>
      </div>
    </div>
  )
}

function QueuePanel(props: {queue: QueueItem[]; onClose: () => void; onCancel: (id: number) => void; onRetry: (id: number) => void; onRetryAll: () => void; onDismiss: (id: number) => void; onOpenSettings?: () => void}) {
  const { translate } = useI18n()
  const translateError = makeTranslateError(translate)
  const failedCount = () => props.queue.filter(q => q.status === 'failed').length
  return (
    <Show when={props.queue.length > 0}>
      <div style={{ position:'fixed', bottom:'26px', right:'26px', 'z-index':'200', background:'var(--bg2)', border:'1px solid var(--border)', 'border-radius':'18px', padding:'18px', width:'380px', 'box-shadow':'0 8px 30px rgba(0,0,0,0.3)', 'max-height':'70vh', overflow:'auto' }}>
        <div style={{ display:'flex', 'align-items':'center', 'justify-content':'space-between', gap:'10px', 'margin-bottom':'13px' }}>
          <span style={{ 'font-family':"'DM Sans',sans-serif", 'font-weight':'700', 'font-size':'15px', color:'var(--text)' }}>Download Queue</span>
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
                  <div style={{ 'font-family':"'DM Sans',sans-serif", 'font-size':'13px', color:'var(--text2)', 'font-weight':'500', overflow:'hidden', 'text-overflow':'ellipsis', 'white-space':'nowrap' }}>
                    <Show when={item.source_url} fallback={<span>-</span>}>
                      <a href={item.source_url} target="_blank" rel="noopener noreferrer"
                        style={{ color:'inherit', 'text-decoration':'none' }}
                        onMouseOver={e => (e.currentTarget.style.textDecoration='underline')}
                        onMouseOut={e => (e.currentTarget.style.textDecoration='none')}>
                        {item.source_url?.replace(/^https?:\/\/(www\.)?/, '').slice(0, 50) || '-'}
                      </a>
                    </Show>
                  </div>
                  <div style={{ 'font-family':"'DM Mono',monospace", 'font-size':'11px', color:item.status==='failed'?'var(--danger)':item.status==='downloading'?'var(--accent-light)':'var(--muted)', 'margin-top':'2px' }}>
                    {item.status === 'failed'
                      ? translate('download_status_failed')
                      : item.status === 'downloading'
                        ? (() => {
                            const step = item.current_step
                            if (!step) return translate('download_status_downloading')
                            const label = translate(`download_step_${step}` as any)
                            return item.step_current != null && item.step_total != null && (item.step_total as number) > 0
                              ? `${label} (${item.step_current}/${item.step_total})`
                              : label
                          })()
                        : translate('download_status_queued')}
                  </div>
                </div>
                <Show when={item.status === 'pending' || item.status === 'downloading'}>
                  <div style={{ display:'flex', 'align-items':'center', gap:'8px', 'flex-shrink':'0', 'margin-left':'11px' }}>
                    <div style={{ width:'17px', height:'17px', border:'2px solid var(--border)', 'border-top':'2px solid var(--accent)', 'border-radius':'50%', 'animation':'spin 0.8s linear infinite' }} />
                    <button onClick={() => props.onCancel(item.id)}
                      title={translate('download_cancel')}
                      style={{ width:'22px', height:'22px', background:'var(--bg2)', border:'1px solid var(--border)', 'border-radius':'50%', color:'var(--muted)', 'font-size':'14px', cursor:'pointer', display:'flex', 'align-items':'center', 'justify-content':'center', 'line-height':'1', 'flex-shrink':'0' }}>
                      ×
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
  loadDesigns(); loadTags(); loadCollections(); downloads.refresh(); notifs.load(true); loadPlatformAccountState()

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
  const deleteDesign = (design: Design) => setPendingDelete(design)

  /** Performs the deletion once the ConfirmModal has been acknowledged. */
  const confirmDeleteDesign = async (design: Design) => {
    setPendingDelete(null)
    try {
      await api.deleteDesign(design.id)
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

  const navBar = (hideSearch = false) => (
    <nav class="stlv-nav" style={{ position:'sticky', top:'0', 'z-index':'100', background:'var(--nav-bg)', 'backdrop-filter':'blur(16px)', 'border-bottom':'1px solid var(--border)', padding:'0 40px', height:NAV_H, display:'flex', 'align-items':'center' }}>
      {/* Left - Logo */}
      <div class="stlv-nav-logo" style={{ flex:'1', display:'flex', 'align-items':'center' }}>
        <div onClick={() => guardCloseGlobal(translate('confirm_discard_changes'), goToGrid)}
          style={{ 'font-family':"'DM Sans',sans-serif", 'font-weight':'700', 'font-size':'26px', color:'var(--text)', 'letter-spacing':'-0.02em', cursor:'pointer', 'user-select':'none' }}>
          Mesh<span style={{ color:'var(--accent)' }}>Depot</span>
        </div>
      </div>

      {/* Center - Search + Filter (hidden on mobile, drops to second row via flex-wrap) */}
      <Show when={!hideSearch}>
        <div class="stlv-nav-center" style={{ display:'flex', 'align-items':'center', gap:'13px' }}>
          <div style={{ position:'relative', flex:'1' }}>
            <svg style={{ position:'absolute', left:'13px', top:'50%', 'transform':'translateY(-50%)', 'pointer-events':'none' }} width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="var(--muted)" stroke-width="2.5">
              <circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/>
            </svg>
            <input class="stlv-search" placeholder={translate('nav_search_placeholder')} value={search()} onInput={e => setSearch(e.currentTarget.value)}
              style={{ background:'var(--surface)', border:'1px solid var(--border)', 'border-radius':'12px', padding:`10px ${search()?'38px':'18px'} 10px 38px`, color:'var(--text2)', 'font-family':"'DM Sans',sans-serif", 'font-size':'15px', outline:'none', width:'280px' }} />
            <Show when={search()}>
              <button onClick={() => setSearch('')}
                style={{ position:'absolute', right:'11px', top:'50%', 'transform':'translateY(-50%)', background:'none', border:'none', cursor:'pointer', display:'flex', 'align-items':'center', padding:'2px', color:'var(--muted)' }}>
                <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5">
                  <line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/>
                </svg>
              </button>
            </Show>
          </div>
          <NavBtn title="Filter" onClick={() => setShowFilters(f => !f)} active={!!hasActive()}>
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2">
              <line x1="4" y1="6" x2="20" y2="6"/><line x1="8" y1="12" x2="16" y2="12"/><line x1="11" y1="18" x2="13" y2="18"/>
            </svg>
          </NavBtn>
        </div>
      </Show>

      {/* Right - desktop buttons + always-visible Add/Bell/Avatar */}
      <div class="stlv-nav-right" style={{ flex:'1', display:'flex', 'align-items':'center', 'justify-content':'flex-end', gap:'13px' }}>
        <Show when={!hideSearch}>
          {/* Secondary actions hidden on mobile */}
          <div class="stlv-nav-extra" style={{ display:'contents' }}>
            <Show when={visibleDownloadJobs().some(j => j.status === 'failed')}>
              <NavBtn title={translate('download_retry_all_failed')} onClick={() => downloads.retryAll(visibleDownloadJobs().filter(job => job.status === 'failed'))} active>
                <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="var(--danger)" stroke-width="2.2">
                  <path d="M1 4v6h6"/><path d="M23 20v-6h-6"/>
                  <path d="M20.49 9A9 9 0 0 0 5.64 5.64L1 10M23 14l-4.64 4.36A9 9 0 0 1 3.51 15"/>
                </svg>
              </NavBtn>
            </Show>
            <NavBtn title={translate('collection_title')} onClick={() => openCollections()}>
              <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z"/>
              </svg>
            </NavBtn>
            <Show when={hasPlatformAccount()}>
              <NavBtn title={translate('nav_sync_all')} onClick={() => setShowSyncAllConfirm(true)}>
                <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2">
                  <path d="M1 4v6h6"/><path d="M23 20v-6h-6"/>
                  <path d="M20.49 9A9 9 0 0 0 5.64 5.64L1 10M23 14l-4.64 4.36A9 9 0 0 1 3.51 15"/>
                </svg>
              </NavBtn>
            </Show>
            <NavBtn
              title={viewMode() === 'grid' ? translate('nav_view_list') : translate('nav_view_grid')}
              onClick={() => { const m = viewMode() === 'grid' ? 'list' : 'grid'; setViewMode(m); localStorage.setItem('stlv_view_mode', m) }}>
              <Show when={viewMode() === 'grid'} fallback={
                <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2">
                  <rect x="3" y="3" width="7" height="7"/><rect x="14" y="3" width="7" height="7"/>
                  <rect x="3" y="14" width="7" height="7"/><rect x="14" y="14" width="7" height="7"/>
                </svg>
              }>
                <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2">
                  <line x1="3" y1="6" x2="21" y2="6"/><line x1="3" y1="12" x2="21" y2="12"/><line x1="3" y1="18" x2="21" y2="18"/>
                </svg>
              </Show>
            </NavBtn>
          </div>
          <NavBtn title={translate('nav_add_design')} onClick={() => setShowAdd(true)}>
            <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5">
              <line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/>
            </svg>
          </NavBtn>
        </Show>

        {/* Bell / Notifications */}
        <NotificationBell store={notifs} translate={translate} />

        {/* User avatar */}
        <div ref={userDropdownRef} style={{ position:'relative' }}>
          <button onClick={() => setShowUser(currentValue => !currentValue)}
            style={{ background:'transparent', border:'none', 'border-radius':'50%', width:'42px', height:'42px', display:'flex', 'align-items':'center', 'justify-content':'center', cursor:'pointer', padding:'0', overflow:'hidden' }}>
            <UserAvatar name={user()?.name || user()?.email || 'U'} avatarUrl={user()?.avatar_url} size={42} fontSize={16} />
          </button>
          <Show when={showUser()}>
            <UserDropdown user={user()!}
              onLogout={() => guardCloseGlobal(translate('confirm_discard_changes'), logout)}
              onAccountSettings={() => { setShowUser(false); setShowAccountSettings(true) }}
              onServerSettings={() => { setShowUser(false); setShowServerSettings(true) }} />
          </Show>
        </div>
      </div>
    </nav>
  )

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
            onClose={() => setPendingDelete(null)}
            onConfirm={() => confirmDeleteDesign(design())} />
        )}
      </Show>
    </>
  )

  // ── Single reactive return - all views rendered with Show for SolidJS reactivity ──
  return (
    <div style={{ 'min-height': '100vh', background: 'var(--bg)' }}>
      <style>{globalStyles}</style>
      <Toast message={toast()} variant={toastVariant()} />

      {/* Design detail page */}
      <Show when={view() === 'design' && !!openDesignId()}>
        {navBar(true)}
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
            } catch { showToast(translate('toast_sync_failed'), 'error') }
          }}
          syncTick={sync.tick()}
          syncStatus={sync.statusMap()[openDesignId()!]}
          syncProgress={sync.progressMap()[openDesignId()!]}
          syncStep={sync.stepMap()[openDesignId()!]} />
        {commonModals()}
      </Show>

      {/* Collections view */}
      <Show when={view() === 'collections'}>
        {navBar(true)}
        <CollectionsPage collections={allCollections()} onBack={() => history.back()} showToast={showToast}
          onChanged={loadCollections} onOpenDesign={openDesignById}
          initialCollectionId={openCollectionId()} onInitialCollectionHandled={() => setOpenCollectionId(null)} />
        {commonModals()}
      </Show>

      {/* Main grid */}
      <Show when={view() === 'grid'}>
        {navBar()}
        <main class="stlv-main" style={{ padding: '54px 40px' }}>
          <Show when={loading()} fallback={<>
            {/* Sort bar */}
            <Show when={designs().length > 0}>
              <div style={{ display:'flex', 'align-items':'center', 'justify-content': viewMode()==='grid' ? 'center' : 'flex-start', gap:'6px', 'max-width': viewMode()==='list' ? '1200px' : '1600px', margin:'0 auto 18px', 'flex-wrap':'wrap' }}>
                <span style={{ 'font-family':"'DM Mono',monospace", 'font-size':'11px', color:'var(--muted)', 'white-space':'nowrap', 'padding-right':'4px' }}>{translate('sort_by')}</span>
                {(['name','platform','updated_at'] as const).map(field => (
                  <button onClick={() => toggleSort(field)}
                    style={{ padding:'4px 11px', 'border-radius':'7px', 'font-family':"'DM Mono',monospace", 'font-size':'11px', cursor:'pointer',
                      border: sortField()===field ? '1px solid var(--accent)' : '1px solid var(--border)',
                      background: sortField()===field ? 'rgba(69,123,157,0.15)' : 'var(--surface)',
                      color: sortField()===field ? 'var(--accent-light)' : 'var(--text2)',
                      'font-weight': sortField()===field ? '700' : '400' }}>
                    {translate(`sort_${field}` as any)}{sortField()===field ? (sortDir()==='asc' ? ' ↑' : ' ↓') : ''}
                  </button>
                ))}
              </div>
            </Show>
            <Show when={viewMode() === 'grid'} fallback={
              <div style={{ display:'flex', 'flex-direction':'column', gap:'6px', 'max-width':'1200px', margin:'0 auto' }}>
                <For each={page() === 1 ? visibleDownloadJobs() : []}>{(job) => <DownloadPlaceholderRow job={job} onCancel={() => downloads.cancel(job.id)} onRetry={() => downloads.retry(job.id)} onDismiss={() => downloads.dismiss(job.id)} onOpenSettings={() => { setAccountSettingsTab('platforms'); setShowAccountSettings(true) }} />}</For>
                <For each={sortedDesigns()}>{(design, designIndex) =>
                  <DesignRow design={design} index={designIndex()} onOpen={openDesign} onSync={syncOne} syncStatus={sync.statusMap()[design.id]} syncProgress={sync.progressMap()[design.id]} syncStep={sync.stepMap()[design.id]} />
                }</For>
                <Show when={designs().length === 0 && visibleDownloadJobs().length === 0}>
                  <div style={{ color: 'var(--muted3)', 'font-family': "'DM Mono',monospace", 'font-size': '18px', 'margin-top': '110px', 'text-align': 'center', width: '100%' }}>
                    <div style={{ 'font-size': '64px', 'margin-bottom': '22px', opacity: '0.4' }}>🖨️</div>
                    <div>{search() ? translate('nav_no_results', {query: search()}) : translate('nav_no_designs')}</div>
                  </div>
                </Show>
              </div>
            }>
            <div class="stlv-grid" style={{ display: 'flex', 'flex-wrap': 'wrap', gap: '32px', 'justify-content': 'center', 'max-width': '1600px', margin: '0 auto' }}>
              <For each={page() === 1 ? visibleDownloadJobs() : []}>{(job) => <DownloadPlaceholderCard job={job} onCancel={() => downloads.cancel(job.id)} onRetry={() => downloads.retry(job.id)} onDismiss={() => downloads.dismiss(job.id)} onOpenSettings={() => { setAccountSettingsTab('platforms'); setShowAccountSettings(true) }} />}</For>
              <For each={sortedDesigns()}>{(design, designIndex) =>
                <DesignCard design={design} index={designIndex()} onOpen={openDesign} onSync={syncOne} syncStatus={sync.statusMap()[design.id]} syncProgress={sync.progressMap()[design.id]} syncStep={sync.stepMap()[design.id]} />
              }</For>
              <Show when={designs().length === 0 && visibleDownloadJobs().length === 0}>
                <div style={{ color: 'var(--muted3)', 'font-family': "'DM Mono',monospace", 'font-size': '18px', 'margin-top': '110px', 'text-align': 'center', width: '100%' }}>
                  <div style={{ 'font-size': '64px', 'margin-bottom': '22px', opacity: '0.4' }}>🖨️</div>
                  <div>{search() ? translate('nav_no_results', {query: search()}) : translate('nav_no_designs')}</div>
                </div>
              </Show>
            </div>
            </Show>
            <Show when={totalDesigns() > perPage()}>
              {(() => {
                const totalPages = () => Math.ceil(totalDesigns() / perPage())
                const goTo = (p: number) => { setPage(p); loadDesigns(search(), filters(), p) }
                const pageButton = (p: number) => (
                  <button onClick={() => goTo(p)}
                    style={{ 'min-width':'36px', height:'36px', padding:'0 10px', 'border-radius':'9px', border:`1px solid ${page()===p?'var(--accent)':'var(--border)'}`, background:page()===p?'rgba(69,123,157,0.2)':'var(--surface)', color:page()===p?'var(--accent-light)':'var(--text2)', cursor:'pointer', 'font-family':"'DM Mono',monospace", 'font-size':'13px', 'font-weight':page()===p?'700':'400' }}>
                    {p}
                  </button>
                )
                return (
                  <div style={{ display:'flex', 'align-items':'center', 'justify-content':'center', gap:'8px', 'margin-top':'48px', 'flex-wrap':'wrap' }}>
                    <button onClick={() => goTo(Math.max(1, page()-1))} disabled={page()===1}
                      style={{ height:'36px', padding:'0 14px', 'border-radius':'9px', border:'1px solid var(--border)', background:'var(--surface)', color:page()===1?'var(--muted)':'var(--text2)', cursor:page()===1?'default':'pointer', 'font-family':"'DM Mono',monospace", 'font-size':'13px' }}>
                      ←
                    </button>
                    {(() => {
                      const total = totalPages()
                      const cur = page()
                      const pages: (number|'…')[] = []
                      const add = (p: number) => { if (!pages.includes(p)) pages.push(p) }
                      add(1)
                      for (let i = Math.max(2, cur-1); i <= Math.min(total-1, cur+1); i++) add(i)
                      add(total)
                      const withDots: (number|'…')[] = []
                      let prev = 0
                      for (const pageNumber of pages) {
                        if (typeof pageNumber === 'number') {
                          if (prev && pageNumber - prev > 1) withDots.push('…')
                          withDots.push(pageNumber)
                          prev = pageNumber
                        }
                      }
                      return <For each={withDots}>{item => item === '…'
                        ? <span style={{ color:'var(--muted)', 'font-family':"'DM Mono',monospace", 'font-size':'13px', padding:'0 4px' }}>…</span>
                        : pageButton(item as number)
                      }</For>
                    })()}
                    <button onClick={() => goTo(Math.min(totalPages(), page()+1))} disabled={page()===totalPages()}
                      style={{ height:'36px', padding:'0 14px', 'border-radius':'9px', border:'1px solid var(--border)', background:'var(--surface)', color:page()===totalPages()?'var(--muted)':'var(--text2)', cursor:page()===totalPages()?'default':'pointer', 'font-family':"'DM Mono',monospace", 'font-size':'13px' }}>
                      →
                    </button>
                    <span style={{ 'font-family':"'DM Mono',monospace", 'font-size':'12px', color:'var(--muted)', 'margin-left':'8px' }}>
                      {(page()-1)*perPage()+1}–{Math.min(page()*perPage(), totalDesigns())} / {totalDesigns()}
                    </span>
                  </div>
                )
              })()}
            </Show>
          </>}>
            <div style={{ display: 'flex', 'justify-content': 'center', 'margin-top': '110px' }}>
              <div style={{ width: '40px', height: '40px', border: '3px solid var(--border)', 'border-top': '3px solid var(--accent)', 'border-radius': '50%', animation: 'spin 0.8s linear infinite' }} />
            </div>
          </Show>
        </main>

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
