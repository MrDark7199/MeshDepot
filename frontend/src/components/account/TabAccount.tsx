import { UserAvatar } from '../UserAvatar'
import { Card, Err, SaveButton, inp, lbl, mono, sans } from './shared'
import { useI18n } from '../../i18n/index'
import { api } from '../../services/api'
import { For, Show, createSignal } from 'solid-js'
import type { User } from '../../types'
import { SELECTABLE_DATE_FORMATS, dateFormat, formatDateExample, languageDatePattern, setDateFormat } from '../../utils/datetime'
import { errorKey } from '../../utils/errorMessage'
import { isFormDirty, markDirty, resetDirty } from '../../utils/unsavedChanges'

/**
 * Account settings tab - displays the user's avatar (with upload/delete),
 * username, email, language and date notation.
 */
export function TabAccount(props: { user: User; translate: any; showToast: (message: string, variant?: string) => void; onUserUpdate: (u: User) => void }) {
  const { lang, setLang, availableLangs, translateDesigns, setTranslateDesigns } = useI18n()
  const [form, setForm] = createSignal({
    name: props.user.name || '', email: props.user.email || '', language: lang(),
    translateDesigns: translateDesigns(),
    // From the module rather than props.user: it is the value actually in force,
    // and a cached user record from an older session may not carry the field yet.
    // An account that has never chosen one starts on whatever its language
    // renders today, so the dropdown opens on the notation actually in use.
    dateFormat: dateFormat() || languageDatePattern(lang()),
  })
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
      await api.updateProfile(props.user.id, { name: form().name, email, language: form().language, date_format: form().dateFormat })
      setLang(form().language)
      setTranslateDesigns(form().translateDesigns)
      // Applied immediately, not on the next load: the examples in this very
      // dropdown would otherwise disagree with the dates on the page behind it.
      setDateFormat(form().dateFormat)
      props.onUserUpdate({ ...props.user, name: form().name, email: email || null, language: form().language, date_format: form().dateFormat })
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
          <label style={lbl}>{props.translate('field_date_format')}</label>
          <select style={inp} value={form().dateFormat} onChange={e => setForm(currentState => ({ ...currentState, dateFormat: e.currentTarget.value }))}>
            <For each={SELECTABLE_DATE_FORMATS}>{pattern =>
              // Every entry shows the same day written its own way, so the choice
              // is made by looking at a date rather than by decoding "MM/DD/YYYY".
              <option value={pattern}>{formatDateExample(pattern, form().language)}</option>
            }</For>
          </select>
          <div style={{ ...mono, 'font-size': '11px', color: 'var(--muted)', 'margin-top': '5px' }}>{props.translate('field_date_format_hint')}</div>
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
