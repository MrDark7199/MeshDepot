import { CardStatusBar } from './CardStatusBar'
import type { CardStatusKind } from './CardStatusBar'
import { CARD_H, CARD_W } from '../constants/layout'
import { PLATFORM_COLORS, platformLabel } from '../constants/platforms'
import { useI18n } from '../i18n/index'
import { useAuth } from '../services/AuthContext'
import { api } from '../services/api'
import { For, Show, createSignal, on } from 'solid-js'
import type { Design, SyncStatus, SyncStep } from '../types'
import { GRADIENTS, openDesignInNewWindow, syncStepLabel } from '../utils/designDisplay'
import { displayAuthor, displayName } from '../utils/designText'

export function DesignCard(props: {design:Design; index:number; onOpen:(design:Design)=>void; onSync?:(design:Design)=>void; syncStatus?:SyncStatus; syncProgress?:number; syncStep?:SyncStep}) {
  const { translate, lang, translateDesigns } = useI18n()
  const { user } = useAuth()
  const authorName = () => displayAuthor(props.design, user()?.name)
  const [hovered, setHovered] = createSignal(false)
  const grad = GRADIENTS[props.index % GRADIENTS.length]
  const gradientString = grad.length === 3
    ? `linear-gradient(135deg,${grad[0]},${grad[1]},${grad[2]})`
    : `linear-gradient(135deg,${grad[0]},${grad[1]})`
  const coverUrl = () => props.design.cover_path ? api.coverUrl(props.design.cover_path) : null
  const plat = () => props.design.source_platform

  return (
    <div class="stlv-card"
      onMouseEnter={() => setHovered(true)}
      onMouseLeave={() => setHovered(false)}
      onClick={() => props.onOpen(props.design)}
      onMouseDown={e => { if (e.button === 1) e.preventDefault() }}
      onAuxClick={e => { if (e.button === 1) { e.preventDefault(); openDesignInNewWindow(props.design.id) } }}
      style={{ width:CARD_W, 'border-radius':'18px', overflow:'hidden', background:'var(--bg2)',
        'box-shadow': hovered() ? '0 20px 56px rgba(0,0,0,0.45)' : '0 4px 20px rgba(0,0,0,0.18)',
        'transform': hovered() ? 'translateY(-8px)' : 'none', 'transition':'all 0.22s ease', cursor:'pointer',
        'flex-shrink':'0', border: props.design.is_shared ? '1px solid var(--accent)' : '1px solid var(--border)' }}>

      {/* Cover */}
      <div class="stlv-card-cover" style={{ position:'relative', width:CARD_W, height:CARD_H, background:gradientString }}>
        <Show when={coverUrl()}>
          <img src={coverUrl()!} alt=""
            style={{ width:'100%', height:'100%', 'object-fit':'cover' }}
            onError={e => { (e.target as HTMLImageElement).style.display = 'none' }} />
        </Show>
        <Show when={!coverUrl()}>
          <div style={{ position:'absolute', inset:'0', display:'flex', 'align-items':'center', 'justify-content':'center', 'font-size':'72px', opacity:'0.18' }}>🖨️</div>
        </Show>

        {/* Shared badge */}
        <Show when={props.design.is_shared}>
          <div style={{ position:'absolute', top:'13px', left:'13px', background:'rgba(69,123,157,0.85)', 'border-radius':'8px', padding:'4px 10px', 'font-size':'11px', 'font-weight':'700', color:'#fff', 'font-family':"'DM Mono',monospace", 'backdrop-filter':'blur(4px)', display:'flex', 'align-items':'center', gap:'5px' }}>
            <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="#fff" stroke-width="2.5">
              <path d="M4 12v8a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2v-8"/>
              <polyline points="16 6 12 2 8 6"/><line x1="12" y1="2" x2="12" y2="15"/>
            </svg>
            shared
          </div>
        </Show>
        <Show when={!props.design.is_shared && plat()}>
          <div style={{ position:'absolute', top:'13px', left:'13px', background: PLATFORM_COLORS[plat()!] ?? 'rgba(0,0,0,0.72)', 'border-radius':'8px', padding:'4px 10px', 'font-size':'11px', 'font-weight':'700', color:'#fff', 'font-family':"'DM Mono',monospace", 'backdrop-filter':'blur(4px)' }}>
            {platformLabel(plat()!, translate)}
          </div>
        </Show>
        <Show when={props.design.is_hidden}>
          <div style={{ position:'absolute', top:'13px', right:'13px', background:'rgba(168,85,247,0.85)', 'border-radius':'8px', padding:'4px 10px', 'font-size':'11px', 'font-weight':'700', color:'#fff', 'font-family':"'DM Mono',monospace", 'backdrop-filter':'blur(4px)', display:'flex', 'align-items':'center', gap:'5px' }}>
            <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="#fff" stroke-width="2.5"><path d="M17.94 17.94A10.07 10.07 0 0 1 12 20c-7 0-11-8-11-8a18.45 18.45 0 0 1 5.06-5.94"/><path d="M9.9 4.24A9.12 9.12 0 0 1 12 4c7 0 11 8 11 8a18.5 18.5 0 0 1-2.16 3.19"/><line x1="1" y1="1" x2="23" y2="23"/></svg>
            hidden
          </div>
        </Show>
        <Show when={(props.design as any).source_deleted}>
          <div style={{ position:'absolute', top:'13px', right:'13px', background:'rgba(230,57,70,0.85)', 'border-radius':'8px', padding:'4px 10px', 'font-size':'11px', 'font-weight':'700', color:'#fff', 'font-family':"'DM Mono',monospace", 'backdrop-filter':'blur(4px)', display:'flex', 'align-items':'center', gap:'5px' }}>
            <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="#fff" stroke-width="2.5"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
            source gone
          </div>
        </Show>

        {/* Rating */}
        <Show when={(props.design.rating ?? 0) > 0}>
          <div style={{ position:'absolute', bottom:'13px', left:'13px', background:'rgba(0,0,0,0.65)', 'border-radius':'7px', padding:'3px 9px', 'font-size':'13px', color:'#f4a261', 'backdrop-filter':'blur(4px)' }}>
            {'★'.repeat(props.design.rating ?? 0)}{'☆'.repeat(5 - (props.design.rating ?? 0))}
          </div>
        </Show>

        {/* Sync-all status overlay */}
        <Show when={props.syncStatus}>
          <CardStatusBar
            kind={props.syncStatus === 'syncing' ? 'active' : props.syncStatus as CardStatusKind}
            label={
              props.syncStatus === 'queued' ? translate('sync_status_queued') :
              props.syncStatus === 'syncing' ? (syncStepLabel(translate, props.syncStep) ?? (props.syncProgress && props.syncProgress > 0 ? `${translate('sync_status_syncing')} ${props.syncProgress}%` : translate('sync_status_syncing'))) :
              props.syncStatus === 'updated' ? translate('sync_status_updated') :
              props.syncStatus === 'no_change' ? translate('sync_status_no_change') :
              translate('sync_status_error')
            }
          />
        </Show>

        {/* Action buttons on hover */}
        <div style={{ position:'absolute', top:'11px', right:'11px', display:'flex', gap:'6px', opacity:hovered()?'1':'0', 'transition':'opacity 0.2s' }}>
          <button onClick={e => { e.stopPropagation(); props.onOpen(props.design) }} title="View"
            style={{ background:'rgba(0,0,0,0.65)', border:'1px solid rgba(255,255,255,0.2)', 'border-radius':'9px', width:'36px', height:'36px', display:'flex', 'align-items':'center', 'justify-content':'center', cursor:'pointer', 'backdrop-filter':'blur(4px)' }}>
            <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="#fff" stroke-width="2.2">
              <path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z"/><circle cx="12" cy="12" r="3"/>
            </svg>
          </button>
        </div>
      </div>

      {/* Footer */}
      <div style={{ padding:'14px 16px 16px', 'border-top':`1px solid ${props.design.is_shared ? 'rgba(69,123,157,0.3)' : 'var(--border)'}` }}>
        <div style={{ 'font-family':"'DM Sans',sans-serif", 'font-size':'16px', 'font-weight':'600', color:'var(--text)', 'white-space':'nowrap', overflow:'hidden', 'text-overflow':'ellipsis' }}>
          {displayName(props.design, lang(), translateDesigns())}
        </div>
        <Show when={authorName()}>
          <div style={{ 'font-family':"'DM Mono',monospace", 'font-size':'12px', color:'var(--muted)', 'margin-top':'3px', overflow:'hidden', 'text-overflow':'ellipsis', 'white-space':'nowrap' }}>
            {translate('card_by')} {authorName()}
          </div>
        </Show>
        <Show when={props.design.is_shared && props.design.shared_by_name}>
          <div style={{ 'font-family':"'DM Mono',monospace", 'font-size':'11px', color:'var(--accent-light)', 'margin-top':'3px' }}>
            ↗ {props.design.shared_by_name}
          </div>
        </Show>
        <Show when={(props.design.tags?.length ?? 0) > 0}>
          <div style={{ display:'flex', 'flex-wrap':'wrap', gap:'5px', 'margin-top':'9px' }}>
            <For each={props.design.tags!.slice(0, 3)}>{tag =>
              <span style={{ 'font-size':'11px', padding:'3px 8px', 'border-radius':'6px', background:tag.color+'33', color:tag.color, 'font-family':"'DM Sans',sans-serif", 'font-weight':'600' }}>
                {tag.name}
              </span>
            }</For>
            <Show when={props.design.tags!.length > 3}>
              <span style={{ 'font-size':'11px', color:'var(--muted)', 'font-family':"'DM Mono',monospace" }}>
                +{props.design.tags!.length - 3}
              </span>
            </Show>
          </div>
        </Show>
      </div>
    </div>
  )
}
