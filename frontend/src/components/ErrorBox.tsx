import { Show } from 'solid-js'
import { sansFont } from '../styles/formStyles'

/** Inline error box. */
export function ErrorBox(props: { message: string }) {
  return (
    <Show when={props.message}>
      <div style={{ background: 'var(--danger-bg)', border: '1px solid var(--danger-border)', 'border-radius': '9px', padding: '9px 13px', color: 'var(--danger)', ...sansFont, 'font-size': '14px', 'margin-bottom': '14px' }}>
        {props.message}
      </div>
    </Show>
  )
}
