import { Show } from 'solid-js'

/**
 * Fixed-position toast notification displayed at the top centre of the screen.
 * Renders nothing when `message` is empty.
 */
export function Toast(props: {message:string; variant:string}) {
  const isError = () => props.variant === 'error'
  return (
    <Show when={props.message}>
      <div style={{ position:'fixed', top:'34px', left:'50%', 'transform':'translateX(-50%)', background:isError()?'var(--danger-bg)':'var(--success-bg)', border:`1px solid ${isError()?'var(--danger-border)':'var(--success-border)'}`, 'border-radius':'14px', padding:'13px 32px', color:isError()?'var(--danger)':'var(--success)', 'font-family':"'DM Sans',sans-serif", 'font-size':'16px', 'font-weight':'600', 'z-index':'9999', 'box-shadow':'0 8px 30px rgba(0,0,0,0.4)', 'pointer-events':'none', 'white-space':'nowrap', 'animation':'toastIn 0.18s ease forwards' }}>
        {props.message}
      </div>
    </Show>
  )
}
