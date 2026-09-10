import { formatDate } from '../utils/datetime'
import { sansFont, monoFont, labelStyle, inputStyle } from '../styles/formStyles'
import { formatDateTime } from '../utils/datetime'
import { ShareLinkSection } from '../components/ShareLinkSection'
import { For, Show } from 'solid-js'
import type { Setter } from 'solid-js'
import type { Design } from '../types'
import type { DesignPageProps } from '../components/DesignPage'
import type { ShareLink } from '../types'

type ShareUser = { id: string; name: string; email: string }

export interface DesignShareTabDeps {
  props: DesignPageProps
  translate: (key: any, params?: any) => string
  lang: () => string
  design: () => Design | null
  activeTab: () => 'details' | 'files' | 'gcode' | 'notes' | 'share'
  shareOpen: () => boolean
  setShareOpen: Setter<boolean>
  shareEmailInput: () => string
  setShareEmailInput: Setter<string>
  shareUserSuggestions: () => ShareUser[]
  loadShareUserSuggestions: (query: string) => void
  sharePicked: () => ShareUser[]
  setSharePicked: Setter<ShareUser[]>
  pickShareUser: (user: ShareUser) => void
  shareWithUsers: () => void
  unshareUser: (shareId: number) => void
  isSharing: () => boolean
  shareLinks: () => ShareLink[]
  createShareLink: () => void
  deleteShareLink: (linkId: number) => void
  copyShareLink: (token: string) => void
  copiedToken: () => string
  creatingLink: () => boolean
  linkDays: () => string
  setLinkDays: Setter<string>
}

/** The share tab: who the design is shared with, and the links handed out. */
export function designShareTab(deps: DesignShareTabDeps) {
  const { props, translate, lang, design, activeTab, shareOpen, setShareOpen, shareEmailInput,
    setShareEmailInput, shareUserSuggestions, loadShareUserSuggestions, sharePicked, setSharePicked,
    pickShareUser, shareWithUsers, unshareUser, isSharing, shareLinks, createShareLink,
    deleteShareLink, copyShareLink, copiedToken, creatingLink, linkDays, setLinkDays } = deps
  return (
    <>
            {/* - Share tab - */}
            <Show when={activeTab() === 'share'}>
              <div style={{ display: 'flex', 'flex-direction': 'column', gap: '18px' }}>
                <Show when={props.isReadOnly}>
                  <div style={{ ...sansFont, 'font-size': '14px', color: 'var(--muted)', padding: '22px' }}>{translate('share_readonly_hint')}</div>
                </Show>
                <Show when={!props.isReadOnly}>
                  <div style={{ ...sansFont, 'font-size': '13px', color: 'var(--muted)' }}>{translate('share_readonly_hint')}</div>
                  {/* Recipient picker: suggestions are selected into chips, and one
                      share request goes out for all of them at once. Typing a name
                      and pressing the button used to send whatever stood there,
                      which answered a stray "a" with a success toast. */}
                  <div style={{ position: 'relative' }}>
                    <div style={{ display: 'flex', gap: '11px', 'align-items': 'flex-start' }}>
                      <div onClick={() => { setShareOpen(true); loadShareUserSuggestions(shareEmailInput()) }}
                        style={{ flex: '1', display: 'flex', 'flex-wrap': 'wrap', 'align-items': 'center', gap: '7px', ...inputStyle, height: 'auto', 'min-height': '42px', padding: '7px 11px', cursor: 'text' }}>
                        <For each={sharePicked()}>{picked => (
                          <span style={{ display: 'inline-flex', 'align-items': 'center', gap: '7px', background: 'var(--surface)', border: '1px solid var(--border2)', 'border-radius': '8px', padding: '4px 9px', ...sansFont, 'font-size': '13px', color: 'var(--text)' }}>
                            {picked.name || picked.email}
                            <button onClick={() => setSharePicked(current => current.filter(entry => entry.id !== picked.id))}
                              style={{ background: 'none', border: 'none', color: 'var(--muted)', cursor: 'pointer', padding: '0', 'font-size': '13px', 'line-height': '1' }}>✕</button>
                          </span>
                        )}</For>
                        <input value={shareEmailInput()} onInput={e => { setShareEmailInput(e.currentTarget.value); setShareOpen(true); loadShareUserSuggestions(e.currentTarget.value) }}
                          placeholder={sharePicked().length === 0 ? translate('share_search_placeholder') : ''}
                          onFocus={() => { setShareOpen(true); loadShareUserSuggestions(shareEmailInput()) }}
                          onBlur={() => setTimeout(() => setShareOpen(false), 150)}
                          onKeyDown={(e: KeyboardEvent) => {
                            if (e.key === 'Enter' && shareUserSuggestions().length > 0) { e.preventDefault(); pickShareUser(shareUserSuggestions()[0]) }
                            else if (e.key === 'Escape') setShareOpen(false)
                            else if (e.key === 'Backspace' && !shareEmailInput()) setSharePicked(current => current.slice(0, -1))
                          }}
                          style={{ flex: '1', 'min-width': '140px', background: 'none', border: 'none', outline: 'none', color: 'var(--text)', ...sansFont, 'font-size': '14px' }} />
                      </div>
                      <button onClick={shareWithUsers} disabled={isSharing() || sharePicked().length === 0}
                        style={{ padding: '10px 20px', background: 'var(--accent)', border: 'none', 'border-radius': '10px', color: '#fff', ...sansFont, 'font-size': '14px', 'font-weight': '600', cursor: 'pointer', opacity: (sharePicked().length === 0 || isSharing()) ? '0.5' : '1', 'flex-shrink': '0' }}>
                        {isSharing() ? '…' : translate('share_add_btn')}
                      </button>
                    </div>
                    <Show when={shareOpen() && shareUserSuggestions().length > 0}>
                      <div style={{ position: 'absolute', top: 'calc(100% + 6px)', left: '0', right: '0', background: 'var(--bg2)', 'border-radius': '12px', border: '1px solid var(--border)', 'box-shadow': '0 8px 24px rgba(0,0,0,0.3)', 'z-index': '50', padding: '6px', 'max-height': '200px', 'overflow-y': 'auto' }}>
                        <For each={shareUserSuggestions()}>{u => (
                          <button onMouseDown={e => e.preventDefault()} onClick={() => pickShareUser(u)}
                            style={{ width: '100%', padding: '9px 13px', background: 'none', border: 'none', 'border-radius': '8px', color: 'var(--text)', ...sansFont, 'font-size': '13px', cursor: 'pointer', 'text-align': 'left', display: 'flex', 'align-items': 'center', gap: '10px' }}
                            onMouseEnter={e => (e.currentTarget.style.background = 'var(--surface)')}
                            onMouseLeave={e => (e.currentTarget.style.background = 'none')}>
                            <div style={{ width: '32px', height: '32px', 'border-radius': '50%', background: 'var(--accent)', display: 'flex', 'align-items': 'center', 'justify-content': 'center', 'font-size': '13px', 'font-weight': '700', color: '#fff', 'flex-shrink': '0' }}>
                              {(u.name || u.email)[0].toUpperCase()}
                            </div>
                            <div>
                              <div style={{ 'font-weight': '600', color: 'var(--text)' }}>{u.name}</div>
                              <div style={{ 'font-size': '12px', color: 'var(--muted)', ...monoFont }}>{u.email}</div>
                            </div>
                          </button>
                        )}</For>
                      </div>
                    </Show>
                  </div>
                  <Show when={!design()?.shares || design()!.shares!.length === 0}>
                    <div style={{ ...sansFont, 'font-size': '14px', color: 'var(--muted)', 'text-align': 'center', padding: '22px' }}>{translate('share_empty')}</div>
                  </Show>
                  <ShareLinkSection
                    links={shareLinks()}
                    days={linkDays()}
                    onDays={setLinkDays}
                    creating={creatingLink()}
                    copiedToken={copiedToken()}
                    onCreate={createShareLink}
                    onCopy={copyShareLink}
                    onDelete={deleteShareLink}
                    translate={translate}
                    formatDate={stamp => formatDate(stamp, lang())}
                  />

                  <For each={design()!.shares || []}>{shareEntry => (
                    <div style={{ display: 'flex', 'align-items': 'center', gap: '13px', background: 'var(--surface)', border: '1px solid var(--border)', 'border-radius': '11px', padding: '11px 15px' }}>
                      <div style={{ width: '40px', height: '40px', 'border-radius': '50%', background: 'var(--accent)', display: 'flex', 'align-items': 'center', 'justify-content': 'center', 'font-size': '16px', 'font-weight': '700', color: '#fff', 'flex-shrink': '0' }}>
                        {shareEntry.shared_with_name?.[0]?.toUpperCase() || '?'}
                      </div>
                      <div style={{ flex: '1' }}>
                        <div style={{ ...sansFont, 'font-size': '14px', 'font-weight': '600', color: 'var(--text)' }}>{shareEntry.shared_with_name}</div>
                        <div style={{ ...monoFont, 'font-size': '12px', color: 'var(--muted)' }}>{shareEntry.shared_with_email}</div>
                      </div>
                      <button onClick={() => unshareUser(shareEntry.id)}
                        style={{ padding: '6px 13px', background: 'var(--danger-bg)', border: '1px solid var(--danger-border)', 'border-radius': '8px', color: 'var(--danger)', 'font-size': '12px', cursor: 'pointer', ...sansFont }}>
                        {translate('btn_remove')}
                      </button>
                    </div>
                  )}</For>
                </Show>
              </div>
            </Show>
    </>
  )
}
