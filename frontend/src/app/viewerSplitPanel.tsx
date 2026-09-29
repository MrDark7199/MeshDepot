import { For, Show, createEffect } from 'solid-js'
import type { Setter } from 'solid-js'
import type { StlViewerModalProps } from '../components/StlViewer'

export interface SplitPanelDeps {
  props: StlViewerModalProps
  translate: (key: any, params?: any) => string
  /** builtPlates is reassigned when a model loads, so it is read through a function. */
  builtPlates: () => unknown[]
  platesUI: () => { name: string; thumbnail?: string; colorIdx: number[] }[]
  splitPhase: () => 'idle' | 'intro' | 'busy' | 'done'
  splitProgress: () => number
  splitNames: () => string[]
  splitChosen: () => boolean[]
  splitAdded: () => boolean[]
  splitFlash: () => number | null
  splitHidden: () => boolean[]
  toggleSplitVisible: (index: number) => void
  splitPlate: () => number | 'all'
  setSplitPlate: Setter<number | 'all'>
  splitSaving: () => boolean
  splitError: () => boolean
  splitAddError: () => string
  splitPos: () => { x: number; y: number } | null
  chosenIndices: () => number[]
  pendingIndices: () => number[]
  toggleSplitChoice: (index: number) => void
  setAllSplitChoices: (on: boolean) => void
  flashSplitPart: (index: number) => void
  runSplit: () => void
  closeSplit: () => void
  downloadSplit: () => void
  addSplitToDesign: () => void
  startSplitDrag: (event: PointerEvent) => void
}

/**
 * The split tool's panel, across all of its steps. A plain function rather than
 * a component, so it keeps running in the viewer's own reactive owner.
 */
export function splitPanel(deps: SplitPanelDeps) {
  const { props, translate, builtPlates, platesUI, splitPhase, splitProgress, splitNames, splitChosen,
    splitAdded, splitFlash, splitHidden, toggleSplitVisible, splitPlate, setSplitPlate, splitSaving, splitError, splitAddError, splitPos,
    chosenIndices, pendingIndices, toggleSplitChoice, setAllSplitChoices, flashSplitPart, runSplit,
    closeSplit, downloadSplit, addSplitToDesign, startSplitDrag } = deps

  // The model can point at a row, so that row has to be in view when it does.
  const rowElements: HTMLDivElement[] = []
  createEffect(() => {
    const index = splitFlash()
    if (index !== null) rowElements[index]?.scrollIntoView({ block: 'nearest' })
  })
  return (
    <>
        {/* Split tool. One panel across all steps: explanation, progress, result.
            Placed centrally until the result arrives, then parked at the right edge
            and draggable by its header so it need not cover the model. */}
        <Show when={splitPhase() !== 'idle'}>
          <div style={{
            position: 'absolute',
            ...(splitPos()
              ? { left: `${splitPos()!.x}px`, top: `${splitPos()!.y}px` }
              : { top: '50%', left: '50%', transform: 'translate(-50%,-50%)' }),
            width: '420px', 'max-width': 'calc(100vw - 32px)',
            background: 'rgba(13,17,23,0.96)', border: '1px solid rgba(255,255,255,0.16)',
            'border-radius': '14px', display: 'flex', 'flex-direction': 'column',
            'backdrop-filter': 'blur(8px)', 'box-shadow': '0 12px 40px rgba(0,0,0,0.55)', 'z-index': '9',
          }}>
            {/* Header: drag handle and the only close control. */}
            <div onPointerDown={splitPhase() === 'done' ? startSplitDrag : undefined}
              style={{ display: 'flex', 'align-items': 'center', 'justify-content': 'space-between', gap: '12px', padding: '14px 16px', 'border-bottom': '1px solid rgba(255,255,255,0.1)', cursor: splitPhase() === 'done' ? 'move' : 'default' }}>
              <span style={{ 'font-family': "'DM Sans',sans-serif", 'font-size': '14px', 'font-weight': '600', color: '#fff' }}>
                {translate('viewer_split_title')}
              </span>
              <button onClick={closeSplit} title={translate('viewer_split_close')}
                style={{ background: 'none', border: 'none', color: 'rgba(255,255,255,0.55)', cursor: 'pointer', padding: '2px', display: 'flex', 'align-items': 'center' }}>
                <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4">
                  <line x1="18" y1="6" x2="6" y2="18" /><line x1="6" y1="6" x2="18" y2="18" />
                </svg>
              </button>
            </div>

            <div style={{ padding: '16px', display: 'flex', 'flex-direction': 'column', gap: '14px' }}>

              {/* - Step 1: what this does, and what to run it on ------- */}
              <Show when={splitPhase() === 'intro'}>
                <span style={{ 'font-family': "'DM Sans',sans-serif", 'font-size': '13px', 'line-height': '1.55', color: 'rgba(255,255,255,0.78)' }}>
                  {translate('viewer_split_explain')}
                </span>
                <Show when={builtPlates().length > 1}>
                  <label style={{ display: 'flex', 'flex-direction': 'column', gap: '6px' }}>
                    <span style={{ 'font-family': "'DM Sans',sans-serif", 'font-size': '12px', color: 'rgba(255,255,255,0.6)' }}>
                      {translate('viewer_split_plate_label')}
                    </span>
                    <select value={String(splitPlate())} onChange={e => setSplitPlate(e.currentTarget.value === 'all' ? 'all' : Number(e.currentTarget.value))}
                      style={{ background: 'rgba(255,255,255,0.07)', border: '1px solid rgba(255,255,255,0.16)', 'border-radius': '8px', padding: '7px 9px', color: '#fff', 'font-size': '13px', 'font-family': "'DM Sans',sans-serif", 'color-scheme': 'dark' }}>
                      <option value="all">{translate('viewer_split_plate_all')}</option>
                      <For each={platesUI()}>{(plate, index) => (
                        <option value={String(index())}>{plate.name || translate('viewer_split_plate_n', { n: String(index() + 1) })}</option>
                      )}</For>
                    </select>
                  </label>
                </Show>
                <div style={{ display: 'flex', gap: '8px', 'justify-content': 'flex-end' }}>
                  <button onClick={closeSplit}
                    style={{ background: 'rgba(255,255,255,0.06)', border: '1px solid rgba(255,255,255,0.15)', 'border-radius': '8px', padding: '8px 14px', color: 'rgba(255,255,255,0.8)', 'font-size': '13px', cursor: 'pointer', 'font-family': "'DM Sans',sans-serif" }}>
                    {translate('viewer_split_cancel')}
                  </button>
                  <button onClick={runSplit}
                    style={{ background: 'rgba(74,144,217,0.3)', border: '1px solid rgba(74,144,217,0.7)', 'border-radius': '8px', padding: '8px 14px', color: '#fff', 'font-size': '13px', 'font-weight': '600', cursor: 'pointer', 'font-family': "'DM Sans',sans-serif" }}>
                    {translate('viewer_split_start')}
                  </button>
                </div>
              </Show>

              {/* - Step 2: separating --------------------- */}
              <Show when={splitPhase() === 'busy'}>
                <span style={{ 'font-family': "'DM Sans',sans-serif", 'font-size': '13px', color: 'rgba(255,255,255,0.78)' }}>
                  {translate('viewer_split_working')}
                </span>
                <div style={{ display: 'flex', 'align-items': 'center', gap: '10px' }}>
                  <div style={{ flex: '1', height: '6px', background: 'rgba(255,255,255,0.1)', 'border-radius': '3px', overflow: 'hidden' }}>
                    <div style={{ height: '100%', width: `${Math.round(splitProgress() * 100)}%`, background: '#4a90d9', transition: 'width 120ms linear' }} />
                  </div>
                  <span style={{ 'font-family': "'DM Mono',monospace", 'font-size': '13px', color: '#9ec5ff', 'min-width': '42px', 'text-align': 'right' }}>
                    {Math.round(splitProgress() * 100)}%
                  </span>
                </div>
              </Show>

              {/* - Step 3: the parts --------------------- */}
              <Show when={splitPhase() === 'done'}>
                <Show when={!splitError() && splitNames().length > 1} fallback={
                  <span style={{ 'font-family': "'DM Sans',sans-serif", 'font-size': '13px', color: 'rgba(255,255,255,0.78)' }}>
                    {splitError() ? translate('viewer_split_failed') : translate('viewer_split_single')}
                  </span>
                }>
                  <div style={{ display: 'flex', 'align-items': 'center', 'justify-content': 'space-between', gap: '10px' }}>
                    <span style={{ 'font-family': "'DM Sans',sans-serif", 'font-size': '13px', color: 'rgba(255,255,255,0.78)' }}>
                      {translate('viewer_split_found', { count: String(splitNames().length) })}
                    </span>
                    <div style={{ display: 'flex', gap: '6px' }}>
                      <button onClick={() => setAllSplitChoices(true)}
                        style={{ background: 'rgba(255,255,255,0.06)', border: '1px solid rgba(255,255,255,0.14)', 'border-radius': '6px', padding: '4px 9px', color: 'rgba(255,255,255,0.8)', 'font-size': '11px', cursor: 'pointer', 'font-family': "'DM Sans',sans-serif" }}>
                        {translate('viewer_split_select_all')}
                      </button>
                      <button onClick={() => setAllSplitChoices(false)}
                        style={{ background: 'rgba(255,255,255,0.06)', border: '1px solid rgba(255,255,255,0.14)', 'border-radius': '6px', padding: '4px 9px', color: 'rgba(255,255,255,0.8)', 'font-size': '11px', cursor: 'pointer', 'font-family': "'DM Sans',sans-serif" }}>
                        {translate('viewer_split_select_none')}
                      </button>
                    </div>
                  </div>

                  <div style={{ 'max-height': '260px', 'overflow-y': 'auto', display: 'flex', 'flex-direction': 'column', gap: '3px', border: '1px solid rgba(255,255,255,0.08)', 'border-radius': '8px', padding: '5px' }}>
                    {/* Magnifier points the part out in the model, the rest of the row is a
                        plain selection toggle. Two separate targets, so neither click has to
                        guess which of the two the user meant. */}
                    <For each={splitNames()}>{(name, index) => (
                      <div onClick={() => toggleSplitChoice(index())}
                        ref={element => (rowElements[index()] = element)}
                        title={translate('viewer_split_box_hint')}
                        style={{ display: 'flex', 'align-items': 'center', gap: '8px', background: splitFlash() === index() ? 'rgba(255,210,63,0.16)' : 'transparent', 'border-radius': '6px', padding: '5px 7px', color: splitHidden()[index()] ? 'rgba(255,255,255,0.4)' : 'rgba(255,255,255,0.8)', 'font-size': '12px', cursor: 'pointer', 'font-family': "'DM Mono',monospace", width: '100%', 'box-sizing': 'border-box' }}>
                        <button onClick={event => { event.stopPropagation(); flashSplitPart(index()) }}
                          title={translate('viewer_split_row_hint')}
                          style={{ background: 'none', border: 'none', padding: '2px', cursor: 'pointer', display: 'flex', 'align-items': 'center', color: splitFlash() === index() ? '#ffd23f' : 'rgba(255,255,255,0.45)', 'flex-shrink': '0' }}>
                          <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4">
                            <circle cx="11" cy="11" r="7" /><line x1="21" y1="21" x2="16.2" y2="16.2" />
                          </svg>
                        </button>
                        <span style={{ width: '14px', height: '14px', 'border-radius': '3px', border: `1.5px solid ${splitChosen()[index()] ? '#4a90d9' : 'rgba(255,255,255,0.35)'}`, background: splitChosen()[index()] ? '#4a90d9' : 'transparent', 'flex-shrink': '0', display: 'flex', 'align-items': 'center', 'justify-content': 'center' }}>
                          <Show when={splitChosen()[index()]}>
                            <svg width="9" height="9" viewBox="0 0 24 24" fill="none" stroke="#fff" stroke-width="4"><polyline points="20 6 9 17 4 12" /></svg>
                          </Show>
                        </span>
                        <span style={{ overflow: 'hidden', 'text-overflow': 'ellipsis', 'white-space': 'nowrap', flex: '1' }}>{name}</span>
                        <button onClick={event => { event.stopPropagation(); toggleSplitVisible(index()) }}
                          title={translate(splitHidden()[index()] ? 'viewer_split_show_part' : 'viewer_split_hide_part')}
                          style={{ background: 'none', border: 'none', padding: '2px', cursor: 'pointer', display: 'flex', 'align-items': 'center', color: splitHidden()[index()] ? 'rgba(255,255,255,0.35)' : 'rgba(255,255,255,0.65)', 'flex-shrink': '0' }}>
                          <Show when={!splitHidden()[index()]} fallback={
                            <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                              <path d="M17.94 17.94A10.07 10.07 0 0 1 12 20c-7 0-11-8-11-8a18.45 18.45 0 0 1 5.06-5.94M9.9 4.24A9.12 9.12 0 0 1 12 4c7 0 11 8 11 8a18.5 18.5 0 0 1-2.16 3.19m-6.72-1.07a3 3 0 1 1-4.24-4.24" />
                              <line x1="1" y1="1" x2="23" y2="23" />
                            </svg>
                          }>
                            <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                              <path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z" /><circle cx="12" cy="12" r="3" />
                            </svg>
                          </Show>
                        </button>
                        <Show when={splitAdded()[index()]}>
                          <span title={translate('viewer_split_added')} style={{ color: '#7fd18b', 'flex-shrink': '0', display: 'flex', 'align-items': 'center' }}>
                            <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3"><polyline points="20 6 9 17 4 12" /></svg>
                          </span>
                        </Show>
                      </div>
                    )}</For>
                  </div>

                  <div style={{ display: 'flex', gap: '8px', 'flex-wrap': 'wrap' }}>
                    <button onClick={downloadSplit} disabled={chosenIndices().length === 0}
                      style={{ background: 'rgba(74,144,217,0.28)', border: '1px solid rgba(74,144,217,0.65)', 'border-radius': '8px', padding: '8px 13px', color: '#fff', 'font-size': '12px', cursor: chosenIndices().length === 0 ? 'default' : 'pointer', opacity: chosenIndices().length === 0 ? '0.45' : '1', 'font-family': "'DM Sans',sans-serif" }}>
                      ⭳ {chosenIndices().length === 1
                        ? translate('viewer_split_download_one')
                        : translate('viewer_split_download')}
                    </button>
                    <Show when={props.onSaveFiles}>
                      {/* Offers only what is not in the design yet, so a second click
                          cannot resend a part the version already has by that name. */}
                      <button onClick={addSplitToDesign} disabled={splitSaving() || pendingIndices().length === 0}
                        style={{ background: 'rgba(255,255,255,0.06)', border: '1px solid rgba(255,255,255,0.15)', 'border-radius': '8px', padding: '8px 13px', color: pendingIndices().length === 0 && chosenIndices().length > 0 ? '#7fd18b' : '#fff', 'font-size': '12px', cursor: splitSaving() || pendingIndices().length === 0 ? 'default' : 'pointer', opacity: pendingIndices().length === 0 ? '0.55' : '1', 'font-family': "'DM Sans',sans-serif" }}>
                        {splitSaving() ? translate('viewer_split_adding')
                          : pendingIndices().length === 0 && chosenIndices().length > 0 ? translate('viewer_split_added')
                          : translate('viewer_split_add')}
                      </button>
                    </Show>
                  </div>
                  <Show when={splitAddError()}>
                    <span style={{ 'font-family': "'DM Sans',sans-serif", 'font-size': '11px', color: '#ff8a8a' }}>
                      {translate('viewer_split_add_failed')} ({splitAddError()})
                    </span>
                  </Show>
                </Show>
              </Show>
            </div>
          </div>
        </Show>
    </>
  )
}
