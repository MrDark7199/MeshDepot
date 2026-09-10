import { For, Show } from 'solid-js'
import type { Setter } from 'solid-js'

export interface ViewerOverlaysDeps {
  translate: (key: any, params?: any) => string
  platesUI: () => { name: string; thumbnail?: string; colorIdx: number[] }[]
  activePlate: () => number
  selectPlate: (index: number) => void
  colorGroups: () => { label: string; hex: string }[]
  colorsOpen: () => boolean
  setGroupColor: (index: number, hex: string) => void
  resetColors: () => void
  toolsOpen: () => boolean
  isGcode: () => boolean
  measureActive: () => boolean
  setMeasure: (on: boolean) => void
  clearMeasure: () => void
  measureCount: () => number
  measureDist: () => number | null
  /** The floating distance label is a plain element reference, so it is handed over as a callback. */
  setMeasureLabelEl: (element: HTMLDivElement) => void
  openSplitIntro: () => void
  startPhoto: () => void
  takePhoto: () => void
  exitPhoto: () => void
  photoMode: () => 'off' | 'aim' | 'review'
  photoRegion: () => { x: number; y: number; w: number; h: number } | null
  setPhotoRegion: Setter<{ x: number; y: number; w: number; h: number } | null>
  photoDragDown: (event: PointerEvent) => void
  photoDragMove: (event: PointerEvent) => void
  photoDragUp: () => void
}

/**
 * Everything drawn over the canvas: the plate switcher, the colour panel, the
 * tools menu, the measure readout and the photo framing overlay.
 */
export function viewerOverlays(deps: ViewerOverlaysDeps) {
  const { translate, platesUI, activePlate, selectPlate, colorGroups, colorsOpen, setGroupColor,
    resetColors, toolsOpen, isGcode, measureActive, setMeasure, clearMeasure, measureCount, measureDist,
    setMeasureLabelEl, openSplitIntro, startPhoto, takePhoto, exitPhoto, photoMode, photoRegion,
    setPhotoRegion, photoDragDown, photoDragMove, photoDragUp } = deps
  return (
    <>
        {/* Left plate switcher - one card per build plate (thumbnail + name + filament colours) */}
        <Show when={platesUI().length > 1}>
          <div style={{ position: 'absolute', top: '14px', left: '14px', bottom: '14px', width: '154px', display: 'flex', 'flex-direction': 'column', gap: '8px', 'z-index': '6' }}>
            <span style={{ 'font-family': "'DM Sans',sans-serif", 'font-size': '11px', 'font-weight': '600', 'letter-spacing': '0.04em', 'text-transform': 'uppercase', color: 'rgba(255,255,255,0.5)', padding: '0 2px', 'flex-shrink': '0' }}>
              {translate('viewer_plates_title')}
            </span>
            <div style={{ display: 'flex', 'flex-direction': 'column', gap: '8px', 'overflow-y': 'auto', 'padding-right': '2px', 'min-height': '0' }}>
              <For each={platesUI()}>{(plate, i) => (
                <button onClick={() => selectPlate(i())}
                  title={plate.name || `${translate('viewer_plate')} ${i() + 1}`}
                  style={{
                    background: activePlate() === i() ? 'rgba(74,144,217,0.16)' : 'rgba(13,17,23,0.85)',
                    border: `1px solid ${activePlate() === i() ? 'rgba(74,144,217,0.7)' : 'rgba(255,255,255,0.12)'}`,
                    'border-radius': '10px', padding: '8px', display: 'flex', 'flex-direction': 'column', gap: '6px',
                    cursor: 'pointer', 'backdrop-filter': 'blur(6px)', 'flex-shrink': '0', 'text-align': 'left',
                    'box-shadow': activePlate() === i() ? '0 4px 16px rgba(0,0,0,0.35)' : 'none',
                  }}>
                  <div style={{ position: 'relative', width: '100%', 'aspect-ratio': '1 / 1', 'border-radius': '6px', background: 'rgba(255,255,255,0.05)', overflow: 'hidden', display: 'flex', 'align-items': 'center', 'justify-content': 'center' }}>
                    <Show when={plate.thumbnail}
                      fallback={<span style={{ 'font-family': "'DM Sans',sans-serif", 'font-size': '22px', 'font-weight': '700', color: 'rgba(255,255,255,0.35)' }}>{i() + 1}</span>}>
                      <img src={plate.thumbnail} alt="" style={{ width: '100%', height: '100%', 'object-fit': 'contain' }} />
                    </Show>
                    <span style={{ position: 'absolute', top: '4px', left: '5px', 'font-family': "'DM Mono',monospace", 'font-size': '10px', 'font-weight': '600', color: '#fff', background: 'rgba(0,0,0,0.55)', 'border-radius': '4px', padding: '0 5px', 'line-height': '15px' }}>
                      {i() + 1}
                    </span>
                  </div>
                  <div style={{ display: 'flex', 'align-items': 'center', 'justify-content': 'space-between', gap: '6px' }}>
                    <span style={{ 'font-family': "'DM Sans',sans-serif", 'font-size': '11px', color: activePlate() === i() ? '#fff' : 'rgba(255,255,255,0.7)', overflow: 'hidden', 'text-overflow': 'ellipsis', 'white-space': 'nowrap' }}>
                      {plate.name || `${translate('viewer_plate')} ${i() + 1}`}
                    </span>
                    <Show when={plate.colorIdx.length > 0}>
                      <span style={{ display: 'flex', gap: '3px', 'flex-shrink': '0' }}>
                        <For each={plate.colorIdx}>{(idx) => (
                          <span title={colorGroups()[idx]?.hex}
                            style={{ width: '11px', height: '11px', 'border-radius': '50%', background: colorGroups()[idx]?.hex || '#888', border: '1px solid rgba(255,255,255,0.35)', 'box-shadow': '0 0 0 1px rgba(0,0,0,0.3)' }} />
                        )}</For>
                      </span>
                    </Show>
                  </div>
                </button>
              )}</For>
            </div>
          </div>
        </Show>

        {/* Live colour panel - filament groups for multi-colour 3MF, a single swatch otherwise */}
        <Show when={colorsOpen() && colorGroups().length > 0}>
          <div style={{ position: 'absolute', top: '14px', right: '14px', background: 'rgba(13,17,23,0.88)', border: '1px solid rgba(255,255,255,0.12)', 'border-radius': '10px', padding: '12px 14px', display: 'flex', 'flex-direction': 'column', gap: '8px', 'min-width': '180px', 'max-width': '260px', 'backdrop-filter': 'blur(6px)', 'box-shadow': '0 8px 24px rgba(0,0,0,0.4)', 'z-index': '6' }}>
            <div style={{ display: 'flex', 'align-items': 'center', 'justify-content': 'space-between', gap: '10px', 'margin-bottom': '2px' }}>
              <span style={{ 'font-family': "'DM Sans',sans-serif", 'font-size': '12px', 'font-weight': '600', color: 'rgba(255,255,255,0.85)' }}>
                {translate('viewer_colors_title')}
              </span>
              <button onClick={resetColors}
                style={{ background: 'rgba(255,255,255,0.08)', border: '1px solid rgba(255,255,255,0.15)', 'border-radius': '6px', padding: '3px 9px', color: 'rgba(255,255,255,0.8)', 'font-size': '11px', cursor: 'pointer', 'font-family': "'DM Sans',sans-serif", 'flex-shrink': '0' }}>
                {translate('viewer_colors_reset')}
              </button>
            </div>
            <For each={colorGroups()}>{(group, i) => (
              <label style={{ display: 'flex', 'align-items': 'center', gap: '10px', cursor: 'pointer' }}>
                <input type="color" value={group.hex} onInput={e => setGroupColor(i(), e.currentTarget.value)}
                  style={{ width: '26px', height: '26px', 'min-width': '26px', padding: '0', border: '1px solid rgba(255,255,255,0.2)', 'border-radius': '6px', background: 'none', cursor: 'pointer' }} />
                <span style={{ 'font-family': "'DM Sans',sans-serif", 'font-size': '12px', color: 'rgba(255,255,255,0.75)', overflow: 'hidden', 'text-overflow': 'ellipsis', 'white-space': 'nowrap' }}>
                  {group.label}
                </span>
              </label>
            )}</For>
          </div>
        </Show>

        {/* Tools panel - selectable viewer tools */}
        <Show when={toolsOpen()}>
          <div style={{ position: 'absolute', top: '14px', right: '14px', background: 'rgba(13,17,23,0.88)', border: '1px solid rgba(255,255,255,0.12)', 'border-radius': '10px', padding: '12px 14px', display: 'flex', 'flex-direction': 'column', gap: '8px', 'min-width': '200px', 'max-width': '260px', 'backdrop-filter': 'blur(6px)', 'box-shadow': '0 8px 24px rgba(0,0,0,0.4)', 'z-index': '6' }}>
            <span style={{ 'font-family': "'DM Sans',sans-serif", 'font-size': '12px', 'font-weight': '600', color: 'rgba(255,255,255,0.85)' }}>
              {translate('viewer_tools_title')}
            </span>
            {/* Messen: nur für nicht-generierte Meshes (STL/OBJ/3MF) – braucht pickbare Oberflächen. */}
            <Show when={!isGcode()}>
              <button onClick={() => setMeasure(!measureActive())}
                style={{ display: 'flex', 'align-items': 'center', 'justify-content': 'space-between', gap: '10px', background: measureActive() ? 'rgba(74,144,217,0.22)' : 'rgba(255,255,255,0.05)', border: `1px solid ${measureActive() ? 'rgba(74,144,217,0.6)' : 'rgba(255,255,255,0.12)'}`, 'border-radius': '8px', padding: '8px 10px', color: '#fff', cursor: 'pointer', 'font-family': "'DM Sans',sans-serif", 'font-size': '12px' }}>
                <span>📏 {translate('viewer_tool_measure')}</span>
                <span style={{ 'font-size': '10px', color: measureActive() ? '#9ec5ff' : 'rgba(255,255,255,0.4)', 'font-family': "'DM Mono',monospace" }}>
                  {measureActive() ? translate('viewer_tool_on') : translate('viewer_tool_off')}
                </span>
              </button>
            </Show>
            {/* Zerlegen: nur für Meshes. Ein G-code-Pfad hat keine Objekte, die man trennen könnte. */}
            <Show when={!isGcode()}>
              <button onClick={openSplitIntro}
                style={{ display: 'flex', 'align-items': 'center', gap: '10px', background: 'rgba(255,255,255,0.05)', border: '1px solid rgba(255,255,255,0.12)', 'border-radius': '8px', padding: '8px 10px', color: '#fff', cursor: 'pointer', 'font-family': "'DM Sans',sans-serif", 'font-size': '12px' }}>
                <span>✂️ {translate('viewer_tool_split')}</span>
              </button>
            </Show>
            {/* Foto erstellen: für alle Typen (STL/OBJ/3MF + G-code) – arbeitet auf dem Framebuffer. */}
            <button onClick={startPhoto}
              style={{ display: 'flex', 'align-items': 'center', gap: '10px', background: 'rgba(255,255,255,0.05)', border: '1px solid rgba(255,255,255,0.12)', 'border-radius': '8px', padding: '8px 10px', color: '#fff', cursor: 'pointer', 'font-family': "'DM Sans',sans-serif", 'font-size': '12px' }}>
              <span>📷 {translate('viewer_tool_photo')}</span>
            </button>
          </div>
        </Show>

        {/* Measure tool: floating distance label + status HUD */}
        <Show when={measureDist() !== null}>
          <div ref={setMeasureLabelEl}
            style={{ position: 'absolute', display: 'none', transform: 'translate(-50%,-140%)', background: 'rgba(20,16,4,0.9)', border: '1px solid rgba(255,209,63,0.7)', color: '#ffd23f', padding: '3px 8px', 'border-radius': '6px', 'font-family': "'DM Mono',monospace", 'font-size': '12px', 'font-weight': '600', 'white-space': 'nowrap', 'pointer-events': 'none', 'z-index': '7' }}>
            {measureDist()!.toFixed(1)} mm
          </div>
        </Show>
        <Show when={measureActive()}>
          <div style={{ position: 'absolute', bottom: '18px', left: '50%', transform: 'translateX(-50%)', background: 'rgba(13,17,23,0.9)', border: '1px solid rgba(255,255,255,0.14)', 'border-radius': '10px', padding: '8px 14px', display: 'flex', 'align-items': 'center', gap: '14px', 'backdrop-filter': 'blur(6px)', 'box-shadow': '0 8px 24px rgba(0,0,0,0.4)', 'z-index': '7' }}>
            <span style={{ 'font-family': "'DM Sans',sans-serif", 'font-size': '13px', color: '#fff' }}>
              {measureDist() !== null
                ? `${translate('viewer_measure_distance')}: ${measureDist()!.toFixed(1)} mm`
                : measureCount() === 1
                  ? translate('viewer_measure_hint2')
                  : translate('viewer_measure_hint1')}
            </span>
            <Show when={measureCount() > 0}>
              <button onClick={clearMeasure}
                style={{ background: 'rgba(255,255,255,0.08)', border: '1px solid rgba(255,255,255,0.15)', 'border-radius': '6px', padding: '3px 10px', color: 'rgba(255,255,255,0.85)', 'font-size': '11px', cursor: 'pointer', 'font-family': "'DM Sans',sans-serif" }}>
                {translate('viewer_measure_reset')}
              </button>
            </Show>
          </div>
        </Show>

        {/* Photo tool - framing overlay: drag to draw an optional crop rectangle (camera is locked). */}
        <Show when={photoMode() === 'aim'}>
          <div onPointerDown={photoDragDown} onPointerMove={photoDragMove} onPointerUp={photoDragUp}
            style={{ position: 'absolute', inset: '0', cursor: 'crosshair', 'z-index': '8', overflow: 'hidden' }}>
            <Show when={photoRegion()}>
              {(r) => (
                <div style={{ position: 'absolute', left: `${r().x}px`, top: `${r().y}px`, width: `${r().w}px`, height: `${r().h}px`,
                  border: '1.5px dashed #4a90d9', 'box-shadow': '0 0 0 9999px rgba(0,0,0,0.28)', 'pointer-events': 'none' }} />
              )}
            </Show>
          </div>
          {/* Top-centre confirm / cancel bar */}
          <div style={{ position: 'absolute', top: '16px', left: '50%', transform: 'translateX(-50%)', background: 'rgba(13,17,23,0.92)', border: '1px solid rgba(255,255,255,0.14)', 'border-radius': '12px', padding: '8px 10px 8px 16px', display: 'flex', 'align-items': 'center', gap: '12px', 'backdrop-filter': 'blur(6px)', 'box-shadow': '0 8px 24px rgba(0,0,0,0.45)', 'z-index': '9' }}>
            <span style={{ 'font-family': "'DM Sans',sans-serif", 'font-size': '12px', color: 'rgba(255,255,255,0.85)', 'white-space': 'nowrap' }}>
              {translate(photoRegion() ? 'viewer_photo_region_hint' : 'viewer_photo_aim_hint')}
            </span>
            <Show when={photoRegion()}>
              <button onClick={() => setPhotoRegion(null)}
                style={{ background: 'rgba(255,255,255,0.08)', border: '1px solid rgba(255,255,255,0.15)', 'border-radius': '7px', padding: '4px 10px', color: 'rgba(255,255,255,0.85)', 'font-size': '11px', cursor: 'pointer', 'font-family': "'DM Sans',sans-serif", 'white-space': 'nowrap' }}>
                {translate('viewer_photo_region_clear')}
              </button>
            </Show>
            <button onClick={takePhoto} title={translate('viewer_photo_take')}
              style={{ display: 'flex', 'align-items': 'center', 'justify-content': 'center', width: '34px', height: '34px', background: 'rgba(46,160,67,0.9)', border: '1px solid rgba(46,160,67,1)', 'border-radius': '9px', color: '#fff', 'font-size': '17px', cursor: 'pointer' }}>
              ✓
            </button>
            <button onClick={exitPhoto} title={translate('viewer_photo_cancel')}
              style={{ display: 'flex', 'align-items': 'center', 'justify-content': 'center', width: '34px', height: '34px', background: 'rgba(255,255,255,0.08)', border: '1px solid rgba(255,255,255,0.2)', 'border-radius': '9px', color: '#fff', 'font-size': '16px', cursor: 'pointer' }}>
              ✕
            </button>
          </div>
        </Show>

    </>
  )
}
