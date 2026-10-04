import { api } from '../services/api'
import { formatDate } from '../utils/datetime'
import { is3dFile } from '../utils/meshTools'
import { sansFont, monoFont } from '../styles/formStyles'
import { formatBytes } from '../utils/format'
import { formatDateTime } from '../utils/datetime'
import { For, Show, createMemo, createSignal } from 'solid-js'
import { diffVersions, diffSummary, type DiffRow, type DiffStatus } from '../utils/versionDiff'
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
  /** Opens the 3D viewer with both fassungen of one file in it. */
  compareInViewer: (olderVersionId: number, olderEntry: DesignFileEntry,
                    newerVersionId: number, newerEntry: DesignFileEntry) => void
  /** Asks for a name and creates the folder in that version. */
  openNewFolder: (fileVersionId: number) => void
  /** Asks for a new name for a folder that is already there. */
  openRenameFolder: (fileVersionId: number, folder: string) => void
  /** Puts one file into a folder of its version, "" for the version's root. The
   *  order, when given, is that folder's files as they should read afterwards. */
  moveEntryToFolder: (fileVersionId: number, entryId: number, folder: string, order?: number[]) => void
  /** Arranges one folder's files, nothing changing folder. */
  reorderEntries: (fileVersionId: number, entryIds: number[]) => void
  /** Asks what should happen to the folder and the files it holds. */
  askDeleteFolder: (fileVersionId: number, folder: string, fileCount: number) => void
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
    openAddFiles, openNewFolder, openRenameFolder, moveEntryToFolder, reorderEntries, askDeleteFolder,
    compareInViewer, deleteFileEntry,
    setConfirmDeleteFileId, showEntryInViewer, isGcodeFile, isResinFile, isResinViewable,
    gcodeSummary } = deps

  // Which two versions are held against each other. Empty until asked for, so
  // the tab looks as it did for anybody not comparing anything.
  const [compareOpen, setCompareOpen] = createSignal(false)
  const [olderId, setOlderId] = createSignal<number | null>(null)
  const [newerId, setNewerId] = createSignal<number | null>(null)
  const versionById = (id: number | null) => fileVersions().find(version => version.id === id)

  // The versions newest first, as the list shows them; the two picks default to
  // the newest and the one before it, which is the comparison people mean.
  const openCompare = () => {
    const versions = fileVersions()
    if (versions.length >= 2) {
      setNewerId(current => current ?? versions[0].id)
      setOlderId(current => current ?? versions[1].id)
    }
    setCompareOpen(true)
  }

  const diffRows = createMemo<DiffRow[]>(() => {
    const older = versionById(olderId())
    const newer = versionById(newerId())
    if (!older || !newer || older.id === newer.id) return []
    return diffVersions(older, newer)
  })

  const statusColour: Record<DiffStatus, string> = {
    changed: 'var(--accent-light)', added: 'var(--success)', removed: 'var(--danger)',
    renamed: '#e9c46a', same: 'var(--muted)',
  }
  const signedSize = (bytes: number) => (bytes > 0 ? '+' : '') + formatBytes(Math.abs(bytes)).replace(/^/, bytes < 0 ? '-' : '')

  // The file being dragged, and the folder row under the pointer. Both are only
  // interesting while a drag is going on, so they live here rather than on the
  // page.
  const [draggedFile, setDraggedFile] = createSignal<{ versionId: number; entryId: number; from: string } | null>(null)
  const [dropTarget, setDropTarget] = createSignal('')
  // The gap the file would drop into: the row it is over and which side of it.
  // Null while the pointer is over a folder but not over any particular row,
  // which means "at the end".
  const [insertBefore, setInsertBefore] = createSignal<{ entryId: number; above: boolean } | null>(null)
  const folderKeyOf = (versionId: number, folder: string) => versionId + ':' + folder

  return (
    <>
            {/* - Files tab - */}
            <Show when={activeTab() === 'files'}>
              <div style={{ display: 'flex', 'flex-direction': 'column', gap: '18px' }}>
                <Show when={isLoadingFiles()}>
                  <div style={{ color: 'var(--muted)', ...sansFont }}>{translate('label_loading')}</div>
                </Show>

                {/* Comparing two versions. Only worth offering from the second
                    version onwards, and folded away until it is asked for. */}
                <Show when={fileVersions().length > 1}>
                  <Show when={compareOpen()} fallback={
                    <button onClick={openCompare}
                      style={{ 'align-self': 'flex-start', padding: '7px 15px', background: 'var(--bg4)', border: '1px solid var(--border2)', 'border-radius': '9px', color: 'var(--text)', 'font-size': '13px', cursor: 'pointer', ...sansFont, 'font-weight': '500' }}>
                      {translate('btn_compare_versions')}
                    </button>
                  }>
                    <div style={{ background: 'var(--bg2)', border: '1px solid var(--border)', 'border-radius': '16px', padding: '18px 20px' }}>
                      <div style={{ display: 'flex', 'align-items': 'center', gap: '11px', 'flex-wrap': 'wrap' }}>
                        <span style={{ ...monoFont, 'font-size': '11px', color: 'var(--muted)', 'text-transform': 'uppercase', 'letter-spacing': '0.08em' }}>
                          {translate('compare_title')}
                        </span>
                        <select value={String(olderId() ?? '')} onChange={event => setOlderId(parseInt(event.currentTarget.value, 10))}
                          style={{ background: 'var(--input-bg)', border: '1px solid var(--border2)', 'border-radius': '8px', padding: '6px 10px', color: 'var(--text)', ...monoFont, 'font-size': '12px' }}>
                          <For each={fileVersions()}>{version => <option value={String(version.id)}>v{version.version}</option>}</For>
                        </select>
                        <span style={{ ...monoFont, 'font-size': '12px', color: 'var(--muted)' }}>→</span>
                        <select value={String(newerId() ?? '')} onChange={event => setNewerId(parseInt(event.currentTarget.value, 10))}
                          style={{ background: 'var(--input-bg)', border: '1px solid var(--border2)', 'border-radius': '8px', padding: '6px 10px', color: 'var(--text)', ...monoFont, 'font-size': '12px' }}>
                          <For each={fileVersions()}>{version => <option value={String(version.id)}>v{version.version}</option>}</For>
                        </select>
                        <button onClick={() => setCompareOpen(false)}
                          style={{ 'margin-left': 'auto', padding: '5px 11px', background: 'var(--danger-bg)', border: '1px solid var(--danger-border)', 'border-radius': '8px', color: 'var(--danger)', 'font-size': '12px', cursor: 'pointer', ...monoFont }}>✕</button>
                      </div>

                      <Show when={diffRows().length > 0} fallback={
                        <div style={{ ...sansFont, 'font-size': '13px', color: 'var(--muted)', 'font-style': 'italic', 'margin-top': '14px' }}>
                          {translate('compare_pick_two')}
                        </div>
                      }>
                        {(() => {
                          const counts = diffSummary(diffRows())
                          return (
                            <div style={{ ...monoFont, 'font-size': '12px', color: 'var(--muted)', 'margin-top': '12px' }}>
                              {translate('compare_summary')
                                .replace('{changed}', String(counts.changed))
                                .replace('{added}', String(counts.added))
                                .replace('{removed}', String(counts.removed))
                                .replace('{same}', String(counts.same + counts.renamed))}
                            </div>
                          )
                        })()}
                        <div style={{ 'margin-top': '12px', display: 'flex', 'flex-direction': 'column', gap: '2px' }}>
                          <For each={diffRows()}>{row => (
                            <div style={{ display: 'flex', 'align-items': 'center', gap: '11px', padding: '7px 11px', 'border-radius': '9px', background: row.status === 'same' ? 'transparent' : 'var(--surface)' }}>
                              <span style={{ ...monoFont, 'font-size': '10px', 'font-weight': '700', color: statusColour[row.status], 'text-transform': 'uppercase', 'letter-spacing': '0.06em', width: '76px', 'flex-shrink': '0' }}>
                                {translate(`compare_status_${row.status}` as any)}
                              </span>
                              <span style={{ ...sansFont, 'font-size': '13px', color: row.status === 'same' ? 'var(--muted)' : 'var(--text)', flex: '1', 'min-width': '0', overflow: 'hidden', 'text-overflow': 'ellipsis', 'white-space': 'nowrap' }}>
                                {row.filename}
                                <Show when={row.previousName}>
                                  <span style={{ ...monoFont, 'font-size': '11px', color: 'var(--muted)' }}>{'  ← ' + row.previousName}</span>
                                </Show>
                              </span>
                              <Show when={row.sizeDelta}>
                                <span style={{ ...monoFont, 'font-size': '11px', color: 'var(--muted)', 'flex-shrink': '0' }}>{signedSize(row.sizeDelta!)}</span>
                              </Show>
                              {/* Only a changed mesh is worth holding against its
                                  older self; the rest have nothing to overlay. */}
                              <Show when={row.status === 'changed' && row.newer && row.older && is3dFile(row.filename)}>
                                <button onClick={() => compareInViewer(olderId()!, row.older!, newerId()!, row.newer!)}
                                  style={{ padding: '4px 10px', background: 'var(--accent)', border: 'none', 'border-radius': '7px', color: '#fff', 'font-size': '11px', cursor: 'pointer', ...monoFont, 'font-weight': '600', 'flex-shrink': '0' }}>
                                  {translate('compare_in_3d')}
                                </button>
                              </Show>
                            </div>
                          )}</For>
                        </div>
                      </Show>
                    </div>
                  </Show>
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
                          <button title={translate('btn_new_folder_title')}
                            onClick={e => { e.stopPropagation(); openNewFolder(fileVersion.id) }}
                            style={{ padding: '6px 12px', background: 'var(--bg4)', border: '1px solid var(--border2)', 'border-radius': '8px', color: 'var(--text)', 'font-size': '12px', cursor: 'pointer', ...monoFont, display: 'flex', 'align-items': 'center', gap: '5px' }}>
                            ＋ {translate('btn_new_folder')}
                          </button>
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
                        // A folder with files follows from their relative_path; one that is
                        // still empty comes from the server's own list. Both are shown, or a
                        // folder just created would look as though nothing had happened.
                        const inFolder: Record<string, DesignFileEntry[]> = { '': [] }
                        for (const folder of fileVersion.folders ?? []) inFolder[folder] = []
                        for (const entry of entries) {
                          const relativePath = entry.relative_path || entry.filename
                          const directory = relativePath.includes('/') ? relativePath.substring(0, relativePath.lastIndexOf('/')) : ''
                          if (!inFolder[directory]) inFolder[directory] = []
                          inFolder[directory].push(entry)
                        }
                        // '' sorts first, so the version's own files stay at the top.
                        const folderKeys = Object.keys(inFolder).sort()
                        const hasFolders = folderKeys.some(key => key !== '')
                        // Everything below the folder, nested folders included: that is what
                        // deleting it is about.
                        const filesUnder = (folder: string) =>
                          entries.filter(entry => (entry.relative_path || entry.filename).startsWith(folder + '/')).length

                        // The same folder counts too: dropping a file back into it is
                        // how it is sorted.
                        const canDropInto = (folder: string) => {
                          const dragged = draggedFile()
                          return !!dragged && dragged.versionId === fileVersion.id
                        }
                        const dragOver = (folder: string) => (event: DragEvent) => {
                          if (!canDropInto(folder)) return
                          // Without this the browser refuses the drop and the file
                          // springs back, which is what "nothing happens" looks like.
                          event.preventDefault()
                          if (event.dataTransfer) event.dataTransfer.dropEffect = 'move'
                          setDropTarget(folderKeyOf(fileVersion.id, folder))
                          // Over the group but not over a row: the end of the folder.
                          if (event.target === event.currentTarget) setInsertBefore(null)
                        }
                        const dragLeave = (folder: string) => (event: DragEvent) => {
                          // Moving onto a child of the group fires a leave on the group
                          // itself; only a pointer that really left it counts.
                          const related = event.relatedTarget as Node | null
                          if (related && (event.currentTarget as HTMLElement).contains(related)) return
                          setInsertBefore(null)
                          setDropTarget(current => current === folderKeyOf(fileVersion.id, folder) ? '' : current)
                        }
                        /**
                         * The folder's files in the order they would read after the
                         * drop: the dragged one taken out, then put back where the
                         * line is showing - or at the end when it is over no row.
                         */
                        const orderAfterDrop = (folder: string, draggedId: number) => {
                          const ids = inFolder[folder].map(entry => entry.id).filter(id => id !== draggedId)
                          const gap = insertBefore()
                          if (!gap) return [...ids, draggedId]
                          const index = ids.indexOf(gap.entryId)
                          if (index < 0) return [...ids, draggedId]
                          ids.splice(gap.above ? index : index + 1, 0, draggedId)
                          return ids
                        }
                        const drop = (folder: string) => (event: DragEvent) => {
                          if (!canDropInto(folder)) return
                          event.preventDefault()
                          const dragged = draggedFile()!
                          const order = orderAfterDrop(folder, dragged.entryId)
                          setDraggedFile(null)
                          setDropTarget('')
                          setInsertBefore(null)
                          if (dragged.from === folder) reorderEntries(fileVersion.id, order)
                          else moveEntryToFolder(fileVersion.id, dragged.entryId, folder, order)
                        }
                        const isDropTarget = (folder: string) => dropTarget() === folderKeyOf(fileVersion.id, folder)

                        return (
                          <For each={folderKeys}>{folderKey => (
                            <div onDragOver={dragOver(folderKey)} onDragLeave={dragLeave(folderKey)} onDrop={drop(folderKey)}
                              style={{ 'border-radius': '10px', 'margin-bottom': '2px', background: isDropTarget(folderKey) ? 'rgba(69,123,157,0.12)' : 'transparent', outline: isDropTarget(folderKey) ? '1px dashed var(--accent)' : 'none' }}>
                              {/* The root gets a row of its own as soon as there are folders:
                                  without one there is nowhere to drop a file to get it out of
                                  a folder again. A version with no folders looks as before. */}
                              <Show when={folderKey === '' ? hasFolders : true}>
                                <div
                                  onClick={() => { if (folderKey === '') return; setCollapsedFolders(currentSet => {
                                    const key = folderKeyOf(fileVersion.id, folderKey)
                                    const nextSet = new Set(currentSet)
                                    if (nextSet.has(key)) nextSet.delete(key); else nextSet.add(key)
                                    return nextSet
                                  }) }}
                                  style={{ display: 'flex', 'align-items': 'center', gap: '7px', padding: '6px 11px 4px', color: isDropTarget(folderKey) ? 'var(--accent-light)' : 'var(--muted)', ...monoFont, 'font-size': '12px', cursor: folderKey === '' ? 'default' : 'pointer', 'user-select': 'none' }}>
                                  <Show when={folderKey !== ''} fallback={
                                    <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M3 9l9-7 9 7v11a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/></svg>
                                  }>
                                    <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"
                                      style={{ transform: collapsedFolders().has(folderKeyOf(fileVersion.id, folderKey)) ? 'rotate(-90deg)' : 'rotate(0deg)', transition: 'transform 0.15s' }}>
                                      <polyline points="6 9 12 15 18 9"/>
                                    </svg>
                                    <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z"/></svg>
                                  </Show>
                                  {folderKey === '' ? translate('label_main_folder') : folderKey}
                                  <Show when={folderKey !== '' && !props.isReadOnly}>
                                    {/* Quiet, and a word rather than a sign: it changes
                                        nothing that cannot be changed back. */}
                                    <button onClick={event => { event.stopPropagation(); openRenameFolder(fileVersion.id, folderKey) }}
                                      style={{ 'margin-left': 'auto', padding: '5px 10px', background: 'var(--bg4)', border: '1px solid var(--border2)', 'border-radius': '7px', color: 'var(--text3)', 'font-size': '11px', cursor: 'pointer', ...monoFont }}>
                                      {translate('btn_rename')}
                                    </button>
                                    {/* At the right end and in the colour of the other
                                        deletes: a folder is removed where a file and a
                                        version are. */}
                                    <button onClick={event => { event.stopPropagation(); askDeleteFolder(fileVersion.id, folderKey, filesUnder(folderKey)) }}
                                      title={translate('btn_delete_folder')}
                                      style={{ padding: '5px 9px', background: 'var(--danger-bg)', border: '1px solid var(--danger-border)', 'border-radius': '7px', color: 'var(--danger)', 'font-size': '11px', cursor: 'pointer', ...monoFont, display: 'flex', 'align-items': 'center' }}>✕</button>
                                  </Show>
                                </div>
                              </Show>
                              <Show when={inFolder[folderKey].length === 0}>
                                <div style={{ ...sansFont, 'font-size': '12px', color: 'var(--muted)', 'font-style': 'italic', padding: '4px 11px 8px 28px' }}>
                                  {translate('label_folder_empty')}
                                </div>
                              </Show>
                              <Show when={folderKey === '' || !collapsedFolders().has(folderKeyOf(fileVersion.id, folderKey))}>
                              <For each={inFolder[folderKey]}>{entry => {
                                // The line showing where it would land, above or below
                                // this row. Only while something is being dragged.
                                const gapAbove = () => insertBefore()?.entryId === entry.id && insertBefore()!.above
                                const gapBelow = () => insertBefore()?.entryId === entry.id && !insertBefore()!.above
                                const gapLine = (shown: boolean) => ({
                                  height: '2px', 'border-radius': '2px', margin: '0 11px',
                                  background: shown ? 'var(--accent)' : 'transparent',
                                })
                                return (
                                <>
                                <div style={gapLine(gapAbove())} />
                                <div draggable={!props.isReadOnly}
                                  onDragStart={event => {
                                    setDraggedFile({ versionId: fileVersion.id, entryId: entry.id, from: folderKey })
                                    event.dataTransfer?.setData('text/plain', entry.filename)
                                    if (event.dataTransfer) event.dataTransfer.effectAllowed = 'move'
                                  }}
                                  onDragEnd={() => { setDraggedFile(null); setDropTarget(''); setInsertBefore(null) }}
                                  onDragOver={event => {
                                    if (!canDropInto(folderKey) || draggedFile()?.entryId === entry.id) return
                                    // Which half of the row the pointer is in decides
                                    // whether the file lands above it or below it.
                                    const box = (event.currentTarget as HTMLElement).getBoundingClientRect()
                                    setInsertBefore({ entryId: entry.id, above: event.clientY < box.top + box.height / 2 })
                                  }}
                                  style={{ display: 'flex', 'align-items': 'center', gap: '11px', padding: '8px 11px 8px ' + (folderKey !== '' ? '28px' : '11px'), 'border-radius': '9px', background: 'transparent', 'transition': 'background 0.1s', opacity: draggedFile()?.entryId === entry.id ? '0.45' : '1' }}
                                  onMouseEnter={e => (e.currentTarget.style.background = 'var(--surface)')}
                                  onMouseLeave={e => (e.currentTarget.style.background = 'transparent')}>
                                  {/* The handle says the row can be dragged; the whole row takes
                                      the drag, which is more forgiving than hitting six pixels. */}
                                  <Show when={!props.isReadOnly}>
                                    <span title={translate('files_drag_hint')} style={{ cursor: 'grab', display: 'flex', 'flex-shrink': '0' }}>
                                      <svg width="11" height="14" viewBox="0 0 11 14" fill="var(--border2)">
                                        <circle cx="3" cy="3" r="1.4"/><circle cx="8" cy="3" r="1.4"/>
                                        <circle cx="3" cy="7" r="1.4"/><circle cx="8" cy="7" r="1.4"/>
                                        <circle cx="3" cy="11" r="1.4"/><circle cx="8" cy="11" r="1.4"/>
                                      </svg>
                                    </span>
                                  </Show>
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
                                <div style={gapLine(gapBelow())} />
                                </>
                                )
                              }}</For>
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
