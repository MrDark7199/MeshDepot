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
  /** The comparison: which two fassungen, what is shown, where the wipe sits. */
  isComparing: () => boolean
  compareReady: () => boolean
  baseLabel: () => string
  compareLabel: () => string
  showBase: () => boolean
  setShowBase: (on: boolean) => void
  showCompare: () => boolean
  setShowCompare: (on: boolean) => void
  wipeAt: () => number | null
  setWipeAt: (at: number | null) => void
  /** The deviation colouring and how far along it is. */
  deviationState: () => 'off' | 'running' | 'on'
  deviationProgress: () => number
  deviationMax: () => number
  toggleDeviation: () => void
  /** The layer slider: how many there are and which one is on top. A count of
   *  0 or 1 hides the whole control. */
  gcodeLayerCount: () => number
  gcodeTopLayer: () => number
  setGcodeTopLayer: (layer: number) => void
  gcodeTopHeight: () => number
  /** The height of the last layer - what the readout will be at its widest. */
  gcodeMaxHeight: () => number
  /** Objects whose faces look inward, and the tool that turns them round. */
  invertedCount: () => number
  flipMeshAll: () => void
  flippedCount: () => number
  canSaveFixed: () => boolean
  addFixedToDesign: () => void
  flipSaving: () => boolean
  flipSaved: () => boolean
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
    resetColors, toolsOpen, isGcode,
    isComparing, compareReady, baseLabel, compareLabel, showBase, setShowBase, showCompare, setShowCompare,
    wipeAt, setWipeAt, deviationState, deviationProgress, deviationMax, toggleDeviation,
    gcodeLayerCount, gcodeTopLayer, setGcodeTopLayer, gcodeTopHeight, gcodeMaxHeight,
    invertedCount, flipMeshAll, flippedCount, canSaveFixed, addFixedToDesign,
    flipSaving, flipSaved,
    measureActive, setMeasure, clearMeasure, measureCount, measureDist,
    setMeasureLabelEl, openSplitIntro, startPhoto, takePhoto, exitPhoto, photoMode, photoRegion,
    setPhotoRegion, photoDragDown, photoDragMove, photoDragUp } = deps
  const sideRow = (label: string, colour: string, on: boolean, toggle: (on: boolean) => void) => (
    <button onClick={() => toggle(!on)}
      style={{ display: 'flex', 'align-items': 'center', gap: '8px', padding: '5px 8px', 'border-radius': '8px',
               background: on ? 'rgba(255,255,255,0.06)' : 'transparent', border: '1px solid transparent',
               color: on ? '#fff' : 'rgba(255,255,255,0.4)', cursor: 'pointer', 'font-family': "'DM Sans',sans-serif",
               'font-size': '13px', width: '100%', 'text-align': 'left' }}>
      <span style={{ width: '12px', height: '12px', 'border-radius': '3px', background: colour, 'flex-shrink': '0', opacity: on ? '1' : '0.35' }} />
      {label}
    </button>
  )

  // where the finished print is.
  const sliderValue = () => gcodeTopLayer() < 0 ? gcodeLayerCount() - 1 : gcodeTopLayer()

  /**
   * The width the two readouts will need at their widest - the last layer over
   * the last layer, and the full height - worked out once rather than left to
   * the text. Otherwise the panel grows and shrinks as the numbers get longer,
   * dragging its left edge along with the slider.
   */
  const readoutWidth = () => {
    const total = String(gcodeLayerCount())
    const counter = `${total} / ${total}`.length
    const height = `${gcodeMaxHeight().toFixed(2)} mm`.length
    return Math.max(counter, height)
  }

  return (
    <>
        {/* Comparing two fassungen: what each colour is, which to show, and the
            wipe that runs one into the other. */}
        <Show when={isComparing() && compareReady()}>
          <div style={{ position: 'absolute', bottom: '14px', left: '50%', transform: 'translateX(-50%)', 'z-index': '6', display: 'flex', 'flex-direction': 'column', gap: '8px', padding: '12px 14px', 'border-radius': '12px', background: 'rgba(13,17,23,0.85)', border: '1px solid rgba(255,255,255,0.12)', 'backdrop-filter': 'blur(6px)', 'min-width': '260px' }}>
            {sideRow(baseLabel() || translate('compare_side_new'), '#4ade80', showBase(), setShowBase)}
            {sideRow(compareLabel() || translate('compare_side_old'), '#e63946', showCompare(), setShowCompare)}
            <div style={{ display: 'flex', 'align-items': 'center', gap: '10px', 'margin-top': '2px' }}>
              <button onClick={() => setWipeAt(wipeAt() === null ? 0.5 : null)}
                style={{ padding: '5px 10px', 'border-radius': '8px', border: `1px solid ${wipeAt() !== null ? 'rgba(74,144,217,0.7)' : 'rgba(255,255,255,0.12)'}`, background: wipeAt() !== null ? 'rgba(74,144,217,0.16)' : 'transparent', color: wipeAt() !== null ? '#9ecbf0' : 'rgba(255,255,255,0.65)', 'font-family': "'DM Mono',monospace", 'font-size': '11px', cursor: 'pointer', 'white-space': 'nowrap' }}>
                {translate('compare_wipe')}
              </button>
              <input type="range" min="0" max="1" step="0.005" value={wipeAt() ?? 0.5}
                disabled={wipeAt() === null}
                onInput={event => setWipeAt(parseFloat(event.currentTarget.value))}
                style={{ flex: '1', 'accent-color': 'var(--accent)', cursor: wipeAt() === null ? 'default' : 'pointer', opacity: wipeAt() === null ? '0.35' : '1' }} />
            </div>

            {/* How far the newer model has moved from the older one, as a colour
                per vertex. Measured on demand: it is the one thing here that
                costs real time. */}
            <button onClick={toggleDeviation} disabled={deviationState() === 'running'}
              style={{ padding: '6px 10px', 'border-radius': '8px', border: `1px solid ${deviationState() === 'on' ? 'rgba(74,144,217,0.7)' : 'rgba(255,255,255,0.12)'}`, background: deviationState() === 'on' ? 'rgba(74,144,217,0.16)' : 'transparent', color: deviationState() === 'on' ? '#9ecbf0' : 'rgba(255,255,255,0.65)', 'font-family': "'DM Sans',sans-serif", 'font-size': '13px', cursor: deviationState() === 'running' ? 'progress' : 'pointer' }}>
              {deviationState() === 'running'
                ? `${translate('compare_deviation_working')} ${Math.round(deviationProgress() * 100)}%`
                : translate('compare_deviation')}
            </button>
            <Show when={deviationState() === 'on'}>
              <div style={{ display: 'flex', 'align-items': 'center', gap: '8px' }}>
                {/* The same scale the model is painted in, so the colours can be
                    read back as millimetres. */}
                <span style={{ 'font-family': "'DM Mono',monospace", 'font-size': '10px', color: 'rgba(255,255,255,0.6)' }}>0</span>
                <div style={{ flex: '1', height: '8px', 'border-radius': '4px', background: 'linear-gradient(to right, hsl(240,85%,100%), hsl(240,85%,50%), hsl(120,85%,50%), hsl(60,85%,50%), hsl(0,85%,50%))' }} />
                <span style={{ 'font-family': "'DM Mono',monospace", 'font-size': '10px', color: 'rgba(255,255,255,0.6)', 'white-space': 'nowrap' }}>
                  {deviationMax().toFixed(2)} mm
                </span>
              </div>
            </Show>
          </div>
        </Show>

        {/* Right layer slider - g-code only, and only when there is more than one
            layer to choose between. */}
        <Show when={isGcode() && gcodeLayerCount() > 1}>
          <div style={{ position: 'absolute', top: '14px', right: '14px', bottom: '14px', display: 'flex', 'flex-direction': 'column', 'align-items': 'center', gap: '10px', 'z-index': '6', 'pointer-events': 'none' }}>
            <div style={{ display: 'flex', 'flex-direction': 'column', 'align-items': 'center', gap: '10px', padding: '12px 10px', 'border-radius': '12px', background: 'rgba(13,17,23,0.85)', border: '1px solid rgba(255,255,255,0.12)', 'backdrop-filter': 'blur(6px)', 'pointer-events': 'auto', 'max-height': '100%', width: `calc(${readoutWidth()}ch + 20px)` }}>
              <span style={{ 'font-family': "'DM Sans',sans-serif", 'font-size': '11px', 'font-weight': '600', 'letter-spacing': '0.04em', 'text-transform': 'uppercase', color: 'rgba(255,255,255,0.5)' }}>
                {translate('viewer_layers_title')}
              </span>
              <span style={{ 'font-family': "'DM Mono',monospace", 'font-size': '12px', color: '#fff', 'white-space': 'nowrap' }}>
                {`${sliderValue() + 1} / ${gcodeLayerCount()}`}
              </span>
              <span style={{ 'font-family': "'DM Mono',monospace", 'font-size': '11px', color: 'rgba(255,255,255,0.5)', 'white-space': 'nowrap' }}>
                {gcodeTopHeight().toFixed(2)} mm
              </span>
              {/* Vertical by writing-mode, with the old WebKit spelling behind it:
                  a rotated slider would drag the wrong way on half the setups. */}
              <input type="range" min="0" max={gcodeLayerCount() - 1} step="1" value={sliderValue()}
                onInput={event => setGcodeTopLayer(parseInt(event.currentTarget.value, 10))}
                title={translate('viewer_layer_hint')}
                style={{ 'writing-mode': 'vertical-lr', direction: 'rtl', '-webkit-appearance': 'slider-vertical', '-moz-orient': 'vertical', width: '22px', flex: '1', 'min-height': '120px', 'accent-color': 'var(--accent)', cursor: 'pointer' }} />
            </div>
          </div>
        </Show>

        {/* Objects whose faces look inward. Top left, where nothing else sits
            while a model is being looked at, and only while there are any. */}
        <Show when={invertedCount() > 0 || flippedCount() > 0}>
          <div style={{ position: 'absolute', top: '14px', left: platesUI().length > 1 ? '182px' : '14px', 'z-index': '7', 'max-width': '320px', display: 'flex', 'flex-direction': 'column', gap: '8px', padding: '12px 14px', 'border-radius': '12px', background: 'rgba(40,20,10,0.88)', border: '1px solid rgba(230,120,50,0.45)', 'backdrop-filter': 'blur(6px)' }}>
            <div style={{ display: 'flex', 'align-items': 'center', gap: '8px' }}>
              <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="#f0a060" stroke-width="2.2" stroke-linecap="round" style={{ 'flex-shrink': '0' }}>
                <path d="M10.29 3.86 1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z" />
                <line x1="12" y1="9" x2="12" y2="13" /><line x1="12" y1="17" x2="12.01" y2="17" />
              </svg>
              <span style={{ 'font-family': "'DM Sans',sans-serif", 'font-size': '13px', 'font-weight': '700', color: '#ffd9b8' }}>
                <Show when={invertedCount() > 0} fallback={translate('viewer_inverted_fixed')}>
                  {translate('viewer_inverted_title').replace('{count}', String(invertedCount()))}
                </Show>
              </span>
            </div>
            <Show when={invertedCount() > 0}>
              <span style={{ 'font-family': "'DM Sans',sans-serif", 'font-size': '12px', color: 'rgba(255,220,195,0.8)', 'line-height': '1.5' }}>
                {translate('viewer_inverted_hint')}
              </span>
            </Show>
            <div style={{ display: 'flex', gap: '8px', 'flex-wrap': 'wrap' }}>
              <Show when={invertedCount() > 0}>
                <button onClick={flipMeshAll}
                  style={{ padding: '6px 11px', 'border-radius': '8px', border: '1px solid rgba(230,120,50,0.6)', background: 'rgba(230,120,50,0.2)', color: '#ffd9b8', 'font-family': "'DM Sans',sans-serif", 'font-size': '12px', 'font-weight': '600', cursor: 'pointer' }}>
                  {translate('viewer_inverted_fix_all')}
                </button>
              </Show>
              <Show when={flippedCount() > 0 && canSaveFixed()}>
                <button onClick={addFixedToDesign} disabled={flipSaving() || flipSaved()}
                  style={{ padding: '6px 11px', 'border-radius': '8px', border: '1px solid rgba(255,255,255,0.18)', background: 'rgba(255,255,255,0.08)', color: '#fff', 'font-family': "'DM Sans',sans-serif", 'font-size': '12px', cursor: flipSaving() || flipSaved() ? 'default' : 'pointer' }}>
                  {flipSaved() ? translate('viewer_inverted_saved')
                    : flipSaving() ? translate('viewer_split_adding')
                    : translate('viewer_inverted_save')}
                </button>
              </Show>
            </div>
          </div>
        </Show>

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
                <span style={{ display: 'flex', 'align-items': 'center', gap: '8px' }}>
                  <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" style={{ 'flex-shrink': '0' }}>
                    <line x1="4" y1="20" x2="20" y2="4" />
                    <circle cx="4.5" cy="19.5" r="2" /><circle cx="19.5" cy="4.5" r="2" />
                  </svg>
                  {translate('viewer_tool_measure')}
                </span>
                <span style={{ 'font-size': '10px', color: measureActive() ? '#9ec5ff' : 'rgba(255,255,255,0.4)', 'font-family': "'DM Mono',monospace" }}>
                  {measureActive() ? translate('viewer_tool_on') : translate('viewer_tool_off')}
                </span>
              </button>
            </Show>
            {/* Zerlegen: nur für Meshes. Ein G-code-Pfad hat keine Objekte, die man trennen könnte. */}
            <Show when={!isGcode()}>
              <button onClick={openSplitIntro}
                style={{ display: 'flex', 'align-items': 'center', gap: '10px', background: 'rgba(255,255,255,0.05)', border: '1px solid rgba(255,255,255,0.12)', 'border-radius': '8px', padding: '8px 10px', color: '#fff', cursor: 'pointer', 'font-family': "'DM Sans',sans-serif", 'font-size': '12px' }}>
                <span style={{ display: 'flex', 'align-items': 'center', gap: '8px' }}>
                  <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" style={{ 'flex-shrink': '0' }}>
                    <circle cx="6" cy="6" r="3" /><circle cx="6" cy="18" r="3" />
                    <line x1="20" y1="4" x2="8.12" y2="15.88" />
                    <line x1="14.47" y1="14.48" x2="20" y2="20" />
                    <line x1="8.12" y1="8.12" x2="12" y2="12" />
                  </svg>
                  {translate('viewer_tool_split')}
                </span>
              </button>
            </Show>
            {/* Foto erstellen: für alle Typen (STL/OBJ/3MF + G-code) – arbeitet auf dem Framebuffer. */}
            <button onClick={startPhoto}
              style={{ display: 'flex', 'align-items': 'center', gap: '10px', background: 'rgba(255,255,255,0.05)', border: '1px solid rgba(255,255,255,0.12)', 'border-radius': '8px', padding: '8px 10px', color: '#fff', cursor: 'pointer', 'font-family': "'DM Sans',sans-serif", 'font-size': '12px' }}>
              <span style={{ display: 'flex', 'align-items': 'center', gap: '8px' }}>
                <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" style={{ 'flex-shrink': '0' }}>
                  <path d="M23 19a2 2 0 0 1-2 2H3a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h4l2-3h6l2 3h4a2 2 0 0 1 2 2z" />
                  <circle cx="12" cy="13" r="4" />
                </svg>
                {translate('viewer_tool_photo')}
              </span>
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
