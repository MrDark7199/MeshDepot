import { createSignal } from 'solid-js'
import { api } from '../services/api'
import { monoFont } from '../styles/formStyles'
import type { DesignID } from '../types'
import { For, Show } from 'solid-js'
import { SLICERS, SLICER_FORMATS, lowerExt } from '../constants/fileFormats'
import { sansFont } from '../styles/formStyles'

/**
 * Slicer deep links for the files of the current version.
 *
 * Picking the file comes first: a design usually carries several printable
 * files, and opening whichever one happened to be first is a guess the user
 * cannot correct. With a single eligible file the menu is skipped - there is
 * nothing to choose.
 */
export function SlicerButtons(props: { designId: DesignID; fileVersionId: number; entries: { id: number; filename: string }[] }) {
  const [openFor, setOpenFor] = createSignal<string | null>(null)
  const [busy, setBusy] = createSignal(false)

  /** Fetches a one-time token for the entry and hands it to the slicer. */
  const open = async (scheme: (url: string) => string, entryId: number) => {
    setOpenFor(null)
    setBusy(true)
    try {
      const response = await api.createDownloadToken(props.designId, props.fileVersionId, entryId) as any
      const token = response?.token ?? response?.data?.token
      if (!token) return
      window.location.href = scheme(`${window.location.origin}/api/v1/files/token/${token}`)
    } catch { /* the button stays available for another try */ }
    finally { setBusy(false) }
  }

  const activate = (slicer: typeof SLICERS[number]) => {
    if (props.entries.length === 1) { open(slicer.scheme, props.entries[0].id); return }
    setOpenFor(current => current === slicer.name ? null : slicer.name)
  }

  return (
    <div style={{ display: 'flex', gap: '7px', 'flex-wrap': 'wrap' }}>
      <For each={SLICERS}>{slicer => (
        <div style={{ position: 'relative' }}>
          <button onClick={() => activate(slicer)} disabled={busy()}
            style={{ padding: '7px 15px', background: 'var(--bg4)', border: '1px solid var(--border2)', 'border-radius': '9px', color: busy() ? 'var(--muted)' : 'var(--text)', 'font-size': '13px', cursor: busy() ? 'default' : 'pointer', ...sansFont, display: 'flex', 'align-items': 'center', gap: '7px', 'font-weight': '500', opacity: busy() ? '0.5' : '1' }}>
            <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <polygon points="5 3 19 12 5 21 5 3"/>
            </svg>
            {slicer.name}
          </button>
          <Show when={openFor() === slicer.name}>
            <>
              {/* Click anywhere else closes the menu. */}
              <div onClick={() => setOpenFor(null)} style={{ position: 'fixed', inset: '0', 'z-index': '40' }} />
              <div style={{ position: 'absolute', top: 'calc(100% + 5px)', left: '0', 'z-index': '41', background: 'var(--bg2)', border: '1px solid var(--border)', 'border-radius': '10px', 'box-shadow': '0 12px 30px rgba(0,0,0,0.35)', padding: '5px', 'min-width': '220px', 'max-width': '340px', 'max-height': '260px', 'overflow-y': 'auto' }}>
                <For each={props.entries}>{entry => (
                  <button onClick={() => open(slicer.scheme, entry.id)}
                    style={{ display: 'block', width: '100%', 'text-align': 'left', padding: '7px 9px', background: 'none', border: 'none', 'border-radius': '7px', cursor: 'pointer', ...monoFont, 'font-size': '12px', color: 'var(--text2)', overflow: 'hidden', 'text-overflow': 'ellipsis', 'white-space': 'nowrap' }}>
                    {entry.filename}
                  </button>
                )}</For>
              </div>
            </>
          </Show>
        </div>
      )}</For>
    </div>
  )
}
