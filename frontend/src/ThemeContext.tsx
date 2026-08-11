import { createContext, useContext, createSignal, createEffect, JSX } from 'solid-js'

const THEME_KEY = 'meshdepot_theme'
const ACCENT_KEY = 'meshdepot_accent'

export const THEMES: Record<string, Record<string, string>> = {
  dark: {
    '--bg': '#141414', '--bg2': '#1c1c1c', '--bg3': '#252525', '--bg4': '#2a2a2a',
    '--surface': 'rgba(255,255,255,0.03)', '--border': 'rgba(255,255,255,0.08)',
    '--border2': 'rgba(255,255,255,0.12)', '--text': '#eee', '--text2': '#ccc',
    '--text3': '#aaa', '--muted': '#666', '--muted2': '#555', '--muted3': '#444',
    '--nav-bg': 'rgba(18,18,18,0.92)', '--danger': '#e63946',
    '--danger-bg': 'rgba(230,57,70,0.1)', '--danger-border': 'rgba(230,57,70,0.25)',
    '--success': '#4ade80', '--success-bg': '#1e3a2e', '--success-border': '#2a9d5c',
    '--input-bg': '#2a2a2a', '--scrollbar': '#333',
  },
  light: {
    '--bg': '#f0f2f5', '--bg2': '#ffffff', '--bg3': '#f8f9fa', '--bg4': '#e9ecef',
    '--surface': 'rgba(0,0,0,0.02)', '--border': 'rgba(0,0,0,0.1)',
    '--border2': 'rgba(0,0,0,0.15)', '--text': '#1a1a2e', '--text2': '#2d3748',
    '--text3': '#4a5568', '--muted': '#718096', '--muted2': '#a0aec0', '--muted3': '#cbd5e0',
    '--nav-bg': 'rgba(255,255,255,0.92)', '--danger': '#dc2626',
    '--danger-bg': 'rgba(220,38,38,0.08)', '--danger-border': 'rgba(220,38,38,0.25)',
    '--success': '#16a34a', '--success-bg': '#f0fdf4', '--success-border': '#bbf7d0',
    '--input-bg': '#ffffff', '--scrollbar': '#cbd5e0',
  },
}

const DEFAULT_ACCENT: Record<string, string> = { dark: '#457b9d', light: '#2563eb' }

function hexToRgb(hex: string) {
  return { r: parseInt(hex.slice(1,3),16), g: parseInt(hex.slice(3,5),16), b: parseInt(hex.slice(5,7),16) }
}
function lighten(hex: string, amt = 0.3) {
  try { const {r,g,b} = hexToRgb(hex); return `rgb(${Math.round(r+(255-r)*amt)},${Math.round(g+(255-g)*amt)},${Math.round(b+(255-b)*amt)})` } catch { return hex }
}
function darken(hex: string, amt = 0.15) {
  try { const {r,g,b} = hexToRgb(hex); return `rgb(${Math.round(r*(1-amt))},${Math.round(g*(1-amt))},${Math.round(b*(1-amt))})` } catch { return hex }
}

function applyAccent(accent: string) {
  const root = document.documentElement
  root.style.setProperty('--accent', accent)
  root.style.setProperty('--accent-light', lighten(accent, 0.35))
  root.style.setProperty('--accent-hover', darken(accent, 0.12))
}

function applyThemeVars(name: string) {
  const vars = THEMES[name]
  const root = document.documentElement
  Object.entries(vars).forEach(([k, v]) => root.style.setProperty(k, v))
  document.documentElement.setAttribute('data-theme', name)
  const saved = localStorage.getItem(ACCENT_KEY)
  applyAccent(saved || DEFAULT_ACCENT[name])
}

interface ThemeCtx {
  theme: () => string
  setTheme: (t: string) => void
  setAccent: (hex: string) => void
  resolvedTheme: () => string
}

const ThemeContext = createContext<ThemeCtx>({} as ThemeCtx)

export function ThemeProvider(props: { children: JSX.Element }) {
  const [theme, setThemeState] = createSignal(localStorage.getItem(THEME_KEY) || 'system')

  const resolve = (t: string) =>
    t === 'system'
      ? (window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light')
      : t

  createEffect(() => {
    const currentTheme = theme()
    applyThemeVars(resolve(currentTheme))
    if (currentTheme === 'system') {
      const mq = window.matchMedia('(prefers-color-scheme: dark)')
      const handler = (e: MediaQueryListEvent) => applyThemeVars(e.matches ? 'dark' : 'light')
      mq.addEventListener('change', handler)
      return () => mq.removeEventListener('change', handler)
    }
  })

  const setTheme = (t: string) => { localStorage.setItem(THEME_KEY, t); setThemeState(t) }
  const setAccent = (hex: string) => { localStorage.setItem(ACCENT_KEY, hex); applyAccent(hex) }

  return (
    <ThemeContext.Provider value={{ theme, setTheme, setAccent, resolvedTheme: () => resolve(theme()) }}>
      {props.children}
    </ThemeContext.Provider>
  )
}

export const useTheme = () => useContext(ThemeContext)
