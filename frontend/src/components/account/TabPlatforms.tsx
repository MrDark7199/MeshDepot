import { PlatformRow } from './PlatformRow'
import { PlatformWizardModal } from './PlatformWizardModal'
import type { PlatformInfo } from './platformInfo'
import { PLATFORM_INFO } from './platformInfo'
import { DisabledNotice, mono, sans , WIP_PLATFORMS, WIZARD_PLATFORMS } from './shared'
import { PLATFORMS, PLATFORM_COLORS } from '../../constants/platforms'
import { api } from '../../services/api'
import { For, Show, createEffect, createSignal } from 'solid-js'
import type { User } from '../../types'
import { createCooldown } from '../../utils/cooldown'
import { errorKey } from '../../utils/errorMessage'

/** Compact status row for a wizard-managed platform (status + sync/edit/remove actions). */
export function PlatformStatusRow(props: {
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
  // Credentials that are stored but no longer decryptable (APP_KEY changed
  // after they were saved). They read as "not configured" everywhere, which
  // gives the user nothing to act on - so they get their own state.
  const credentialsUnreadable = () => !!props.account()?.credentials_unreadable
  const fullyConfigured = () => {
    const acc = props.account()
    if (!acc || credentialsUnreadable()) return false
    const hasUser = !!acc.username, hasPw = !!acc.has_password, hasTok = !!acc.token, hasTotp = !!acc.has_totp_secret
    if (info.requiresTotpSeed) return hasUser && hasPw && hasTotp
    if (!info.supportsAutoLogin) return hasTok && (!info.requiresUsername || hasUser)
    if (info.hasToken) return hasUser && hasPw && hasTok
    return hasUser && hasPw
  }

  return (
    <div style={{ border: '1px solid var(--border)', 'border-radius': '12px', padding: '12px 16px', display: 'flex', 'align-items': 'center', gap: '12px', background: 'var(--surface)' }}>
      <div title={t(credentialsUnreadable() ? 'platform_unreadable_short' : fullyConfigured() ? 'platform_configured_short' : 'platform_not_configured_short')}
        style={{ width: '10px', height: '10px', 'border-radius': '50%', background: credentialsUnreadable() ? 'var(--danger)' : fullyConfigured() ? '#22c55e' : 'var(--border)', 'flex-shrink': '0' }} />
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
export function TabPlatforms(props: { user: User; translate: any; showToast: (message: string, variant?: string) => void }) {
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
