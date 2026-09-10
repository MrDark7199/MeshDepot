import { createSignal, createEffect, onCleanup, Show, For, onMount, JSX } from 'solid-js'
import { api } from '../services/api'
import { useI18n } from '../i18n/index'
import { useAuth } from '../services/AuthContext'
import type { DesignID, Tag } from '../types'
import { errorKey } from '../utils/errorMessage'
import { PLATFORM_COLORS, PLATFORM_LABELS, PLATFORMS_REQUIRING_CREDENTIALS, detectPlatformFromUrl } from '../constants/platforms'

const labelStyle: JSX.CSSProperties = { display: 'block', 'font-family': "'DM Mono',monospace", 'font-size': '12px', color: 'var(--muted)', 'margin-bottom': '6px', 'text-transform': 'uppercase', 'letter-spacing': '0.05em' }
const inputStyle: JSX.CSSProperties = { width: '100%', background: 'var(--input-bg)', border: '1px solid var(--border2)', 'border-radius': '10px', padding: '10px 14px', color: 'var(--text)', 'font-family': "'DM Sans',sans-serif", 'font-size': '14px', outline: 'none', 'box-sizing': 'border-box' }
const sansFont: JSX.CSSProperties = { 'font-family': "'DM Sans',sans-serif" }
const monoFont: JSX.CSSProperties = { 'font-family': "'DM Mono',monospace" }

/** Inline error box. */
function ErrorMessage(props: { message: string; onOpenAccountSettings?: (tab?: string) => void }) {
  const { translate } = useI18n()
  const isAuthError = () => props.message.startsWith('__AUTH__:')
  const displayMessage = () => isAuthError() ? props.message.replace('__AUTH__:', '') : props.message
  return (
    <Show when={props.message}>
      <Show when={isAuthError()} fallback={
        <div style={{ background: 'var(--danger-bg)', border: '1px solid var(--danger-border)', 'border-radius': '9px', padding: '9px 13px', color: 'var(--danger)', ...sansFont, 'font-size': '13px', 'margin-bottom': '13px' }}>
          {displayMessage()}
        </div>
      }>
        <div style={{ background: 'rgba(250,104,49,0.1)', border: '1px solid rgba(250,104,49,0.4)', 'border-radius': '10px', padding: '13px 16px', 'margin-bottom': '13px', 'line-height': '1.6' }}>
          <div style={{ ...sansFont, 'font-size': '13px', 'font-weight': '700', color: '#fa6831', 'margin-bottom': '6px' }}>🔒 {translate('credentials_required_title')}</div>
          <div style={{ ...sansFont, 'font-size': '13px', color: '#fa6831' }}>{displayMessage()}</div>
          <Show when={props.onOpenAccountSettings}>
            <button onClick={() => props.onOpenAccountSettings?.('platforms')}
              style={{ 'margin-top': '10px', padding: '7px 16px', background: '#fa6831', border: 'none', 'border-radius': '8px', color: '#fff', 'font-size': '12px', 'font-weight': '700', cursor: 'pointer', ...sansFont }}>
              {translate('credentials_open_settings')}
            </button>
          </Show>
        </div>
      </Show>
    </Show>
  )
}

interface AddDesignModalProps {
  onClose:               () => void
  onSaved:               (designId?: DesignID) => void
  showToast:             (message: string, variant?: string) => void
  onOpenAccountSettings?: (tab?: string) => void
  onQueued?:             (url: string, platform: string) => void
}

/**
 * Modal for adding a new design - either via platform URL import or manual creation.
 *
 * URL import behavior:
 *   - Modal stays open while the queue item is pending/downloading
 *   - Progress spinner shown inside the modal
 *   - On success: modal closes + success toast
 *   - On failure: error shown inside modal, no toast
 */

export function AddDesignModal(props: AddDesignModalProps) {
  const { translate } = useI18n()
  const { user } = useAuth()

  const [sourceUrl, setSourceUrl] = createSignal('')
  const [designName, setDesignName] = createSignal('')
  const [activeMode, setActiveMode] = createSignal<'url' | 'manual'>('url')
  const [isSubmitting, setIsSubmitting] = createSignal(false)
  const [errorMessage, setErrorMessage] = createSignal('')
  const [duplicateName, setDuplicateName] = createSignal<string | null>(null)
  const [credentialsWarning, setCredentialsWarning] = createSignal<string | null>(null)
  const [configuredPlatforms, setConfiguredPlatforms] = createSignal<Set<string>>(new Set())
  /** Until the accounts are in, the warning cannot be trusted either way. */
  const [accountsLoaded, setAccountsLoaded] = createSignal(false)

  let urlInputRef: HTMLInputElement | undefined

  createEffect(() => { document.body.style.overflow = 'hidden'; onCleanup(() => { document.body.style.overflow = '' }) })

  // Load configured platform accounts once on open
  onMount(() => {
    const userId = user()?.id
    if (!userId) return
    api.getPlatformAccounts(userId).then(res => {
      const accounts = res.data || []
      const platforms = new Set<string>(
        accounts
          .filter(account => account.token || account.username)
          .map(account => account.platform)
      )
      setConfiguredPlatforms(platforms)
      setAccountsLoaded(true)
    }).catch(() => setAccountsLoaded(true))
  })

  // Debounced: duplicate check + proactive credentials warning
  createEffect(() => {
    const url = sourceUrl().trim()
    const platform = detectPlatformFromUrl(url)
    if (!url || platform === 'manual') {
      setDuplicateName(null)
      setCredentialsWarning(null)
      return
    }

    // Proactive credentials warning (instant, no debounce needed)
    if (PLATFORMS_REQUIRING_CREDENTIALS.includes(platform) && !configuredPlatforms().has(platform)) {
      setCredentialsWarning(platform)
    } else {
      setCredentialsWarning(null)
    }

    const timer = setTimeout(() => {
      api.checkUrl(url).then(res => {
        setDuplicateName(res.data?.exists ? (res.data.design?.name ?? 'Unknown') : null)
      }).catch(() => setDuplicateName(null))
    }, 400)
    onCleanup(() => clearTimeout(timer))
  })

  const authErrorPrefixes = [
    'error.printables_no_token:', 'error.printables_auth_failed:',
    'error.thingiverse_no_token:', 'error.thingiverse_auth_failed:',
    'error.makerworld_no_token:', 'error.makerworld_auth_failed:',
    'error.thangs_no_token:', 'error.thangs_auth_failed:',
    'error.cults3d_no_token:', 'error.cults3d_auth_failed:',
    'error.platform_credentials_required:',
  ]

  /**
   * True while the entered URL points at a platform this account cannot
   * download from. The queue would take the entry and fail it minutes later,
   * so the import is refused here instead - with the reason, and a way to it.
   */
  const missingCredentials = () => {
    const platform = detectPlatformFromUrl(sourceUrl().trim())
    return accountsLoaded() && PLATFORMS_REQUIRING_CREDENTIALS.includes(platform) && !configuredPlatforms().has(platform)
  }

  /** Queues a URL download in the background and closes the modal. */
  const handleImport = async () => {
    const urlValue = urlInputRef?.value?.trim() || sourceUrl().trim()
    if (!urlValue) { setErrorMessage(translate('validation.url_required')); return }
    if (missingCredentials()) {
      const platform = detectPlatformFromUrl(urlValue)
      setErrorMessage('__AUTH__:' + translate('credentials_required_hint', { platform: PLATFORM_LABELS[platform] || platform }))
      return
    }

    setIsSubmitting(true)
    setErrorMessage('')
    try {
      await api.queueDownload({ source_url: urlValue })
      props.onQueued?.(urlValue, detectPlatformFromUrl(urlValue))
      props.onClose()
    } catch (failure: unknown) {
      const raw = errorKey(failure, 'Unknown error')
      if (raw.startsWith('error.duplicate_design:')) {
        const name = raw.split(':').slice(1).join(':')
        setDuplicateName(name)
        setErrorMessage('')
      } else if (raw.startsWith('error.platform_credentials_required:')) {
        // Proactive warning is already visible - don't add a second alert
        if (!credentialsWarning()) {
          const platform = raw.split(':')[1] || ''
          setErrorMessage('__AUTH__:' + translate('download_credentials_required') + (platform ? ` (${platform})` : ''))
        }
      } else if (authErrorPrefixes.some(prefix => raw.startsWith(prefix))) {
        setErrorMessage('__AUTH__:' + raw.split(':').slice(1).join(':'))
      } else {
        setErrorMessage(raw)
      }
    } finally {
      setIsSubmitting(false)
    }
  }

  /** Creates a blank manual design entry. */
  const handleAddManual = async () => {
    const nameValue = designName().trim()
    if (!nameValue) { setErrorMessage(translate('validation.name_required')); return }

    setIsSubmitting(true)
    setErrorMessage('')
    try {
      const response = await api.createDesign({ name: nameValue, source_platform: 'manual' }) as any
      props.showToast(translate('toast_design_saved'))
      props.onSaved(response?.data?.id)
      props.onClose()
    } catch (caughtError: unknown) {
      const apiError = caughtError instanceof Error ? caughtError.message : 'error.internal'
      setErrorMessage(translate(apiError) !== apiError ? translate(apiError) : apiError)
    } finally {
      setIsSubmitting(false)
    }
  }


  const detectedPlatform = () => detectPlatformFromUrl(sourceUrl())



  return (
    <div style={{ position: 'fixed', inset: '0', background: 'rgba(0,0,0,0.65)', display: 'flex', 'align-items': 'center', 'justify-content': 'center', 'z-index': '500', 'backdrop-filter': 'blur(5px)' }}
      onClick={props.onClose}>
      <div onClick={e => e.stopPropagation()}
        class="stlv-modal" style={{ background: 'var(--bg2)', 'border-radius': '20px', padding: '32px 32px 20px 32px', width: '520px', border: '1px solid var(--border)', 'box-shadow': '0 32px 90px rgba(0,0,0,0.45)' }}>

        {/* The close button is taken out of the flow rather than balanced with a
            spacer: centring the title in a space-between row would shift it by
            half the button's width. Same header shape as the settings modals. */}
        <div style={{ position: 'relative', display: 'flex', 'justify-content': 'center', 'align-items': 'center', 'margin-bottom': '22px' }}>
          <div style={{ ...sansFont, 'font-weight': '700', 'font-size': '18px', color: 'var(--text)' }}>{translate('add_design_title')}</div>
          <button onClick={props.onClose} style={{ position: 'absolute', right: '0', top: '50%', transform: 'translateY(-50%)', background: 'none', border: 'none', color: 'var(--muted)', cursor: 'pointer', 'font-size': '24px', 'line-height': '1' }}>×</button>
        </div>

        {/* Mode selector */}
        <div style={{ display: 'flex', background: 'var(--bg3)', 'border-radius': '11px', padding: '4px', gap: '4px', 'margin-bottom': '22px' }}>
          {(['url', 'manual'] as const).map(mode => (
            <button onClick={() => { setActiveMode(mode); setErrorMessage('') }}
              style={{ flex: '1', padding: '9px', background: activeMode() === mode ? 'var(--bg)' : 'none', border: 'none', 'border-radius': '8px', color: activeMode() === mode ? 'var(--text)' : 'var(--muted)', ...sansFont, 'font-size': '14px', 'font-weight': activeMode() === mode ? '600' : '400', cursor: 'pointer' }}>
              {mode === 'url' ? translate('tab_import_url') : translate('tab_manual_entry')}
            </button>
          ))}
        </div>

        <ErrorMessage message={errorMessage()} onOpenAccountSettings={props.onOpenAccountSettings} />

        <div style={{ display: 'flex', 'flex-direction': 'column' }}>

        {/* - URL Import mode - */}
        <Show when={activeMode() === 'url'}>
          <div style={{ display: 'flex', 'flex-direction': 'column', gap: '15px' }}>
            <div>
              <input ref={urlInputRef} style={inputStyle} placeholder={translate('add_design_url_placeholder')}
                onInput={e => setSourceUrl(e.currentTarget.value)}
                onKeyDown={(e: KeyboardEvent) => { if (e.key === 'Enter' && !isSubmitting()) handleImport() }} />
            </div>

            <Show when={sourceUrl()}>
              <div style={{ display: 'flex', 'align-items': 'center', gap: '8px', 'font-size': '12px', ...monoFont, color: 'var(--muted)' }}>
                <span>{translate('label_platform')}:</span>
                <span style={{ color: PLATFORM_COLORS[detectedPlatform()] || 'var(--muted)', 'font-weight': '700' }}>
                  {PLATFORM_LABELS[detectedPlatform()] || translate('label_unknown')}
                </span>
                <Show when={detectedPlatform() === 'manual'}>
                  <span style={{ color: 'var(--danger)', 'font-size': '11px' }}>{translate('warning_unknown_platform')}</span>
                </Show>
              </div>
            </Show>

            <Show when={duplicateName()}>
              <div style={{ background: 'rgba(234,179,8,0.1)', border: '1px solid rgba(234,179,8,0.4)', 'border-radius': '9px', padding: '9px 13px', display: 'flex', 'align-items': 'center', gap: '8px' }}>
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="#eab308" stroke-width="2.2"><path d="M10.29 3.86L1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z"/><line x1="12" y1="9" x2="12" y2="13"/><line x1="12" y1="17" x2="12.01" y2="17"/></svg>
                <span style={{ ...sansFont, 'font-size': '12px', color: '#eab308' }}>
                  {translate('duplicate_warning_named', { name: duplicateName()! })}
                </span>
              </div>
            </Show>

            <Show when={credentialsWarning()}>
              <div style={{ background: 'rgba(250,104,49,0.1)', border: '1px solid rgba(250,104,49,0.4)', 'border-radius': '10px', padding: '12px 15px' }}>
                <div style={{ ...sansFont, 'font-size': '13px', 'font-weight': '700', color: '#fa6831', 'margin-bottom': '5px' }}>🔒 {translate('credentials_required_title')}</div>
                <div style={{ ...sansFont, 'font-size': '12px', color: '#fa6831', 'line-height': '1.5', 'margin-bottom': '9px' }}>
                  {translate('credentials_required_hint', { platform: PLATFORM_LABELS[credentialsWarning()!] || credentialsWarning()! })}
                </div>
                <Show when={props.onOpenAccountSettings}>
                  <button onClick={() => props.onOpenAccountSettings?.('platforms')}
                    style={{ padding: '6px 14px', background: '#fa6831', border: 'none', 'border-radius': '7px', color: '#fff', 'font-size': '12px', 'font-weight': '700', cursor: 'pointer', ...sansFont }}>
                    {translate('credentials_open_settings')}
                  </button>
                </Show>
              </div>
            </Show>

            {/* Supported sources */}
            <div>
              <label style={labelStyle}>{translate('add_design_url_label')}</label>
              <div style={{ display: 'flex', 'flex-wrap': 'wrap', gap: '6px' }}>
              {Object.entries(PLATFORM_LABELS).filter(([k]) => k !== 'manual').map(([key, label]) => (
                <span style={{ 'font-size': '11px', padding: '3px 9px', 'border-radius': '6px', background: PLATFORM_COLORS[key] + '22', color: PLATFORM_COLORS[key], ...monoFont, 'font-weight': '700' }}>
                  {label}
                </span>
              ))}
              </div>
            </div>

            <button onClick={handleImport} disabled={isSubmitting() || missingCredentials()}
              style={{ width: '100%', padding: '13px', background: (isSubmitting() || missingCredentials()) ? 'var(--bg4)' : 'var(--accent)', border: 'none', 'border-radius': '11px', color: '#fff', ...sansFont, 'font-size': '15px', 'font-weight': '700', cursor: (isSubmitting() || missingCredentials()) ? 'not-allowed' : 'pointer', display: 'flex', 'align-items': 'center', 'justify-content': 'center', gap: '8px' }}>
              <Show when={isSubmitting()}>
                <div style={{ width: '16px', height: '16px', border: '2px solid rgba(255,255,255,0.4)', 'border-top-color': '#fff', 'border-radius': '50%', animation: 'spin 0.7s linear infinite' }} />
              </Show>
              {translate('btn_import')}
            </button>
          </div>
        </Show>

        {/* - Manual mode - */}
        <Show when={activeMode() === 'manual'}>
          <div style={{ display: 'flex', 'flex-direction': 'column', gap: '15px' }}>
            <div>
              <label style={labelStyle}>{translate('add_design_manual_name')}</label>
              <input style={inputStyle} value={designName()} onInput={e => setDesignName(e.currentTarget.value)}
                placeholder={translate('add_design_name_placeholder')}
                onKeyDown={(e: KeyboardEvent) => e.key === 'Enter' && handleAddManual()} />
            </div>
            <button onClick={handleAddManual} disabled={isSubmitting()}
              style={{ width: '100%', padding: '13px', background: isSubmitting() ? 'var(--bg4)' : 'var(--accent)', border: 'none', 'border-radius': '11px', color: '#fff', ...sansFont, 'font-size': '15px', 'font-weight': '700', cursor: isSubmitting() ? 'not-allowed' : 'pointer' }}>
              {isSubmitting() ? translate('btn_saving') : translate('btn_add_manual')}
            </button>
          </div>
        </Show>


        </div>{/* end min-height wrapper */}
      </div>
    </div>
  )
}

interface SyncModalProps {
  designId:   number
  designName: string
  status:     () => string | undefined  // reactive accessor from parent syncStatusMap
  onClose:    () => void
  onDone?:    () => void
}

/**
 * Minimizable modal that shows the background-queue sync status for a single design.
 * No SSE - relies on parent polling sync_queue and passing the current status reactively.
 * When minimized it renders as a floating pill in the bottom-right corner.
 */
export function SyncModal(props: SyncModalProps) {
  const { translate } = useI18n()
  const [minimized, setMinimized] = createSignal(false)
  const [doneFired, setDoneFired] = createSignal(false)

  const isFinished = () => {
    const statusValue = props.status()
    return statusValue === 'updated' || statusValue === 'no_change' || statusValue === 'error'
  }

  // Call onDone exactly once when the job finishes
  createEffect(() => {
    if (isFinished() && !doneFired()) {
      setDoneFired(true)
      props.onDone?.()
    }
  })

  const StatusIcon = (iconProps: { size?: number }) => {
    const sz = iconProps.size ?? 20
    const statusValue = props.status()
    if (statusValue === 'syncing')
      return <div style={{ width:`${sz}px`, height:`${sz}px`, 'flex-shrink':'0', border:'2px solid var(--accent)', 'border-top-color':'transparent', 'border-radius':'50%', animation:'spin 0.7s linear infinite' }} />
    if (statusValue === 'updated')
      return <div style={{ width:`${sz}px`, height:`${sz}px`, 'flex-shrink':'0', background:'#22c55e', 'border-radius':'50%', display:'flex', 'align-items':'center', 'justify-content':'center', 'font-size':`${sz*0.55}px`, color:'#fff' }}>✓</div>
    if (statusValue === 'no_change')
      return <div style={{ width:`${sz}px`, height:`${sz}px`, 'flex-shrink':'0', background:'#64748b', 'border-radius':'50%', display:'flex', 'align-items':'center', 'justify-content':'center', 'font-size':`${sz*0.55}px`, color:'#fff' }}>✓</div>
    if (statusValue === 'error')
      return <div style={{ width:`${sz}px`, height:`${sz}px`, 'flex-shrink':'0', background:'#ef4444', 'border-radius':'50%', display:'flex', 'align-items':'center', 'justify-content':'center', 'font-size':`${sz*0.55}px`, color:'#fff' }}>✕</div>
    // queued / undefined
    return <div style={{ width:`${sz}px`, height:`${sz}px`, 'flex-shrink':'0', border:`2px solid #64748b`, 'border-radius':'50%', animation: (!props.status() || props.status() === 'queued') ? 'none' : 'spin 0.7s linear infinite' }} />
  }

  const statusLabel = () => {
    const statusValue = props.status()
    if (!statusValue || statusValue === 'queued')  return translate('sync_status_queued')
    if (statusValue === 'syncing')       return translate('sync_status_syncing')
    if (statusValue === 'updated')       return translate('sync_status_updated')
    if (statusValue === 'no_change')     return translate('sync_status_no_change')
    if (statusValue === 'error')         return translate('sync_status_error')
    return '…'
  }

  return (
    <>
      {/* Minimized pill */}
      <Show when={minimized()}>
        <div
          style={{ position:'fixed', bottom:'24px', right:'24px', 'z-index':'700', display:'flex', 'align-items':'center', gap:'10px', background:'var(--bg2)', border:'1px solid var(--border)', 'border-radius':'50px', padding:'10px 16px', 'box-shadow':'0 8px 32px rgba(0,0,0,0.4)', cursor:'pointer', 'min-width':'180px', 'max-width':'280px' }}
          onClick={() => setMinimized(false)}>
          <StatusIcon size={18} />
          <div style={{ flex:'1', overflow:'hidden' }}>
            <div style={{ ...monoFont, 'font-size':'10px', color:'var(--muted)', 'text-transform':'uppercase', 'letter-spacing':'0.06em' }}>Sync</div>
            <div style={{ ...sansFont, 'font-size':'12px', 'font-weight':'600', color:'var(--text)', 'white-space':'nowrap', overflow:'hidden', 'text-overflow':'ellipsis' }}>{props.designName}</div>
          </div>
          <Show when={isFinished()}>
            <button onClick={e => { e.stopPropagation(); props.onClose() }}
              style={{ background:'none', border:'none', color:'var(--muted)', cursor:'pointer', 'font-size':'18px', 'line-height':'1', padding:'0 2px', 'flex-shrink':'0' }}>×</button>
          </Show>
        </div>
      </Show>

      {/* Full modal */}
      <Show when={!minimized()}>
        <div style={{ position:'fixed', inset:'0', background:'rgba(0,0,0,0.65)', display:'flex', 'align-items':'center', 'justify-content':'center', 'z-index':'600', 'backdrop-filter':'blur(5px)' }}>
          <div onClick={e => e.stopPropagation()}
            class="stlv-modal" style={{ background:'var(--bg2)', 'border-radius':'20px', padding:'28px 32px', width:'420px', border:'1px solid var(--border)', 'box-shadow':'0 32px 90px rgba(0,0,0,0.45)' }}>

            {/* Header */}
            <div style={{ display:'flex', 'align-items':'center', gap:'10px', 'margin-bottom':'6px' }}>
              <div style={{ ...sansFont, 'font-weight':'700', 'font-size':'17px', color:'var(--text)', flex:'1' }}>{translate('btn_sync')}</div>
              {/* Minimize */}
              <button onClick={() => setMinimized(true)} title={translate('btn_minimize')}
                style={{ background:'none', border:'none', color:'var(--muted)', cursor:'pointer', padding:'4px 6px', display:'flex', 'align-items':'center', 'border-radius':'6px' }}>
                <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5">
                  <line x1="5" y1="12" x2="19" y2="12"/>
                </svg>
              </button>
              {/* Close - only when done */}
              <Show when={isFinished()}>
                <button onClick={props.onClose}
                  style={{ background:'none', border:'none', color:'var(--muted)', cursor:'pointer', 'font-size':'22px', 'line-height':'1', padding:'0 2px' }}>×</button>
              </Show>
            </div>

            {/* Design name subtitle */}
            <div style={{ ...sansFont, 'font-size':'13px', color:'var(--muted)', 'margin-bottom':'22px', 'white-space':'nowrap', overflow:'hidden', 'text-overflow':'ellipsis' }}>
              {props.designName}
            </div>

            {/* Status card */}
            <div style={{ background:'var(--bg3)', border:'1px solid var(--border)', 'border-radius':'14px', padding:'20px 22px', display:'flex', 'align-items':'center', gap:'16px' }}>
              <StatusIcon size={22} />
              <div>
                <div style={{ ...sansFont, 'font-size':'14px', 'font-weight':'600', color:'var(--text)' }}>{statusLabel()}</div>
                <Show when={isFinished() === false}>
                  <div style={{ ...sansFont, 'font-size':'12px', color:'var(--muted)', 'margin-top':'2px' }}>
                    {translate('sync_modal_waiting')}
                  </div>
                </Show>
              </div>
            </div>

            {/* Footer */}
            <Show when={isFinished()}>
              <div style={{ 'margin-top':'18px', display:'flex', 'justify-content':'flex-end' }}>
                <button onClick={props.onClose}
                  style={{ padding:'10px 24px', background:'var(--accent)', border:'none', 'border-radius':'10px', color:'#fff', ...sansFont, 'font-size':'14px', 'font-weight':'700', cursor:'pointer' }}>
                  {translate('btn_close')}
                </button>
              </div>
            </Show>
          </div>
        </div>
      </Show>
    </>
  )
}
