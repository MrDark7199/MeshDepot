import { useTheme } from '../../ThemeContext'
import { SaveButton, inp, lbl, mono, sans } from './shared'
import { api } from '../../services/api'
import { For, Show, createEffect, createSignal } from 'solid-js'
import type { JSX } from 'solid-js'
import type { User } from '../../types'
import { applyCustomCss } from '../../utils/customCss'
import { errorKey } from '../../utils/errorMessage'
import { markDirty, resetDirty } from '../../utils/unsavedChanges'

/**
 * Appearance settings tab - theme selector (dark/light/system), accent-color
 * presets with an HSL slider picker, and a custom CSS textarea.
 * Changes are applied live to the document and persisted in localStorage.
 */
export function TabAppearance(props: { user: User; translate: any; showToast: (message: string, variant?: string) => void; onUserUpdate: (u: User) => void }) {
  const { theme, setTheme, setAccent } = useTheme()
  const [accentHex, setAccentHex] = createSignal(localStorage.getItem('meshdepot_accent') || '#457b9d')
  // The stylesheet belongs to the account, not to this browser: it is what the
  // session hands over, and what the save below writes back.
  const [customCss, setCustomCss] = createSignal(props.user.custom_css || '')
  const [saving, setSaving] = createSignal(false)
  const [hue, setHue] = createSignal(210)
  const [sat, setSat] = createSignal(55)
  const [lit, setLit] = createSignal(52)
  const [showPicker, setShowPicker] = createSignal(false)

  const PRESETS = ['#457b9d','#2563eb','#7c3aed','#db2777','#dc2626','#ea580c','#d97706','#16a34a','#0891b2','#64748b']

  const hslToHex = (hue: number, saturation: number, lightness: number) => {
    saturation /= 100; lightness /= 100
    const chromaAmplitude = saturation * Math.min(lightness, 1 - lightness)
    const channel = (channelIndex: number) => { const sectorIndex = (channelIndex + hue / 30) % 12; const channelValue = lightness - chromaAmplitude * Math.max(-1, Math.min(sectorIndex - 3, 9 - sectorIndex, 1)); return Math.round(255 * channelValue).toString(16).padStart(2, '0') }
    return `#${channel(0)}${channel(8)}${channel(4)}`
  }

  createEffect(() => { if (showPicker()) setAccentHex(hslToHex(hue(), sat(), lit())) })

  const save = async () => {
    setAccent(accentHex())
    const css = customCss()
    setSaving(true)
    try {
      await api.updateProfile(props.user.id, { custom_css: css })
      // Applied straight away as well as stored: waiting for the next load to
      // show what was just saved is the behaviour this replaces.
      applyCustomCss(css)
      props.onUserUpdate({ ...props.user, custom_css: css })
      props.showToast(props.translate('toast_appearance_saved'))
      resetDirty()
    } catch (failure: unknown) {
      props.showToast(props.translate(errorKey(failure)), 'error')
    } finally { setSaving(false) }
  }

  const themes = [{ key: 'dark', label: props.translate('theme_dark') }, { key: 'light', label: props.translate('theme_light') }, { key: 'system', label: props.translate('theme_system') }]
  const sliderStyle = (bg: string): JSX.CSSProperties => ({ width: '100%', height: '10px', 'border-radius': '5px', outline: 'none', cursor: 'pointer', border: 'none', padding: '0', background: bg, '-webkit-appearance': 'none' })

  return (
    <div style={{ display: 'flex', 'flex-direction': 'column', gap: '18px' }}>
      <div>
        <label style={lbl}>{props.translate('field_theme')}</label>
        <div style={{ display: 'flex', gap: '8px' }}>
          <For each={themes}>{({ key, label }) =>
            <button onClick={() => setTheme(key)} style={{ flex: '1', padding: '8px', background: theme() === key ? 'var(--accent)' : 'var(--surface)', border: `1px solid ${theme() === key ? 'var(--accent)' : 'var(--border)'}`, 'border-radius': '8px', color: theme() === key ? '#fff' : 'var(--text3)', ...sans, 'font-size': '13px', cursor: 'pointer' }}>{label}</button>
          }</For>
        </div>
      </div>
      <div>
        <label style={lbl}>{props.translate('field_accent_color')}</label>
        <div style={{ display: 'flex', 'flex-wrap': 'wrap', gap: '6px', 'margin-bottom': '10px' }}>
          <For each={PRESETS}>{color =>
            <div onClick={() => { setAccentHex(color); setShowPicker(false); markDirty() }}
              style={{ width: '26px', height: '26px', 'border-radius': '6px', background: color, cursor: 'pointer', border: accentHex() === color ? '2px solid #fff' : '2px solid transparent', 'box-shadow': accentHex() === color ? `0 0 0 2px ${color}` : 'none', transition: 'transform 0.1s', transform: accentHex() === color ? 'scale(1.15)' : 'scale(1)' }} />
          }</For>
          <div onClick={() => setShowPicker(currentValue => !currentValue)}
            style={{ width: '26px', height: '26px', 'border-radius': '6px', background: 'var(--bg4)', border: '2px solid var(--border2)', cursor: 'pointer', display: 'flex', 'align-items': 'center', 'justify-content': 'center' }}>
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="var(--text3)" stroke-width="2"><circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83-2.83l.06-.06A1.65 1.65 0 0 0 4.68 15a1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 2.83-2.83l.06.06A1.65 1.65 0 0 0 9 4.68a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 0 1 2.83 2.83l-.06.06A1.65 1.65 0 0 0 19.4 9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z"/></svg>
          </div>
        </div>
        <Show when={showPicker()}>
          <div style={{ background: 'var(--bg3)', border: '1px solid var(--border)', 'border-radius': '10px', padding: '14px', display: 'flex', 'flex-direction': 'column', gap: '10px', 'margin-bottom': '8px' }}>
            <style>{`input[type=range]::-webkit-slider-thumb{-webkit-appearance:none;width:16px;height:16px;border-radius:50%;background:#fff;box-shadow:0 1px 4px rgba(0,0,0,0.4);cursor:pointer;border:none;}`}</style>
            <div>
              <label style={{ ...lbl, 'margin-bottom': '6px' }}>Hue</label>
              <input type="range" min="0" max="359" value={hue()} onInput={e => setHue(+e.currentTarget.value)}
                style={sliderStyle(`linear-gradient(to right,hsl(0,80%,55%),hsl(60,80%,55%),hsl(120,80%,55%),hsl(180,80%,55%),hsl(240,80%,55%),hsl(300,80%,55%),hsl(360,80%,55%))`)} />
            </div>
            <div>
              <label style={{ ...lbl, 'margin-bottom': '6px' }}>Saturation ({sat()}%)</label>
              <input type="range" min="10" max="100" value={sat()} onInput={e => setSat(+e.currentTarget.value)}
                style={sliderStyle(`linear-gradient(to right,hsl(${hue()},10%,${lit()}%),hsl(${hue()},100%,${lit()}%))`)} />
            </div>
            <div>
              <label style={{ ...lbl, 'margin-bottom': '6px' }}>Lightness ({lit()}%)</label>
              <input type="range" min="20" max="80" value={lit()} onInput={e => setLit(+e.currentTarget.value)}
                style={sliderStyle(`linear-gradient(to right,hsl(${hue()},${sat()}%,20%),hsl(${hue()},${sat()}%,80%))`)} />
            </div>
          </div>
        </Show>
        <div style={{ display: 'flex', gap: '8px', 'align-items': 'center' }}>
          <div style={{ width: '34px', height: '34px', 'border-radius': '8px', background: accentHex(), border: '2px solid var(--border2)', 'flex-shrink': '0' }} />
          <input value={accentHex()} onInput={e => { if (/^#[0-9a-fA-F]{6}$/.test(e.currentTarget.value)) setAccentHex(e.currentTarget.value) }} style={{ ...inp, width: '110px', ...mono, 'font-size': '12px' }} placeholder="#457b9d" />
        </div>
      </div>
      <div>
        <label style={lbl}>{props.translate('field_custom_css')}</label>
        <textarea value={customCss()} onInput={e => setCustomCss(e.currentTarget.value)} rows={5} placeholder=":root { --accent: #ff0000; }"
          style={{ ...inp, resize: 'vertical', 'min-height': '90px', ...mono, 'font-size': '12px' }} />
        <div style={{ 'margin-top': '6px', ...mono, 'font-size': '11px', color: 'var(--muted)' }}>{props.translate('custom_css_hint')}</div>
      </div>
      <SaveButton onClick={save} loading={saving()} label={props.translate('btn_save_appearance')} />
    </div>
  )
}
