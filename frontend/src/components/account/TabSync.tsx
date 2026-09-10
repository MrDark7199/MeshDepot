import { ToggleSwitch } from '../ToggleSwitch'
import { ConfirmDialog, DESIGN_UPDATE_MIN_DAYS, DisabledNotice, SaveButton, sans } from './shared'
import { api } from '../../services/api'
import { Show, createSignal, onCleanup } from 'solid-js'
import type { SyncState, User } from '../../types'
import { MANUAL_SYNC_COOLDOWN_SECONDS, createCooldown } from '../../utils/cooldown'
import { errorKey } from '../../utils/errorMessage'
import { markDirty, resetDirty } from '../../utils/unsavedChanges'

export function TabSync(props: { user: User; translate: any; showToast: (message: string, variant?: string) => void }) {
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

        {/* - Manual library sync - */}
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

        {/* - Update every design that has a source - */}
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
