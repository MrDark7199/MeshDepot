import { createSignal, createEffect, createMemo, onCleanup, Show, For, JSX } from 'solid-js'
import { api } from '../services/api'
import { useI18n } from '../i18n/index'
import { errorKey } from '../utils/errorMessage'
import type { User } from '../types'
import { useUnsavedChanges, markDirty } from '../utils/unsavedChanges'
import { TabAccount } from './account/TabAccount'
import { TabApiKeys } from './account/TabApiKeys'
import { TabPassword } from './account/TabPassword'
import { TabAppearance } from './account/TabAppearance'
import { TabStats } from './account/TabStats'
import { TabPlatforms } from './account/TabPlatforms'
import { TabNotifications } from './account/TabNotifications'
import { TabSync } from './account/TabSync'
import { TabShareLinks } from './account/TabShareLinks'
import { lbl, inp, sans, Err } from './account/shared'


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
    { key: 'apikeys',       label: translate('section_api_keys') },
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
          <Show when={tab() === 'apikeys'}>       <TabApiKeys translate={translate} showToast={props.showToast} /></Show>
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
