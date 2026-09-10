import { Card, fmtBytes, mono, sans } from './shared'
import { PLATFORM_COLORS } from '../../constants/platforms'
import { useI18n } from '../../i18n/index'
import { api } from '../../services/api'
import { For, Show, createSignal } from 'solid-js'
import type { User } from '../../types'
import { formatDate } from '../../utils/datetime'

/**
 * Personal stats tab - shows storage usage bar, a summary grid of design/file/
 * tag/collection counts, and a platform-breakdown chart for the current user.
 */
export function TabStats(props: { user: User; translate: any }) {
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
            <div style={{ ...sans, 'font-size': '18px', 'font-weight': '700', color: usedPct() !== null && usedPct()! >= 80 ? 'var(--danger)' : 'var(--text)', 'line-height': '1' }}>
              {fmtBytes(stats().used_bytes)}
              {/* The limit only appears when there is one - "/ ∞" beside every
                  figure would be noise for an account that has none. */}
              <Show when={stats().max_bytes > 0}>
                <span style={{ ...sans, 'font-size': '13px', 'font-weight': '600', color: 'var(--muted)' }}> / {fmtBytes(stats().max_bytes)}</span>
              </Show>
            </div>
            <div style={{ ...sans, 'font-size': '13px', 'font-weight': '600', color: 'var(--text2)', 'margin-top': '6px' }}>{props.translate('stats_label_storage_used')}</div>
            <Show when={stats().max_bytes > 0} fallback={
              <div style={{ ...mono, 'font-size': '10px', color: 'var(--muted)', 'margin-top': '2px' }}>{props.translate('stats_sub_all_versions')}</div>
            }>
              <div style={{ height: '5px', 'border-radius': '3px', background: 'var(--border)', overflow: 'hidden', 'margin-top': '8px' }}>
                <div style={{ height: '100%', width: `${usedPct() ?? 0}%`, background: (usedPct() ?? 0) >= 80 ? 'var(--danger)' : 'var(--accent)' }} />
              </div>
              <div style={{ ...mono, 'font-size': '10px', color: 'var(--muted)', 'margin-top': '4px' }}>
                {props.translate('stats_sub_storage_free', { free: fmtBytes(freeBytes() ?? 0) })}
              </div>
            </Show>
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
