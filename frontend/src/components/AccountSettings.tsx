import { createSignal, createEffect, createMemo, onCleanup, Show, For, JSX } from 'solid-js'
import { api } from '../services/api'
import { useI18n } from '../i18n/index'
import { errorKey } from '../utils/errorMessage'
import { formatDate } from '../utils/datetime'
import { useTheme } from '../ThemeContext'
import type { SyncState, User, UserShareLink } from '../types'
import { UserAvatar } from './UserAvatar'
import { useUnsavedChanges, markDirty, resetDirty, isFormDirty } from '../utils/unsavedChanges'
import { createCooldown, MANUAL_SYNC_COOLDOWN_SECONDS } from '../utils/cooldown'
import { applyCustomCss } from '../utils/customCss'
import { PLATFORM_COLORS, PLATFORMS } from '../constants/platforms'

const lbl: JSX.CSSProperties = { display: 'block', 'font-family': "'DM Mono',monospace", 'font-size': '11px', color: 'var(--muted)', 'margin-bottom': '5px', 'text-transform': 'uppercase', 'letter-spacing': '0.05em' }
const inp: JSX.CSSProperties = { width: '100%', background: 'var(--input-bg)', border: '1px solid var(--border2)', 'border-radius': '9px', padding: '9px 12px', color: 'var(--text)', 'font-family': "'DM Sans',sans-serif", 'font-size': '13px', outline: 'none', 'box-sizing': 'border-box' }
const sans: JSX.CSSProperties = { 'font-family': "'DM Sans',sans-serif" }
const mono: JSX.CSSProperties = { 'font-family': "'DM Mono',monospace" }

function Card(props: { children: JSX.Element }) {
  return <div style={{ background: 'var(--surface)', border: '1px solid var(--border)', 'border-radius': '12px', padding: '14px 16px', display: 'flex', 'flex-direction': 'column', gap: '10px' }}>{props.children}</div>
}
/** Conditionally renders a styled error box when `message` is non-empty. */
function Err(props: { message: string }) {
  return <Show when={props.message}><div style={{ background: 'var(--danger-bg)', border: '1px solid var(--danger-border)', 'border-radius': '8px', padding: '8px 12px', color: 'var(--danger)', ...sans, 'font-size': '13px' }}>{props.message}</div></Show>
}
function SaveButton(props: { onClick: () => void; loading: boolean; label: string }) {
  return (
    <button onClick={props.onClick} disabled={props.loading}
      style={{ width: '100%', height: '40px', background: props.loading ? 'var(--bg4)' : 'var(--accent)', border: 'none', 'border-radius': '10px', color: '#fff', ...sans, 'font-size': '13px', 'font-weight': '700', cursor: props.loading ? 'not-allowed' : 'pointer' }}>
      {props.loading ? '…' : props.label}
    </button>
  )
}

/** Shortest design update interval the backend accepts (scheduler.DesignUpdateMinDays). */
const DESIGN_UPDATE_MIN_DAYS = 7

function fmtBytes(b: number): string {
  if (!b || b === 0) return '0 B'
  if (b >= 1073741824) return (b / 1073741824).toFixed(1) + ' GB'
  if (b >= 1048576) return (b / 1048576).toFixed(1) + ' MB'
  if (b >= 1024) return (b / 1024).toFixed(0) + ' KB'
  return b + ' B'
}

/**
 * Account settings tab - displays the user's avatar (with upload/delete),
 * username, email, and language selector.
 */
function TabAccount(props: { user: User; translate: any; showToast: (message: string, variant?: string) => void; onUserUpdate: (u: User) => void }) {
  const { lang, setLang, availableLangs, translateDesigns, setTranslateDesigns } = useI18n()
  const [form, setForm] = createSignal({ name: props.user.name || '', email: props.user.email || '', language: lang(), translateDesigns: translateDesigns() })
  const [saving, setSaving] = createSignal(false)
  const [err, setErr] = createSignal('')
  const [avatarUploading, setAvatarUploading] = createSignal(false)
  // The avatar saves on its own, so it must not tie into "Account speichern".
  // Selecting a file fires input/change events that bubble to the form's
  // markDirty handler; we snapshot the real dirty state before the picker opens
  // and restore it afterwards so the avatar action stays dirty-neutral.
  let dirtyBeforeAvatar = false

  const handleAvatarUpload = async (file: File) => {
    setAvatarUploading(true)
    try {
      const response = await api.uploadAvatar(props.user.id, file) as any
      const avatarUrl = response?.data?.avatar_url ?? response?.avatar_url
      if (avatarUrl) props.onUserUpdate({ ...props.user, avatar_url: avatarUrl + '?t=' + Date.now() })
    } catch (failure: any) {
      // A rejected upload (e.g. 422 "invalid image type") used to end as a silent
      // no-op: the spinner stopped and the old avatar stayed, with no explanation.
      props.showToast(props.translate(failure?.message || 'error.server'), 'error')
    }
    finally {
      setAvatarUploading(false)
      if (!dirtyBeforeAvatar) resetDirty()
    }
  }

  const handleAvatarDelete = async () => {
    try {
      await api.deleteAvatar(props.user.id)
      props.onUserUpdate({ ...props.user, avatar_url: null })
    } catch (_) {}
  }

  const save = async () => {
    setSaving(true); setErr('')
    try {
      const email = form().email.trim()
      if (email && !/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email)) {
        setErr(props.translate('error.invalid_email')); setSaving(false); return
      }
      // The language goes to the server too, so the account keeps it on the next
      // device instead of only in this browser's localStorage.
      await api.updateProfile(props.user.id, { name: form().name, email, language: form().language })
      setLang(form().language)
      setTranslateDesigns(form().translateDesigns)
      props.onUserUpdate({ ...props.user, name: form().name, email: email || null, language: form().language })
      props.showToast(props.translate('toast_account_saved'))
      resetDirty()
    } catch (e: unknown) { setErr(props.translate(errorKey(e))) }
    finally { setSaving(false) }
  }

  return (
    <div style={{ display: 'flex', 'flex-direction': 'column', gap: '18px' }}>
      <Err message={err()} />
      <Card>
        <div style={{ display: 'flex', 'align-items': 'center', gap: '14px' }}>
          {/* Avatar with upload overlay */}
          <div style={{ position: 'relative', 'flex-shrink': '0' }}>
            <UserAvatar name={props.user.name || '?'} avatarUrl={props.user.avatar_url} size={56} fontSize={20} />
            <label style={{ position: 'absolute', inset: '0', 'border-radius': '50%', background: 'rgba(0,0,0,0.45)', display: 'flex', 'align-items': 'center', 'justify-content': 'center', cursor: 'pointer', opacity: '0', transition: 'opacity 0.15s' }}
              onMouseEnter={e => (e.currentTarget.style.opacity = '1')}
              onMouseLeave={e => (e.currentTarget.style.opacity = '0')}
              onClick={() => { dirtyBeforeAvatar = isFormDirty() }}>
              <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="#fff" stroke-width="2.2"><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><polyline points="17 8 12 3 7 8"/><line x1="12" y1="3" x2="12" y2="15"/></svg>
              <input type="file" accept="image/jpeg,image/png,image/webp" style={{ display: 'none' }}
                onChange={e => { const file = e.currentTarget.files?.[0]; if (file) handleAvatarUpload(file) }} />
            </label>
            <Show when={avatarUploading()}>
              <div style={{ position: 'absolute', inset: '0', 'border-radius': '50%', background: 'rgba(0,0,0,0.5)', display: 'flex', 'align-items': 'center', 'justify-content': 'center' }}>
                <div style={{ width: '16px', height: '16px', border: '2px solid #fff', 'border-top-color': 'transparent', 'border-radius': '50%', animation: 'spin 0.7s linear infinite' }} />
              </div>
            </Show>
          </div>
          <div style={{ flex: '1' }}>
            <div style={{ ...sans, 'font-weight': '600', 'font-size': '14px', color: 'var(--text)' }}>{props.user.name || '-'}</div>
            <Show when={props.user.avatar_url}>
              <button onClick={handleAvatarDelete} style={{ background: 'none', border: 'none', ...mono, 'font-size': '11px', color: 'var(--danger, #e63946)', cursor: 'pointer', padding: '0', 'margin-top': '4px' }}>
                {props.translate('avatar_remove')}
              </button>
            </Show>
          </div>
        </div>
      </Card>
      <div style={{ display: 'flex', 'flex-direction': 'column', gap: '12px' }}>
        <div><label style={lbl}>{props.translate('field_username')}</label><input style={inp} value={form().name} onInput={e => setForm(currentState => ({ ...currentState, name: e.currentTarget.value }))} /></div>
        <div><label style={lbl}>{props.translate('field_email')}</label><input style={inp} type="email" value={form().email} onInput={e => setForm(currentState => ({ ...currentState, email: e.currentTarget.value }))} /></div>
        <div>
          <label style={lbl}>{props.translate('field_language')}</label>
          <select style={inp} value={form().language} onChange={e => setForm(currentState => ({ ...currentState, language: e.currentTarget.value }))}>
            <For each={availableLangs}>{l => <option value={l}>{l.toUpperCase()}</option>}</For>
          </select>
        </div>
        <div>
          <div style={{ display: 'flex', 'align-items': 'center', gap: '10px', cursor: 'pointer' }}
            onClick={() => setForm(currentState => ({ ...currentState, translateDesigns: !currentState.translateDesigns }))}>
            <div style={{ width: '36px', height: '20px', 'border-radius': '10px', background: form().translateDesigns ? 'var(--accent)' : 'var(--border2)', position: 'relative', transition: 'background 0.2s', 'flex-shrink': '0' }}>
              <div style={{ width: '16px', height: '16px', 'border-radius': '50%', background: '#fff', position: 'absolute', top: '2px', left: form().translateDesigns ? '18px' : '2px', transition: 'left 0.2s', 'box-shadow': '0 1px 4px rgba(0,0,0,0.3)' }} />
            </div>
            <span style={{ ...sans, 'font-size': '13px', color: 'var(--text)' }}>{props.translate('field_translate_designs')}</span>
          </div>
          <div style={{ ...mono, 'font-size': '11px', color: 'var(--muted)', 'margin-top': '5px' }}>{props.translate('field_translate_designs_hint')}</div>
        </div>
      </div>
      <SaveButton onClick={save} loading={saving()} label={props.translate('btn_save_account')} />
    </div>
  )
}

/**
 * Password-change tab - three fields (current, new, confirm) with
 * visibility toggles and client-side validation before submission.
 */
function TabPassword(props: { user: User; translate: any; showToast: (message: string, variant?: string) => void }) {
  const [cur, setCur] = createSignal('')
  const [nw, setNw] = createSignal('')
  const [rep, setRep] = createSignal('')
  const [saving, setSaving] = createSignal(false)
  const [err, setErr] = createSignal('')
  const [showCur, setShowCur] = createSignal(false)
  const [showNw, setShowNw] = createSignal(false)

  /**
   * Inline eye/eye-off button for toggling password field visibility.
   */
  const EyeButton = (eyeProps: { show: boolean; onClick: () => void }) => (
    <button type="button" onClick={eyeProps.onClick} style={{ position: 'absolute', right: '10px', top: '50%', transform: 'translateY(-50%)', background: 'none', border: 'none', cursor: 'pointer', color: 'var(--muted)', display: 'flex', 'align-items': 'center', padding: '2px' }}>
      {eyeProps.show
        ? <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M17.94 17.94A10.07 10.07 0 0 1 12 20c-7 0-11-8-11-8a18.45 18.45 0 0 1 5.06-5.94M9.9 4.24A9.12 9.12 0 0 1 12 4c7 0 11 8 11 8a18.5 18.5 0 0 1-2.16 3.19"/><line x1="1" y1="1" x2="23" y2="23"/></svg>
        : <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z"/><circle cx="12" cy="12" r="3"/></svg>}
    </button>
  )

  const save = async () => {
    if (!cur()) { setErr(props.translate('validation.current_password')); return }
    if (nw().length < 8) { setErr(props.translate('error.password_too_short')); return }
    if (nw() !== rep()) { setErr(props.translate('validation.password_mismatch')); return }
    if (nw() === cur()) { setErr(props.translate('validation.password_same')); return }
    setSaving(true); setErr('')
    try { await api.changePassword(props.user.id, { current_password: cur(), new_password: nw() }); props.showToast(props.translate('toast_password_changed')); resetDirty(); setCur(''); setNw(''); setRep('') }
    // The server answers with an i18n key ("error.wrong_password"); printing the
    // raw message put that key on screen.
    catch (e: unknown) { setErr(props.translate(errorKey(e))) }
    finally { setSaving(false) }
  }

  return (
    <div style={{ display: 'flex', 'flex-direction': 'column', gap: '18px' }}>
      <Err message={err()} />
      <div style={{ display: 'flex', 'flex-direction': 'column', gap: '12px' }}>
        {([
          [props.translate('field_current_password'), cur, setCur, showCur, setShowCur] as const,
          [props.translate('field_new_password'),     nw,  setNw,  showNw,  setShowNw] as const,
          [props.translate('field_new_password_again'), rep, setRep, showNw, setShowNw] as const,
        ]).map(([label, val, setVal, show, toggleShow]) => (
          <div>
            <label style={lbl}>{label}</label>
            <div style={{ position: 'relative' }}>
              <input type={show() ? 'text' : 'password'} style={{ ...inp, 'padding-right': '40px' }} value={val()} onInput={e => setVal(e.currentTarget.value)} onKeyDown={(e: KeyboardEvent) => e.key === 'Enter' && save()} />
              <EyeButton show={show()} onClick={() => toggleShow((currentValue: boolean) => !currentValue)} />
            </div>
          </div>
        ))}
      </div>
      <SaveButton onClick={save} loading={saving()} label={props.translate('btn_save_password')} />
    </div>
  )
}

/**
 * Appearance settings tab - theme selector (dark/light/system), accent-color
 * presets with an HSL slider picker, and a custom CSS textarea.
 * Changes are applied live to the document and persisted in localStorage.
 */
function TabAppearance(props: { user: User; translate: any; showToast: (message: string, variant?: string) => void; onUserUpdate: (u: User) => void }) {
  const { theme, setTheme, setAccent } = useTheme()
  const [accentHex, setAccentHex] = createSignal(localStorage.getItem('meshdepot_accent') || '#457b9d')
  // The stylesheet belongs to the account, not to this browser: it is what the
  // session hands over, and what the save below writes back.
  const [customCss, setCustomCss] = createSignal(props.user.custom_css || '')
  const [saving, setSaving] = createSignal(false)
  const [hue, setHue] = createSignal(210)
  const [sat, setSat] = createSignal(55)
  const [lit, setLit] = createSignal(52)
  const [showPicker, setShowPicker] = createSignal(false)

  const PRESETS = ['#457b9d','#2563eb','#7c3aed','#db2777','#dc2626','#ea580c','#d97706','#16a34a','#0891b2','#64748b']

  const hslToHex = (hue: number, saturation: number, lightness: number) => {
    saturation /= 100; lightness /= 100
    const chromaAmplitude = saturation * Math.min(lightness, 1 - lightness)
    const channel = (channelIndex: number) => { const sectorIndex = (channelIndex + hue / 30) % 12; const channelValue = lightness - chromaAmplitude * Math.max(-1, Math.min(sectorIndex - 3, 9 - sectorIndex, 1)); return Math.round(255 * channelValue).toString(16).padStart(2, '0') }
    return `#${channel(0)}${channel(8)}${channel(4)}`
  }

  createEffect(() => { if (showPicker()) setAccentHex(hslToHex(hue(), sat(), lit())) })

  const save = async () => {
    setAccent(accentHex())
    const css = customCss()
    setSaving(true)
    try {
      await api.updateProfile(props.user.id, { custom_css: css })
      // Applied straight away as well as stored: waiting for the next load to
      // show what was just saved is the behaviour this replaces.
      applyCustomCss(css)
      props.onUserUpdate({ ...props.user, custom_css: css })
      props.showToast(props.translate('toast_appearance_saved'))
      resetDirty()
    } catch (failure: unknown) {
      props.showToast(props.translate(errorKey(failure)), 'error')
    } finally { setSaving(false) }
  }

  const themes = [{ key: 'dark', label: props.translate('theme_dark') }, { key: 'light', label: props.translate('theme_light') }, { key: 'system', label: props.translate('theme_system') }]
  const sliderStyle = (bg: string): JSX.CSSProperties => ({ width: '100%', height: '10px', 'border-radius': '5px', outline: 'none', cursor: 'pointer', border: 'none', padding: '0', background: bg, '-webkit-appearance': 'none' })

  return (
    <div style={{ display: 'flex', 'flex-direction': 'column', gap: '18px' }}>
      <div>
        <label style={lbl}>{props.translate('field_theme')}</label>
        <div style={{ display: 'flex', gap: '8px' }}>
          <For each={themes}>{({ key, label }) =>
            <button onClick={() => setTheme(key)} style={{ flex: '1', padding: '8px', background: theme() === key ? 'var(--accent)' : 'var(--surface)', border: `1px solid ${theme() === key ? 'var(--accent)' : 'var(--border)'}`, 'border-radius': '8px', color: theme() === key ? '#fff' : 'var(--text3)', ...sans, 'font-size': '13px', cursor: 'pointer' }}>{label}</button>
          }</For>
        </div>
      </div>
      <div>
        <label style={lbl}>{props.translate('field_accent_color')}</label>
        <div style={{ display: 'flex', 'flex-wrap': 'wrap', gap: '6px', 'margin-bottom': '10px' }}>
          <For each={PRESETS}>{color =>
            <div onClick={() => { setAccentHex(color); setShowPicker(false); markDirty() }}
              style={{ width: '26px', height: '26px', 'border-radius': '6px', background: color, cursor: 'pointer', border: accentHex() === color ? '2px solid #fff' : '2px solid transparent', 'box-shadow': accentHex() === color ? `0 0 0 2px ${color}` : 'none', transition: 'transform 0.1s', transform: accentHex() === color ? 'scale(1.15)' : 'scale(1)' }} />
          }</For>
          <div onClick={() => setShowPicker(currentValue => !currentValue)}
            style={{ width: '26px', height: '26px', 'border-radius': '6px', background: 'var(--bg4)', border: '2px solid var(--border2)', cursor: 'pointer', display: 'flex', 'align-items': 'center', 'justify-content': 'center' }}>
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="var(--text3)" stroke-width="2"><circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83-2.83l.06-.06A1.65 1.65 0 0 0 4.68 15a1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 2.83-2.83l.06.06A1.65 1.65 0 0 0 9 4.68a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 0 1 2.83 2.83l-.06.06A1.65 1.65 0 0 0 19.4 9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z"/></svg>
          </div>
        </div>
        <Show when={showPicker()}>
          <div style={{ background: 'var(--bg3)', border: '1px solid var(--border)', 'border-radius': '10px', padding: '14px', display: 'flex', 'flex-direction': 'column', gap: '10px', 'margin-bottom': '8px' }}>
            <style>{`input[type=range]::-webkit-slider-thumb{-webkit-appearance:none;width:16px;height:16px;border-radius:50%;background:#fff;box-shadow:0 1px 4px rgba(0,0,0,0.4);cursor:pointer;border:none;}`}</style>
            <div>
              <label style={{ ...lbl, 'margin-bottom': '6px' }}>Hue</label>
              <input type="range" min="0" max="359" value={hue()} onInput={e => setHue(+e.currentTarget.value)}
                style={sliderStyle(`linear-gradient(to right,hsl(0,80%,55%),hsl(60,80%,55%),hsl(120,80%,55%),hsl(180,80%,55%),hsl(240,80%,55%),hsl(300,80%,55%),hsl(360,80%,55%))`)} />
            </div>
            <div>
              <label style={{ ...lbl, 'margin-bottom': '6px' }}>Saturation ({sat()}%)</label>
              <input type="range" min="10" max="100" value={sat()} onInput={e => setSat(+e.currentTarget.value)}
                style={sliderStyle(`linear-gradient(to right,hsl(${hue()},10%,${lit()}%),hsl(${hue()},100%,${lit()}%))`)} />
            </div>
            <div>
              <label style={{ ...lbl, 'margin-bottom': '6px' }}>Lightness ({lit()}%)</label>
              <input type="range" min="20" max="80" value={lit()} onInput={e => setLit(+e.currentTarget.value)}
                style={sliderStyle(`linear-gradient(to right,hsl(${hue()},${sat()}%,20%),hsl(${hue()},${sat()}%,80%))`)} />
            </div>
          </div>
        </Show>
        <div style={{ display: 'flex', gap: '8px', 'align-items': 'center' }}>
          <div style={{ width: '34px', height: '34px', 'border-radius': '8px', background: accentHex(), border: '2px solid var(--border2)', 'flex-shrink': '0' }} />
          <input value={accentHex()} onInput={e => { if (/^#[0-9a-fA-F]{6}$/.test(e.currentTarget.value)) setAccentHex(e.currentTarget.value) }} style={{ ...inp, width: '110px', ...mono, 'font-size': '12px' }} placeholder="#457b9d" />
        </div>
      </div>
      <div>
        <label style={lbl}>{props.translate('field_custom_css')}</label>
        <textarea value={customCss()} onInput={e => setCustomCss(e.currentTarget.value)} rows={5} placeholder=":root { --accent: #ff0000; }"
          style={{ ...inp, resize: 'vertical', 'min-height': '90px', ...mono, 'font-size': '12px' }} />
        <div style={{ 'margin-top': '6px', ...mono, 'font-size': '11px', color: 'var(--muted)' }}>{props.translate('custom_css_hint')}</div>
      </div>
      <SaveButton onClick={save} loading={saving()} label={props.translate('btn_save_appearance')} />
    </div>
  )
}

/**
 * Personal stats tab - shows storage usage bar, a summary grid of design/file/
 * tag/collection counts, and a platform-breakdown chart for the current user.
 */
function TabStats(props: { user: User; translate: any }) {
  const { lang } = useI18n()
  const [stats, setStats] = createSignal<any>(null)
  const [loading, setLoading] = createSignal(true)

  api.getUserStats(props.user.id).then((r: any) => setStats(r.data)).catch(() => {}).finally(() => setLoading(false))

  const usedPct = () => stats()?.max_bytes ? Math.min(100, (stats().used_bytes / stats().max_bytes) * 100) : null
  const barColor = () => (usedPct() ?? 0) > 90 ? 'var(--danger)' : (usedPct() ?? 0) > 70 ? '#f4a261' : 'var(--accent)'
  const freeBytes = () => stats()?.max_bytes ? Math.max(0, stats().max_bytes - stats().used_bytes) : null

  const newestDate = () => formatDate(stats()?.newest_design_at, lang())

  const avgFilesPerDesign = () => {
    const statsData = stats()
    if (!statsData || !statsData.design_count) return '-'
    return (statsData.entry_count / statsData.design_count).toFixed(1)
  }

  const platformTotal = () => (stats()?.platforms ?? []).reduce((total: number, platform: any) => total + platform.cnt, 0)

  return (
    <div style={{ display: 'flex', 'flex-direction': 'column', gap: '16px' }}>
      <Show when={loading()}>
        <div style={{ color: 'var(--muted)', ...sans, 'font-size': '13px' }}>{props.translate('label_loading')}</div>
      </Show>
      <Show when={!loading() && stats()}>
        {/* Storage bar */}
        <Show when={stats().max_bytes}>
          <Card>
            <div style={{ display: 'flex', 'justify-content': 'space-between', 'align-items': 'baseline' }}>
              <span style={{ ...sans, 'font-size': '12px', color: 'var(--muted)' }}>{props.translate('stats_storage_heading')}</span>
              <span style={{ ...sans, 'font-size': '13px', color: 'var(--text2)' }}>
                <strong style={{ color: 'var(--text)' }}>{fmtBytes(stats().used_bytes)}</strong>
                <span style={{ color: 'var(--muted)' }}> / {fmtBytes(stats().max_bytes)}</span>
              </span>
            </div>
            <div style={{ height: '8px', background: 'var(--bg4)', 'border-radius': '4px', overflow: 'hidden' }}>
              <div style={{ height: '100%', width: (usedPct() ?? 0) + '%', background: barColor(), 'border-radius': '4px', transition: 'width 0.6s' }} />
            </div>
            <div style={{ ...mono, 'font-size': '11px', color: 'var(--muted)' }}>
              {props.translate('stats_pct_used_free', { pct: usedPct()?.toFixed(1) ?? '0', free: freeBytes() !== null ? fmtBytes(freeBytes()!) : '' })}
            </div>
          </Card>
        </Show>

        {/* Main stat grid */}
        <div style={{ display: 'grid', 'grid-template-columns': '1fr 1fr 1fr', gap: '10px' }}>
          {([
            { label: props.translate('stats_label_designs'),     value: stats().design_count ?? 0,     sub: props.translate('stats_sub_total') },
            { label: props.translate('stats_label_files'),       value: stats().entry_count ?? 0,      sub: props.translate('stats_sub_formats') },
            { label: props.translate('stats_label_avg_files'),   value: avgFilesPerDesign(),            sub: props.translate('stats_sub_per_design') },
            { label: props.translate('stats_label_tags'),        value: stats().tag_count ?? 0,        sub: props.translate('stats_sub_created') },
            { label: props.translate('stats_label_collections'), value: stats().collection_count ?? 0, sub: props.translate('stats_sub_created') },
            { label: props.translate('stats_label_synced'),      value: stats().synced_count ?? 0,     sub: props.translate('stats_sub_with_source') },
          ] as {label:string;value:any;sub:string}[]).map(({ label, value, sub }) => (
            <div style={{ background: 'var(--surface)', border: '1px solid var(--border)', 'border-radius': '12px', padding: '14px 16px' }}>
              <div style={{ ...sans, 'font-size': '24px', 'font-weight': '700', color: 'var(--text)', 'line-height': '1' }}>{value}</div>
              <div style={{ ...sans, 'font-size': '13px', 'font-weight': '600', color: 'var(--text2)', 'margin-top': '6px' }}>{label}</div>
              <div style={{ ...mono, 'font-size': '10px', color: 'var(--muted)', 'margin-top': '2px' }}>{sub}</div>
            </div>
          ))}
        </div>

        {/* Storage + newest */}
        <div style={{ display: 'grid', 'grid-template-columns': '1fr 1fr', gap: '10px' }}>
          <div style={{ background: 'var(--surface)', border: '1px solid var(--border)', 'border-radius': '12px', padding: '14px 16px' }}>
            <div style={{ ...sans, 'font-size': '18px', 'font-weight': '700', color: 'var(--text)', 'line-height': '1' }}>{fmtBytes(stats().used_bytes)}</div>
            <div style={{ ...sans, 'font-size': '13px', 'font-weight': '600', color: 'var(--text2)', 'margin-top': '6px' }}>{props.translate('stats_label_storage_used')}</div>
            <div style={{ ...mono, 'font-size': '10px', color: 'var(--muted)', 'margin-top': '2px' }}>{props.translate('stats_sub_all_versions')}</div>
          </div>
          <div style={{ background: 'var(--surface)', border: '1px solid var(--border)', 'border-radius': '12px', padding: '14px 16px' }}>
            <div style={{ ...sans, 'font-size': '15px', 'font-weight': '700', color: 'var(--text)', 'line-height': '1.2' }}>{newestDate()}</div>
            <div style={{ ...sans, 'font-size': '13px', 'font-weight': '600', color: 'var(--text2)', 'margin-top': '6px' }}>{props.translate('stats_label_latest_design')}</div>
            <div style={{ ...mono, 'font-size': '10px', color: 'var(--muted)', 'margin-top': '2px' }}>{props.translate('stats_sub_added')}</div>
          </div>
        </div>

        {/* Platform breakdown */}
        <Show when={(stats().platforms ?? []).length > 0}>
          <Card>
            <div style={{ ...mono, 'font-size': '11px', color: 'var(--muted)', 'margin-bottom': '4px' }}>{props.translate('stats_platforms_heading')}</div>
            <div style={{ display: 'flex', 'flex-direction': 'column', gap: '8px' }}>
              <For each={stats().platforms}>{(platform: any) => {
                const pct = () => platformTotal() ? (platform.cnt / platformTotal() * 100) : 0
                const color = PLATFORM_COLORS[platform.source_platform] ?? '#64748b'
                return (
                  <div>
                    <div style={{ display: 'flex', 'justify-content': 'space-between', 'margin-bottom': '4px' }}>
                      <span style={{ ...sans, 'font-size': '12px', color: 'var(--text2)', 'font-weight': '600', 'text-transform': 'capitalize' }}>{platform.source_platform}</span>
                      <span style={{ ...mono, 'font-size': '11px', color: 'var(--muted)' }}>{platform.cnt} · {pct().toFixed(0)}%</span>
                    </div>
                    <div style={{ height: '5px', background: 'var(--bg4)', 'border-radius': '3px', overflow: 'hidden' }}>
                      <div style={{ height: '100%', width: pct() + '%', background: color, 'border-radius': '3px', transition: 'width 0.6s' }} />
                    </div>
                  </div>
                )
              }}</For>
            </div>
          </Card>
        </Show>
      </Show>
    </div>
  )
}

interface PlatformInfo {
  supportsAutoLogin: boolean
  requiresTotpSeed?: boolean
  requiresUsername?: boolean
  hasToken?: boolean
  hint: string
  tokenLabel: string
  tokenPlaceholder: string
}

const PLATFORM_INFO: Record<string, PlatformInfo> = {
  printables: {
    supportsAutoLogin: true,
    hasToken: false,
    hint: 'Free models work without login. For paid/private models, save your email + password for auto-login.',
    tokenLabel: '',
    tokenPlaceholder: '',
  },
  thingiverse: {
    supportsAutoLogin: false,
    requiresUsername: true,
    hint: 'Get your App Token at thingiverse.com/developers → Create App → copy the App Token.',
    tokenLabel: 'App Token',
    tokenPlaceholder: 'Paste your App Token here…',
  },
  makerworld: {
    supportsAutoLogin: true,
    requiresTotpSeed: true,
    hasToken: false,
    hint: 'Use your Bambu Lab account credentials. 2FA must be enabled in your Bambu Lab account (authenticator app - not email). Enter the seed (secret key) from your authenticator setup.',
    tokenLabel: '',
    tokenPlaceholder: '',
  },
  thangs: {
    supportsAutoLogin: true,
    hasToken: false,
    hint: 'Enter your Thangs email and password for automatic login.',
    tokenLabel: '',
    tokenPlaceholder: '',
  },
  cults3d: {
    supportsAutoLogin: true,
    hasToken: true,
    hint: 'API key (cults3d.com/en/api/keys) syncs your collections reliably. Format: username:apikey. Email + password are only needed to download files.',
    tokenLabel: 'API Key',
    tokenPlaceholder: 'username:apikey (e.g. mrdark7199:abc123…)',
  },
  myminifactory: {
    supportsAutoLogin: false,
    requiresUsername: true,
    hint: 'API key + your MyMiniFactory username. No password/login needed.',
    tokenLabel: 'API Key',
    tokenPlaceholder: 'Paste your API key here…',
  },
}

/** Single platform row - shows either a saved-state or an add-form. */
function PlatformRow(props: {
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
          style={{ width: '10px', height: '10px', 'border-radius': '50%', background: isFullyConfigured() ? '#22c55e' : 'var(--border)', 'flex-shrink': '0', cursor: 'help' }} />
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

/** Platforms whose setup runs through the generic wizard (vs. the legacy inline form). */
const WIZARD_PLATFORMS = ['printables', 'thingiverse', 'makerworld', 'thangs', 'cults3d', 'myminifactory']

/**
 * Platforms whose automated login is currently blocked by anti-bot protection
 * (Thangs/Cults3D: Cloudflare Turnstile). The setup button is hidden and a "WIP"
 * badge is shown instead - the wizard/backend code stays intact for later. */
const WIP_PLATFORMS = ['thangs', 'cults3d']

/**
 * Multi-step setup wizard for a platform account (generic, config-driven).
 * Step 1: credentials → validated against the platform (real login attempt).
 * Step 2: sync settings (master auto-sync + collections), then save.
 * On save the backend auto-starts a sync when a sync flag was newly enabled.
 */
function PlatformWizardModal(props: {
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
      resetDirty()
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
      <div onClick={e => e.stopPropagation()}
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

          {/* ── Step 1: credentials ── */}
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

          {/* ── Step 2: sync settings ── */}
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

/** Compact status row for a wizard-managed platform (status + sync/edit/remove actions). */
function PlatformStatusRow(props: {
  platform: string
  account: () => any | null
  onConfigure: () => void
  onRemove: () => void
  onSync: () => void
  syncing: () => boolean
  /** Server-wide library-sync switch; false hides the sync button. */
  syncEnabled: () => boolean
  translate: (k: string, vars?: Record<string, string | number>) => string
}) {
  const t = props.translate
  const color = PLATFORM_COLORS[props.platform] || 'var(--accent)'
  const info = PLATFORM_INFO[props.platform] || { supportsAutoLogin: true } as PlatformInfo
  const configured = () => !!props.account()
  const cooldown = createCooldown()
  const blocked = () => cooldown.remaining() > 0
  // Seeded from the account, which the parent reloads after a triggered sync -
  // the server decides how long the button stays closed, not the click.
  createEffect(() => cooldown.start(props.account()?.sync_cooldown_seconds ?? 0))
  const fullyConfigured = () => {
    const acc = props.account()
    if (!acc) return false
    const hasUser = !!acc.username, hasPw = !!acc.has_password, hasTok = !!acc.token, hasTotp = !!acc.has_totp_secret
    if (info.requiresTotpSeed) return hasUser && hasPw && hasTotp
    if (!info.supportsAutoLogin) return hasTok && (!info.requiresUsername || hasUser)
    if (info.hasToken) return hasUser && hasPw && hasTok
    return hasUser && hasPw
  }

  return (
    <div style={{ border: '1px solid var(--border)', 'border-radius': '12px', padding: '12px 16px', display: 'flex', 'align-items': 'center', gap: '12px', background: 'var(--surface)' }}>
      <div title={t(fullyConfigured() ? 'platform_configured_short' : 'platform_not_configured_short')}
        style={{ width: '10px', height: '10px', 'border-radius': '50%', background: fullyConfigured() ? '#22c55e' : 'var(--border)', 'flex-shrink': '0', cursor: 'help' }} />
      <span style={{ ...mono, 'font-size': '12px', background: color + '22', color: color, 'border-radius': '5px', padding: '2px 9px', 'flex-shrink': '0' }}>
        {props.platform.charAt(0).toUpperCase() + props.platform.slice(1)}
      </span>
      <div style={{ flex: '1' }} />
      <div style={{ display: 'flex', gap: '8px', 'flex-shrink': '0' }}>
        <Show when={configured() && props.syncEnabled()}>
          <button onClick={props.onSync} disabled={props.syncing() || blocked()}
            title={blocked() ? t('sync_cooldown_hint', { min: String(cooldown.minutes()) }) : ''}
            style={{ padding: '5px 12px', background: 'var(--bg3)', border: '1px solid var(--border)', 'border-radius': '7px', color: props.syncing() || blocked() ? 'var(--muted)' : 'var(--text2)', 'font-size': '11px', cursor: props.syncing() || blocked() ? 'not-allowed' : 'pointer', ...sans }}>
            {props.syncing() ? '…' : blocked() ? t('sync_cooldown_short', { min: String(cooldown.minutes()) }) : t('platform_sync_now_btn')}
          </button>
        </Show>
        {/* Thangs/Cults3D: Login derzeit durch Anti-Bot-Schutz (Cloudflare
            Turnstile) blockiert. Setup-Button ausblenden, WIP-Badge zeigen - der
            Wizard-/Backend-Code bleibt für später bestehen. */}
        <Show
          when={!WIP_PLATFORMS.includes(props.platform)}
          fallback={
            <span title={t('platform_wip_hint')}
              style={{ ...mono, padding: '5px 12px', background: 'var(--bg3)', border: '1px dashed var(--border)', 'border-radius': '7px', color: 'var(--muted)', 'font-size': '11px', 'font-weight': '600', cursor: 'help', 'flex-shrink': '0' }}>
              {t('platform_wip_badge')}
            </span>
          }>
          <button onClick={props.onConfigure}
            style={{ padding: '5px 12px', background: 'var(--accent)', border: 'none', 'border-radius': '7px', color: '#fff', 'font-size': '11px', cursor: 'pointer', 'font-weight': '600', ...sans }}>
            {configured() ? t('platform_update_btn') : t('platform_setup_btn')}
          </button>
        </Show>
        <Show when={configured()}>
          <button onClick={props.onRemove}
            style={{ padding: '5px 12px', background: 'var(--danger-bg)', border: '1px solid var(--danger-border)', 'border-radius': '7px', color: 'var(--danger)', 'font-size': '11px', cursor: 'pointer', ...sans }}>
            {t('btn_remove')}
          </button>
        </Show>
      </div>
    </div>
  )
}

/**
 * Platform accounts tab - lists all supported platforms with their configured
 * credentials. Wizard-managed platforms get a {@link PlatformStatusRow} (setup via
 * {@link PlatformWizardModal}); the rest keep the legacy inline {@link PlatformRow}.
 */
function TabPlatforms(props: { user: User; translate: any; showToast: (message: string, variant?: string) => void }) {
  const [accounts, setAccounts] = createSignal<any[]>([])
  const [syncHour, setSyncHour] = createSignal(3)
  // Server-wide switch. It outranks every per-account setting, so while it is
  // off no sync can be started here and the buttons that would try are hidden.
  const [syncEnabled, setSyncEnabled] = createSignal(true)
  const [expandedPlatform, setExpandedPlatform] = createSignal<string | null>(null)
  const [wizardPlatform, setWizardPlatform] = createSignal<string | null>(null)
  const [syncingPlatform, setSyncingPlatform] = createSignal<string | null>(null)

  const load = () =>
    api.getPlatformAccounts(props.user.id)
      .then((r: any) => setAccounts(r.data || []))
      .catch(() => {})

  api.getPublicSettings()
    .then((r: any) => {
      setSyncHour(r.data?.library_sync_hour ?? 3)
      setSyncEnabled((r.data?.library_sync_enabled ?? 1) === 1)
    })
    .catch(() => {})

  load()

  const accountForPlatform = (platform: string) =>
    accounts().find((a: any) => a.platform === platform) || null

  const removeAccount = async (platform: string) => {
    const acc = accountForPlatform(platform)
    if (!acc) return
    try {
      await api.deletePlatformAccount(props.user.id, acc.id)
      props.showToast(props.translate('toast_platform_removed'))
      load()
    } catch {}
  }

  const syncNow = async (platform: string) => {
    setSyncingPlatform(platform)
    try {
      await api.syncPlatformLibrary(props.user.id, platform)
      props.showToast(props.translate('platform_sync_started_toast'))
      // Picks up the cooldown the trigger just started, which closes the button.
      load()
    } catch (failure: unknown) {
      props.showToast(props.translate(errorKey(failure)), 'error')
    } finally { setSyncingPlatform(null) }
  }

  return (
    <div style={{ display: 'flex', 'flex-direction': 'column', gap: '10px' }}>
      <div style={{ ...sans, 'font-size': '12px', color: 'var(--muted)', 'line-height': '1.5' }}>
        {props.translate('platform_accounts_intro')}
      </div>
      <Show when={!syncEnabled()}>
        <DisabledNotice text={props.translate('platform_sync_server_disabled')} />
      </Show>
      <For each={PLATFORMS}>{platform => (
        <Show
          when={WIZARD_PLATFORMS.includes(platform)}
          fallback={
            <PlatformRow
              platform={platform}
              account={() => accountForPlatform(platform)}
              userId={props.user.id}
              syncHour={syncHour}
              syncEnabled={syncEnabled}
              showToast={props.showToast}
              onRefresh={load}
              translate={props.translate}
              isExpanded={() => expandedPlatform() === platform}
              onExpand={() => setExpandedPlatform(platform)}
              onCollapse={() => setExpandedPlatform(null)}
            />
          }>
          <PlatformStatusRow
            platform={platform}
            account={() => accountForPlatform(platform)}
            onConfigure={() => setWizardPlatform(platform)}
            onRemove={() => removeAccount(platform)}
            onSync={() => syncNow(platform)}
            syncing={() => syncingPlatform() === platform}
            syncEnabled={syncEnabled}
            translate={props.translate}
          />
        </Show>
      )}</For>

      <Show when={wizardPlatform()}>
        <PlatformWizardModal
          platform={wizardPlatform()!}
          account={() => accountForPlatform(wizardPlatform()!)}
          userId={props.user.id}
          syncHour={syncHour}
          syncEnabled={syncEnabled}
          onClose={() => setWizardPlatform(null)}
          onSaved={load}
          showToast={props.showToast}
          translate={props.translate}
        />
      </Show>
    </div>
  )
}

const NOTIFICATION_PREFERENCES = [
  { key: 'sync_update',     labelKey: 'notif_pref_sync_update',     descKey: 'notif_pref_sync_update_desc' },
  { key: 'download_done',   labelKey: 'notif_pref_download_done',   descKey: 'notif_pref_download_done_desc' },
  { key: 'download_failed', labelKey: 'notif_pref_download_failed', descKey: 'notif_pref_download_failed_desc' },
  { key: 'design_shared',   labelKey: 'notif_pref_design_shared',   descKey: 'notif_pref_design_shared_desc' },
  { key: 'storage_80',      labelKey: 'notif_pref_storage_80',      descKey: 'notif_pref_storage_80_desc' },
]

function ToggleSwitch(props: { checked: boolean; onChange: (newValue: boolean) => void }) {
  return (
    <div onClick={() => props.onChange(!props.checked)}
      style={{ width: '42px', height: '24px', 'border-radius': '12px', background: props.checked ? 'var(--accent)' : 'var(--bg4)', cursor: 'pointer', position: 'relative', transition: 'background 0.2s', 'flex-shrink': '0' }}>
      <div style={{ position: 'absolute', top: '3px', left: props.checked ? '21px' : '3px', width: '18px', height: '18px', 'border-radius': '50%', background: '#fff', transition: 'left 0.2s', 'box-shadow': '0 1px 3px rgba(0,0,0,0.3)' }} />
    </div>
  )
}

/**
 * Notification preferences tab - a list of toggles for each notification
 * category. Changes are persisted to the API immediately on toggle.
 */
function TabNotifications(props: { user: User; translate: any }) {
  const [prefs, setPrefs] = createSignal<Record<string, number | null>>({})
  const [loaded, setLoaded] = createSignal(false)

  api.getNotifPrefs(props.user.id)
    .then((response: any) => { setPrefs(response.data || {}); setLoaded(true) })
    .catch(() => setLoaded(true))

  const savePrefs = async (updated: Record<string, number | null>) => {
    try {
      await api.saveNotifPrefs(props.user.id, {
        sync_update:       updated.sync_update       ?? 1,
        download_done:     updated.download_done     ?? 1,
        download_failed:   updated.download_failed   ?? 1,
        design_shared:     updated.design_shared     ?? 1,
        storage_80:        updated.storage_80        ?? 1,
        sync_min_age_days: updated.sync_min_age_days ?? 7,
      })
    } catch (_error) {}
  }

  const toggle = async (key: string, newValue: boolean) => {
    const updated = { ...prefs(), [key]: newValue ? 1 : 0 }
    setPrefs(updated)
    await savePrefs(updated)
    resetDirty()
  }

  return (
    <div style={{ display: 'flex', 'flex-direction': 'column', gap: '10px' }}>
      <div style={{ ...sans, 'font-size': '12px', color: 'var(--muted)', 'line-height': '1.5', 'margin-bottom': '4px' }}>
        {props.translate('notif_tab_intro')}
      </div>
      <Show when={loaded()}>
        <For each={NOTIFICATION_PREFERENCES}>{(preference) => (
          <div style={{ display: 'flex', 'align-items': 'center', gap: '14px', background: 'var(--surface)', border: '1px solid var(--border)', 'border-radius': '12px', padding: '12px 14px' }}>
            <ToggleSwitch checked={(prefs()[preference.key] ?? 1) === 1} onChange={(newValue) => toggle(preference.key, newValue)} />
            <div style={{ flex: '1' }}>
              <div style={{ ...sans, 'font-size': '13px', 'font-weight': '600', color: 'var(--text)' }}>{props.translate(preference.labelKey)}</div>
              <div style={{ ...sans, 'font-size': '11px', color: 'var(--muted)', 'margin-top': '2px' }}>{props.translate(preference.descKey)}</div>
            </div>
          </div>
        )}</For>
      </Show>
    </div>
  )
}


function TabSync(props: { user: User; translate: any; showToast: (message: string, variant?: string) => void }) {
  const [prefs, setPrefs] = createSignal<Record<string, number | null>>({})
  const [enabled, setEnabled] = createSignal(true)
  const [days, setDays] = createSignal('7')
  const [loaded, setLoaded] = createSignal(false)
  const [saving, setSaving] = createSignal(false)
  const [syncingAll, setSyncingAll] = createSignal(false)
  // Server-wide library-sync switch; while it is off the manual trigger below
  // would only be rejected, so it is replaced by the reason.
  const [librarySyncEnabled, setLibrarySyncEnabled] = createSignal(true)
  // Server-wide design-update switch and interval floor. The floor outranks the
  // value below it: the server rejects anything shorter.
  const [updatesEnabled, setUpdatesEnabled] = createSignal(true)
  const [minDays, setMinDays] = createSignal(DESIGN_UPDATE_MIN_DAYS)
  const cooldown = createCooldown()
  const updateAllCooldown = createCooldown()
  const [updatingAll, setUpdatingAll] = createSignal(false)
  // Both runs hit every connected platform at once and block the next one for
  // ten minutes, so they ask first.
  const [confirmSyncAll, setConfirmSyncAll] = createSignal(false)
  const [confirmUpdateAll, setConfirmUpdateAll] = createSignal(false)
  /** Server state of the two buttons: accounts, cooldowns and queue depth. */
  const [syncState, setSyncState] = createSignal<SyncState | null>(null)

  /**
   * Re-reads the server state. Polled while the tab is open because the queue is
   * filled by a background worker: right after the trigger it is still empty,
   * and a button that only watched its own countdown would reopen in the middle
   * of the run it just started.
   */
  const loadSyncState = () => api.getSyncState(props.user.id)
    .then(response => {
      const state = response.data
      setSyncState(state)
      cooldown.start(state.library_cooldown_seconds)
      updateAllCooldown.start(state.update_all_cooldown_seconds)
    })
    .catch(() => {})

  const SYNC_STATE_POLL_MS = 5000
  const statePoll = setInterval(loadSyncState, SYNC_STATE_POLL_MS)
  onCleanup(() => clearInterval(statePoll))

  /** No platform account means nothing to sync - the server has nowhere to go. */
  const hasAccounts = () => syncState()?.has_accounts ?? false
  /** Work still in the queues; a second run would only pile onto it. */
  const queueBusy = () => syncState()?.queue_busy ?? false
  const syncAllBlocked = () => syncingAll() || !hasAccounts() || queueBusy() || cooldown.remaining() > 0
  // The re-download needs the same credentials as the first one, so without a
  // platform account every queued design would fail its way through the queue.
  const updateAllBlocked = () => updatingAll() || !hasAccounts() || queueBusy() || updateAllCooldown.remaining() > 0

  const runSyncAll = async () => {
    setConfirmSyncAll(false)
    setSyncingAll(true)
    try {
      await api.syncAllPlatformLibraries(props.user.id)
      cooldown.startPersisted(props.user.id, MANUAL_SYNC_COOLDOWN_SECONDS)
      props.showToast(props.translate('sync_all_started_toast'))
      loadSyncState()
    } catch (failure: unknown) {
      props.showToast(props.translate(errorKey(failure)), 'error')
    } finally { setSyncingAll(false) }
  }

  /** Queues every design that has a source url for a re-download. */
  const runUpdateAll = async () => {
    setConfirmUpdateAll(false)
    setUpdatingAll(true)
    try {
      const response = await api.syncAll() as { data?: { queued?: number } }
      updateAllCooldown.startPersisted(props.user.id + ':update-all', MANUAL_SYNC_COOLDOWN_SECONDS)
      props.showToast(props.translate('update_all_started_toast', { count: String(response.data?.queued ?? 0) }))
      loadSyncState()
    } catch (failure: unknown) {
      props.showToast(props.translate(errorKey(failure)), 'error')
    } finally { setUpdatingAll(false) }
  }

  api.getPublicSettings()
    .then((response: any) => {
      setLibrarySyncEnabled((response.data?.library_sync_enabled ?? 1) === 1)
      setUpdatesEnabled((response.data?.design_update_enabled ?? 1) === 1)
      setMinDays(response.data?.design_update_min_days ?? DESIGN_UPDATE_MIN_DAYS)
    })
    .catch(() => {})

  // Pick running cooldowns up again: reopening the dialog used to reset them,
  // and with no platform account there was nothing to restore them from either.
  cooldown.resume(props.user.id)
  updateAllCooldown.resume(props.user.id + ':update-all')
  loadSyncState()

  api.getNotifPrefs(props.user.id)
    .then((response: any) => {
      const data = response.data || {}
      setPrefs(data)
      const syncDays = data.sync_min_age_days
      setEnabled(syncDays === undefined ? true : syncDays !== null)
      setDays(String(syncDays ?? minDays()))
      setLoaded(true)
    })
    .catch(() => setLoaded(true))

  const save = async () => {
    const n = parseInt(days(), 10)
    // Lifted rather than rejected: a shorter interval is refused by the server,
    // and the floor is the value the member wanted anyway ("as often as allowed").
    const clamped = isNaN(n) || n < minDays() ? minDays() : n
    setDays(String(clamped))
    const syncDays = enabled() ? clamped : null
    setSaving(true)
    try {
      await api.saveNotifPrefs(props.user.id, {
        sync_update:       prefs().sync_update       ?? 1,
        download_done:     prefs().download_done     ?? 1,
        download_failed:   prefs().download_failed   ?? 1,
        design_shared:     prefs().design_shared     ?? 1,
        storage_80:        prefs().storage_80        ?? 1,
        sync_min_age_days: syncDays,
      })
      setPrefs(p => ({ ...p, sync_min_age_days: syncDays }))
      props.showToast(props.translate('toast_sync_saved'))
      resetDirty()
    } catch (e: unknown) {
      props.showToast(e instanceof Error ? e.message : props.translate('toast_sync_error'), 'error')
    }
    finally { setSaving(false) }
  }

  return (
    <div style={{ display: 'flex', 'flex-direction': 'column', gap: '18px' }}>
      <Show when={loaded()}>
        <div style={{ display: 'flex', 'flex-direction': 'column', gap: '12px' }}>
          {/* The server switch outranks this form, so it says so instead of
              pretending the setting below has an effect. In the danger colours:
              a grey box next to the setting it overrules read like a footnote. */}
          <Show when={!updatesEnabled()}>
            <div style={{ ...sans, 'font-size': '12px', 'line-height': '1.5', background: 'var(--danger-bg)', border: '1px solid var(--danger-border)', 'border-radius': '9px', padding: '10px 12px', color: 'var(--danger)' }}>
              {props.translate('sync_server_disabled')}
            </div>
          </Show>
          <div style={{ display: 'flex', 'align-items': 'center', gap: '14px', background: 'var(--surface)', border: '1px solid var(--border)', 'border-radius': '12px', padding: '12px 14px' }}>
            <div style={{ flex: '1' }}>
              <div style={{ ...sans, 'font-size': '13px', 'font-weight': '600', color: 'var(--text)' }}>{props.translate('sync_enabled_label')}</div>
              <div style={{ ...sans, 'font-size': '11px', color: 'var(--muted)', 'margin-top': '2px' }}>{props.translate('sync_enabled_desc')}</div>
            </div>
            <ToggleSwitch checked={enabled()} onChange={(v) => { setEnabled(v); markDirty() }} />
          </div>

          <Show when={enabled()}>
            <div style={{ display: 'flex', 'align-items': 'center', gap: '14px', background: 'var(--surface)', border: '1px solid var(--border)', 'border-radius': '12px', padding: '12px 14px' }}>
              <div style={{ flex: '1' }}>
                <div style={{ ...sans, 'font-size': '13px', 'font-weight': '600', color: 'var(--text)' }}>{props.translate('sync_interval_label')}</div>
                <div style={{ ...sans, 'font-size': '11px', color: 'var(--muted)', 'margin-top': '2px' }}>{props.translate('sync_interval_desc', { min: String(minDays()) })}</div>
              </div>
              <div style={{ display: 'flex', 'align-items': 'center', gap: '8px' }}>
                <input
                  type="number" min={minDays()}
                  value={days()}
                  onInput={(e) => { setDays(e.currentTarget.value); markDirty() }}
                  style={{
                    width: '70px', 'flex-shrink': '0', 'text-align': 'center', padding: '6px 10px',
                    background: 'var(--input-bg)', 'border-radius': '9px', color: 'var(--text)',
                    ...sans, 'font-size': '13px', outline: 'none', 'box-sizing': 'border-box',
                    border: `1px solid ${parseInt(days(), 10) < minDays() ? 'var(--danger)' : 'var(--border2)'}`,
                  }}
                />
                <span style={{ ...sans, 'font-size': '13px', color: 'var(--muted)' }}>{props.translate('sync_interval_unit')}</span>
              </div>
            </div>
          </Show>
        </div>
        <SaveButton onClick={save} loading={saving()} label={props.translate('btn_save')} />

        {/* ── Manual library sync ── */}
        <div style={{ 'border-top': '1px solid var(--border)', 'padding-top': '18px', display: 'flex', 'flex-direction': 'column', gap: '12px' }}>
          <div>
            <div style={{ ...sans, 'font-size': '13px', 'font-weight': '600', color: 'var(--text)', 'margin-bottom': '4px' }}>{props.translate('sync_now_heading')}</div>
            <div style={{ ...sans, 'font-size': '12px', color: 'var(--muted)', 'line-height': '1.6' }}>
              {props.translate('sync_now_desc')}
            </div>
          </div>
          <Show when={librarySyncEnabled()} fallback={<DisabledNotice text={props.translate('platform_sync_server_disabled')} />}>
            <button
              onClick={() => setConfirmSyncAll(true)}
              disabled={syncAllBlocked()}
              style={{ width: '100%', height: '40px', background: 'var(--bg3)', border: '1px solid var(--border)', 'border-radius': '10px', color: syncAllBlocked() ? 'var(--muted)' : 'var(--text2)', ...sans, 'font-size': '13px', cursor: syncAllBlocked() ? 'not-allowed' : 'pointer' }}>
              {syncingAll() ? '…' : props.translate('sync_all_platforms_btn')}
            </button>
            {/* Why it is closed - one reason, in the order the user can act on it. */}
            <Show when={syncAllBlocked() && !syncingAll()}>
              <div style={{ ...sans, 'font-size': '11px', color: 'var(--muted)', 'margin-top': '-6px' }}>
                {!hasAccounts()
                  ? props.translate('sync_needs_account_hint')
                  : queueBusy()
                    ? props.translate('sync_queue_busy_hint')
                    : props.translate('sync_cooldown_hint', { min: String(cooldown.minutes()) })}
              </div>
            </Show>
          </Show>
        </div>

        {/* ── Update every design that has a source ── */}
        <div style={{ 'border-top': '1px solid var(--border)', 'padding-top': '18px', display: 'flex', 'flex-direction': 'column', gap: '12px' }}>
          <div>
            <div style={{ ...sans, 'font-size': '13px', 'font-weight': '600', color: 'var(--text)', 'margin-bottom': '4px' }}>{props.translate('update_all_heading')}</div>
            <div style={{ ...sans, 'font-size': '12px', color: 'var(--muted)', 'line-height': '1.6' }}>
              {props.translate('update_all_desc')}
            </div>
          </div>
          <Show when={updatesEnabled()} fallback={<DisabledNotice text={props.translate('update_all_server_disabled')} />}>
            <button
              onClick={() => setConfirmUpdateAll(true)}
              disabled={updateAllBlocked()}
              style={{ width: '100%', height: '40px', background: 'var(--bg3)', border: '1px solid var(--border)', 'border-radius': '10px', color: updateAllBlocked() ? 'var(--muted)' : 'var(--text2)', ...sans, 'font-size': '13px', cursor: updateAllBlocked() ? 'not-allowed' : 'pointer' }}>
              {updatingAll() ? '…' : props.translate('update_all_btn')}
            </button>
            <Show when={updateAllBlocked() && !updatingAll()}>
              <div style={{ ...sans, 'font-size': '11px', color: 'var(--muted)', 'margin-top': '-6px' }}>
                {!hasAccounts()
                  ? props.translate('sync_needs_account_hint')
                  : queueBusy()
                    ? props.translate('sync_queue_busy_hint')
                    : props.translate('sync_cooldown_hint', { min: String(updateAllCooldown.minutes()) })}
              </div>
            </Show>
          </Show>
        </div>

        <Show when={confirmSyncAll()}>
          <ConfirmDialog
            title={props.translate('platform_sync_confirm_title')}
            body={props.translate('platform_sync_confirm_body')}
            confirmLabel={props.translate('platform_sync_confirm_start')}
            cancelLabel={props.translate('btn_cancel')}
            onCancel={() => setConfirmSyncAll(false)}
            onConfirm={runSyncAll}
          />
        </Show>
        <Show when={confirmUpdateAll()}>
          <ConfirmDialog
            title={props.translate('update_all_confirm_title')}
            body={props.translate('update_all_confirm_body')}
            confirmLabel={props.translate('platform_sync_confirm_start')}
            cancelLabel={props.translate('btn_cancel')}
            onCancel={() => setConfirmUpdateAll(false)}
            onConfirm={runUpdateAll}
          />
        </Show>
      </Show>
    </div>
  )
}

/** Notice that a server switch has turned this section off. */
function DisabledNotice(props: { text: string }) {
  return (
    <div style={{ ...sans, 'font-size': '12px', 'line-height': '1.5', background: 'var(--danger-bg)', border: '1px solid var(--danger-border)', 'border-radius': '9px', padding: '10px 12px', color: 'var(--danger)' }}>
      {props.text}
    </div>
  )
}

/** Modal yes/no question, used by both manual sync runs. */
function ConfirmDialog(props: { title: string; body: string; confirmLabel: string; cancelLabel: string; onCancel: () => void; onConfirm: () => void }) {
  return (
    <div onClick={props.onCancel}
      style={{ position: 'fixed', inset: '0', background: 'rgba(0,0,0,0.7)', display: 'flex', 'align-items': 'center', 'justify-content': 'center', 'z-index': '10000', 'backdrop-filter': 'blur(4px)' }}>
      <div onClick={e => e.stopPropagation()}
        style={{ background: 'var(--bg2)', border: '1px solid var(--border)', 'border-radius': '16px', padding: '26px', width: '380px', 'box-shadow': '0 30px 80px rgba(0,0,0,0.5)' }}>
        <div style={{ ...sans, 'font-size': '16px', 'font-weight': '700', color: 'var(--text)', 'margin-bottom': '8px' }}>{props.title}</div>
        <div style={{ ...sans, 'font-size': '13px', color: 'var(--text2)', 'line-height': '1.6', 'margin-bottom': '18px' }}>{props.body}</div>
        <div style={{ display: 'flex', gap: '10px', 'justify-content': 'flex-end' }}>
          <button onClick={props.onCancel}
            style={{ padding: '9px 18px', background: 'var(--surface)', border: '1px solid var(--border)', 'border-radius': '10px', color: 'var(--text2)', ...sans, 'font-size': '13px', cursor: 'pointer' }}>
            {props.cancelLabel}
          </button>
          <button onClick={props.onConfirm}
            style={{ padding: '9px 18px', background: 'var(--accent)', border: 'none', 'border-radius': '10px', color: '#fff', ...sans, 'font-size': '13px', 'font-weight': '600', cursor: 'pointer' }}>
            {props.confirmLabel}
          </button>
        </div>
      </div>
    </div>
  )
}

/**
 * Every link the member has handed out, across all of their designs.
 *
 * A link lives on one design, and a design nobody opens is where a forgotten
 * link sits. This is the one place that shows all of them - with what they
 * point at, when they run out and a way to end them.
 */
function TabShareLinks(props: { user: User; translate: any; showToast: (message: string, variant?: string) => void }) {
  const { lang } = useI18n()
  const [links, setLinks] = createSignal<UserShareLink[]>([])
  const [loaded, setLoaded] = createSignal(false)
  const [copied, setCopied] = createSignal('')

  const load = () => api.getUserShareLinks(props.user.id)
    .then(response => setLinks(response.data || []))
    .catch(() => {})
    .finally(() => setLoaded(true))
  load()

  const remove = async (link: UserShareLink) => {
    try {
      await api.deleteShareLink(link.design_id, link.id)
      props.showToast(props.translate('share_link_revoked'))
      load()
    } catch (failure: unknown) {
      props.showToast(props.translate(errorKey(failure)), 'error')
    }
  }

  const copy = async (token: string) => {
    const url = api.shareLinkUrl(token)
    try {
      await navigator.clipboard.writeText(url)
      setCopied(token)
      setTimeout(() => setCopied(current => current === token ? '' : current), 2000)
    } catch { window.prompt(props.translate('share_link_copy'), url) }
  }

  const expiry = (link: UserShareLink) => {
    if (link.expired) return props.translate('share_link_expired')
    if (!link.expires_at) return props.translate('share_link_no_expiry')
    return props.translate('share_link_until', { date: formatDate(link.expires_at, lang()) })
  }

  return (
    <div style={{ display: 'flex', 'flex-direction': 'column', gap: '12px' }}>
      <div style={{ ...sans, 'font-size': '12px', color: 'var(--muted)', 'line-height': '1.5' }}>
        {props.translate('share_links_tab_intro')}
      </div>
      <Show when={loaded()}>
        <Show when={links().length > 0} fallback={
          <div style={{ ...sans, 'font-size': '13px', color: 'var(--muted)', 'text-align': 'center', padding: '26px' }}>
            {props.translate('share_links_tab_empty')}
          </div>
        }>
          <For each={links()}>{link => (
            <div style={{ display: 'flex', 'align-items': 'center', gap: '11px', background: 'var(--surface)', border: '1px solid var(--border)',
              'border-radius': '11px', padding: '11px 14px', opacity: link.expired ? '0.55' : '1' }}>
              <div style={{ flex: '1', 'min-width': '0' }}>
                <div style={{ ...sans, 'font-size': '13px', 'font-weight': '600', color: 'var(--text)', overflow: 'hidden', 'text-overflow': 'ellipsis', 'white-space': 'nowrap' }}>
                  {link.design_name}
                </div>
                <div style={{ ...mono, 'font-size': '11px', color: 'var(--muted)', 'margin-top': '3px' }}>
                  {expiry(link)} · {props.translate('share_link_views', { count: link.view_count })}
                </div>
              </div>
              <button onClick={() => copy(link.token)}
                style={{ padding: '6px 12px', background: 'var(--bg3)', border: '1px solid var(--border2)', 'border-radius': '8px',
                  color: 'var(--text2)', 'font-size': '12px', cursor: 'pointer', ...sans, 'flex-shrink': '0' }}>
                {copied() === link.token ? props.translate('share_link_copied') : props.translate('share_link_copy')}
              </button>
              <button onClick={() => remove(link)}
                style={{ padding: '6px 12px', background: 'var(--danger-bg)', border: '1px solid var(--danger-border)', 'border-radius': '8px',
                  color: 'var(--danger)', 'font-size': '12px', cursor: 'pointer', ...sans, 'flex-shrink': '0' }}>
                {props.translate('btn_delete')}
              </button>
            </div>
          )}</For>
        </Show>
      </Show>
    </div>
  )
}

interface AccountSettingsModalProps {
  user: User
  onClose: () => void
  showToast: (message: string, variant?: string) => void
  onUserUpdate: (u: User) => void
  initialTab?: string
}

/**
 * Full-screen account settings modal with a sidebar tab navigation.
 * Tabs: Account · Password · Security · Appearance · Stats · Platforms · Notifications.
 * Optionally opens on a specific tab via `props.initialTab`.
 */
export function AccountSettingsModal(props: AccountSettingsModalProps) {
  const { translate } = useI18n()
  const [tab, setTab] = createSignal(props.initialTab || 'account')
  const { guardClose } = useUnsavedChanges(translate('confirm_discard_changes'))
  createEffect(() => { document.body.style.overflow = 'hidden'; onCleanup(() => { document.body.style.overflow = '' }) })

  const TABS = createMemo(() => [
    { key: 'account',       label: translate('section_account') },
    { key: 'password',      label: translate('section_password') },
    { key: 'appearance',    label: translate('section_appearance') },
    { key: 'stats',         label: translate('section_stats') },
    { key: 'platforms',     label: translate('section_platforms') },
    { key: 'notifications', label: translate('section_notifications') },
    { key: 'sync',          label: translate('section_sync') },
    { key: 'sharelinks',    label: translate('section_share_links') },
  ])

  const sideButton = (active: boolean): JSX.CSSProperties => ({
    width: '100%', height: '40px', padding: '0 18px',
    background: active ? 'rgba(69,123,157,0.12)' : 'none', border: 'none',
    'border-left': active ? '2px solid var(--accent)' : '2px solid transparent',
    color: active ? 'var(--accent-light)' : 'var(--muted)', 'font-family': "'DM Sans',sans-serif",
    'font-size': '13px', 'font-weight': active ? '600' : '400', cursor: 'pointer',
    'text-align': 'left', transition: 'color 0.15s',
  })

  return (
    <div style={{ position: 'fixed', inset: '0', background: 'rgba(0,0,0,0.78)', display: 'flex', 'align-items': 'center', 'justify-content': 'center', 'z-index': '2000', 'backdrop-filter': 'blur(6px)' }}>
      <div style={{ background: 'var(--bg)', 'border-radius': '20px', width: '860px', height: 'min(96vh, 680px)', display: 'flex', 'flex-direction': 'column', overflow: 'hidden', 'box-shadow': '0 40px 100px rgba(0,0,0,0.5)', border: '1px solid var(--border)' }}>
        <div style={{ position: 'relative', height: '56px', 'border-bottom': '1px solid var(--border)', display: 'flex', 'align-items': 'center', 'justify-content': 'center', 'flex-shrink': '0' }}>
          <span style={{ 'font-family': "'DM Sans',sans-serif", 'font-weight': '700', 'font-size': '15px', color: 'var(--accent)' }}>
            {translate('account_settings_title')}
          </span>
          <button onClick={() => guardClose(props.onClose)} style={{ position: 'absolute', right: '18px', top: '0', height: '100%', display: 'flex', 'align-items': 'center', background: 'none', border: 'none', color: 'var(--muted)', cursor: 'pointer', 'font-size': '20px', 'line-height': '1' }}>×</button>
        </div>
        <div style={{ flex: '1', display: 'flex', overflow: 'hidden' }}>
          <div style={{ width: '190px', background: 'var(--bg2)', 'border-right': '1px solid var(--border)', display: 'flex', 'flex-direction': 'column', 'flex-shrink': '0' }}>
            <div style={{ flex: '1', 'overflow-y': 'auto' }}>
            <For each={TABS()}>{({ key, label }) =>
              <button onClick={() => setTab(key)} style={sideButton(tab() === key)}>{label}</button>
            }</For>
          </div>
        </div>
        <div style={{ flex: '1', 'overflow-y': 'auto', padding: '10px 28px 28px' }} onInput={markDirty} onChange={markDirty}>
          <Show when={tab() === 'account'}>       <TabAccount user={props.user} translate={translate} showToast={props.showToast} onUserUpdate={props.onUserUpdate} /></Show>
          <Show when={tab() === 'password'}>      <TabPassword user={props.user} translate={translate} showToast={props.showToast} /></Show>
          <Show when={tab() === 'appearance'}>    <TabAppearance user={props.user} translate={translate} showToast={props.showToast} onUserUpdate={props.onUserUpdate} /></Show>
          <Show when={tab() === 'stats'}>         <TabStats user={props.user} translate={translate} /></Show>
          <Show when={tab() === 'platforms'}>     <TabPlatforms user={props.user} translate={translate} showToast={props.showToast} /></Show>
          <Show when={tab() === 'notifications'}> <TabNotifications user={props.user} translate={translate} /></Show>
          <Show when={tab() === 'sync'}>          <TabSync user={props.user} translate={translate} showToast={props.showToast} /></Show>
          <Show when={tab() === 'sharelinks'}>    <TabShareLinks user={props.user} translate={translate} showToast={props.showToast} /></Show>
        </div>
        </div>
      </div>
    </div>
  )
}

/**
 * Blocking modal that forces the user to set a new password before continuing.
 * Shown automatically when `user.must_change_password` is true.
 */
export function ForcePasswordChangeModal(props: { user: User; onChanged: () => void }) {
  const { translate } = useI18n()
  const [newPw, setNewPw] = createSignal('')
  const [confirm, setConfirm] = createSignal('')
  const [saving, setSaving] = createSignal(false)
  const [err, setErr] = createSignal('')

  const handle = async () => {
    if (!newPw() || newPw().length < 8) { setErr(translate('validation.password_short')); return }
    if (newPw() !== confirm()) { setErr(translate('validation.password_mismatch')); return }
    setSaving(true); setErr('')
    try { await api.forcePasswordChange(props.user.id, newPw()); props.onChanged() }
    catch (failure: unknown) { setErr(translate(errorKey(failure))) }
    finally { setSaving(false) }
  }

  return (
    <div style={{ position: 'fixed', inset: '0', background: 'rgba(0,0,0,0.85)', display: 'flex', 'align-items': 'center', 'justify-content': 'center', 'z-index': '9999', 'backdrop-filter': 'blur(8px)' }}>
      <div style={{ background: 'var(--bg2)', 'border-radius': '18px', padding: '36px', width: '400px', 'box-shadow': '0 40px 100px rgba(0,0,0,0.6)', border: '1px solid var(--border)' }}>
        <div style={{ 'margin-bottom': '24px', 'text-align': 'center' }}>
          <div style={{ 'font-size': '32px', 'margin-bottom': '8px' }}>🔐</div>
          <div style={{ 'font-family': "'DM Sans',sans-serif", 'font-weight': '700', 'font-size': '18px', color: 'var(--text)', 'margin-bottom': '6px' }}>{translate('force_pwd_title')}</div>
          <div style={{ 'font-family': "'DM Mono',monospace", 'font-size': '12px', color: 'var(--muted)' }}>{translate('force_pwd_hint')}</div>
        </div>
        <Err message={err()} />
        <div style={{ display: 'flex', 'flex-direction': 'column', gap: '12px', 'margin-top': err() ? '12px' : '0' }}>
          <div><label style={lbl}>{translate('field_new_password')}</label><input type="password" style={inp} value={newPw()} onInput={e => setNewPw(e.currentTarget.value)} /></div>
          <div><label style={lbl}>{translate('field_new_password_again')}</label><input type="password" style={inp} value={confirm()} onInput={e => setConfirm(e.currentTarget.value)} onKeyDown={(e: KeyboardEvent) => e.key === 'Enter' && handle()} /></div>
          <button onClick={handle} disabled={saving()}
            style={{ 'margin-top': '4px', height: '42px', background: saving() ? 'var(--bg4)' : 'var(--accent)', border: 'none', 'border-radius': '10px', color: '#fff', 'font-family': "'DM Sans',sans-serif", 'font-size': '14px', 'font-weight': '700', cursor: saving() ? 'not-allowed' : 'pointer' }}>
            {saving() ? translate('btn_saving') : translate('force_pwd_submit')}
          </button>
        </div>
      </div>
    </div>
  )
}
