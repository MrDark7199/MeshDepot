import { Show, createSignal } from 'solid-js'
import { api } from '../services/api'
import { PAGE_X } from '../constants/layout'

interface VersionInfo {
  current: string
  latest: string
  newer: boolean
  url: string
}

const CLOSED_KEY = 'meshdepot_update_notice_closed'

const readClosedVersion = (): string => {
  try { return sessionStorage.getItem(CLOSED_KEY) ?? '' } catch { return '' }
}

/**
 * Tells an admin that a newer MeshDepot has been released on GitHub. Quiet on
 * purpose: a line under the logo, not a banner over the library. Closing it
 * lasts for the browser session and for that version only, so a later release
 * shows up again.
 */
export function UpdateNotice(props: { translate: (key: any, params?: any) => string }) {
  const [info, setInfo] = createSignal<VersionInfo | null>(null)
  const [closedVersion, setClosedVersion] = createSignal(readClosedVersion())
  api.adminVersion().then((response: any) => setInfo(response.data)).catch(() => {})

  const visible = () => {
    const current = info()
    return !!current && current.newer && current.latest !== closedVersion()
  }
  const close = () => {
    const latest = info()?.latest ?? ''
    setClosedVersion(latest)
    try { sessionStorage.setItem(CLOSED_KEY, latest) } catch { /* hidden for this page only */ }
  }

  return (
    <Show when={visible()}>
      <div class="stlv-update-notice" style={{ position:'absolute', top:'calc(100% + 8px)', left:PAGE_X, display:'flex', 'align-items':'center', gap:'12px',
        padding:'6px 8px 6px 12px', background:'var(--nav-bg)', 'backdrop-filter':'blur(16px)', border:'1px solid var(--border)',
        'border-radius':'10px', 'font-family':"'DM Sans',sans-serif", 'font-size':'12px', color:'var(--muted)', 'white-space':'nowrap',
        'box-shadow':'0 4px 16px rgba(0,0,0,0.12)' }}>
        <Show when={info()!.url} fallback={<span>{props.translate('nav_update_available', { version: info()!.latest })}</span>}>
          <a href={info()!.url} target="_blank" rel="noopener noreferrer" style={{ color:'var(--accent)', 'text-decoration':'none', 'font-weight':'600' }}>
            {props.translate('nav_update_available', { version: info()!.latest })}
          </a>
        </Show>
        <button onClick={close}
          style={{ background:'transparent', border:'1px solid var(--border)', 'border-radius':'7px', padding:'2px 9px', color:'var(--muted)', 'font-size':'11px', cursor:'pointer', 'font-family':"'DM Sans',sans-serif" }}>
          {props.translate('btn_close')}
        </button>
      </div>
    </Show>
  )
}
