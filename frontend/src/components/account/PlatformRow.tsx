import { ToggleSwitch } from '../ToggleSwitch'
import { PLATFORM_INFO } from './platformInfo'
import { SaveButton, inp, lbl, mono, sans } from './shared'
import { PLATFORM_COLORS } from '../../constants/platforms'
import { api } from '../../services/api'
import { Show, createEffect, createSignal } from 'solid-js'
import { MANUAL_SYNC_COOLDOWN_SECONDS, createCooldown } from '../../utils/cooldown'
import { errorKey } from '../../utils/errorMessage'
import { markDirty, resetDirty } from '../../utils/unsavedChanges'

/** Single platform row - shows either a saved-state or an add-form. */
export function PlatformRow(props: {
  platform: string
  account: () => any | null
  userId: string
  syncHour: () => number
  /** Server-wide library-sync switch; false hides everything that starts a sync. */
  syncEnabled: () => boolean
  showToast: (m: string, v?: string) => void
  onRefresh: () => void
  translate: (key: string, vars?: Record<string, string | number>) => string
  isExpanded: () => boolean
  onExpand: () => void
  onCollapse: () => void
}) {
  const [token, setToken] = createSignal('')
  const [username, setUsername] = createSignal('')
  const [password, setPassword] = createSignal('')
  const [totpSeed, setTotpSeed] = createSignal('')
  const [showPw, setShowPw] = createSignal(false)
  const [saving, setSaving] = createSignal(false)
  const [syncing, setSyncing] = createSignal(false)
  const [err, setErr] = createSignal('')
  const [syncCollections, setSyncCollections] = createSignal<boolean>(props.account()?.sync_collections ?? true)
  createEffect(() => {
    const acc = props.account()
    if (acc) {
      setSyncCollections(acc.sync_collections ?? true)
    }
  })

  const info = {
    supportsAutoLogin: PLATFORM_INFO[props.platform]?.supportsAutoLogin ?? false,
    requiresTotpSeed:  PLATFORM_INFO[props.platform]?.requiresTotpSeed  ?? false,
    requiresUsername:  PLATFORM_INFO[props.platform]?.requiresUsername  ?? false,
    hasToken:          PLATFORM_INFO[props.platform]?.hasToken          ?? true,
    hint:             props.translate(`platform_hint_${props.platform}`),
    tokenLabel:       props.translate(`platform_token_label_${props.platform}`),
    tokenPlaceholder: props.translate(`platform_token_ph_${props.platform}`),
  }
  const color = PLATFORM_COLORS[props.platform] || 'var(--accent)'

  const save = async () => {
    const trimmedToken = token().trim(), trimmedUsername = username().trim(), pw = password(), seed = totpSeed().trim()
    const acc = props.account()
    const userOk = !!trimmedUsername || !!acc?.username
    const pwOk   = !!(pw && pw !== '***') || !!acc?.has_password
    const tokOk  = !!trimmedToken || !!acc?.token
    const seedOk = !!seed || !!acc?.has_totp_secret
    if (info.requiresTotpSeed) {
      if (!userOk || !pwOk) { setErr(props.translate('platform_err_credentials')); return }
      if (!seedOk) { setErr(props.translate('platform_err_totp_required')); return }
    } else if (!info.supportsAutoLogin) {
      if (!tokOk) { setErr(props.translate('platform_err_token_required')); return }
      if (info.requiresUsername && !userOk) { setErr(props.translate('platform_err_username_required')); return }
    } else if (info.hasToken) {
      if (!userOk || !pwOk) { setErr(props.translate('platform_err_credentials')); return }
      if (!tokOk) { setErr(props.translate('platform_err_api_key_required')); return }
    } else {
      if (!userOk || !pwOk) { setErr(props.translate('platform_err_credentials')); return }
    }
    setSaving(true); setErr('')
    try {
      await api.savePlatformAccount(props.userId, {
        platform:         props.platform,
        token:            info.hasToken ? (trimmedToken || null) : null,
        username:         trimmedUsername || null,
        password:         pw || '***',
        totp_secret:      seed || null,
        sync_collections: syncCollections(),
      })
      props.showToast(props.translate('toast_platform_saved'))
      resetDirty()
      setToken(''); setUsername(''); setPassword(''); setTotpSeed('')
      props.onCollapse()
      props.onRefresh()
    } catch (failure: unknown) { setErr(props.translate(errorKey(failure))) }
    finally { setSaving(false) }
  }

  const remove = async () => {
    if (!props.account()) return
    try {
      await api.deletePlatformAccount(props.userId, props.account()!.id)
      props.showToast(props.translate('toast_platform_removed'))
      props.onRefresh()
    } catch {}
  }

  const cooldown = createCooldown()
  // The account carries the remaining cooldown of its last manual sync, so a
  // reloaded page keeps the button closed instead of running into a 429.
  createEffect(() => cooldown.start(props.account()?.sync_cooldown_seconds ?? 0))

  const syncNow = async () => {
    setSyncing(true)
    try {
      await api.syncPlatformLibrary(props.userId, props.platform)
      cooldown.start(MANUAL_SYNC_COOLDOWN_SECONDS)
      props.showToast(props.translate('platform_sync_started_toast'))
    } catch (failure: unknown) {
      props.showToast(props.translate(errorKey(failure)), 'error')
    } finally {
      setSyncing(false)
    }
  }

  const hasSaved = () => !!props.account()

  const isFullyConfigured = () => {
    const acc = props.account()
    if (!acc) return false
    const hasUser = !!acc.username, hasPw = !!acc.has_password, hasTok = !!acc.token, hasTotp = !!acc.has_totp_secret
    if (info.requiresTotpSeed) return hasUser && hasPw && hasTotp
    if (!info.supportsAutoLogin) return hasTok && (!info.requiresUsername || hasUser)
    if (info.hasToken) return hasUser && hasPw && hasTok
    return hasUser && hasPw
  }

  return (
    <div style={{ border: '1px solid var(--border)', 'border-radius': '12px', overflow: 'hidden' }}>
      {/* Header row */}
      <div style={{ display: 'flex', 'align-items': 'center', gap: '12px', padding: '12px 16px', background: 'var(--surface)', cursor: 'pointer' }}
           onClick={() => { props.isExpanded() ? props.onCollapse() : props.onExpand() }}>
        {/* Status dot. Its meaning is a tooltip rather than a sentence in the
            intro above - a legend for a dot that sits right here read like an
            instruction manual. */}
        <div title={props.translate(isFullyConfigured() ? 'platform_configured_short' : 'platform_not_configured_short')}
          style={{ width: '10px', height: '10px', 'border-radius': '50%', background: isFullyConfigured() ? '#22c55e' : 'var(--border)', 'flex-shrink': '0' }} />
        {/* Platform name badge */}
        <span style={{ ...mono, 'font-size': '12px', background: color + '22', color: color, 'border-radius': '5px', padding: '2px 9px', 'flex-shrink': '0' }}>
          {props.platform.charAt(0).toUpperCase() + props.platform.slice(1)}
        </span>

        {/* Status or expand hint */}
        <Show when={hasSaved()} fallback={
          <span style={{ ...sans, 'font-size': '12px', color: 'var(--muted)', flex: '1' }}>
            {props.isExpanded() ? props.translate('platform_cancel_form') : props.translate('platform_not_configured')}
          </span>
        }>
          <div style={{ flex: '1' }} />
          <div style={{ display: 'flex', gap: '8px' }}>
            <button onClick={(e) => { e.stopPropagation(); props.isExpanded() ? props.onCollapse() : props.onExpand() }}
              style={{ padding: '4px 12px', background: 'var(--bg3)', border: '1px solid var(--border)', 'border-radius': '7px', color: 'var(--text2)', 'font-size': '11px', cursor: 'pointer', ...sans }}>
              {props.isExpanded() ? props.translate('btn_cancel') : props.translate('platform_update_btn')}
            </button>
            <button onClick={(e) => { e.stopPropagation(); remove() }}
              style={{ padding: '4px 12px', background: 'var(--danger-bg)', border: '1px solid var(--danger-border)', 'border-radius': '7px', color: 'var(--danger)', 'font-size': '11px', cursor: 'pointer', ...sans }}>
              {props.translate('btn_remove')}
            </button>
          </div>
        </Show>
      </div>

      {/* Expandable form */}
      <Show when={props.isExpanded()}>
        <div style={{ padding: '16px', display: 'flex', 'flex-direction': 'column', gap: '12px', background: 'var(--bg2)', 'border-top': '1px solid var(--border)' }}>
          <Show when={err()}>
            <div style={{ ...sans, 'font-size': '12px', color: 'var(--danger)', background: 'var(--danger-bg)', border: '1px solid var(--danger-border)', 'border-radius': '8px', padding: '8px 12px' }}>{err()}</div>
          </Show>

          {/* Auto-login block */}
          <Show when={info.supportsAutoLogin}>
            <div style={{ display: 'flex', 'flex-direction': 'column', gap: '8px' }}>
              <div style={{ ...mono, 'font-size': '10px', color: color, 'margin-bottom': '10px', 'font-weight': '600', 'text-transform': 'uppercase', 'letter-spacing': '0.05em' }}>{props.translate('platform_auto_login_title')}</div>
              <div style={{ display: 'grid', 'grid-template-columns': '1fr 1fr', gap: '8px' }}>
                <div>
                  <label style={lbl}>{props.translate('field_email')}</label>
                  <input style={inp} type="email" value={username()} onInput={e => setUsername(e.currentTarget.value)}
                    placeholder={props.account()?.username || 'your@email.com'} />
                </div>
                <div>
                  <label style={lbl}>{props.translate('section_password')}</label>
                  <div style={{ position: 'relative' }}>
                    <input style={{ ...inp, 'padding-right': '36px' }} type={showPw() ? 'text' : 'password'}
                      value={password()} onInput={e => setPassword(e.currentTarget.value)}
                      placeholder={props.account()?.has_password ? props.translate('platform_token_saved_placeholder') : '••••••••'} />
                    <button onClick={() => setShowPw(currentValue => !currentValue)}
                      style={{ position: 'absolute', right: '10px', top: '50%', transform: 'translateY(-50%)', background: 'none', border: 'none', color: 'var(--muted)', cursor: 'pointer', 'font-size': '14px' }}>
                      {showPw() ? '🙈' : '👁'}
                    </button>
                  </div>
                </div>
              </div>
              <div style={{ ...sans, 'font-size': '11px', color: 'var(--muted)', 'margin-top': '8px' }}>{props.translate('platform_password_encrypted_hint')}</div>
            </div>

            {/* TOTP seed field - MakerWorld only */}
            <Show when={info.requiresTotpSeed}>
              <div>
                <label style={lbl}>{props.translate(`platform_totp_seed_label_${props.platform}`) || '2FA Seed (secret key)'} *</label>
                <input style={{ ...inp, 'font-family': "'DM Mono',monospace", 'font-size': '12px' }}
                  type="password"
                  value={totpSeed()} onInput={e => setTotpSeed(e.currentTarget.value)}
                  placeholder={props.account()?.has_totp_secret ? props.translate('platform_token_saved_placeholder') : (props.translate(`platform_totp_seed_ph_${props.platform}`) || 'XXXX XXXX XXXX …')} />
              </div>
            </Show>

            <Show when={!info.requiresTotpSeed && info.hasToken}>
              <div style={{ ...sans, 'font-size': '11px', color: 'var(--muted)', 'text-align': 'center' }}>{props.translate('platform_or_token_divider')}</div>
            </Show>
          </Show>

          {/* Token field - hidden for platforms that don't use it */}
          <Show when={info.hasToken}>
            <div>
              <label style={lbl}>{info.tokenLabel}</label>
              <input style={{ ...inp, 'font-family': "'DM Mono',monospace", 'font-size': '12px' }} type="password"
                value={token()} onInput={e => setToken(e.currentTarget.value)}
                placeholder={props.account()?.token ? props.translate('platform_token_saved_placeholder') : info.tokenPlaceholder} />
            </div>
          </Show>

          {/* Username field - required for platforms where the token alone can't identify the user */}
          <Show when={info.requiresUsername}>
            <div>
              <label style={lbl}>{props.translate(`platform_username_label_${props.platform}`)}</label>
              <input style={inp}
                value={username()} onInput={e => setUsername(e.currentTarget.value)}
                placeholder={props.account()?.username || props.translate(`platform_username_ph_${props.platform}`)} />
            </div>
          </Show>

          {/* Hint */}
          <div style={{ ...sans, 'font-size': '11px', color: 'var(--muted)', 'line-height': '1.5', padding: '8px 12px', background: 'var(--bg3)', 'border-radius': '8px' }}>
            ℹ️ {info.hint}
          </div>

          {/* Sync options - dimmed while the server switch is off, because the
              setting is then without effect. */}
          <div style={{ display: 'flex', 'flex-direction': 'column', gap: '8px', opacity: props.syncEnabled() ? '1' : '0.55' }}>
            <div style={{ display: 'flex', 'align-items': 'center', gap: '12px', padding: '2px 0' }}>
              <ToggleSwitch checked={syncCollections()} onChange={(v) => { setSyncCollections(v); markDirty() }} />
              <div style={{ ...sans, 'font-size': '12px', 'font-weight': '600', color: 'var(--text)' }}>{props.translate('platform_sync_collections_label')}</div>
            </div>
            <div style={{ ...sans, 'font-size': '11px', color: 'var(--muted)' }}>
              {props.syncEnabled()
                ? props.translate('platform_sync_hint', { hour: String(props.syncHour()).padStart(2, '0') })
                : props.translate('platform_sync_server_disabled')}
            </div>
          </div>

          {/* Sync now button */}
          <Show when={hasSaved() && props.syncEnabled()}>
            <button onClick={syncNow} disabled={syncing() || cooldown.remaining() > 0}
              style={{ width: '100%', height: '36px', background: 'var(--bg3)', border: '1px solid var(--border)', 'border-radius': '10px', color: syncing() || cooldown.remaining() > 0 ? 'var(--muted)' : 'var(--text2)', ...sans, 'font-size': '13px', cursor: syncing() || cooldown.remaining() > 0 ? 'not-allowed' : 'pointer' }}>
              {syncing() ? '…' : props.translate('platform_sync_now_btn')}
            </button>
            <Show when={cooldown.remaining() > 0}>
              <div style={{ ...sans, 'font-size': '11px', color: 'var(--muted)', 'margin-top': '-4px' }}>
                {props.translate('sync_cooldown_hint', { min: String(cooldown.minutes()) })}
              </div>
            </Show>
          </Show>

          <SaveButton onClick={save} loading={saving()} label={props.translate('btn_save_account')} />
        </div>
      </Show>
    </div>
  )
}
