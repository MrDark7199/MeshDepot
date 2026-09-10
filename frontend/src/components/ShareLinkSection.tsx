import { api } from '../services/api'
import type { ShareLink } from '../types'
import { For, Show, createSignal } from 'solid-js'
import { sansFont, monoFont, labelStyle, inputStyle } from '../styles/formStyles'

/**
 * The share-link half of the share tab: create one with a lifetime, copy it,
 * revoke it. Kept out of the page component because it is self-contained and
 * that component is long enough.
 */
export function ShareLinkSection(props: {
  links: ShareLink[]
  days: string
  onDays: (days: string) => void
  creating: boolean
  copiedToken: string
  onCreate: () => void
  onCopy: (token: string) => void
  onDelete: (linkId: number) => void
  translate: (key: string, vars?: Record<string, string | number>) => string
  formatDate: (stamp: string) => string
}) {
  const expiryText = (link: ShareLink) => {
    if (link.expired) return props.translate('share_link_expired')
    if (!link.expires_at) return props.translate('share_link_no_expiry')
    return props.translate('share_link_until', { date: props.formatDate(link.expires_at) })
  }

  return (
    <div style={{ 'border-top': '1px solid var(--border)', 'padding-top': '18px', display: 'flex', 'flex-direction': 'column', gap: '12px' }}>
      <div>
        <div style={{ ...sansFont, 'font-size': '14px', 'font-weight': '700', color: 'var(--text)', 'margin-bottom': '4px' }}>
          {props.translate('share_link_heading')}
        </div>
        <div style={{ ...sansFont, 'font-size': '12px', color: 'var(--muted)', 'line-height': '1.6' }}>
          {props.translate('share_link_hint')}
        </div>
      </div>

      <div style={{ display: 'flex', gap: '10px', 'align-items': 'center' }}>
        {/* Written out rather than spread from inputStyle: inside a JSX style
            object the spread wins over the keys after it, so ...inputStyle
            followed by a width silently kept the shared width: 100%. */}
        <input type="number" min="0" max="3650" value={props.days}
          onInput={event => props.onDays(event.currentTarget.value)}
          style={{
            width: '76px', 'flex-shrink': '0', 'text-align': 'right',
            background: 'var(--input-bg)', border: '1px solid var(--border2)', 'border-radius': '10px',
            padding: '9px 11px', color: 'var(--text)', ...sansFont, 'font-size': '13px',
            outline: 'none', 'box-sizing': 'border-box',
          }} />
        <span style={{ ...sansFont, 'font-size': '13px', color: 'var(--text3)', 'flex-shrink': '0' }}>
          {props.translate('share_link_days_unit')}
        </span>
        <button onClick={props.onCreate} disabled={props.creating}
          style={{ padding: '9px 18px', background: 'var(--accent)', border: 'none', 'border-radius': '10px', color: '#fff',
            'flex-shrink': '0', ...sansFont, 'font-size': '13px', 'font-weight': '600',
            cursor: props.creating ? 'default' : 'pointer', opacity: props.creating ? '0.6' : '1' }}>
          {props.creating ? '…' : props.translate('share_link_create')}
        </button>
      </div>
      <div style={{ ...sansFont, 'font-size': '11px', color: 'var(--muted)', 'margin-top': '-6px' }}>
        {props.translate('share_link_days_hint')}
      </div>

      <For each={props.links}>{link => (
        <div style={{ display: 'flex', 'align-items': 'center', gap: '11px', background: 'var(--surface)', border: '1px solid var(--border)',
          'border-radius': '11px', padding: '10px 14px', opacity: link.expired ? '0.55' : '1' }}>
          <div style={{ flex: '1', 'min-width': '0' }}>
            <div style={{ ...monoFont, 'font-size': '12px', color: 'var(--text2)', overflow: 'hidden', 'text-overflow': 'ellipsis', 'white-space': 'nowrap' }}>
              {api.shareLinkUrl(link.token)}
            </div>
            <div style={{ ...sansFont, 'font-size': '11px', color: 'var(--muted)', 'margin-top': '3px' }}>
              {expiryText(link)} · {props.translate('share_link_views', { count: link.view_count })}
            </div>
          </div>
          <button onClick={() => props.onCopy(link.token)}
            style={{ padding: '6px 13px', background: 'var(--bg3)', border: '1px solid var(--border2)', 'border-radius': '8px',
              color: 'var(--text2)', 'font-size': '12px', cursor: 'pointer', ...sansFont, 'flex-shrink': '0' }}>
            {props.copiedToken === link.token ? props.translate('share_link_copied') : props.translate('share_link_copy')}
          </button>
          <button onClick={() => props.onDelete(link.id)}
            style={{ padding: '6px 13px', background: 'var(--danger-bg)', border: '1px solid var(--danger-border)', 'border-radius': '8px',
              color: 'var(--danger)', 'font-size': '12px', cursor: 'pointer', ...sansFont, 'flex-shrink': '0' }}>
            {props.translate('share_link_revoke')}
          </button>
        </div>
      )}</For>
    </div>
  )
}
