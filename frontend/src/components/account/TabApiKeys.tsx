import { ConfirmDialog, inpFlexible, inpNarrow, mono, sans } from './shared'
import { useI18n } from '../../i18n/index'
import { api } from '../../services/api'
import { For, Show, createSignal } from 'solid-js'
import { formatDate } from '../../utils/datetime'
import { errorKey } from '../../utils/errorMessage'

/**
 * API keys tab - access for clients that cannot hold a session cookie.
 *
 * The browser extension is the reason they exist: its requests to MeshDepot are
 * cross-site and the session cookie is SameSite=Strict, so it is not sent even
 * with MeshDepot open in the next tab.
 *
 * A created key is shown once and then never again. MeshDepot stores only its
 * hash, so there is nothing to show later; whoever loses one creates another.
 */
export function TabApiKeys(props: { translate: any; showToast: (message: string, variant?: string) => void }) {
  const { lang } = useI18n()
  const [keys, setKeys] = createSignal<any[]>([])
  const [creating, setCreating] = createSignal(false)
  const [freshKey, setFreshKey] = createSignal('')
  const [name, setName] = createSignal('')
  // Days until the key stops working, as typed. Empty means never - which stays
  // possible for a client nobody will be there to renew, but is a decision
  // rather than the default: the plaintext lives in a browser profile, a file on
  // somebody's disk, and a key without an end turns a copied profile into a
  // permanent way in.
  const [lifetime, setLifetime] = createSignal('90')
  // The key being revoked, or null. Revoking cannot be undone - whatever holds
  // that key stops working the moment it happens, and there is no way to put it
  // back - so it is asked about first, like every other irreversible action here.
  const [pendingRevoke, setPendingRevoke] = createSignal<any>(null)

  const load = async () => {
    try {
      const answer = await api.apiKeys() as any
      setKeys(answer?.data || [])
    } catch (_) { setKeys([]) }
  }
  load()

  const create = async () => {
    setCreating(true)
    try {
      // Anything that is not a positive number of days means no expiry.
      const days = Math.max(0, Math.floor(Number(lifetime().trim())) || 0)
      const answer = await api.createApiKey(
        name().trim() || props.translate('api_key_default_name'), days) as any
      // Held in a signal rather than re-fetched: this is the only moment the
      // plaintext exists anywhere outside the client that will use it.
      setFreshKey(answer?.data?.key || '')
      setName('')
      await load()
    } catch (failure: unknown) {
      props.showToast(props.translate(errorKey(failure)), 'error')
    } finally { setCreating(false) }
  }

  const revoke = async (id: number) => {
    try {
      await api.revokeApiKey(id)
      await load()
    } catch (failure: unknown) {
      props.showToast(props.translate(errorKey(failure)), 'error')
    }
  }

  return (
    // Input events are kept inside this tab. The panel around it marks the form
    // dirty on any input so the modal can warn about unsaved changes, but nothing
    // here waits to be saved - creating and revoking take effect at once. Without
    // this, typing a name earned a "you have unsaved changes" on the way out.
    <div style={{ display: 'flex', 'flex-direction': 'column' }}
      onInput={event => event.stopPropagation()}
      onChange={event => event.stopPropagation()}>
      <div style={{ ...mono, 'font-size': '11px', color: 'var(--muted)', 'margin-bottom': '14px', 'line-height': '1.7' }}>
        {props.translate('api_keys_hint')}
      </div>

      <Show when={freshKey()}>
        <div style={{ background: 'var(--bg3)', border: '1px solid var(--accent)', 'border-radius': '10px', padding: '12px', 'margin-bottom': '12px' }}>
          <div style={{ ...sans, 'font-size': '12px', color: 'var(--text)', 'margin-bottom': '6px', 'font-weight': '600' }}>
            {props.translate('api_key_shown_once')}
          </div>
          <div style={{ ...mono, 'font-size': '12px', color: 'var(--accent-light)', 'word-break': 'break-all', 'user-select': 'all' }}>
            {freshKey()}
          </div>
          <button onClick={() => setFreshKey('')}
            style={{ 'margin-top': '8px', background: 'none', border: 'none', ...mono, 'font-size': '11px', color: 'var(--muted)', cursor: 'pointer', padding: '0' }}>
            {props.translate('api_key_hide')}
          </button>
        </div>
      </Show>

      <For each={keys()}>{entry =>
        <div style={{ display: 'flex', 'align-items': 'center', gap: '10px', padding: '8px 0', 'border-bottom': '1px solid var(--border)' }}>
          <div style={{ flex: '1', 'min-width': '0' }}>
            <div style={{ ...sans, 'font-size': '13px', color: 'var(--text)' }}>{entry.name}</div>
            <div style={{ ...mono, 'font-size': '11px', color: 'var(--muted)' }}>
              {entry.prefix}… · {props.translate('api_key_created')}: {formatDate(entry.created_at, lang())}
              {entry.last_used_at ? ' · ' + props.translate('api_key_last_used') + ': ' + formatDate(entry.last_used_at, lang()) : ' · ' + props.translate('api_key_never_used')}
            </div>
            <div style={{ ...mono, 'font-size': '11px', color: entry.expired ? 'var(--danger, #e63946)' : 'var(--muted)' }}>
              {entry.expires_at
                ? (entry.expired ? props.translate('api_key_expired') : props.translate('api_key_expires'))
                  + ': ' + formatDate(entry.expires_at, lang())
                : props.translate('api_key_no_expiry')}
            </div>
          </div>
          <button onClick={() => setPendingRevoke(entry)}
            style={{ 'flex-shrink': '0', padding: '7px 13px', background: 'transparent',
              border: '1px solid var(--danger, #e63946)', 'border-radius': '8px',
              color: 'var(--danger, #e63946)', ...sans, 'font-size': '12px', 'font-weight': '600',
              cursor: 'pointer' }}>
            {props.translate('api_key_revoke')}
          </button>
        </div>
      }</For>

      <div style={{ display: 'flex', gap: '8px', 'margin-top': '10px', 'align-items': 'center' }}>
        <input style={inpFlexible} value={name()}
          onInput={e => setName(e.currentTarget.value)}
          placeholder={props.translate('api_key_name_placeholder')} />
        <input style={inpNarrow}
          value={lifetime()} onInput={e => setLifetime(e.currentTarget.value)}
          inputmode="numeric" placeholder={props.translate('api_key_days_placeholder')} />
        <button onClick={create} disabled={creating()}
          style={{ padding: '9px 14px', 'flex-shrink': '0', background: 'var(--accent)', border: 'none', 'border-radius': '8px', color: '#fff', ...sans, 'font-size': '13px', 'font-weight': '600', cursor: 'pointer', opacity: creating() ? '0.6' : '1' }}>
          {props.translate('api_key_create')}
        </button>
      </div>
      <div style={{ ...mono, 'font-size': '11px', color: 'var(--muted)', 'margin-top': '6px' }}>
        {props.translate('api_key_days_hint')}
      </div>

      <Show when={pendingRevoke()}>
        {entry => (
          <ConfirmDialog
            title={props.translate('api_key_revoke_title')}
            body={props.translate('api_key_revoke_body').replace('{name}', entry().name)}
            confirmLabel={props.translate('api_key_revoke')}
            cancelLabel={props.translate('btn_cancel')}
            onCancel={() => setPendingRevoke(null)}
            onConfirm={() => { const id = entry().id; setPendingRevoke(null); revoke(id) }} />
        )}
      </Show>
    </div>
  )
}
