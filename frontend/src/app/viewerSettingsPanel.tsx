import { Show } from 'solid-js'
import type { JSX } from 'solid-js'

export interface SettingsPanelDeps {
  translate: (key: any, params?: any) => string
  settingsOpen: () => boolean
  isGcode: () => boolean
  bgColor: () => string
  applyBgColor: (hex: string) => void
  gridOn: () => boolean
  setGrid: (on: boolean) => void
  bedOn: () => boolean
  setBed: (on: boolean) => void
  bedW: () => number
  bedL: () => number
  applyBedSize: (which: 'w' | 'l', raw: number) => void
  bedColor: () => string
  applyBedColor: (raw: string) => void
  gcodeMode: () => 'lines' | 'solid'
  applyGcodeMode: (mode: 'lines' | 'solid') => void
  gcodeLineColor: () => 'heat' | 'custom'
  applyGcodeLineColor: (mode: 'heat' | 'custom') => void
  gcodeColor: () => string
  applyGcodeColor: (raw: string) => void
  segBtnStyle: (active: boolean) => JSX.CSSProperties
  colorInputStyle: JSX.CSSProperties
}

/**
 * The viewer's settings panel: background, grid and build plate, plus the
 * G-code display options. A plain function, like splitPanel.
 */
export function settingsPanel(deps: SettingsPanelDeps) {
  const { translate, settingsOpen, isGcode, bgColor, applyBgColor, gridOn, setGrid, bedOn, setBed,
    bedW, bedL, applyBedSize, bedColor, applyBedColor, gcodeMode, applyGcodeMode, gcodeLineColor,
    applyGcodeLineColor, gcodeColor, applyGcodeColor, segBtnStyle, colorInputStyle } = deps
  return (
    <>
        {/* Settings panel - background colour + optional grid / build-plate overlays */}
        <Show when={settingsOpen()}>
          <div style={{ position: 'absolute', top: '14px', right: '14px', background: 'rgba(13,17,23,0.88)', border: '1px solid rgba(255,255,255,0.12)', 'border-radius': '10px', padding: '12px 14px', display: 'flex', 'flex-direction': 'column', gap: '12px', 'min-width': '210px', 'max-width': '260px', 'backdrop-filter': 'blur(6px)', 'box-shadow': '0 8px 24px rgba(0,0,0,0.4)', 'z-index': '6' }}>
            <span style={{ 'font-family': "'DM Sans',sans-serif", 'font-size': '12px', 'font-weight': '600', color: 'rgba(255,255,255,0.85)' }}>
              {translate('viewer_settings_title')}
            </span>
            {/* Darstellung - nur für generierte Strukturen (G-code): Linien vs. Solid + Farbe. */}
            <Show when={isGcode()}>
              <div style={{ display: 'flex', 'flex-direction': 'column', gap: '8px', 'padding-bottom': '10px', 'border-bottom': '1px solid rgba(255,255,255,0.1)' }}>
                <span style={{ 'font-family': "'DM Sans',sans-serif", 'font-size': '12px', color: 'rgba(255,255,255,0.75)' }}>{translate('viewer_gcode_display')}</span>
                <div style={{ display: 'flex', gap: '6px' }}>
                  <button onClick={() => applyGcodeMode('lines')} style={segBtnStyle(gcodeMode() === 'lines')}>{translate('viewer_gcode_lines')}</button>
                  <button onClick={() => applyGcodeMode('solid')} style={segBtnStyle(gcodeMode() === 'solid')}>{translate('viewer_gcode_solid')}</button>
                </div>
                {/* Linien: Höhen-Gradient („Heat") oder eigene Farbe. */}
                <Show when={gcodeMode() === 'lines'}>
                  <div style={{ display: 'flex', 'align-items': 'center', 'justify-content': 'space-between', gap: '10px' }}>
                    <div style={{ display: 'flex', gap: '6px', flex: '1' }}>
                      <button onClick={() => applyGcodeLineColor('heat')} style={segBtnStyle(gcodeLineColor() === 'heat')}>{translate('viewer_gcode_heat')}</button>
                      <button onClick={() => applyGcodeLineColor('custom')} style={segBtnStyle(gcodeLineColor() === 'custom')}>{translate('viewer_gcode_custom')}</button>
                    </div>
                    <Show when={gcodeLineColor() === 'custom'}>
                      <input type="color" value={gcodeColor()} onInput={e => applyGcodeColor(e.currentTarget.value)} style={colorInputStyle} />
                    </Show>
                  </div>
                </Show>
                {/* Solid: feste Farbe. */}
                <Show when={gcodeMode() === 'solid'}>
                  <label style={{ display: 'flex', 'align-items': 'center', 'justify-content': 'space-between', gap: '10px', cursor: 'pointer' }}>
                    <span style={{ 'font-family': "'DM Sans',sans-serif", 'font-size': '12px', color: 'rgba(255,255,255,0.75)' }}>{translate('viewer_gcode_color')}</span>
                    <input type="color" value={gcodeColor()} onInput={e => applyGcodeColor(e.currentTarget.value)} style={colorInputStyle} />
                  </label>
                </Show>
              </div>
            </Show>
            {/* Background colour */}
            <label style={{ display: 'flex', 'align-items': 'center', 'justify-content': 'space-between', gap: '10px', cursor: 'pointer' }}>
              <span style={{ 'font-family': "'DM Sans',sans-serif", 'font-size': '12px', color: 'rgba(255,255,255,0.75)' }}>{translate('viewer_bg_title')}</span>
              <input type="color" value={bgColor()} onInput={e => applyBgColor(e.currentTarget.value)}
                style={{ width: '26px', height: '26px', 'min-width': '26px', padding: '0', border: '1px solid rgba(255,255,255,0.2)', 'border-radius': '6px', background: 'none', cursor: 'pointer' }} />
            </label>
            {/* Grid (XYZ) overlay */}
            <label style={{ display: 'flex', 'align-items': 'center', 'justify-content': 'space-between', gap: '10px', cursor: 'pointer' }}>
              <span style={{ 'font-family': "'DM Sans',sans-serif", 'font-size': '12px', color: 'rgba(255,255,255,0.75)' }}>{translate('viewer_grid_toggle')}</span>
              <input type="checkbox" checked={gridOn()} onChange={e => setGrid(e.currentTarget.checked)}
                style={{ width: '16px', height: '16px', 'accent-color': '#4a90d9', cursor: 'pointer' }} />
            </label>
            {/* Build-plate overlay */}
            <label style={{ display: 'flex', 'align-items': 'center', 'justify-content': 'space-between', gap: '10px', cursor: 'pointer' }}>
              <span style={{ 'font-family': "'DM Sans',sans-serif", 'font-size': '12px', color: 'rgba(255,255,255,0.75)' }}>{translate('viewer_bed_toggle')}</span>
              <input type="checkbox" checked={bedOn()} onChange={e => setBed(e.currentTarget.checked)}
                style={{ width: '16px', height: '16px', 'accent-color': '#4a90d9', cursor: 'pointer' }} />
            </label>
            {/* Build-plate footprint (width × length in mm) */}
            <div style={{ display: 'flex', 'align-items': 'center', gap: '6px', 'padding-left': '4px', opacity: bedOn() ? '1' : '0.5' }}>
              <span style={{ 'font-family': "'DM Sans',sans-serif", 'font-size': '11px', color: 'rgba(255,255,255,0.55)', 'flex-shrink': '0' }}>{translate('viewer_bed_size')}</span>
              <input type="number" min="10" max="2000" value={bedW()} onChange={e => applyBedSize('w', parseFloat(e.currentTarget.value))}
                title={translate('viewer_bed_width')} class="stlv-num"
                style={{ width: '52px', padding: '3px 6px', background: 'rgba(255,255,255,0.06)', border: '1px solid rgba(255,255,255,0.15)', 'border-radius': '6px', color: '#fff', 'font-size': '12px', 'font-family': "'DM Mono',monospace", 'text-align': 'right', 'color-scheme': 'dark' }} />
              <span style={{ 'font-family': "'DM Sans',sans-serif", 'font-size': '11px', color: 'rgba(255,255,255,0.55)' }}>×</span>
              <input type="number" min="10" max="2000" value={bedL()} onChange={e => applyBedSize('l', parseFloat(e.currentTarget.value))}
                title={translate('viewer_bed_length')} class="stlv-num"
                style={{ width: '52px', padding: '3px 6px', background: 'rgba(255,255,255,0.06)', border: '1px solid rgba(255,255,255,0.15)', 'border-radius': '6px', color: '#fff', 'font-size': '12px', 'font-family': "'DM Mono',monospace", 'text-align': 'right', 'color-scheme': 'dark' }} />
              <span style={{ 'font-family': "'DM Sans',sans-serif", 'font-size': '11px', color: 'rgba(255,255,255,0.55)' }}>mm</span>
            </div>
            {/* Build-plate colour - visually subordinated under the plate toggle, like the size row. */}
            <label style={{ display: 'flex', 'align-items': 'center', gap: '6px', 'padding-left': '4px', cursor: 'pointer', opacity: bedOn() ? '1' : '0.5' }}>
              <span style={{ 'font-family': "'DM Sans',sans-serif", 'font-size': '11px', color: 'rgba(255,255,255,0.55)', 'flex-shrink': '0' }}>{translate('viewer_bed_color')}</span>
              <input type="color" value={bedColor()} onInput={e => applyBedColor(e.currentTarget.value)} style={colorInputStyle} />
            </label>
          </div>
        </Show>
    </>
  )
}
