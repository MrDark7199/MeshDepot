import { Show } from 'solid-js'
import type { Setter } from 'solid-js'
import type { StlViewerModalProps } from '../components/StlViewer'

export interface ViewerHeaderDeps {
  props: StlViewerModalProps
  translate: (key: any, params?: any) => string
  isLoading: () => boolean
  formatLabel: () => string
  colorGroups: () => { label: string; hex: string }[]
  colorsOpen: () => boolean
  setColorsOpen: Setter<boolean>
  settingsOpen: () => boolean
  setSettingsOpen: Setter<boolean>
  toolsOpen: () => boolean
  setToolsOpen: Setter<boolean>
  /** Two models in the scene; the tools that act on one step aside. */
  isComparing: () => boolean
  measureActive: () => boolean
  resetView: () => void
  downloadFile: () => void
}

/** The viewer's title bar. A plain function, so it stays in the viewer's own owner. */
export function viewerHeader(deps: ViewerHeaderDeps) {
  const { props, translate, isLoading, formatLabel, colorGroups, colorsOpen, setColorsOpen,
    settingsOpen, setSettingsOpen, toolsOpen, setToolsOpen, isComparing, measureActive, resetView,
    downloadFile } = deps
  return (
    <>
      {/* Header */}
      <div style={{ display: 'flex', 'align-items': 'center', gap: '14px', padding: '12px 20px', background: 'rgba(0,0,0,0.6)', 'border-bottom': '1px solid rgba(255,255,255,0.08)', 'flex-shrink': '0' }}>
        <span style={{ 'font-family': "'DM Sans',sans-serif", 'font-size': '15px', 'font-weight': '600', color: '#fff', flex: '1', overflow: 'hidden', 'text-overflow': 'ellipsis', 'white-space': 'nowrap' }}>
          {props.designName}
        </span>
        <Show when={formatLabel()}>
          <span style={{ 'font-family': "'DM Mono',monospace", 'font-size': '11px', background: 'rgba(74,144,217,0.2)', color: '#4a90d9', 'border-radius': '5px', padding: '2px 8px', 'flex-shrink': '0' }}>
            {formatLabel()}
          </span>
        </Show>
        <span style={{ 'font-family': "'DM Mono',monospace", 'font-size': '11px', color: 'rgba(255,255,255,0.4)', 'flex-shrink': '0' }}>
          {translate('viewer_controls_hint')}
        </span>
        {/* Saving the file is not a tool - it does nothing to the model - so it sits
            in the bar rather than in the menu of things that do. */}
        <button onClick={downloadFile} title={translate('viewer_btn_download')}
          style={{ background: 'rgba(255,255,255,0.08)', border: '1px solid rgba(255,255,255,0.15)', 'border-radius': '8px', padding: '6px 12px', color: '#fff', cursor: 'pointer', 'flex-shrink': '0', display: 'flex', 'align-items': 'center' }}>
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4" />
            <polyline points="7 10 12 15 17 10" /><line x1="12" y1="15" x2="12" y2="3" />
          </svg>
        </button>
        {/* Werkzeuge: für alle Typen sichtbar (Foto funktioniert überall). "Messen" ist im Menü
            weiterhin nur für nicht-generierte Meshes (STL/OBJ/3MF) - bei G-code fehlt der Punkt. */}
        {/* Unavailable until the model is there. Every tool behind it works on the
            loaded geometry - measuring needs a surface to pick, splitting needs
            triangles to walk - so offering them mid-load can only disappoint. */}
        <Show when={!isComparing()}>
        <button onClick={() => { if (isLoading()) return; setToolsOpen(o => !o); setColorsOpen(false); setSettingsOpen(false) }}
          disabled={isLoading()}
          title={isLoading() ? translate('viewer_tools_wait') : translate('viewer_btn_tools')}
          style={{ background: toolsOpen() || measureActive() ? 'rgba(74,144,217,0.25)' : 'rgba(255,255,255,0.08)', border: `1px solid ${toolsOpen() || measureActive() ? 'rgba(74,144,217,0.6)' : 'rgba(255,255,255,0.15)'}`, 'border-radius': '8px', padding: '6px 14px', color: '#fff', 'font-size': '12px', cursor: isLoading() ? 'not-allowed' : 'pointer', opacity: isLoading() ? '0.4' : '1', 'font-family': "'DM Sans',sans-serif", 'flex-shrink': '0' }}>
          {translate('viewer_btn_tools')}
        </button>
        </Show>
        <Show when={colorGroups().length > 0 && !isComparing()}>
          <button onClick={() => { setColorsOpen(o => !o); setSettingsOpen(false); setToolsOpen(false) }}
            style={{ background: colorsOpen() ? 'rgba(74,144,217,0.25)' : 'rgba(255,255,255,0.08)', border: `1px solid ${colorsOpen() ? 'rgba(74,144,217,0.6)' : 'rgba(255,255,255,0.15)'}`, 'border-radius': '8px', padding: '6px 14px', color: '#fff', 'font-size': '12px', cursor: 'pointer', 'font-family': "'DM Sans',sans-serif", 'flex-shrink': '0' }}>
            {translate('viewer_btn_colors')}
          </button>
        </Show>
        <button onClick={() => { setSettingsOpen(o => !o); setColorsOpen(false); setToolsOpen(false) }}
          style={{ background: settingsOpen() ? 'rgba(74,144,217,0.25)' : 'rgba(255,255,255,0.08)', border: `1px solid ${settingsOpen() ? 'rgba(74,144,217,0.6)' : 'rgba(255,255,255,0.15)'}`, 'border-radius': '8px', padding: '6px 14px', color: '#fff', 'font-size': '12px', cursor: 'pointer', 'font-family': "'DM Sans',sans-serif", 'flex-shrink': '0' }}>
          {translate('viewer_btn_settings')}
        </button>
        <button onClick={resetView} title={translate('viewer_btn_reset')}
          style={{ background: 'rgba(255,255,255,0.08)', border: '1px solid rgba(255,255,255,0.15)', 'border-radius': '8px', padding: '6px 14px', color: '#fff', 'font-size': '12px', cursor: 'pointer', 'font-family': "'DM Sans',sans-serif", 'flex-shrink': '0' }}>
          {translate('viewer_btn_reset')}
        </button>
        <button onClick={props.onClose}
          style={{ background: 'none', border: 'none', color: '#fff', cursor: 'pointer', 'font-size': '24px', 'line-height': '1', 'flex-shrink': '0', opacity: '0.7', padding: '0 4px' }}>
          ×
        </button>
      </div>

    </>
  )
}
