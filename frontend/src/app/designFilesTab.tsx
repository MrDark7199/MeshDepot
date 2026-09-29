import { api } from '../services/api'
import { formatDate } from '../utils/datetime'
import { is3dFile } from '../utils/meshTools'
import { sansFont, monoFont } from '../styles/formStyles'
import { formatBytes } from '../utils/format'
import { formatDateTime } from '../utils/datetime'
import { For, Show } from 'solid-js'
import type { Setter } from 'solid-js'
import type { Design } from '../types'
import type { DesignPageProps } from '../components/DesignPage'
import type { DesignFile, DesignFileEntry, GcodeMeta } from '../types'

export interface DesignFilesTabDeps {
  props: DesignPageProps
  translate: (key: any, params?: any) => string
  lang: () => string
  user: () => any
  design: () => Design | null
  activeTab: () => 'details' | 'files' | 'gcode' | 'notes' | 'share'
  fileVersions: () => DesignFile[]
  isLoadingFiles: () => boolean
  expandedVersionIds: () => Set<number>
  setExpandedVersionIds: Setter<Set<number>>
  collapsedFolders: () => Set<string>
  setCollapsedFolders: Setter<Set<string>>
  openAddFiles: (fileVersionId: number) => void
  deleteFileEntry: (fileVersionId: number, entry: DesignFileEntry) => void
  setConfirmDeleteFileId: Setter<number | null>
  showEntryInViewer: (fileVersionId: number, entry: DesignFileEntry) => void
  isGcodeFile: (name: string) => boolean
  isResinFile: (name: string) => boolean
  isResinViewable: (name: string) => boolean
  gcodeSummary: (meta: GcodeMeta) => string
}

/** The files tab: every version with its entries, plus the upload form. */
export function designFilesTab(deps: DesignFilesTabDeps) {
  const { props, translate, lang, user, design, activeTab, fileVersions, isLoadingFiles,
    expandedVersionIds, setExpandedVersionIds, collapsedFolders, setCollapsedFolders,
    openAddFiles, deleteFileEntry,
    setConfirmDeleteFileId, showEntryInViewer, isGcodeFile, isResinFile, isResinViewable,
    gcodeSummary } = deps
  return (
    <>
            {/* - Files tab - */}
            <Show when={activeTab() === 'files'}>
              <div style={{ display: 'flex', 'flex-direction': 'column', gap: '18px' }}>
                <Show when={isLoadingFiles()}>
                  <div style={{ color: 'var(--muted)', ...sansFont }}>{translate('label_loading')}</div>
                </Show>
                <For each={fileVersions()}>{fileVersion => (
                  <div style={{ background: 'var(--bg2)', border: '1px solid var(--border)', 'border-radius': '16px', overflow: 'hidden' }}>
                    {/* Version header (click to expand/collapse) */}
                    <div
                      onClick={() => setExpandedVersionIds(currentSet => {
                        const nextSet = new Set(currentSet)
                        if (nextSet.has(fileVersion.id)) nextSet.delete(fileVersion.id); else nextSet.add(fileVersion.id)
                        return nextSet
                      })}
                      style={{ padding: '15px 20px', background: 'var(--bg3)', 'border-bottom': expandedVersionIds().has(fileVersion.id) ? '1px solid var(--border)' : 'none', display: 'flex', 'align-items': 'center', gap: '11px', cursor: 'pointer', 'user-select': 'none' }}>
                      <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="var(--muted)" stroke-width="2.5"
                        style={{ transform: expandedVersionIds().has(fileVersion.id) ? 'rotate(0deg)' : 'rotate(-90deg)', transition: 'transform 0.15s', 'flex-shrink': '0' }}>
                        <polyline points="6 9 12 15 18 9"/>
                      </svg>
                      <span style={{ ...monoFont, 'font-size': '14px', 'font-weight': '700', color: 'var(--accent)' }}>v{fileVersion.version}</span>
                      <Show when={fileVersion.is_current}>
                        <span style={{ ...monoFont, 'font-size': '10px', 'font-weight': '700', background: 'var(--accent)', color: '#fff', 'border-radius': '5px', padding: '2px 7px' }}>{translate('label_current')}</span>
                      </Show>
                      <Show when={fileVersion.notes}>
                        <span style={{ ...sansFont, 'font-size': '13px', color: 'var(--muted)', 'font-style': 'italic' }}>
                          {fileVersion.notes === 'Downloaded' ? translate('file_version_note_downloaded') : fileVersion.notes}
                        </span>
                      </Show>
                      <div onClick={e => e.stopPropagation()} style={{ 'margin-left': 'auto', display: 'flex', gap: '9px', 'align-items': 'center' }}>
                        <span style={{ ...monoFont, 'font-size': '12px', color: 'var(--muted)' }}>
                          {formatDate(fileVersion.created_at, lang())}
                        </span>
                        {(() => {
                          const totalBytes = (fileVersion.entries || []).reduce((sum, entry) => sum + (entry.size_bytes || 0), 0) || fileVersion.size_bytes || 0
                          return totalBytes > 0 ? (
                            <span style={{ ...monoFont, 'font-size': '12px', color: 'var(--muted)' }}>
                              {formatBytes(totalBytes)}
                            </span>
                          ) : null
                        })()}
                        <a href={api.downloadUrl(props.designId, fileVersion.id)} title={translate('btn_download')}
                          style={{ padding: '6px 14px', background: 'var(--bg4)', border: '1px solid var(--border2)', 'border-radius': '8px', color: 'var(--text)', 'font-size': '12px', ...monoFont, 'text-decoration': 'none', display: 'flex', 'align-items': 'center', gap: '6px' }}>
                          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5">
                            <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/>
                            <polyline points="7 10 12 15 17 10"/><line x1="12" y1="15" x2="12" y2="3"/>
                          </svg>
                          ZIP
                        </a>
                        <Show when={!props.isReadOnly}>
                          <button title={translate('btn_add_files_title')}
                            onClick={e => { e.stopPropagation(); openAddFiles(fileVersion.id) }}
                            style={{ padding: '6px 12px', background: 'var(--bg4)', border: '1px solid var(--border2)', 'border-radius': '8px', color: 'var(--text)', 'font-size': '12px', cursor: 'pointer', ...monoFont, display: 'flex', 'align-items': 'center', gap: '5px' }}>
                            ＋ {translate('btn_add_files')}
                          </button>
                          <button onClick={() => setConfirmDeleteFileId(fileVersion.id)}
                            style={{ padding: '6px 11px', background: 'var(--danger-bg)', border: '1px solid var(--danger-border)', 'border-radius': '8px', color: 'var(--danger)', 'font-size': '12px', cursor: 'pointer', ...monoFont }}>✕</button>
                        </Show>
                      </div>
                    </div>

                    {/* File entries with folder structure */}
                    <Show when={expandedVersionIds().has(fileVersion.id)}>
                    <div style={{ padding: '9px' }}>
                      <Show when={!fileVersion.entries || fileVersion.entries.length === 0}>
                        <div style={{ ...sansFont, 'font-size': '13px', color: 'var(--muted)', padding: '11px', 'text-align': 'center' }}>{translate('label_no_files_recorded')}</div>
                      </Show>
                      {(() => {
                        const entries = fileVersion.entries || []
                        // Group by folder (from relative_path)
                        const folders: Record<string, typeof entries> = {}
                        for (const entry of entries) {
                          const rel = (entry as any).relative_path || entry.filename
                          const directory = rel.includes('/') ? rel.substring(0, rel.lastIndexOf('/')) : ''
                          if (!folders[directory]) folders[directory] = []
                          folders[directory].push(entry)
                        }
                        const folderKeys = Object.keys(folders).sort()
                        return (
                          <For each={folderKeys}>{folderKey => (
                            <div>
                              <Show when={folderKey !== ''}>
                                <div
                                  onClick={() => setCollapsedFolders(currentSet => {
                                    const key = fileVersion.id + ':' + folderKey
                                    const nextSet = new Set(currentSet)
                                    if (nextSet.has(key)) nextSet.delete(key); else nextSet.add(key)
                                    return nextSet
                                  })}
                                  style={{ display: 'flex', 'align-items': 'center', gap: '7px', padding: '6px 11px 4px', color: 'var(--muted)', ...monoFont, 'font-size': '12px', cursor: 'pointer', 'user-select': 'none' }}>
                                  <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"
                                    style={{ transform: collapsedFolders().has(fileVersion.id + ':' + folderKey) ? 'rotate(-90deg)' : 'rotate(0deg)', transition: 'transform 0.15s' }}>
                                    <polyline points="6 9 12 15 18 9"/>
                                  </svg>
                                  <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z"/></svg>
                                  {folderKey}
                                </div>
                              </Show>
                              <Show when={folderKey === '' || !collapsedFolders().has(fileVersion.id + ':' + folderKey)}>
                              <For each={folders[folderKey]}>{entry => (
                                <div style={{ display: 'flex', 'align-items': 'center', gap: '11px', padding: '8px 11px 8px ' + (folderKey !== '' ? '28px' : '11px'), 'border-radius': '9px', background: 'transparent', 'transition': 'background 0.1s' }}
                                  onMouseEnter={e => (e.currentTarget.style.background = 'var(--surface)')}
                                  onMouseLeave={e => (e.currentTarget.style.background = 'transparent')}>
                                  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="var(--text3)" stroke-width="1.8">
                                    <path d="M13 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V9z"/>
                                    <polyline points="13 2 13 9 20 9"/>
                                  </svg>
                                  <div style={{ flex: '1', 'min-width': '0', display: 'flex', 'flex-direction': 'column', gap: '2px' }}>
                                    <span style={{ ...sansFont, 'font-size': '13px', color: 'var(--text)', overflow: 'hidden', 'text-overflow': 'ellipsis', 'white-space': 'nowrap' }}>{entry.filename}</span>
                                    <Show when={entry.gcode_meta && gcodeSummary(entry.gcode_meta)}>
                                      <span style={{ ...monoFont, 'font-size': '11px', color: 'var(--accent-light)', overflow: 'hidden', 'text-overflow': 'ellipsis', 'white-space': 'nowrap' }}>{gcodeSummary(entry.gcode_meta!)}</span>
                                    </Show>
                                    <Show when={isResinFile(entry.filename)}>
                                      <span style={{ ...monoFont, 'font-size': '10px', 'font-weight': '700', color: '#fff', background: 'rgba(124,58,237,0.85)', 'border-radius': '4px', padding: '1px 6px', 'align-self': 'flex-start' }}>{translate('label_resin')}</span>
                                    </Show>
                                  </div>
                                  <span style={{ ...monoFont, 'font-size': '12px', color: 'var(--muted)', 'flex-shrink': '0' }}>{formatBytes(entry.size_bytes)}</span>
                                  <div style={{ display: 'flex', gap: '6px', 'flex-shrink': '0' }}>
                                    <Show when={is3dFile(entry.filename) || isGcodeFile(entry.filename) || isResinViewable(entry.filename)}>
                                      <button title={isGcodeFile(entry.filename) ? translate('btn_view_gcode') : translate('btn_view_3d')}
                                        onClick={() => showEntryInViewer(fileVersion.id, entry)}
                                        onMouseDown={e => { if (e.button === 1) e.preventDefault() }}
                                        onAuxClick={e => { if (e.button === 1) { e.preventDefault(); window.open(`${window.location.pathname}?design=${props.designId}&viewer=${fileVersion.id}-${entry.id}`, '_blank', 'noopener') } }}
                                        style={{ padding: '5px 11px', background: 'var(--accent)', border: 'none', 'border-radius': '7px', color: '#fff', 'font-size': '11px', cursor: 'pointer', ...monoFont, 'font-weight': '600' }}>
                                        {isGcodeFile(entry.filename) ? 'GCODE' : '3D'}
                                      </button>
                                    </Show>
                                    <a href={api.entryUrl(props.designId, fileVersion.id, entry.id)} download={entry.filename} title={translate('btn_download')}
                                      style={{ padding: '5px 9px', background: 'var(--bg4)', border: '1px solid var(--border2)', 'border-radius': '7px', color: 'var(--text)', 'font-size': '11px', ...monoFont, 'text-decoration': 'none', display: 'flex', 'align-items': 'center' }}>↓</a>
                                    {/* Every file of an own design is deletable. Restricting this to
                                        printer formats left models (stl, 3mf, obj) in place with no way
                                        to remove them. */}
                                    <Show when={!props.isReadOnly}>
                                      <button onClick={() => deleteFileEntry(fileVersion.id, entry)} title={translate('btn_delete')}
                                        style={{ padding: '5px 9px', background: 'var(--danger-bg)', border: '1px solid var(--danger-border)', 'border-radius': '7px', color: 'var(--danger)', 'font-size': '11px', cursor: 'pointer', ...monoFont, display: 'flex', 'align-items': 'center' }}>✕</button>
                                    </Show>
                                  </div>
                                </div>
                              )}</For>
                              </Show>
                            </div>
                          )}</For>
                        )
                      })()}
                    </div>
                    </Show>
                  </div>
                )}</For>

              </div>
            </Show>
    </>
  )
}
