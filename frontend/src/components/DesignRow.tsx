import { PLATFORM_COLORS, platformLabel } from '../constants/platforms'
import { useI18n } from '../i18n/index'
import { useAuth } from '../services/AuthContext'
import { api } from '../services/api'
import { For, Show, on } from 'solid-js'
import type { Design, SyncStatus, SyncStep } from '../types'
import { GRADIENTS, openDesignInNewWindow, syncStepLabel } from '../utils/designDisplay'
import { displayAuthor, displayName } from '../utils/designText'

export function DesignRow(props: {design:Design; index:number; onOpen:(design:Design)=>void; onSync?:(design:Design)=>void; syncStatus?:SyncStatus; syncProgress?:number; syncStep?:SyncStep}) {
  const { translate, lang, translateDesigns } = useI18n()
  const { user } = useAuth()
  const authorName = () => displayAuthor(props.design, user()?.name)
  const grad = GRADIENTS[props.index % GRADIENTS.length]
  const gradientString = grad.length === 3
    ? `linear-gradient(135deg,${grad[0]},${grad[1]},${grad[2]})`
    : `linear-gradient(135deg,${grad[0]},${grad[1]})`
  const coverUrl = () => props.design.cover_path ? api.coverUrl(props.design.cover_path) : null
  const plat = () => props.design.source_platform

  return (
    <div onClick={() => props.onOpen(props.design)}
      onMouseDown={e => { if (e.button === 1) e.preventDefault() }}
      onAuxClick={e => { if (e.button === 1) { e.preventDefault(); openDesignInNewWindow(props.design.id) } }}
      style={{ display:'flex', 'align-items':'center', gap:'12px', padding:'8px 12px', background:'var(--bg2)',
        'border-radius':'12px', border: props.design.is_shared ? '1px solid var(--accent)' : '1px solid var(--border)',
        cursor:'pointer', transition:'background 0.15s' }}
      onMouseEnter={e => (e.currentTarget as HTMLElement).style.background = 'var(--bg3)'}
      onMouseLeave={e => (e.currentTarget as HTMLElement).style.background = 'var(--bg2)'}>

      {/* Thumbnail */}
      <div style={{ width:'48px', height:'48px', 'flex-shrink':'0', 'border-radius':'8px', overflow:'hidden', background: coverUrl() ? 'var(--bg3)' : gradientString, position:'relative' }}>
        <Show when={coverUrl()}>
          <img src={coverUrl()!} alt="" style={{ width:'100%', height:'100%', 'object-fit':'cover', 'border-radius':'8px', display:'block' }}
            onError={e => { (e.target as HTMLImageElement).style.display='none' }} />
        </Show>
      </div>

      {/* Platform badge */}
      <Show when={plat()}>
        <span style={{ 'flex-shrink':'0', 'font-size':'10px', 'font-weight':'700', color:'#fff', 'font-family':"'DM Mono',monospace",
          background: props.design.is_shared ? 'rgba(69,123,157,0.85)' : (PLATFORM_COLORS[plat()!] ?? 'rgba(0,0,0,0.45)'),
          'border-radius':'5px', padding:'2px 7px' }}>
          {props.design.is_shared ? 'shared' : platformLabel(plat()!, translate)}
        </span>
      </Show>

      {/* Name + author */}
      <div style={{ flex:'1', 'min-width':'0' }}>
        <div style={{ 'font-family':"'DM Sans',sans-serif", 'font-size':'14px', 'font-weight':'600', color:'var(--text)', overflow:'hidden', 'text-overflow':'ellipsis', 'white-space':'nowrap' }}>
          {displayName(props.design, lang(), translateDesigns())}
        </div>
        <Show when={authorName()}>
          <div style={{ 'font-family':"'DM Mono',monospace", 'font-size':'11px', color:'var(--muted)', overflow:'hidden', 'text-overflow':'ellipsis', 'white-space':'nowrap' }}>
            {translate('card_by')} {authorName()}
          </div>
        </Show>
        <Show when={props.design.is_shared && props.design.shared_by_name}>
          <div style={{ 'font-family':"'DM Mono',monospace", 'font-size':'11px', color:'var(--accent-light)' }}>
            ↗ {props.design.shared_by_name}
          </div>
        </Show>
      </div>

      {/* Tags */}
      <Show when={(props.design.tags?.length ?? 0) > 0}>
        <div class="stlv-row-tags" style={{ display:'flex', gap:'4px', 'flex-shrink':'0' }}>
          <For each={props.design.tags!.slice(0, 3)}>{tag =>
            <span style={{ 'font-size':'10px', padding:'2px 7px', 'border-radius':'5px', background:tag.color+'33', color:tag.color, 'font-family':"'DM Sans',sans-serif", 'font-weight':'600', 'white-space':'nowrap' }}>
              {tag.name}
            </span>
          }</For>
          <Show when={props.design.tags!.length > 3}>
            <span style={{ 'font-size':'10px', color:'var(--muted)', 'font-family':"'DM Mono',monospace" }}>+{props.design.tags!.length - 3}</span>
          </Show>
        </div>
      </Show>

      {/* Rating */}
      <Show when={(props.design.rating ?? 0) > 0}>
        <span style={{ 'flex-shrink':'0', 'font-size':'12px', color:'#f4a261', 'white-space':'nowrap' }}>
          {'★'.repeat(props.design.rating ?? 0)}
        </span>
      </Show>

      {/* source gone */}
      <Show when={(props.design as any).source_deleted}>
        <span style={{ 'flex-shrink':'0', 'font-size':'10px', 'font-weight':'700', color:'#ef4444', 'font-family':"'DM Mono',monospace", 'white-space':'nowrap' }}>✕ source gone</span>
      </Show>

      {/* Sync status pill */}
      <Show when={props.syncStatus}>
        <div style={{ 'flex-shrink':'0', display:'flex', 'align-items':'center', gap:'5px' }}>
          <Show when={props.syncStatus === 'queued'}>
            <div style={{ width:'10px', height:'10px', border:'2px solid #64748b', 'border-radius':'50%' }} />
            <span style={{ 'font-family':"'DM Mono',monospace", 'font-size':'10px', color:'#94a3b8', 'font-weight':'600', 'white-space':'nowrap' }}>{translate('sync_status_queued')}</span>
          </Show>
          <Show when={props.syncStatus === 'syncing'}>
            <div style={{ width:'10px', height:'10px', border:'2px solid #fff', 'border-top-color':'transparent', 'border-radius':'50%', animation:'spin 0.7s linear infinite' }} />
            <span style={{ 'font-family':"'DM Mono',monospace", 'font-size':'10px', color:'#fff', 'font-weight':'600', 'white-space':'nowrap' }}>
              {syncStepLabel(translate, props.syncStep) ?? (props.syncProgress && props.syncProgress > 0 ? `${translate('sync_status_syncing')} ${props.syncProgress}%` : translate('sync_status_syncing'))}
            </span>
          </Show>
          <Show when={props.syncStatus === 'updated'}>
            <span style={{ 'font-size':'10px', color:'#22c55e', 'font-weight':'600', 'font-family':"'DM Mono',monospace", 'white-space':'nowrap' }}>✓ {translate('sync_status_updated')}</span>
          </Show>
          <Show when={props.syncStatus === 'no_change'}>
            <span style={{ 'font-size':'10px', color:'#94a3b8', 'font-family':"'DM Mono',monospace", 'white-space':'nowrap' }}>✓ {translate('sync_status_no_change')}</span>
          </Show>
          <Show when={props.syncStatus === 'error'}>
            <span style={{ 'font-size':'10px', color:'#ef4444', 'font-family':"'DM Mono',monospace", 'white-space':'nowrap' }}>✕ {translate('sync_status_error')}</span>
          </Show>
        </div>
      </Show>

      {/* Action buttons */}
      <div style={{ 'flex-shrink':'0', display:'flex', gap:'6px' }} onClick={e => e.stopPropagation()}>
        <button onClick={e => { e.stopPropagation(); props.onOpen(props.design) }} title={translate('btn_view') || 'View'}
          style={{ background:'var(--bg3)', border:'1px solid var(--border)', 'border-radius':'8px', width:'32px', height:'32px', display:'flex', 'align-items':'center', 'justify-content':'center', cursor:'pointer' }}>
          <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="var(--text2)" stroke-width="2.2">
            <path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z"/><circle cx="12" cy="12" r="3"/>
          </svg>
        </button>
      </div>
    </div>
  )
}
