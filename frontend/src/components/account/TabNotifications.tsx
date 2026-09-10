import { ToggleSwitch } from '../ToggleSwitch'
import { api } from '../../services/api'
import { For, Show, createSignal } from 'solid-js'
import type { JSX } from 'solid-js'
import type { User } from '../../types'
import { resetDirty } from '../../utils/unsavedChanges'
import { sans } from './shared'

export const NOTIFICATION_PREFERENCES = [
  { key: 'sync_update',      labelKey: 'notif_pref_sync_update',      descKey: 'notif_pref_sync_update_desc' },
  { key: 'download_done',    labelKey: 'notif_pref_download_done',    descKey: 'notif_pref_download_done_desc' },
  { key: 'download_failed',  labelKey: 'notif_pref_download_failed',  descKey: 'notif_pref_download_failed_desc' },
  { key: 'design_shared',    labelKey: 'notif_pref_design_shared',    descKey: 'notif_pref_design_shared_desc' },
  // The member's own storage against their quota - everyone has one of these.
  { key: 'user_storage_80',  labelKey: 'notif_pref_user_storage_80',  descKey: 'notif_pref_user_storage_80_desc' },
  // The server as a whole. Only an administrator can do anything about it, and
  // only an administrator is shown it.
  { key: 'storage_80',       labelKey: 'notif_pref_storage_80',       descKey: 'notif_pref_storage_80_desc', adminOnly: true },
]

/**
 * Notification preferences tab - one row per notification type, with a switch
 * per delivery channel.
 *
 * The two channels are independent: a type can go to the bell, to the inbox, to
 * both, or nowhere. E-mail is only offered when it can actually be delivered -
 * the server has to have a mail server configured and the account an address -
 * because a switch that silently does nothing is worse than one that is not
 * there.
 */
export function TabNotifications(props: { user: User; translate: any }) {
  const [prefs, setPrefs] = createSignal<Record<string, number | null>>({})
  const [loaded, setLoaded] = createSignal(false)
  /** Whether the server can send mail at all; from the public settings. */
  const [mailAvailable, setMailAvailable] = createSignal(false)

  api.getNotifPrefs(props.user.id)
    .then((response: any) => { setPrefs(response.data || {}); setLoaded(true) })
    .catch(() => setLoaded(true))
  api.getPublicSettings()
    .then((response: any) => setMailAvailable(!!response.data?.mail_enabled))
    .catch(() => {})

  const ownAddress = () => (props.user.email || '').trim()
  /** Why e-mail cannot be chosen, or "" when it can. */
  const mailBlockedReason = () => {
    if (!mailAvailable()) return props.translate('notif_email_server_off')
    if (!ownAddress()) return props.translate('notif_email_no_address')
    return ''
  }

  const savePrefs = async (updated: Record<string, number | null>) => {
    try {
      await api.saveNotifPrefs(props.user.id, {
        sync_update:       updated.sync_update       ?? 1,
        download_done:     updated.download_done     ?? 1,
        download_failed:   updated.download_failed   ?? 1,
        design_shared:     updated.design_shared     ?? 1,
        storage_80:        updated.storage_80        ?? 1,
        user_storage_80:   updated.user_storage_80   ?? 1,
        sync_update_email:     updated.sync_update_email     ?? 0,
        download_done_email:   updated.download_done_email   ?? 0,
        download_failed_email: updated.download_failed_email ?? 0,
        design_shared_email:   updated.design_shared_email   ?? 0,
        storage_80_email:      updated.storage_80_email      ?? 0,
        user_storage_80_email: updated.user_storage_80_email ?? 0,
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

  const columnLabel: JSX.CSSProperties = {
    ...sans, 'font-size': '10px', 'font-weight': '600', color: 'var(--muted)',
    'text-transform': 'uppercase', 'letter-spacing': '0.06em', 'text-align': 'center', width: '54px',
  }

  return (
    <div style={{ display: 'flex', 'flex-direction': 'column', gap: '10px' }}>
      <div style={{ ...sans, 'font-size': '12px', color: 'var(--muted)', 'line-height': '1.5', 'margin-bottom': '4px' }}>
        {props.translate('notif_tab_intro')}
      </div>

      {/* Stated once above the list rather than repeated on every greyed-out
          switch: the reason is the same for all of them. */}
      <Show when={loaded() && mailBlockedReason()}>
        <div style={{ ...sans, 'font-size': '12px', color: 'var(--text3)', background: 'var(--bg3)', border: '1px solid var(--border)', 'border-radius': '10px', padding: '10px 12px', 'line-height': '1.5' }}>
          {mailBlockedReason()}
        </div>
      </Show>

      <Show when={loaded()}>
        {/* Column headings, aligned with the switches below. */}
        <div style={{ display: 'flex', 'align-items': 'center', gap: '14px', padding: '0 14px' }}>
          <div style={{ flex: '1' }} />
          <span style={columnLabel}>{props.translate('notif_channel_app')}</span>
          <span style={columnLabel}>{props.translate('notif_channel_email')}</span>
        </div>

        <For each={NOTIFICATION_PREFERENCES.filter(preference => !preference.adminOnly || props.user.admin)}>{(preference) => (
          <div style={{ display: 'flex', 'align-items': 'center', gap: '14px', background: 'var(--surface)', border: '1px solid var(--border)', 'border-radius': '12px', padding: '12px 14px' }}>
            <div style={{ flex: '1' }}>
              <div style={{ ...sans, 'font-size': '13px', 'font-weight': '600', color: 'var(--text)' }}>{props.translate(preference.labelKey)}</div>
              <div style={{ ...sans, 'font-size': '11px', color: 'var(--muted)', 'margin-top': '2px' }}>{props.translate(preference.descKey)}</div>
            </div>
            <div style={{ width: '54px', display: 'flex', 'justify-content': 'center' }}>
              <ToggleSwitch checked={(prefs()[preference.key] ?? 1) === 1}
                onChange={(newValue) => toggle(preference.key, newValue)} />
            </div>
            <div style={{ width: '54px', display: 'flex', 'justify-content': 'center', opacity: mailBlockedReason() ? '0.4' : '1' }}
              title={mailBlockedReason()}>
              <ToggleSwitch checked={(prefs()[preference.key + '_email'] ?? 0) === 1}
                disabled={!!mailBlockedReason()}
                onChange={(newValue) => toggle(preference.key + '_email', newValue)} />
            </div>
          </div>
        )}</For>
      </Show>
    </div>
  )
}
