import { ToggleSwitch } from './ToggleSwitch'
import { useI18n } from '../i18n/index'
import { Show } from 'solid-js'
import { cancelDiscard, confirmDiscard, pendingActionSignal, pendingMessageSignal } from '../utils/unsavedChanges'

export function ConfirmDiscardModal() {
  const { translate } = useI18n()
  return (
    <Show when={!!pendingActionSignal()}>
      <div style={{ position:'fixed', inset:'0', background:'rgba(0,0,0,0.55)', display:'flex', 'align-items':'center', 'justify-content':'center', 'z-index':'9999' }}
        onClick={cancelDiscard}>
        <div onClick={(e: MouseEvent) => e.stopPropagation()}
          style={{ background:'var(--bg2)', 'border-radius':'20px', padding:'28px', width:'380px', 'max-width':'calc(100vw - 32px)', border:'1px solid var(--border)', 'box-shadow':'0 20px 60px rgba(0,0,0,0.45)' }}>
          <div style={{ 'font-family':"'DM Sans',sans-serif", 'font-weight':'700', 'font-size':'18px', color:'var(--text)', 'margin-bottom':'12px' }}>
            {translate('confirm_discard_title')}
          </div>
          <p style={{ 'font-family':"'DM Sans',sans-serif", 'font-size':'14px', color:'var(--text2)', margin:'0 0 24px', 'line-height':'1.55' }}>
            {pendingMessageSignal()}
          </p>
          <div style={{ display:'flex', gap:'10px', 'justify-content':'flex-end' }}>
            <button onClick={cancelDiscard}
              style={{ padding:'9px 20px', 'border-radius':'10px', border:'1px solid var(--border)', background:'var(--surface)', color:'var(--text2)', 'font-family':"'DM Sans',sans-serif", 'font-size':'14px', cursor:'pointer' }}>
              {translate('btn_cancel')}
            </button>
            <button onClick={confirmDiscard}
              style={{ padding:'9px 20px', 'border-radius':'10px', border:'1px solid var(--danger-border)', background:'var(--danger-bg)', color:'var(--danger)', 'font-family':"'DM Sans',sans-serif", 'font-size':'14px', 'font-weight':'600', cursor:'pointer' }}>
              {translate('btn_discard')}
            </button>
          </div>
        </div>
      </div>
    </Show>
  )
}

/**
 * Styled confirmation dialog. Replaces the native, blocking `confirm()` that
 * the card-delete action used to call - the app now has exactly one look for
 * "are you sure?", and the text goes through i18n like everything else.
 */

export function ConfirmModal(props: { title: string; body: string; hint?: string; confirmLabel: string; danger?: boolean;
  /** Optional opt-out shown above the buttons, e.g. "also keep this out of the sync". */
  checkboxLabel?: string; checkboxChecked?: boolean; onCheckboxChange?: (value: boolean) => void;
  onClose: () => void; onConfirm: () => void }) {
  const { translate } = useI18n()
  const cancelLabel = () => translate('btn_cancel')
  return (
    <div style={{ position:'fixed', inset:'0', background:'rgba(0,0,0,0.55)', display:'flex', 'align-items':'center', 'justify-content':'center', 'z-index':'500' }}
      onClick={props.onClose}>
      <div onClick={e => e.stopPropagation()}
        style={{ background:'var(--bg2)', 'border-radius':'20px', padding:'30px', width:'420px', 'max-width':'calc(100vw - 32px)', border:'1px solid var(--border)', 'box-shadow':'0 20px 60px rgba(0,0,0,0.45)' }}>
        <div style={{ 'font-family':"'DM Sans',sans-serif", 'font-weight':'700', 'font-size':'18px', color:'var(--text)', 'margin-bottom':'14px' }}>
          {props.title}
        </div>
        <p style={{ 'font-family':"'DM Sans',sans-serif", 'font-size':'14px', color:'var(--text2)', margin:'0 0 10px 0', 'line-height':'1.55' }}>
          {props.body}
        </p>
        <Show when={props.hint}>
          <p style={{ 'font-family':"'DM Sans',sans-serif", 'font-size':'13px', color:'var(--muted)', margin:'0 0 16px 0', 'line-height':'1.5' }}>
            {props.hint}
          </p>
        </Show>
        <Show when={props.checkboxLabel}>
          <div style={{ display:'flex', 'align-items':'center', gap:'11px',
            'font-family':"'DM Sans',sans-serif", 'font-size':'13px', color:'var(--text2)', 'line-height':'1.5' }}>
            <ToggleSwitch checked={props.checkboxChecked ?? false}
              onChange={value => props.onCheckboxChange?.(value)} />
            <span style={{ cursor:'pointer' }}
              onClick={() => props.onCheckboxChange?.(!(props.checkboxChecked ?? false))}>
              {props.checkboxLabel}
            </span>
          </div>
        </Show>
        <div style={{ height:'10px' }} />
        <div style={{ display:'flex', gap:'10px', 'justify-content':'flex-end' }}>
          <button onClick={props.onClose}
            style={{ padding:'9px 20px', 'border-radius':'10px', border:'1px solid var(--border)', background:'var(--surface)', color:'var(--text2)', 'font-family':"'DM Sans',sans-serif", 'font-size':'14px', cursor:'pointer' }}>
            {cancelLabel()}
          </button>
          <button onClick={props.onConfirm}
            style={{ padding:'9px 20px', 'border-radius':'10px', border:'none', background: props.danger ? 'var(--danger)' : 'var(--accent)', color:'#fff', 'font-family':"'DM Sans',sans-serif", 'font-size':'14px', 'font-weight':'600', cursor:'pointer' }}>
            {props.confirmLabel}
          </button>
        </div>
      </div>
    </div>
  )
}
