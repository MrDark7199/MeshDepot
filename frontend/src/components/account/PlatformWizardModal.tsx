import { ToggleSwitch } from '../ToggleSwitch'
import { PLATFORM_INFO } from './platformInfo'
import { inp, lbl, mono, sans } from './shared'
import { PLATFORM_COLORS } from '../../constants/platforms'
import { api } from '../../services/api'
import { Show, createSignal } from 'solid-js'
import { errorKey } from '../../utils/errorMessage'

/**
 * Multi-step setup wizard for a platform account (generic, config-driven).
 * Step 1: credentials → validated against the platform (real login attempt).
 * Step 2: sync settings (master auto-sync + collections), then save.
 * On save the backend auto-starts a sync when a sync flag was newly enabled.
 */
export function PlatformWizardModal(props: {
  platform: string
  account: () => any | null
  userId: string
  syncHour: () => number
  /** Server-wide library-sync switch; false replaces the schedule hint. */
  syncEnabled: () => boolean
  onClose: () => void
  onSaved: () => void
  showToast: (m: string, v?: string) => void
  translate: (k: string, vars?: Record<string, string | number>) => string
}) {
  const t = props.translate
  const info = {
    supportsAutoLogin: PLATFORM_INFO[props.platform]?.supportsAutoLogin ?? false,
    requiresTotpSeed:  PLATFORM_INFO[props.platform]?.requiresTotpSeed  ?? false,
    requiresUsername:  PLATFORM_INFO[props.platform]?.requiresUsername  ?? false,
    hasToken:          PLATFORM_INFO[props.platform]?.hasToken          ?? true,
    hint:             t(`platform_hint_${props.platform}`),
    tokenLabel:       t(`platform_token_label_${props.platform}`),
    tokenPlaceholder: t(`platform_token_ph_${props.platform}`),
  }
  const color = PLATFORM_COLORS[props.platform] || 'var(--accent)'
  const acc = props.account()
  const title = props.platform.charAt(0).toUpperCase() + props.platform.slice(1)

  const [step, setStep] = createSignal(1)
  // Token + username come back decrypted from the API, so pre-fill them when
  // editing a saved account. This lets the eye toggle reveal the stored token
  // (an empty field had nothing to show). Password/TOTP stay secret (booleans).
  const [token, setToken] = createSignal(acc?.token || '')
  const [username, setUsername] = createSignal(acc?.username || '')
  const [password, setPassword] = createSignal('')
  const [totpSeed, setTotpSeed] = createSignal('')
  const [showPw, setShowPw] = createSignal(false)
  const [showToken, setShowToken] = createSignal(false)
  const [validating, setValidating] = createSignal(false)
  const [saving, setSaving] = createSignal(false)
  const [err, setErr] = createSignal('')
  const [syncCollections, setSyncCollections] = createSignal<boolean>(acc?.sync_collections ?? true)
  const [autoSync, setAutoSync] = createSignal<boolean>(acc?.auto_library_sync ?? true)

  // Masked password ('***') tells the backend to keep the stored one.
  const pwToSend = () => {
    const pw = password()
    if (pw && pw !== '***') return pw
    return acc?.has_password ? '***' : ''
  }

  const validate = async () => {
    setErr('')
    const userOk = !!username().trim() || !!acc?.username
    const pwOk   = !!(password() && password() !== '***') || !!acc?.has_password
    const tokOk  = !!token().trim() || !!acc?.token
    if (info.supportsAutoLogin && (!userOk || !pwOk)) { setErr(t('platform_err_credentials')); return }
    if (!info.supportsAutoLogin && info.hasToken && !tokOk) { setErr(t('platform_err_token_required')); return }
    if (info.requiresUsername && !userOk) { setErr(t('platform_err_username_required')); return }
    setValidating(true)
    try {
      const res: any = await api.validatePlatformAccount(props.userId, {
        platform:    props.platform,
        username:    username().trim() || acc?.username || '',
        password:    pwToSend(),
        token:       token().trim() || '',
        totp_secret: totpSeed().trim() || '',
      })
      if (res.data?.ok) setStep(2)
      else {
        // The backend names which half was wrong (token vs. username).
        const reason: Record<string, string> = {
          'error.platform_credentials_unreadable':  t('error.platform_credentials_unreadable'),
          'error.platform_invalid_token':           t('platform_err_token_invalid'),
          'error.platform_invalid_username':        t('platform_err_username_invalid'),
          'error.myminifactory_invalid_api_key':    t('platform_err_api_key_invalid'),
          'error.makerworld_invalid_credentials':   t('platform_err_makerworld_credentials'),
          'error.makerworld_invalid_totp':          t('platform_err_makerworld_totp'),
          'error.makerworld_totp_required':         t('platform_err_makerworld_totp_required'),
          'error.makerworld_2fa_unsupported':       t('platform_err_makerworld_2fa_unsupported'),
          'error.makerworld_login_unavailable':     t('platform_err_makerworld_unavailable'),
          'error.makerworld_login_blocked':         t('platform_err_makerworld_blocked'),
          'error.makerworld_rate_limited':          t('platform_err_makerworld_rate_limited'),
        }
        setErr(reason[res.data?.error] || t('platform_wizard_invalid'))
      }
    } catch (failure: unknown) { setErr(t(errorKey(failure, 'platform_wizard_invalid'))) }
    finally { setValidating(false) }
  }

  const save = async () => {
    setSaving(true); setErr('')
    try {
      const res: any = await api.savePlatformAccount(props.userId, {
        platform:          props.platform,
        token:             info.hasToken ? (token().trim() || null) : null,
        username:          username().trim() || null,
        password:          pwToSend() || '***',
        totp_secret:       totpSeed().trim() || null,
        sync_collections:  syncCollections(),
        auto_library_sync: autoSync(),
      })
      props.showToast(res.data?.auto_synced ? t('platform_wizard_saved_synced') : t('toast_platform_saved'))
      props.onSaved()
      props.onClose()
    } catch (failure: unknown) { setErr(t(errorKey(failure))) }
    finally { setSaving(false) }
  }

  const stepDot = (n: number) => ({
    width: '24px', height: '24px', 'border-radius': '50%', 'flex-shrink': '0',
    display: 'flex', 'align-items': 'center', 'justify-content': 'center', 'font-size': '12px', 'font-weight': '700',
    background: step() >= n ? color : 'var(--bg4)', color: step() >= n ? '#fff' : 'var(--muted)', ...mono,
  })

  return (
    <div onClick={props.onClose}
      style={{ position: 'fixed', inset: '0', background: 'rgba(0,0,0,0.8)', display: 'flex', 'align-items': 'center', 'justify-content': 'center', 'z-index': '2100', 'backdrop-filter': 'blur(6px)' }}>
      {/* The wizard renders inside the account settings modal body, which marks
          itself dirty on every input event that bubbles up to it. This dialog
          has its own save button, so what is typed here is never an unsaved
          change of that form - keep the events from reaching it. */}
      <div onClick={e => e.stopPropagation()}
        onInput={e => e.stopPropagation()} onChange={e => e.stopPropagation()}
        style={{ background: 'var(--bg)', 'border-radius': '18px', width: '520px', 'max-width': '94vw', 'max-height': '92vh', overflow: 'auto', border: '1px solid var(--border)', 'box-shadow': '0 40px 100px rgba(0,0,0,0.5)' }}>
        {/* Header + stepper */}
        <div style={{ padding: '18px 22px', 'border-bottom': '1px solid var(--border)' }}>
          <div style={{ display: 'flex', 'align-items': 'center', 'justify-content': 'space-between', 'margin-bottom': '14px' }}>
            <span style={{ ...sans, 'font-weight': '700', 'font-size': '16px', color: 'var(--text)' }}>
              {t(acc ? 'platform_wizard_title_edit' : 'platform_wizard_title_new', { name: title })}
            </span>
            <button onClick={props.onClose} style={{ background: 'none', border: 'none', color: 'var(--muted)', cursor: 'pointer', 'font-size': '22px', 'line-height': '1' }}>×</button>
          </div>
          <div style={{ display: 'flex', 'align-items': 'center', gap: '10px' }}>
            <div style={stepDot(1)}>1</div>
            <span style={{ ...sans, 'font-size': '12px', color: step() === 1 ? 'var(--text)' : 'var(--muted)', 'font-weight': step() === 1 ? '600' : '400' }}>{t('platform_wizard_step_creds')}</span>
            <div style={{ flex: '1', height: '1px', background: 'var(--border)' }} />
            <div style={stepDot(2)}>2</div>
            <span style={{ ...sans, 'font-size': '12px', color: step() === 2 ? 'var(--text)' : 'var(--muted)', 'font-weight': step() === 2 ? '600' : '400' }}>{t('platform_wizard_step_sync')}</span>
          </div>
        </div>

        <div style={{ padding: '18px 22px', display: 'flex', 'flex-direction': 'column', gap: '12px' }}>
          <Show when={err()}>
            <div style={{ ...sans, 'font-size': '12px', color: 'var(--danger)', background: 'var(--danger-bg)', border: '1px solid var(--danger-border)', 'border-radius': '8px', padding: '8px 12px' }}>{err()}</div>
          </Show>

          {/* - Step 1: credentials - */}
          <Show when={step() === 1}>
            <Show when={info.supportsAutoLogin}>
              <div>
                <label style={lbl}>{t('field_email')}</label>
                <input style={inp} type="email" value={username()} onInput={e => setUsername(e.currentTarget.value)}
                  placeholder={acc?.username || 'your@email.com'} />
              </div>
              <div>
                <label style={lbl}>{t('section_password')}</label>
                <div style={{ position: 'relative' }}>
                  <input style={{ ...inp, 'padding-right': '36px' }} type={showPw() ? 'text' : 'password'}
                    value={password()} onInput={e => setPassword(e.currentTarget.value)}
                    placeholder={acc?.has_password ? t('platform_token_saved_placeholder') : '••••••••'} />
                  <button type="button" onClick={() => setShowPw(v => !v)} style={{ position: 'absolute', right: '10px', top: '50%', transform: 'translateY(-50%)', background: 'none', border: 'none', color: 'var(--muted)', cursor: 'pointer', display: 'flex', 'align-items': 'center', padding: '2px' }}>
                    {showPw()
                      ? <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M17.94 17.94A10.07 10.07 0 0 1 12 20c-7 0-11-8-11-8a18.45 18.45 0 0 1 5.06-5.94M9.9 4.24A9.12 9.12 0 0 1 12 4c7 0 11 8 11 8a18.5 18.5 0 0 1-2.16 3.19"/><line x1="1" y1="1" x2="23" y2="23"/></svg>
                      : <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z"/><circle cx="12" cy="12" r="3"/></svg>}
                  </button>
                </div>
              </div>
            </Show>
            <Show when={info.requiresTotpSeed}>
              <div>
                <label style={lbl}>{t(`platform_totp_seed_label_${props.platform}`)} *</label>
                <input style={{ ...inp, 'font-family': "'DM Mono',monospace", 'font-size': '12px' }} type="password"
                  value={totpSeed()} onInput={e => setTotpSeed(e.currentTarget.value)}
                  placeholder={acc?.has_totp_secret ? t('platform_token_saved_placeholder') : 'XXXX XXXX XXXX …'} />
              </div>
            </Show>
            <Show when={info.hasToken}>
              <div>
                <label style={lbl}>{info.tokenLabel}</label>
                <div style={{ position: 'relative' }}>
                  <input style={{ ...inp, 'font-family': "'DM Mono',monospace", 'font-size': '12px', 'padding-right': '36px' }} type={showToken() ? 'text' : 'password'}
                    value={token()} onInput={e => setToken(e.currentTarget.value)}
                    placeholder={acc?.token ? t('platform_token_saved_placeholder') : info.tokenPlaceholder} />
                  <button type="button" onClick={() => setShowToken(v => !v)} style={{ position: 'absolute', right: '10px', top: '50%', transform: 'translateY(-50%)', background: 'none', border: 'none', color: 'var(--muted)', cursor: 'pointer', display: 'flex', 'align-items': 'center', padding: '2px' }}>
                    {showToken()
                      ? <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M17.94 17.94A10.07 10.07 0 0 1 12 20c-7 0-11-8-11-8a18.45 18.45 0 0 1 5.06-5.94M9.9 4.24A9.12 9.12 0 0 1 12 4c7 0 11 8 11 8a18.5 18.5 0 0 1-2.16 3.19"/><line x1="1" y1="1" x2="23" y2="23"/></svg>
                      : <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z"/><circle cx="12" cy="12" r="3"/></svg>}
                  </button>
                </div>
              </div>
            </Show>
            <Show when={info.requiresUsername}>
              <div>
                <label style={lbl}>{t(`platform_username_label_${props.platform}`)}</label>
                <input style={inp} value={username()} onInput={e => setUsername(e.currentTarget.value)}
                  placeholder={acc?.username || t(`platform_username_ph_${props.platform}`)} />
              </div>
            </Show>
            <div style={{ ...sans, 'font-size': '11px', color: 'var(--muted)', 'line-height': '1.5', padding: '8px 12px', background: 'var(--bg3)', 'border-radius': '8px' }}>ℹ️ {info.hint}</div>
            <div style={{ display: 'flex', 'justify-content': 'flex-end', gap: '9px', 'margin-top': '4px' }}>
              <button onClick={props.onClose} style={{ background: 'transparent', border: '1px solid var(--border)', 'border-radius': '9px', padding: '9px 16px', ...sans, 'font-size': '13px', color: 'var(--text2)', cursor: 'pointer' }}>{t('btn_cancel')}</button>
              <button onClick={validate} disabled={validating()}
                style={{ background: 'var(--accent)', border: 'none', 'border-radius': '9px', padding: '9px 18px', ...sans, 'font-size': '13px', 'font-weight': '600', color: '#fff', cursor: validating() ? 'wait' : 'pointer', opacity: validating() ? '0.7' : '1' }}>
                {validating() ? t('platform_wizard_validating') : t('platform_wizard_next')}
              </button>
            </div>
          </Show>

          {/* - Step 2: sync settings - */}
          <Show when={step() === 2}>
            <div style={{ display: 'flex', 'align-items': 'center', gap: '12px' }}>
              <ToggleSwitch checked={autoSync()} onChange={setAutoSync} />
              <div>
                <div style={{ ...sans, 'font-size': '13px', 'font-weight': '600', color: 'var(--text)' }}>{t('platform_auto_sync_label')}</div>
                <div style={{ ...sans, 'font-size': '11px', color: 'var(--muted)' }}>{t('platform_auto_sync_hint')}</div>
              </div>
            </div>
            <div style={{ height: '1px', background: 'var(--border)', margin: '2px 0' }} />
            <div style={{ display: 'flex', 'align-items': 'center', gap: '12px', opacity: autoSync() ? '1' : '0.55' }}>
              <ToggleSwitch checked={syncCollections()} onChange={setSyncCollections} />
              <div style={{ ...sans, 'font-size': '13px', 'font-weight': '600', color: 'var(--text)' }}>{t('platform_sync_collections_label')}</div>
            </div>
            <div style={{ ...sans, 'font-size': '11px', color: 'var(--muted)' }}>
              {props.syncEnabled()
                ? t('platform_sync_hint', { hour: String(props.syncHour()).padStart(2, '0') })
                : t('platform_sync_server_disabled')}
            </div>
            <div style={{ display: 'flex', 'justify-content': 'space-between', gap: '9px', 'margin-top': '4px' }}>
              <button onClick={() => setStep(1)} style={{ background: 'transparent', border: '1px solid var(--border)', 'border-radius': '9px', padding: '9px 16px', ...sans, 'font-size': '13px', color: 'var(--text2)', cursor: 'pointer' }}>{t('platform_wizard_back')}</button>
              <button onClick={save} disabled={saving()}
                style={{ background: 'var(--accent)', border: 'none', 'border-radius': '9px', padding: '9px 18px', ...sans, 'font-size': '13px', 'font-weight': '600', color: '#fff', cursor: saving() ? 'wait' : 'pointer', opacity: saving() ? '0.7' : '1' }}>
                {saving() ? '…' : t('platform_wizard_finish')}
              </button>
            </div>
          </Show>
        </div>
      </div>
    </div>
  )
}
