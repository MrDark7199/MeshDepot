export function NavBtn(props: {children: any; onClick: () => void; title: string; active?: boolean}) {
  return (
    <button onClick={props.onClick} title={props.title}
      style={{ background:props.active?'rgba(69,123,157,0.2)':'var(--surface)', border:`1px solid ${props.active?'var(--accent)':'var(--border)'}`, 'border-radius':'11px', width:'44px', height:'44px', display:'flex', 'align-items':'center', 'justify-content':'center', cursor:'pointer', 'transition':'background 0.15s', 'flex-shrink':'0', color:'var(--muted)' }}>
      {props.children}
    </button>
  )
}
