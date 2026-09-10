import { Show } from 'solid-js'
import type { JSX } from 'solid-js'

export const lbl: JSX.CSSProperties = { display: 'block', 'font-family': "'DM Mono',monospace", 'font-size': '11px', color: 'var(--muted)', 'margin-bottom': '5px', 'text-transform': 'uppercase', 'letter-spacing': '0.05em' }

export const inp: JSX.CSSProperties = { width: '100%', background: 'var(--input-bg)', border: '1px solid var(--border2)', 'border-radius': '9px', padding: '9px 12px', color: 'var(--text)', 'font-family': "'DM Sans',sans-serif", 'font-size': '13px', outline: 'none', 'box-sizing': 'border-box' }

/**
 * Variants of `inp` that must win over what it sets.
 *
 * Written as their own objects and passed by name, and that is not cosmetic. A
 * style literal in JSX is compiled into the element's static template, while a
 * spread in the same object is applied afterwards at runtime - so `{...inp,
 * width: '92px'}` ends up as width:92px in the template and then width:100% from
 * `inp` on top of it, whatever the order in the source says. Passing one
 * finished object leaves nothing to be applied afterwards.
 *
 * This cost an afternoon on a field that would not stop filling its row.
 */
export const inpFlexible: JSX.CSSProperties = { ...inp, flex: '1', 'min-width': '0' }

export const inpNarrow: JSX.CSSProperties = { ...inp, width: '92px', 'flex-shrink': '0', 'text-align': 'right' }

export const sans: JSX.CSSProperties = { 'font-family': "'DM Sans',sans-serif" }

export const mono: JSX.CSSProperties = { 'font-family': "'DM Mono',monospace" }

export function Card(props: { children: JSX.Element }) {
  return <div style={{ background: 'var(--surface)', border: '1px solid var(--border)', 'border-radius': '12px', padding: '14px 16px', display: 'flex', 'flex-direction': 'column', gap: '10px' }}>{props.children}</div>
}

/** Conditionally renders a styled error box when `message` is non-empty. */
export function Err(props: { message: string }) {
  return <Show when={props.message}><div style={{ background: 'var(--danger-bg)', border: '1px solid var(--danger-border)', 'border-radius': '8px', padding: '8px 12px', color: 'var(--danger)', ...sans, 'font-size': '13px' }}>{props.message}</div></Show>
}

export function SaveButton(props: { onClick: () => void; loading: boolean; label: string }) {
  return (
    <button onClick={props.onClick} disabled={props.loading}
      style={{ width: '100%', height: '40px', background: props.loading ? 'var(--bg4)' : 'var(--accent)', border: 'none', 'border-radius': '10px', color: '#fff', ...sans, 'font-size': '13px', 'font-weight': '700', cursor: props.loading ? 'not-allowed' : 'pointer' }}>
      {props.loading ? '…' : props.label}
    </button>
  )
}

/** Shortest design update interval the backend accepts (scheduler.DesignUpdateMinDays). */
export const DESIGN_UPDATE_MIN_DAYS = 7

export function fmtBytes(b: number): string {
  if (!b || b === 0) return '0 B'
  if (b >= 1073741824) return (b / 1073741824).toFixed(1) + ' GB'
  if (b >= 1048576) return (b / 1048576).toFixed(1) + ' MB'
  if (b >= 1024) return (b / 1024).toFixed(0) + ' KB'
  return b + ' B'
}

/** Notice that a server switch has turned this section off. */
export function DisabledNotice(props: { text: string }) {
  return (
    <div style={{ ...sans, 'font-size': '12px', 'line-height': '1.5', background: 'var(--danger-bg)', border: '1px solid var(--danger-border)', 'border-radius': '9px', padding: '10px 12px', color: 'var(--danger)' }}>
      {props.text}
    </div>
  )
}

/** Modal yes/no question, used by both manual sync runs. */
export function ConfirmDialog(props: { title: string; body: string; confirmLabel: string; cancelLabel: string; onCancel: () => void; onConfirm: () => void }) {
  return (
    <div onClick={props.onCancel}
      style={{ position: 'fixed', inset: '0', background: 'rgba(0,0,0,0.7)', display: 'flex', 'align-items': 'center', 'justify-content': 'center', 'z-index': '10000', 'backdrop-filter': 'blur(4px)' }}>
      <div onClick={e => e.stopPropagation()}
        style={{ background: 'var(--bg2)', border: '1px solid var(--border)', 'border-radius': '16px', padding: '26px', width: '380px', 'box-shadow': '0 30px 80px rgba(0,0,0,0.5)' }}>
        <div style={{ ...sans, 'font-size': '16px', 'font-weight': '700', color: 'var(--text)', 'margin-bottom': '8px' }}>{props.title}</div>
        <div style={{ ...sans, 'font-size': '13px', color: 'var(--text2)', 'line-height': '1.6', 'margin-bottom': '18px' }}>{props.body}</div>
        <div style={{ display: 'flex', gap: '10px', 'justify-content': 'flex-end' }}>
          <button onClick={props.onCancel}
            style={{ padding: '9px 18px', background: 'var(--surface)', border: '1px solid var(--border)', 'border-radius': '10px', color: 'var(--text2)', ...sans, 'font-size': '13px', cursor: 'pointer' }}>
            {props.cancelLabel}
          </button>
          <button onClick={props.onConfirm}
            style={{ padding: '9px 18px', background: 'var(--accent)', border: 'none', 'border-radius': '10px', color: '#fff', ...sans, 'font-size': '13px', 'font-weight': '600', cursor: 'pointer' }}>
            {props.confirmLabel}
          </button>
        </div>
      </div>
    </div>
  )
}

/** Platforms whose setup runs through the generic wizard (vs. the legacy inline form). */
export const WIZARD_PLATFORMS = ['printables', 'thingiverse', 'makerworld', 'thangs', 'cults3d', 'myminifactory']

/**
 * Platforms whose automated login is currently blocked by anti-bot protection
 * (Thangs/Cults3D: Cloudflare Turnstile). The setup button is hidden and a "WIP"
 * badge is shown instead - the wizard/backend code stays intact for later. */
export const WIP_PLATFORMS = ['thangs', 'cults3d']
