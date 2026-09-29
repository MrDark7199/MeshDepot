import { createSignal, Show, For } from 'solid-js'
import { useI18n } from '../../i18n/index'
import { formatBytes } from '../../utils/format'
import { sansFont, monoFont, labelStyle, inputStyle } from '../../styles/formStyles'
import { ErrorBox } from '../ErrorBox'

export interface UploadFilesModalProps {
  title: string
  /** The version and notes fields, which only a new version asks for. */
  withVersionFields?: boolean
  version?: string
  onVersionInput?: (value: string) => void
  versionPlaceholder?: string
  notes?: string
  onNotesInput?: (value: string) => void
  error?: string
  busy?: boolean
  /** Filenames the target version already holds, marked as they are picked. */
  takenNames?: string[]
  onClose: () => void
  onSubmit: (files: File[]) => void
}

/**
 * Picks files by click or by dropping them, and lists what was picked before
 * anything is sent. Shared by "upload a new version" and "add files to this
 * version": the two differ only in the version and notes fields.
 */
export function UploadFilesModal(props: UploadFilesModalProps) {
  const { translate } = useI18n()
  const [chosen, setChosen] = createSignal<File[]>([])
  const [dragging, setDragging] = createSignal(false)
  let fileInput: HTMLInputElement | undefined

  // Added to what is there rather than replacing it, so dropping twice collects
  // instead of starting over. Same name and size counts as the same file.
  const add = (files: File[]) => {
    if (files.length === 0) return
    setChosen(current => {
      const known = new Set(current.map(file => file.name + ':' + file.size))
      return [...current, ...files.filter(file => !known.has(file.name + ':' + file.size))]
    })
  }

  const totalBytes = () => chosen().reduce((sum, file) => sum + file.size, 0)

  // The server refuses the whole upload over one name the version already has,
  // so the clash is shown while picking rather than after sending.
  const taken = () => new Set((props.takenNames ?? []).map(name => name.toLowerCase()))
  const isTaken = (file: File) => taken().has(file.name.toLowerCase())
  const clashCount = () => chosen().filter(isTaken).length

  // No closing on a click beside it: that would throw away a file selection
  // someone just assembled. Cancel is the way out.
  return (
    <div style={{ position: 'fixed', inset: '0', background: 'rgba(0,0,0,0.65)', display: 'flex', 'align-items': 'center', 'justify-content': 'center', 'z-index': '600', 'backdrop-filter': 'blur(5px)' }}>
      <div
        style={{ background: 'var(--bg2)', 'border-radius': '18px', padding: '26px 30px', width: '560px', 'max-width': '92vw', 'max-height': '88vh', overflow: 'auto', border: '1px solid var(--border)', 'box-shadow': '0 24px 64px rgba(0,0,0,0.5)' }}>
        <div style={{ ...sansFont, 'font-size': '18px', 'font-weight': '700', color: 'var(--text)', 'margin-bottom': '18px' }}>
          {props.title}
        </div>

        <ErrorBox message={props.error ?? ''} />

        <div style={{ display: 'flex', 'flex-direction': 'column', gap: '14px' }}>
          <div onClick={() => fileInput?.click()}
            onDragOver={event => { event.preventDefault(); setDragging(true) }}
            onDragLeave={() => setDragging(false)}
            onDrop={event => {
              event.preventDefault()
              setDragging(false)
              add(Array.from(event.dataTransfer?.files ?? []))
            }}
            style={{ border: `2px dashed ${dragging() || chosen().length ? 'var(--accent)' : 'var(--border2)'}`, 'border-radius': '11px', padding: '26px', 'text-align': 'center', cursor: 'pointer', background: dragging() ? 'rgba(69,123,157,0.12)' : chosen().length ? 'rgba(69,123,157,0.05)' : 'transparent' }}>
            <input ref={element => (fileInput = element)} type="file" multiple style={{ display: 'none' }}
              onChange={event => { add(Array.from(event.currentTarget.files ?? [])); event.currentTarget.value = '' }} />
            <div style={{ ...monoFont, 'font-size': '13px', color: 'var(--muted)', display: 'flex', 'align-items': 'center', gap: '8px', 'justify-content': 'center' }}>
              <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4" />
                <polyline points="17 8 12 3 7 8" /><line x1="12" y1="3" x2="12" y2="15" />
              </svg>
              {translate('upload_drop_hint')}
            </div>
          </div>

          <Show when={chosen().length}>
            <div style={{ border: '1px solid var(--border)', 'border-radius': '11px', 'max-height': '200px', 'overflow-y': 'auto' }}>
              <For each={chosen()}>{(file, index) => (
                <div style={{ display: 'flex', 'align-items': 'center', gap: '10px', padding: '9px 13px', 'border-bottom': '1px solid var(--border)', background: isTaken(file) ? 'var(--danger-bg)' : 'transparent' }}>
                  <span title={isTaken(file) ? translate('error.filename_exists') : undefined}
                    style={{ ...monoFont, 'font-size': '12px', color: isTaken(file) ? 'var(--danger)' : 'var(--text2)', flex: '1', overflow: 'hidden', 'text-overflow': 'ellipsis', 'white-space': 'nowrap' }}>
                    {file.name}
                  </span>
                  <span style={{ ...monoFont, 'font-size': '11px', color: isTaken(file) ? 'var(--danger)' : 'var(--muted)', 'flex-shrink': '0' }}>
                    {formatBytes(file.size)}
                  </span>
                  <button onClick={() => setChosen(current => current.filter((_, position) => position !== index()))}
                    title={translate('btn_remove')}
                    style={{ background: 'none', border: 'none', color: 'var(--muted)', cursor: 'pointer', padding: '2px 4px', 'font-size': '13px', 'flex-shrink': '0' }}>✕</button>
                </div>
              )}</For>
            </div>
            <div style={{ ...monoFont, 'font-size': '11px', color: clashCount() ? 'var(--danger)' : 'var(--muted)' }}>
              <Show when={clashCount()} fallback={
                translate('upload_selected_summary')
                  .replace('{count}', String(chosen().length))
                  .replace('{size}', formatBytes(totalBytes()))
              }>
                {translate('upload_clash_hint').replace('{count}', String(clashCount()))}
              </Show>
            </div>
          </Show>

          <Show when={props.withVersionFields}>
            <div style={{ display: 'grid', 'grid-template-columns': '1fr 2fr', gap: '11px' }}>
              <div>
                <label style={labelStyle}>{translate('field_version')} *</label>
                <input style={inputStyle} value={props.version ?? ''} placeholder={props.versionPlaceholder}
                  onInput={event => props.onVersionInput?.(event.currentTarget.value)} />
              </div>
              <div>
                <label style={labelStyle}>{translate('field_notes')}</label>
                <input style={inputStyle} value={props.notes ?? ''} placeholder="optional"
                  onInput={event => props.onNotesInput?.(event.currentTarget.value)} />
              </div>
            </div>
          </Show>

          <div style={{ display: 'flex', gap: '11px', 'justify-content': 'flex-end', 'margin-top': '4px' }}>
            <button onClick={props.onClose}
              style={{ padding: '9px 20px', background: 'var(--surface)', border: '1px solid var(--border)', 'border-radius': '10px', color: 'var(--muted)', 'font-size': '14px', cursor: 'pointer', ...sansFont }}>
              {translate('btn_cancel')}
            </button>
            <button onClick={() => props.onSubmit(chosen())} disabled={props.busy || chosen().length === 0 || clashCount() > 0}
              style={{ padding: '9px 24px', background: (props.busy || chosen().length === 0 || clashCount() > 0) ? 'var(--bg4)' : 'var(--accent)', border: (props.busy || chosen().length === 0 || clashCount() > 0) ? '1px solid var(--border2)' : 'none', 'border-radius': '10px', color: (props.busy || chosen().length === 0 || clashCount() > 0) ? 'var(--muted)' : '#fff', ...sansFont, 'font-size': '14px', 'font-weight': '700', cursor: (props.busy || chosen().length === 0 || clashCount() > 0) ? 'not-allowed' : 'pointer' }}>
              {props.busy ? translate('btn_uploading') : translate('btn_upload')}
            </button>
          </div>
        </div>
      </div>
    </div>
  )
}
