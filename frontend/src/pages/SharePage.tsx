import { createSignal, createEffect, For, Show, onMount } from 'solid-js'
import { useI18n } from '../i18n/index'
import { buildDescriptionFragment, descriptionCss } from '../utils/description'
import { PLATFORM_COLORS, PLATFORM_LABELS } from '../constants/platforms'

/**
 * The design behind a share link, for someone with no account.
 *
 * Deliberately its own page rather than the detail view in a read-only mode:
 * that view assumes a session on nearly every line - it edits, syncs, shares
 * and deletes - and the safest way not to offer any of it is not to mount it.
 * What arrives here is only what the server chose to put in the public payload.
 */

const sans = { 'font-family': "'DM Sans',sans-serif" }
const mono = { 'font-family': "'DM Mono',monospace" }

interface SharedFile {
  id: number
  filename: string
  size_bytes?: number
}

interface SharedDesign {
  name: string
  description?: string | null
  author?: string | null
  source_url?: string | null
  source_platform?: string | null
  cover_path?: string | null
  category?: string | null
  license?: string | null
  version?: string
  images?: { path: string; is_cover?: number }[]
  files?: SharedFile[]
}

function formatBytes(bytes?: number): string {
  if (!bytes) return ''
  if (bytes >= 1073741824) return (bytes / 1073741824).toFixed(1) + ' GB'
  if (bytes >= 1048576) return (bytes / 1048576).toFixed(1) + ' MB'
  if (bytes >= 1024) return (bytes / 1024).toFixed(0) + ' KB'
  return bytes + ' B'
}

export default function SharePage(props: { token: string }) {
  const { translate } = useI18n()
  const [design, setDesign] = createSignal<SharedDesign | null>(null)
  const [failed, setFailed] = createSignal(false)
  const [activeImage, setActiveImage] = createSignal(0)

  const base = `/api/v1/public/share/${encodeURIComponent(props.token)}`

  onMount(() => {
    fetch(base)
      .then(response => response.ok ? response.json() : Promise.reject(response.status))
      .then(payload => setDesign(payload.data))
      .catch(() => setFailed(true))
  })

  const images = () => {
    const shared = design()
    if (!shared) return []
    if (shared.images?.length) return shared.images.map(image => image.path)
    return shared.cover_path ? [shared.cover_path] : []
  }
  // The token travels with every picture: this page has no session, and the
  // image endpoint now asks who is looking before it serves a design's cover.
  const imageUrl = (path: string) =>
    `/api/v1/covers/${path}?share=${encodeURIComponent(props.token)}`

  // The description arrives as stored: platform HTML for an imported design,
  // otherwise plain text. Rendering and styling it are shared with the detail
  // view, so a share link shows the same thing an account holder sees.
  let descriptionHost: HTMLDivElement | undefined
  createEffect(() => {
    const raw = design()?.description || ''
    if (!descriptionHost) return
    descriptionHost.replaceChildren(raw ? buildDescriptionFragment(raw, design()?.source_platform) : '')
  })

  return (
    <div style={{ 'min-height': '100vh', background: 'var(--bg)', padding: '0 0 60px' }}>
      <div style={{ background: 'var(--nav-bg)', 'border-bottom': '1px solid var(--border)', padding: '18px 32px', display: 'flex', 'align-items': 'center', gap: '12px' }}>
        <div style={{ ...sans, 'font-weight': '700', 'font-size': '22px', color: 'var(--text)', 'letter-spacing': '-0.02em' }}>
          Mesh<span style={{ color: 'var(--accent)' }}>Depot</span>
        </div>
        <span style={{ ...mono, 'font-size': '11px', color: 'var(--muted)', border: '1px solid var(--border)', 'border-radius': '6px', padding: '3px 9px' }}>
          {translate('share_link_badge')}
        </span>
      </div>

      <Show when={failed()}>
        <div style={{ padding: '80px 32px', 'max-width': '560px', margin: '0 auto', 'text-align': 'center' }}>
          <div style={{ 'font-size': '42px', 'margin-bottom': '14px', opacity: '0.35' }}>🔗</div>
          <div style={{ ...sans, 'font-size': '18px', 'font-weight': '700', color: 'var(--text)', 'margin-bottom': '8px' }}>
            {translate('share_link_invalid_title')}
          </div>
          <div style={{ ...sans, 'font-size': '14px', color: 'var(--muted)', 'line-height': '1.6' }}>
            {translate('share_link_invalid_hint')}
          </div>
        </div>
      </Show>

      <Show when={design()}>
        {shared => (
          <div style={{ padding: '32px 24px', 'max-width': '820px', margin: '0 auto', display: 'flex', 'flex-direction': 'column', gap: '22px' }}>
            {/* Name first: whoever opens the link wants to know what it is
                before they see a picture of it. */}
            <div>
              <h1 style={{ ...sans, 'font-size': '28px', 'font-weight': '700', color: 'var(--text)', margin: '0 0 8px', 'line-height': '1.25' }}>
                {shared().name}
              </h1>
              <div style={{ display: 'flex', gap: '9px', 'align-items': 'center', 'flex-wrap': 'wrap' }}>
                <Show when={shared().source_platform && shared().source_platform !== 'manual'}>
                  <span style={{ ...mono, 'font-size': '11px', 'font-weight': '700', color: '#fff', 'border-radius': '7px', padding: '3px 9px',
                    background: PLATFORM_COLORS[shared().source_platform!] || 'var(--muted)' }}>
                    {PLATFORM_LABELS[shared().source_platform!] || shared().source_platform}
                  </span>
                </Show>
                <Show when={shared().author}>
                  <span style={{ ...sans, 'font-size': '13px', color: 'var(--muted)' }}>{translate('card_by')} {shared().author}</span>
                </Show>
              </div>
            </div>

            <div>
              <Show when={images().length > 0} fallback={
                <div style={{ height: '360px', 'border-radius': '16px', background: 'var(--bg3)', display: 'flex', 'align-items': 'center', 'justify-content': 'center', 'font-size': '56px', opacity: '0.25' }}>🖨️</div>
              }>
                {/* contain, not cover: this is the one picture the recipient came
                    for, and a fixed frame that crops it takes the top and bottom
                    off every portrait photo. The frame keeps its height, the
                    background fills what the image leaves over. */}
                <img src={imageUrl(images()[activeImage()] || images()[0])} alt={shared().name}
                  style={{ width: '100%', height: '360px', 'object-fit': 'contain', background: 'var(--bg3)',
                    'border-radius': '16px', border: '1px solid var(--border)' }} />
              </Show>
              <Show when={images().length > 1}>
                <div style={{ display: 'flex', gap: '8px', 'margin-top': '10px', 'overflow-x': 'auto', 'padding-bottom': '4px' }}>
                  <For each={images()}>{(path, index) => (
                    <img src={imageUrl(path)} alt="" onClick={() => setActiveImage(index())}
                      style={{ width: '72px', height: '72px', 'object-fit': 'cover', 'border-radius': '9px', cursor: 'pointer', 'flex-shrink': '0',
                        border: activeImage() === index() ? '2px solid var(--accent)' : '2px solid var(--border)' }} />
                  )}</For>
                </div>
              </Show>
            </div>

            <div>
              <div style={{ display: 'flex', 'align-items': 'center', gap: '12px', 'margin-bottom': '10px', 'flex-wrap': 'wrap' }}>
                <div style={{ ...mono, 'font-size': '11px', color: 'var(--muted)', 'text-transform': 'uppercase', 'letter-spacing': '0.06em', flex: '1' }}>
                  {translate('tab_files')}
                </div>
                <Show when={(shared().files?.length ?? 0) > 1}>
                  <a href={`${base}/download`} download=""
                    style={{ display: 'inline-flex', 'align-items': 'center', gap: '7px', padding: '8px 15px', background: 'var(--accent)',
                      'border-radius': '9px', color: '#fff', ...sans, 'font-size': '13px', 'font-weight': '600', 'text-decoration': 'none' }}>
                    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4">
                      <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><polyline points="7 10 12 15 17 10"/><line x1="12" y1="15" x2="12" y2="3"/>
                    </svg>
                    {translate('share_link_download_all')}
                  </a>
                </Show>
              </div>
              <Show when={shared().files?.length} fallback={
                <div style={{ ...sans, 'font-size': '13px', color: 'var(--muted)', 'font-style': 'italic' }}>{translate('label_no_files')}</div>
              }>
                <div style={{ display: 'flex', 'flex-direction': 'column', gap: '7px' }}>
                  <For each={shared().files}>{file => (
                    <a href={`${base}/files/${file.id}`} download=""
                      style={{ display: 'flex', 'align-items': 'center', gap: '11px', background: 'var(--surface)', border: '1px solid var(--border)',
                        'border-radius': '10px', padding: '10px 14px', 'text-decoration': 'none' }}>
                      <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="var(--accent)" stroke-width="2.2" style={{ 'flex-shrink': '0' }}>
                        <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><polyline points="7 10 12 15 17 10"/><line x1="12" y1="15" x2="12" y2="3"/>
                      </svg>
                      <span style={{ ...sans, 'font-size': '13px', color: 'var(--text)', flex: '1', overflow: 'hidden', 'text-overflow': 'ellipsis', 'white-space': 'nowrap' }}>
                        {file.filename}
                      </span>
                      <span style={{ ...mono, 'font-size': '11px', color: 'var(--muted)', 'flex-shrink': '0' }}>{formatBytes(file.size_bytes)}</span>
                    </a>
                  )}</For>
                </div>
              </Show>
            </div>

            <div style={{ display: 'grid', 'grid-template-columns': 'repeat(auto-fit, minmax(150px, 1fr))', gap: '10px' }}>
              <For each={[
                { label: translate('field_category'), value: shared().category },
                { label: translate('field_license'),  value: shared().license },
                { label: translate('label_version'),  value: shared().version ? 'v' + shared().version : '' },
              ].filter(item => item.value)}>{item => (
                <div style={{ background: 'var(--surface)', border: '1px solid var(--border)', 'border-radius': '10px', padding: '9px 13px' }}>
                  <div style={{ ...mono, 'font-size': '10px', color: 'var(--muted)', 'text-transform': 'uppercase', 'letter-spacing': '0.05em' }}>{item.label}</div>
                  <div style={{ ...sans, 'font-size': '13px', color: 'var(--text2)', 'font-weight': '500' }}>{item.value}</div>
                </div>
              )}</For>
            </div>

            <Show when={shared().source_url}>
              <a href={shared().source_url!} target="_blank" rel="noopener noreferrer"
                style={{ ...mono, 'font-size': '12px', color: 'var(--accent)', 'overflow-wrap': 'anywhere' }}>
                {shared().source_url}
              </a>
            </Show>

            <Show when={shared().description}>
              <div>
                <div style={{ ...mono, 'font-size': '11px', color: 'var(--muted)', 'text-transform': 'uppercase', 'letter-spacing': '0.06em', 'margin-bottom': '8px' }}>
                  {translate('field_description')}
                </div>
                <style>{descriptionCss}</style>
                <div ref={descriptionHost} class="design-description"
                  style={{ ...sans, 'font-size': '14px', color: 'var(--text2)', 'line-height': '1.7', 'overflow-wrap': 'anywhere' }} />
              </div>
            </Show>
          </div>
        )}
      </Show>
    </div>
  )
}
