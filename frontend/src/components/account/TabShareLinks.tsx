import { useI18n } from '../../i18n/index'
import { api } from '../../services/api'
import { For, Show, createSignal } from 'solid-js'
import type { User, UserShareLink } from '../../types'
import { formatDate } from '../../utils/datetime'
import { errorKey } from '../../utils/errorMessage'
import { mono, sans } from './shared'

/**
 * Every link the member has handed out, across all of their designs.
 *
 * A link lives on one design, and a design nobody opens is where a forgotten
 * link sits. This is the one place that shows all of them - with what they
 * point at, when they run out and a way to end them.
 */
export function TabShareLinks(props: { user: User; translate: any; showToast: (message: string, variant?: string) => void }) {
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
