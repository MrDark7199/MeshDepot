import { Err, SaveButton, inp, lbl } from './shared'
import { api } from '../../services/api'
import { createSignal } from 'solid-js'
import type { User } from '../../types'
import { errorKey } from '../../utils/errorMessage'
import { resetDirty } from '../../utils/unsavedChanges'

/**
 * Password-change tab - three fields (current, new, confirm) with
 * visibility toggles and client-side validation before submission.
 */
export function TabPassword(props: { user: User; translate: any; showToast: (message: string, variant?: string) => void }) {
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
