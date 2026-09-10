import { Show } from 'solid-js'

export type CardStatusKind = 'queued' | 'active' | 'updated' | 'no_change' | 'error'

export function CardStatusBar(props: { kind: CardStatusKind; label: string }) {
  const mono = "'DM Mono',monospace"
  return (
    <div style={{ position:'absolute', bottom:'0', left:'0', right:'0', background:'rgba(0,0,0,0.72)', 'backdrop-filter':'blur(4px)', display:'flex', 'align-items':'center', 'justify-content':'center', gap:'7px', padding:'8px 12px' }}>
      <Show when={props.kind === 'queued'}>
        <div style={{ width:'13px', height:'13px', 'flex-shrink':'0', border:'2px solid #64748b', 'border-radius':'50%' }} />
        <span style={{ 'font-family':mono, 'font-size':'11px', color:'#94a3b8', 'font-weight':'600' }}>{props.label}</span>
      </Show>
      <Show when={props.kind === 'active'}>
        <div style={{ width:'13px', height:'13px', 'flex-shrink':'0', border:'2px solid rgba(255,255,255,0.3)', 'border-top-color':'#fff', 'border-radius':'50%', animation:'spin 0.7s linear infinite' }} />
        <span style={{ 'font-family':mono, 'font-size':'11px', color:'#fff', 'font-weight':'600' }}>{props.label}</span>
      </Show>
      <Show when={props.kind === 'updated'}>
        <div style={{ width:'13px', height:'13px', 'flex-shrink':'0', background:'#22c55e', 'border-radius':'50%', display:'flex', 'align-items':'center', 'justify-content':'center', 'font-size':'9px', color:'#fff' }}>✓</div>
        <span style={{ 'font-family':mono, 'font-size':'11px', color:'#22c55e', 'font-weight':'600' }}>{props.label}</span>
      </Show>
      <Show when={props.kind === 'no_change'}>
        <div style={{ width:'13px', height:'13px', 'flex-shrink':'0', background:'#64748b', 'border-radius':'50%', display:'flex', 'align-items':'center', 'justify-content':'center', 'font-size':'9px', color:'#fff' }}>✓</div>
        <span style={{ 'font-family':mono, 'font-size':'11px', color:'#94a3b8', 'font-weight':'600' }}>{props.label}</span>
      </Show>
      <Show when={props.kind === 'error'}>
        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="#ef4444" stroke-width="2.5"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
        <span style={{ 'font-family':mono, 'font-size':'11px', color:'#ef4444', 'font-weight':'600' }}>{props.label}</span>
      </Show>
    </div>
  )
}
