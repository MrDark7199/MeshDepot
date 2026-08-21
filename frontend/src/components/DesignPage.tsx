import { createSignal, createEffect, onCleanup, onMount, Show, For } from 'solid-js'
import { api } from '../services/api'
import { useAuth } from '../services/AuthContext'
import { useI18n } from '../i18n/index'
import { useUnsavedChanges } from '../utils/unsavedChanges'
import { StlViewerModal, is3dFile } from './StlViewer'
import { displayName, displayDescription } from '../utils/designText'
import { formatDate } from '../utils/datetime'
import { escapeHtml } from '../utils/sanitizeHtml'
import { errorKey } from '../utils/errorMessage'
import type { Design, DesignFile, DesignFileEntry, DesignID, DesignImage, Tag, Collection, DesignShare, GcodeMeta, ShareLink } from '../types'
import { PLATFORM_COLORS, platformLabel } from '../constants/platforms'
import { buildDescriptionFragment, descriptionCss } from '../utils/description'

const sansFont = { 'font-family': "'DM Sans',sans-serif" }
const monoFont = { 'font-family': "'DM Mono',monospace" }
const labelStyle = { display: 'block', 'font-family': "'DM Mono',monospace", 'font-size': '12px', color: 'var(--muted)', 'margin-bottom': '6px', 'text-transform': 'uppercase', 'letter-spacing': '0.05em' }
const inputStyle = { width: '100%', background: 'var(--input-bg)', border: '1px solid var(--border2)', 'border-radius': '10px', padding: '10px 14px', color: 'var(--text)', 'font-family': "'DM Sans',sans-serif", 'font-size': '14px', outline: 'none', 'box-sizing': 'border-box' } as any

const TAG_COLOR_PRESETS = ['#e63946','#f4a261','#e9c46a','#2a9d8f','#457b9d','#a8dadc','#7c3aed','#db2777','#16a34a','#64748b']

// ── Printer file formats (extensions without the dot) ───────────────────────
// Text g-code (FDM) - the server reads print parameters out of these (gcode_meta).
const GCODE_FORMATS = ['gcode', 'gco', 'g']
// Further FDM slicer/print-job formats (binary, no parameters extracted).
const FDM_JOB_FORMATS = ['bgcode', 'gx', 'g3drem', 'ufp', 'makerbot']
// Resin/MSLA slicer output. The server reads print parameters from all of these.
const RESIN_FORMATS = ['pwmx', 'pwmo', 'pws', 'pw0', 'pwms', 'pwmb', 'sl1', 'sl1s', 'ctb', 'cbddlp', 'photon']
// Resin formats the server can rebuild a mesh from - only these get the 3D button.
// The layer decoder is Anycubic-PWMX-specific; everything else would 422.
const RESIN_VIEWER_FORMATS = ['pwmx']
// Every extension that counts as a finished printer file.
const PRINTER_FORMATS = [...GCODE_FORMATS, ...FDM_JOB_FORMATS, ...RESIN_FORMATS]
// Extensions the desktop slicers can open: meshes and CAD exchange formats plus
// the text/binary g-code they load for preview. Resin formats are absent - none
// of the three slicers reads them.
const SLICER_FORMATS = ['stl', '3mf', 'obj', 'step', 'stp', 'amf', ...GCODE_FORMATS, 'bgcode']
// The accept value for the upload pickers (models, archives, printer files).
const FILE_ACCEPT = ['.zip', '.stl', '.3mf', '.obj', ...PRINTER_FORMATS.map(e => '.' + e)].join(',')

const lowerExt = (name: string) => (name.split('.').pop() || '').toLowerCase()

/** Mirrors validHostname in backend/internal/api/designs.go. */
const isResolvableHost = (host: string): boolean => {
  if (!host) return false
  // Bracketed IPv6 and plain IPv4 are addresses, not names.
  if (host.startsWith('[') || /^\d{1,3}(\.\d{1,3}){3}$/.test(host)) return true
  const labels = host.replace(/\.$/, '').split('.')
  if (labels.length < 2) return false
  if (!labels.every(label => label.length <= 63 && /^[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?$/.test(label))) return false
  return /^[a-zA-Z]{2,}$/.test(labels[labels.length - 1])
}

/**
 * The source url is optional; a filled one has to be an address that leads
 * somewhere, because it becomes the outgoing link and the sync's starting point.
 *
 * "http://afeefafefefaffefef" parses and has a host, which is why it used to
 * pass - but there is no such site. A registrable name is required instead.
 * The scheme may be left out: nobody types "https://" in front of an address
 * they copied, and refusing it for that reason is a rule the user has to learn
 * from an error message. Mirrors normalizeSourceURL in the backend.
 */
const normalizeSourceUrl = (value: string): string | null => {
  const raw = (value || '').trim()
  if (!raw) return ''
  try {
    const parsed = new URL(raw.includes('://') ? raw : 'https://' + raw)
    if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') return null
    return isResolvableHost(parsed.hostname) ? parsed.toString() : null
  } catch { return null }
}

// Bambu Studio / OrcaSlicer / PrusaSlicer all share one downloader that matches
// the URL scheme `<slicer>://open?file=<percent-encoded download URL>`
// (regex ^(orcaslicer|prusaslicer|bambustudio|cura)://open[/]?\?file= in their
// Downloader.cpp). The download URL must be percent-encoded, as Printables emits.
const SLICERS = [
  { name: 'Bambu Studio', scheme: (url: string) => `bambustudio://open?file=${encodeURIComponent(url)}` },
  { name: 'OrcaSlicer',   scheme: (url: string) => `orcaslicer://open?file=${encodeURIComponent(url)}` },
  { name: 'PrusaSlicer',  scheme: (url: string) => `prusaslicer://open?file=${encodeURIComponent(url)}` },
]

/** Formats bytes as a human-readable string. */
function formatBytes(bytes: number): string {
  if (!bytes) return '0 B'
  if (bytes >= 1073741824) return (bytes / 1073741824).toFixed(1) + ' GB'
  if (bytes >= 1048576)    return (bytes / 1048576).toFixed(1) + ' MB'
  if (bytes >= 1024)       return (bytes / 1024).toFixed(0) + ' KB'
  return bytes + ' B'
}

/** Formats minutes as "Xh Ym". */
function formatPrintTime(minutes: number): string {
  if (!minutes) return '-'
  const hours = Math.floor(minutes / 60)
  const mins = minutes % 60
  return hours > 0 ? `${hours}h ${mins}m` : `${mins}m`
}

/** Renders star rating (0–5) with optional change handler. */
function StarRating(props: { value: number; onChange?: (rating: number) => void }) {
  return (
    <div style={{ display: 'flex', gap: '4px' }}>
      <For each={[1,2,3,4,5]}>{star => (
        <button
          onClick={() => props.onChange?.(star === props.value ? 0 : star)}
          style={{ background: 'none', border: 'none', cursor: props.onChange ? 'pointer' : 'default', padding: '0', 'font-size': '22px', color: star <= props.value ? '#f4a261' : 'var(--border2)', 'transition': 'color 0.1s' }}>
          ★
        </button>
      )}</For>
    </div>
  )
}

/**
 * Slicer deep links for the files of the current version.
 *
 * Picking the file comes first: a design usually carries several printable
 * files, and opening whichever one happened to be first is a guess the user
 * cannot correct. With a single eligible file the menu is skipped - there is
 * nothing to choose.
 */
function SlicerButtons(props: { designId: DesignID; fileVersionId: number; entries: { id: number; filename: string }[] }) {
  const [openFor, setOpenFor] = createSignal<string | null>(null)
  const [busy, setBusy] = createSignal(false)

  /** Fetches a one-time token for the entry and hands it to the slicer. */
  const open = async (scheme: (url: string) => string, entryId: number) => {
    setOpenFor(null)
    setBusy(true)
    try {
      const response = await api.createDownloadToken(props.designId, props.fileVersionId, entryId) as any
      const token = response?.token ?? response?.data?.token
      if (!token) return
      window.location.href = scheme(`${window.location.origin}/api/v1/files/token/${token}`)
    } catch { /* the button stays available for another try */ }
    finally { setBusy(false) }
  }

  const activate = (slicer: typeof SLICERS[number]) => {
    if (props.entries.length === 1) { open(slicer.scheme, props.entries[0].id); return }
    setOpenFor(current => current === slicer.name ? null : slicer.name)
  }

  return (
    <div style={{ display: 'flex', gap: '7px', 'flex-wrap': 'wrap' }}>
      <For each={SLICERS}>{slicer => (
        <div style={{ position: 'relative' }}>
          <button onClick={() => activate(slicer)} disabled={busy()}
            style={{ padding: '7px 15px', background: 'var(--bg4)', border: '1px solid var(--border2)', 'border-radius': '9px', color: busy() ? 'var(--muted)' : 'var(--text)', 'font-size': '13px', cursor: busy() ? 'default' : 'pointer', ...sansFont, display: 'flex', 'align-items': 'center', gap: '7px', 'font-weight': '500', opacity: busy() ? '0.5' : '1' }}>
            <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <polygon points="5 3 19 12 5 21 5 3"/>
            </svg>
            {slicer.name}
          </button>
          <Show when={openFor() === slicer.name}>
            <>
              {/* Click anywhere else closes the menu. */}
              <div onClick={() => setOpenFor(null)} style={{ position: 'fixed', inset: '0', 'z-index': '40' }} />
              <div style={{ position: 'absolute', top: 'calc(100% + 5px)', left: '0', 'z-index': '41', background: 'var(--bg2)', border: '1px solid var(--border)', 'border-radius': '10px', 'box-shadow': '0 12px 30px rgba(0,0,0,0.35)', padding: '5px', 'min-width': '220px', 'max-width': '340px', 'max-height': '260px', 'overflow-y': 'auto' }}>
                <For each={props.entries}>{entry => (
                  <button onClick={() => open(slicer.scheme, entry.id)}
                    style={{ display: 'block', width: '100%', 'text-align': 'left', padding: '7px 9px', background: 'none', border: 'none', 'border-radius': '7px', cursor: 'pointer', ...monoFont, 'font-size': '12px', color: 'var(--text2)', overflow: 'hidden', 'text-overflow': 'ellipsis', 'white-space': 'nowrap' }}>
                    {entry.filename}
                  </button>
                )}</For>
              </div>
            </>
          </Show>
        </div>
      )}</For>
    </div>
  )
}

/**
 * The share-link half of the share tab: create one with a lifetime, copy it,
 * revoke it. Kept out of the page component because it is self-contained and
 * that component is long enough.
 */
function ShareLinkSection(props: {
  links: ShareLink[]
  days: string
  onDays: (days: string) => void
  creating: boolean
  copiedToken: string
  onCreate: () => void
  onCopy: (token: string) => void
  onDelete: (linkId: number) => void
  translate: (key: string, vars?: Record<string, string | number>) => string
  formatDate: (stamp: string) => string
}) {
  const expiryText = (link: ShareLink) => {
    if (link.expired) return props.translate('share_link_expired')
    if (!link.expires_at) return props.translate('share_link_no_expiry')
    return props.translate('share_link_until', { date: props.formatDate(link.expires_at) })
  }

  return (
    <div style={{ 'border-top': '1px solid var(--border)', 'padding-top': '18px', display: 'flex', 'flex-direction': 'column', gap: '12px' }}>
      <div>
        <div style={{ ...sansFont, 'font-size': '14px', 'font-weight': '700', color: 'var(--text)', 'margin-bottom': '4px' }}>
          {props.translate('share_link_heading')}
        </div>
        <div style={{ ...sansFont, 'font-size': '12px', color: 'var(--muted)', 'line-height': '1.6' }}>
          {props.translate('share_link_hint')}
        </div>
      </div>

      <div style={{ display: 'flex', gap: '10px', 'align-items': 'center' }}>
        {/* Written out rather than spread from inputStyle: inside a JSX style
            object the spread wins over the keys after it, so ...inputStyle
            followed by a width silently kept the shared width: 100%. */}
        <input type="number" min="0" max="3650" value={props.days}
          onInput={event => props.onDays(event.currentTarget.value)}
          style={{
            width: '76px', 'flex-shrink': '0', 'text-align': 'right',
            background: 'var(--input-bg)', border: '1px solid var(--border2)', 'border-radius': '10px',
            padding: '9px 11px', color: 'var(--text)', ...sansFont, 'font-size': '13px',
            outline: 'none', 'box-sizing': 'border-box',
          }} />
        <span style={{ ...sansFont, 'font-size': '13px', color: 'var(--text3)', 'flex-shrink': '0' }}>
          {props.translate('share_link_days_unit')}
        </span>
        <button onClick={props.onCreate} disabled={props.creating}
          style={{ padding: '9px 18px', background: 'var(--accent)', border: 'none', 'border-radius': '10px', color: '#fff',
            'flex-shrink': '0', ...sansFont, 'font-size': '13px', 'font-weight': '600',
            cursor: props.creating ? 'default' : 'pointer', opacity: props.creating ? '0.6' : '1' }}>
          {props.creating ? '…' : props.translate('share_link_create')}
        </button>
      </div>
      <div style={{ ...sansFont, 'font-size': '11px', color: 'var(--muted)', 'margin-top': '-6px' }}>
        {props.translate('share_link_days_hint')}
      </div>

      <For each={props.links}>{link => (
        <div style={{ display: 'flex', 'align-items': 'center', gap: '11px', background: 'var(--surface)', border: '1px solid var(--border)',
          'border-radius': '11px', padding: '10px 14px', opacity: link.expired ? '0.55' : '1' }}>
          <div style={{ flex: '1', 'min-width': '0' }}>
            <div style={{ ...monoFont, 'font-size': '12px', color: 'var(--text2)', overflow: 'hidden', 'text-overflow': 'ellipsis', 'white-space': 'nowrap' }}>
              {api.shareLinkUrl(link.token)}
            </div>
            <div style={{ ...sansFont, 'font-size': '11px', color: 'var(--muted)', 'margin-top': '3px' }}>
              {expiryText(link)} · {props.translate('share_link_views', { count: link.view_count })}
            </div>
          </div>
          <button onClick={() => props.onCopy(link.token)}
            style={{ padding: '6px 13px', background: 'var(--bg3)', border: '1px solid var(--border2)', 'border-radius': '8px',
              color: 'var(--text2)', 'font-size': '12px', cursor: 'pointer', ...sansFont, 'flex-shrink': '0' }}>
            {props.copiedToken === link.token ? props.translate('share_link_copied') : props.translate('share_link_copy')}
          </button>
          <button onClick={() => props.onDelete(link.id)}
            style={{ padding: '6px 13px', background: 'var(--danger-bg)', border: '1px solid var(--danger-border)', 'border-radius': '8px',
              color: 'var(--danger)', 'font-size': '12px', cursor: 'pointer', ...sansFont, 'flex-shrink': '0' }}>
            {props.translate('share_link_revoke')}
          </button>
        </div>
      )}</For>
    </div>
  )
}

/** Inline error box. */
function ErrorBox(props: { message: string }) {
  return (
    <Show when={props.message}>
      <div style={{ background: 'var(--danger-bg)', border: '1px solid var(--danger-border)', 'border-radius': '9px', padding: '9px 13px', color: 'var(--danger)', ...sansFont, 'font-size': '14px', 'margin-bottom': '14px' }}>
        {props.message}
      </div>
    </Show>
  )
}

interface DesignPageProps {
  designId:             DesignID
  onBack:               () => void
  showToast:            (message: string, variant?: string) => void
  allTags:              Tag[]
  allCollections:       Collection[]
  onTagsChanged:        () => void
  onCollectionsChanged: () => void
  onOpenCollection?:    (id: number) => void
  isReadOnly:           boolean
  initialEditMode?:     boolean
  onSync?:              (id: DesignID, name: string) => void
  syncTick?:            number
  syncStatus?:          string
  syncProgress?:        number
  syncStep?:            { step: string; cur: number; tot: number }
}

/**
 * Full-page design detail view.
 *
 * Layout:
 * - Left column: image gallery (main image + thumbnail strip), 3D view button, slicer buttons.
 * - Right column: name, platform badge, star rating, metadata grid, tags, collections, source URL.
 * - Tabs below: Details · Files · Notes · Share.
 *
 * The breadcrumb bar is sticky and contains back, sync, delete, and edit actions.
 * All mutations reload the design from the API to keep the view in sync.
 */
/**
 * The design fields the detail view edits inline. Kept separate from `Design`
 * because the form normalizes nullable columns to '' and the hidden flag to
 * 0/1, which is what the PUT endpoint expects.
 */
interface DesignEditForm {
  name: string
  description: string
  category: string
  license: string
  author: string
  source_url: string
  rating: number
  print_time_minutes: number | string
  notes: string
  is_hidden: number
}

/** Blank form used before a design has loaded. */
function emptyEditForm(): DesignEditForm {
  return { name: '', description: '', category: '', license: '', author: '', source_url: '', rating: 0, print_time_minutes: '', notes: '', is_hidden: 0 }
}

export function DesignPage(props: DesignPageProps) {
  const { translate, lang, translateDesigns } = useI18n()
  const t = translate // Kurz-Alias, u. a. für die Fehler-Übersetzung in catch-Blöcken.
  const { user } = useAuth()

  /**
   * Author line for the header. A design added by hand ("manual") has no real
   * foreign author field, so it shows the owner's own name. On a shared design
   * (isReadOnly) the stored author stays, or the viewer's name would appear in
   * place of the owner's.
   */
  const shownAuthor = () => {
    const d = design()
    if (!d) return ''
    if (d.source_platform === 'manual' && !props.isReadOnly) return user()?.name || ''
    return d.author || ''
  }

  const [design, setDesign] = createSignal<Design | null>(null)
  const [isLoading, setIsLoading] = createSignal(true)
  const [activeImageIndex, setActiveImageIndex] = createSignal(0)
  const [activeTab, setActiveTab] = createSignal<'details' | 'files' | 'gcode' | 'notes' | 'share'>('details')
  const [fileVersions, setFileVersions] = createSignal<DesignFile[]>([])
  const [isLoadingFiles, setIsLoadingFiles] = createSignal(false)
  const [stlViewUrl, setStlViewUrl] = createSignal<string | null>(null)
  const [stlViewName, setStlViewName] = createSignal('')
  const [stlViewFilename, setStlViewFilename] = createSignal('')
  /** The open model is Z-up (a reconstructed resin mesh), so the viewer stands it upright. */
  const [stlViewZUp, setStlViewZUp] = createSignal(false)
  /** When true, the detail view shows the canonical (untranslated) name/description. */
  const [showOriginal, setShowOriginal] = createSignal(false)

  /**
   * True when a translation is actually being shown - i.e. a translation for the
   * display language exists AND the user has design translation enabled. Drives the
   * "translated" badge and the per-design original/translation toggle.
   */
  const isTranslated = () => {
    const d = design()
    if (!d || !translateDesigns()) return false
    return displayName(d, lang(), true) !== d.name || displayDescription(d, lang(), true) !== (d.description || '')
  }
  /** Name to render, honouring the global preference and the per-design toggle. */
  const shownName = () => {
    const d = design()!
    return showOriginal() ? d.name : displayName(d, lang(), translateDesigns())
  }
  /** Description to render, honouring the global preference and the per-design toggle. */
  const shownDescription = () => {
    const d = design()!
    return showOriginal() ? (d.description || '') : displayDescription(d, lang(), translateDesigns())
  }

  /** The rendered description as a DOM fragment (see utils/description). */
  const descriptionFragment = (): DocumentFragment =>
    buildDescriptionFragment(shownDescription(), design()?.source_platform)

  const [isEditing, setIsEditing] = createSignal(false)
  const [editForm, setEditForm] = createSignal<DesignEditForm>(emptyEditForm())
  /**
   * Staged cover selection while editing: the design_image id the user picked
   * as new cover. Only applied to the server on save; `null` means unchanged.
   */
  const [pendingCoverId, setPendingCoverId] = createSignal<number | null>(null)
  /**
   * Gallery images staged for deletion while editing. Nothing is removed on the
   * server until the user clicks Save (see {@link saveDesign}); until then the
   * thumbnail stays visible but marked, and the deletion can be undone.
   */
  const [pendingDeleteImageIds, setPendingDeleteImageIds] = createSignal<number[]>([])
  const isStagedDelete = (id: number) => pendingDeleteImageIds().includes(id)
  /** Stages/unstages a gallery image for deletion (applied on Save). */
  const toggleDeleteImage = (id: number) => {
    setPendingDeleteImageIds(ids => ids.includes(id) ? ids.filter(x => x !== id) : [...ids, id])
    if (pendingCoverId() === id) setPendingCoverId(null) // can't keep a to-be-deleted image as cover
  }
  /** Image id awaiting the delete-confirmation modal (null = modal closed). */
  const [confirmDeleteImageId, setConfirmDeleteImageId] = createSignal<number | null>(null)
  const [isSaving, setIsSaving] = createSignal(false)
  const [errorMessage, setErrorMessage] = createSignal('')
  const { markDirty, resetDirty, guardClose, setShouldBlock } = useUnsavedChanges(translate('confirm_discard_changes'))
  let editSnapshot = ''
  let initialEditModeDone = false

  const editState = () => JSON.stringify({ form: editForm(), tags: selectedTagIds(), cols: designCollectionIds(), cover: pendingCoverId(), del: pendingDeleteImageIds() })

  const enterEditMode = () => {
    setPendingCoverId(null)
    setPendingDeleteImageIds([])
    editSnapshot = editState()
    setShouldBlock(() => editState() !== editSnapshot)
    setIsEditing(true)
  }

  const exitEditMode = () => {
    resetDirty()
    setPendingCoverId(null)
    setPendingDeleteImageIds([])
    setIsEditing(false)
  }

  const [confirmDelete, setConfirmDelete] = createSignal(false)
  const [isDeleting, setIsDeleting] = createSignal(false)
  // Preselected: a design deleted on purpose is one the user does not want
  // back, and the sync would otherwise hand it over again on its next run.
  const [excludeFromSync, setExcludeFromSync] = createSignal(true)

  // syncTick: reload design+files when a background sync finishes
  createEffect(() => {
    const tick = props.syncTick
    if (tick && tick > 0) { loadDesign(); loadFiles() }
  })

  const [confirmDeleteFileId, setConfirmDeleteFileId] = createSignal<number | null>(null)

  const [collapsedFolders, setCollapsedFolders] = createSignal<Set<string>>(new Set())
  // Which file-version cards are expanded. Newest (current) is expanded by default.
  const [expandedVersionIds, setExpandedVersionIds] = createSignal<Set<number>>(new Set())

  const [isUploadingImage, setIsUploadingImage] = createSignal(false)
  let imageInputRef: HTMLInputElement | undefined

  // Tags as a select2-style combobox: the picked ones are held as full objects
  // (chips), and matches are searched on the server rather than loaded up front.
  const [selectedTags, setSelectedTags] = createSignal<Tag[]>([])
  const selectedTagIds = () => selectedTags().map(t => t.id)
  const [tagQuery, setTagQuery] = createSignal('')
  const [tagResults, setTagResults] = createSignal<Tag[]>([])
  const [tagOpen, setTagOpen] = createSignal(false)
  const [newTagColor, setNewTagColor] = createSignal(TAG_COLOR_PRESETS[0])
  const [isCreatingTag, setIsCreatingTag] = createSignal(false)

  const notSelected = (t: Tag) => !selectedTagIds().includes(t.id)
  const addSelectedTag = (t: Tag) => {
    if (notSelected(t)) setSelectedTags(ts => [...ts, t])
    setTagQuery(''); setTagResults([]); setTagOpen(false)
  }
  const removeSelectedTag = (id: number) => setSelectedTags(ts => ts.filter(t => t.id !== id))
  const runTagSearch = async (v: string) => {
    try { const r = await api.searchTags(v.trim()) as { data: Tag[] }; setTagResults(r.data || []) }
    catch { setTagResults([]) }
  }
  let tagSearchTimer: ReturnType<typeof setTimeout>
  const onTagInput = (v: string) => {
    setTagQuery(v); setTagOpen(true)
    clearTimeout(tagSearchTimer)
    tagSearchTimer = setTimeout(() => runTagSearch(v), 200)
  }
  // Only offer the create row when the input is not an exact name match.
  const showCreateTag = () => {
    const q = tagQuery().trim().toLowerCase()
    return q !== '' && !tagResults().some(t => t.name.toLowerCase() === q) && !selectedTags().some(t => t.name.toLowerCase() === q)
  }
  const onTagKeyDown = (e: KeyboardEvent) => {
    if (e.key === 'Enter') {
      e.preventDefault()
      const avail = tagResults().filter(notSelected)
      if (avail.length > 0) addSelectedTag(avail[0])
      else if (tagQuery().trim()) createNewTag()
    } else if (e.key === 'Escape') { setTagOpen(false) }
  }

  // Collections as the same select2-style combobox: the picks are full objects
  // (chips), and the dropdown filters props.allCollections on the client.
  const [selectedCollections, setSelectedCollections] = createSignal<Collection[]>([])
  const designCollectionIds = () => selectedCollections().map(c => c.id)
  const [colQuery, setColQuery] = createSignal('')
  const [colOpen, setColOpen] = createSignal(false)
  const [isCreatingCol, setIsCreatingCol] = createSignal(false)
  // The picker offers hidden collections too, so a design can still be put into
  // one. The read-only view further down keeps hiding them (it intersects
  // props.allCollections, which never contains hidden ones).
  const [pickerCollections, setPickerCollections] = createSignal<Collection[]>([])

  const notColSelected = (c: Collection) => !designCollectionIds().includes(c.id)
  const colResults = () => {
    const q = colQuery().trim().toLowerCase()
    return pickerCollections().filter(c => notColSelected(c) && (q === '' || c.name.toLowerCase().includes(q)))
  }
  // Only offer the create row when the input is not an exact name match (hidden
  // collections included, since the backend rejects a duplicate name).
  const showCreateCol = () => {
    const q = colQuery().trim().toLowerCase()
    return q !== '' && !pickerCollections().some(c => c.name.toLowerCase() === q)
  }
  const onColKeyDown = (e: KeyboardEvent) => {
    if (e.key === 'Enter') {
      e.preventDefault()
      const avail = colResults()
      if (avail.length > 0) addCollectionSel(avail[0])
      else if (colQuery().trim()) createNewCollection()
    } else if (e.key === 'Escape') { setColOpen(false) }
  }

  const [shareEmailInput, setShareEmailInput] = createSignal('')
  /** Recipients picked but not shared yet - the dialog collects them before one
   *  request goes out, so sharing with five people is one action, not five. */
  const [sharePicked, setSharePicked] = createSignal<{id:string,name:string,email:string}[]>([])
  const [shareUserSuggestions, setShareUserSuggestions] = createSignal<{id:string,name:string,email:string}[]>([])

  /** Whether the recipient dropdown is open (opens on click, like a select2). */
  const [shareOpen, setShareOpen] = createSignal(false)

  // Links that hand the design to someone without an account.
  const [shareLinks, setShareLinks] = createSignal<ShareLink[]>([])
  // Days rather than a fixed list: an owner knows how long they want the link to
  // last better than a dropdown does. Empty or 0 means it never expires.
  const [linkDays, setLinkDays] = createSignal('7')
  const [creatingLink, setCreatingLink] = createSignal(false)
  const [copiedToken, setCopiedToken] = createSignal('')

  const loadShareLinks = () => {
    if (props.isReadOnly) return
    api.getShareLinks(props.designId)
      .then(response => setShareLinks(response.data || []))
      .catch(() => {})
  }

  const createShareLink = async () => {
    setCreatingLink(true)
    try {
      await api.createShareLink(props.designId, Math.max(0, parseInt(linkDays(), 10) || 0) * 24)
      loadShareLinks()
      props.showToast(translate('share_link_created'))
    } catch (failure: unknown) {
      props.showToast(t(errorKey(failure)), 'error')
    } finally { setCreatingLink(false) }
  }

  const deleteShareLink = async (linkId: number) => {
    try {
      await api.deleteShareLink(props.designId, linkId)
      loadShareLinks()
      props.showToast(translate('share_link_revoked'))
    } catch (failure: unknown) {
      props.showToast(t(errorKey(failure)), 'error')
    }
  }

  const copyShareLink = async (token: string) => {
    const url = api.shareLinkUrl(token)
    try {
      await navigator.clipboard.writeText(url)
      setCopiedToken(token)
      setTimeout(() => setCopiedToken(current => current === token ? '' : current), 2000)
    } catch {
      // A browser without clipboard permission still has to hand the link over.
      window.prompt(translate('share_link_copy'), url)
    }
  }

  // Loaded when the tab is opened rather than with the design: most visits never
  // look at it, and it is one request per design otherwise.
  createEffect(() => { if (activeTab() === 'share') loadShareLinks() })

  let shareSearchTimer: ReturnType<typeof setTimeout> | undefined
  /**
   * Loads the pickable accounts. An empty query lists the first accounts rather
   * than nothing: the field opens its list on click, and a member who does not
   * know who else is on the server has no first letter to guess.
   */
  const loadShareUserSuggestions = (query: string) => {
    clearTimeout(shareSearchTimer)
    shareSearchTimer = setTimeout(async () => {
      try {
        const res = await api.searchUsers(query.trim()) as { data: {id:string,name:string,email:string}[] }
        // Neither the people it is already shared with nor the ones waiting in
        // the chips are worth suggesting again.
        const alreadyShared = new Set((design()?.shares || []).map(share => share.shared_with_email))
        const picked = new Set(sharePicked().map(user => user.id))
        setShareUserSuggestions((res.data || []).filter(u => !alreadyShared.has(u.email) && !picked.has(u.id)))
      } catch {}
    }, 200)
  }
  const [isSharing, setIsSharing] = createSignal(false)

  // File upload (multiple files allowed per version)
  const [uploadFileObject, setUploadFileObject] = createSignal<File[]>([])
  const [uploadVersion, setUploadVersion] = createSignal('')
  const [uploadNotes, setUploadNotes] = createSignal('')
  const [isUploading, setIsUploading] = createSignal(false)
  const [uploadError, setUploadError] = createSignal('')
  let fileInputRef: HTMLInputElement | undefined

  /** True for text g-code extensions (.gcode/.gco/.g) - the source of the print parameters. */
  const isGcodeFile = (name: string) => GCODE_FORMATS.includes(lowerExt(name))

  /** True for resin/MSLA slicer formats (.pwmx among others - see {@link RESIN_FORMATS}). */
  const isResinFile = (name: string) => RESIN_FORMATS.includes(lowerExt(name))

  /** True for resin files the server can turn back into a mesh - see {@link RESIN_VIEWER_FORMATS}. */
  const isResinViewable = (name: string) => RESIN_VIEWER_FORMATS.includes(lowerExt(name))

  /** True for machine-ready printer files (g-code, other FDM jobs and resin). */

  /** Compact one-line summary of the print parameters for the file list. */
  const gcodeSummary = (m: GcodeMeta): string => {
    const parts: string[] = []
    if (m.layer_height != null) parts.push(`${m.layer_height} mm`)
    if (m.kind === 'resin') {
      if (m.exposure_time != null) parts.push(`${m.exposure_time} s`)
      if (m.print_time) parts.push(m.print_time)
      if (m.resin_volume != null) parts.push(`${m.resin_volume} ml`)
      return parts.join(' · ')
    }
    if (m.nozzle_temp != null || m.bed_temp != null) parts.push(`${m.nozzle_temp ?? '–'}/${m.bed_temp ?? '–'} °C`)
    if (m.infill != null) parts.push(`${m.infill} %`)
    if (m.print_time) parts.push(m.print_time)
    if (m.filament_used_g != null) parts.push(`${m.filament_used_g} g`)
    return parts.join(' · ')
  }

  /** Filament usage as text (weight and/or length), null when neither is known. */
  const filamentText = (m: GcodeMeta): string | null => {
    const p: string[] = []
    if (m.filament_used_g != null) p.push(`${m.filament_used_g} g`)
    if (m.filament_used_m != null) p.push(`${m.filament_used_m} m`)
    return p.length ? p.join(' · ') : null
  }

  /** Suggestion for the next version: the highest existing number + 1.0, mirroring the backend. */
  const nextVersion = () => {
    const nums = fileVersions().map(v => parseFloat(v.version)).filter(n => !isNaN(n))
    return ((nums.length ? Math.max(...nums) : 0) + 1).toFixed(1)
  }

  /** All sliced files of the current version that carry print parameters - G-code and resin alike, sorted by name. */
  const currentPrintEntries = (): DesignFileEntry[] =>
    (currentVersion()?.entries || [])
      .filter(e => (isGcodeFile(e.filename) || isResinFile(e.filename)) && e.gcode_meta)
      .sort((a, b) => a.filename.localeCompare(b.filename))

  /**
   * Label/value tiles for the print settings tab.
   *
   * FDM and resin have almost no parameters in common, so the two processes get
   * their own field list; `kind` picks it. Empty values are dropped by the caller.
   */
  const printFields = (m: GcodeMeta): { label: string, value: string | null }[] => {
    if (m.kind === 'resin') {
      const resolution = m.resolution_x != null && m.resolution_y != null ? `${m.resolution_x} × ${m.resolution_y} px` : null
      const display = m.display_width != null && m.display_height != null ? `${m.display_width} × ${m.display_height} mm` : null
      return [
        { label: translate('label_layer_height'),          value: m.layer_height != null ? `${m.layer_height} mm` : null },
        { label: translate('label_exposure_time'),         value: m.exposure_time != null ? `${m.exposure_time} s` : null },
        { label: translate('label_bottom_exposure_time'),  value: m.bottom_exposure_time != null ? `${m.bottom_exposure_time} s` : null },
        { label: translate('label_bottom_layers'),         value: m.bottom_layers != null ? String(m.bottom_layers) : null },
        { label: translate('label_light_off_time'),        value: m.light_off_time != null ? `${m.light_off_time} s` : null },
        { label: translate('label_print_time'),            value: m.print_time || null },
        { label: translate('label_layer_count'),           value: m.layer_count != null ? String(m.layer_count) : null },
        { label: translate('label_resin_volume'),          value: m.resin_volume != null ? `${m.resin_volume} ml` : null },
        { label: translate('label_resin_weight'),          value: m.resin_weight != null ? `${m.resin_weight} g` : null },
        { label: translate('label_resin_cost'),            value: m.resin_cost != null ? String(m.resin_cost) : null },
        { label: translate('label_lift_height'),           value: m.lift_height != null ? `${m.lift_height} mm` : null },
        { label: translate('label_lift_speed'),            value: m.lift_speed != null ? `${m.lift_speed} mm/min` : null },
        { label: translate('label_bottom_lift_height'),    value: m.bottom_lift_height != null ? `${m.bottom_lift_height} mm` : null },
        { label: translate('label_bottom_lift_speed'),     value: m.bottom_lift_speed != null ? `${m.bottom_lift_speed} mm/min` : null },
        { label: translate('label_retract_speed'),         value: m.retract_speed != null ? `${m.retract_speed} mm/min` : null },
        { label: translate('label_resolution'),            value: resolution },
        { label: translate('label_pixel_size'),            value: m.pixel_size != null ? `${m.pixel_size} µm` : null },
        { label: translate('label_display_size'),          value: display },
        { label: translate('label_anti_aliasing'),         value: m.anti_aliasing != null ? String(m.anti_aliasing) : null },
        { label: translate('label_material'),              value: m.material || null },
        { label: translate('label_printer'),               value: m.printer || null },
        { label: translate('label_slicer'),                value: m.slicer || null },
      ]
    }
    return [
      { label: translate('label_layer_height'),    value: m.layer_height != null ? `${m.layer_height} mm` : null },
      { label: translate('label_nozzle_temp'),     value: m.nozzle_temp != null ? `${m.nozzle_temp} °C` : null },
      { label: translate('label_bed_temp'),        value: m.bed_temp != null ? `${m.bed_temp} °C` : null },
      { label: translate('label_infill'),          value: m.infill != null ? `${m.infill} %` : null },
      { label: translate('label_print_time'),      value: m.print_time || null },
      { label: translate('label_filament_used'),   value: filamentText(m) },
      { label: translate('label_nozzle_diameter'), value: m.nozzle_diameter != null ? `${m.nozzle_diameter} mm` : null },
      { label: translate('label_filament_type'),   value: m.filament_type || null },
      { label: translate('label_slicer'),          value: m.slicer || null },
    ]
  }

  /**
   * Fetches the full design record and its collection memberships from the API,
   * then hydrates the edit form and tag/collection signals.
   */
  const loadDesign = async () => {
    setIsLoading(true)
    try {
      const response = await api.getDesign(props.designId)
      const loaded = response.data
      setDesign(loaded)
      setShowOriginal(false)
      setEditForm({
        name:               loaded.name || '',
        description:        loaded.description || '',
        category:           loaded.category || '',
        license:            loaded.license || '',
        author:             loaded.author || '',
        source_url:         loaded.source_url || '',
        rating:             loaded.rating || 0,
        print_time_minutes: loaded.print_time_minutes || '',
        notes:              loaded.notes || '',
        is_hidden:          loaded.is_hidden ? 1 : 0,
      })
      setSelectedTags(loaded.tags || [])
      const [colResponse, pickerResponse]: [any, any] = await Promise.all([
        api.getDesignCollections(props.designId, true),
        api.getCollections(true),
      ])
      setSelectedCollections(colResponse.data)
      setPickerCollections(pickerResponse.data || [])
      if (props.initialEditMode && !initialEditModeDone) { initialEditModeDone = true; enterEditMode() }
    } catch (failure: unknown) {
      // The server's own reason, not a blanket "not found": a rejected session
      // or a pending password change say what is wrong, and the page has to
      // repeat that instead of inventing a reason. Only the 404 is rephrased -
      // it is the same answer for a deleted design and for one that is no
      // longer shared (the server deliberately does not tell them apart), and
      // "Not found." on its own leaves the reader guessing which it was.
      const key = errorKey(failure, 'error.not_found')
      setDesign(null)
      setErrorMessage(key === 'error.not_found' ? translate('design_unavailable_hint') : t(key))
    } finally { setIsLoading(false) }
  }

  /**
   * Opens a file entry in the 3D viewer. A resin file (.pwmx) carries no
   * geometry, so what is loaded is the mesh the server rebuilt from the layer
   * stack, parsed as an STL (hence the "model.stl" format hint).
   */
  const showEntryInViewer = (fileVersionId: number, entry: DesignFileEntry) => {
    const resin = isResinFile(entry.filename)
    if (resin) {
      setStlViewUrl(api.pwmxMeshUrl(props.designId, fileVersionId, entry.id))
      setStlViewFilename('model.stl')
    } else {
      setStlViewUrl(api.entryUrl(props.designId, fileVersionId, entry.id))
      setStlViewFilename(entry.filename)
    }
    setStlViewZUp(resin)
    setStlViewName(entry.filename)
  }

  /**
   * Deep link: ?viewer=<fileVersionId>-<entryId> opens the 3D viewer for that
   * file directly (used by middle-click on the "3D" button → new window).
   * Runs once after the first file load.
   */
  let viewerDeepLinkDone = false
  const openViewerFromUrl = (versions: DesignFile[]) => {
    if (viewerDeepLinkDone) return
    viewerDeepLinkDone = true
    const param = new URLSearchParams(window.location.search).get('viewer')
    if (!param) return
    const [fvId, entryId] = param.split('-').map(Number)
    const fileVersion = versions.find(v => v.id === fvId)
    const entry = fileVersion?.entries?.find(en => en.id === entryId)
    if (!fileVersion || !entry) return
    setActiveTab('files')
    showEntryInViewer(fileVersion.id, entry)
  }

  /** Fetches all file versions for the design and updates `fileVersions`. */
  const loadFiles = () => {
    setIsLoadingFiles(true)
    api.getFiles(props.designId)
      .then((r: any) => {
        const versions: DesignFile[] = r.data || []
        setFileVersions(versions)
        // Prefill the version field with the suggestion while it is untouched.
        if (!uploadVersion().trim()) setUploadVersion(nextVersion())
        // Expand newest (current) version by default, collapse the rest.
        const newest = versions.find(v => v.is_current) || versions[0]
        setExpandedVersionIds(newest ? new Set<number>([newest.id]) : new Set<number>())
        openViewerFromUrl(versions)
      })
      .catch(() => {})
      .finally(() => setIsLoadingFiles(false))
  }

  loadDesign()
  loadFiles()

  /** All gallery images: uploaded images OR cover_path fallback. */
  const galleryImages = () => {
    const currentDesign = design()
    if (!currentDesign) return []
    if (currentDesign.images && currentDesign.images.length > 0) return currentDesign.images
    if (currentDesign.cover_path) return [{ path: currentDesign.cover_path, id: 0, design_id: props.designId, sort_order: 0, is_cover: true }]
    return []
  }

  const currentImageUrl = () => {
    const imgs = galleryImages()
    if (!imgs.length) return null
    return api.coverUrl((imgs[activeImageIndex()] || imgs[0]).path)
  }

  /** Persisted cover image id (from the server's is_cover flag), or null. */
  const persistedCoverId = () => {
    const cover = (design()?.images || []).find(img => (img as any).is_cover)
    return cover ? (cover as any).id as number : null
  }
  /** Cover shown in the UI: the staged pick while editing, else the persisted one. */
  const effectiveCoverId = () => pendingCoverId() !== null ? pendingCoverId() : persistedCoverId()

  /** The most recent (current) file version. */
  const currentVersion = () => fileVersions().find(version => version.is_current) || fileVersions()[0] || null

  /** First 3D-previewable file entry in the current version. */
  const firstPreviewEntry = () => {
    const cv = currentVersion()
    return cv?.entries?.find(entry => is3dFile(entry.filename)) || null
  }

  /** The files of the current version a desktop slicer can open. */
  const slicerEntries = () => {
    const cv = currentVersion()
    if (!cv) return null
    const entries = (cv.entries || []).filter(entry => SLICER_FORMATS.includes(lowerExt(entry.filename)))
    return entries.length > 0 ? { fileVersionId: cv.id, entries } : null
  }

  // ── Actions ──────────────────────────────────────────────────────────────────

  /**
   * Persists the current edit form to the API, updates the design's tag
   * associations, and exits edit mode. Shows a toast on success.
   */
  const saveDesign = async () => {
    if (!editForm().name?.trim()) { setErrorMessage(translate('validation.name_required')); return }
    // Checked here as well as on the server, so a typo is answered at the field
    // instead of after a round trip.
    const sourceUrl = normalizeSourceUrl(editForm().source_url)
    if (sourceUrl === null) { setErrorMessage(translate('error.invalid_url')); return }
    setIsSaving(true); setErrorMessage('')
    try {
      await api.updateDesign(props.designId, { ...editForm(), source_url: sourceUrl })
      await api.setDesignTags(props.designId, selectedTagIds())
      // Staged image deletions (Galerie) - only now removed on the server.
      const toDelete = pendingDeleteImageIds()
      for (const id of toDelete) { try { await api.deleteImage(props.designId, id) } catch {} }
      if (toDelete.length) setActiveImageIndex(0)
      // Staged cover selection (Titelbild) - only now committed to the server.
      const pendingCover = pendingCoverId()
      if (pendingCover !== null) await api.setDesignImageCover(props.designId, pendingCover)
      props.showToast(translate('toast_design_saved'))
      props.onTagsChanged()
      exitEditMode()
      loadDesign()
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : 'error.internal'
      setErrorMessage(t(message) !== message ? t(message) : message)
    } finally { setIsSaving(false) }
  }

  /**
   * Deletes the design via the API, shows a toast, and navigates back.
   */
  const deleteDesign = async () => {
    setIsDeleting(true)
    try {
      await api.deleteDesign(props.designId, excludeFromSync() && !!design()?.source_url)
      props.showToast(translate('toast_design_deleted'))
      props.onBack()
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : 'error.internal'
      setErrorMessage(t(message) !== message ? t(message) : message)
      setIsDeleting(false)
    }
  }

  const startSyncStream = () => props.onSync?.(props.designId, design()?.name ?? '')

  /**
   * Uploads one or more image files to the design's gallery.
   * Iterates the array sequentially and shows a single toast with the total count.
   * Returns how many images actually made it through, so callers (the viewer photo) can tell a
   * failure apart from success instead of reporting success either way.
   */
  const uploadDesignImage = async (files: FileList | File[]): Promise<number> => {
    setIsUploadingImage(true)
    const fileArray = Array.from(files)
    let uploaded = 0
    for (const file of fileArray) {
      const formData = new FormData()
      formData.append('image', file)
      try {
        await api.uploadImage(props.designId, formData)
        uploaded++
      } catch (err: unknown) {
        const message = err instanceof Error ? err.message : 'error.internal'
        props.showToast(t(message) !== message ? t(message) : message, 'error')
      }
    }
    if (uploaded > 0) {
      props.showToast(uploaded === 1 ? translate('toast_image_uploaded') : translate('toast_images_uploaded', { count: uploaded }))
      loadDesign()
      // The first image becomes the cover server-side, so reload the overview.
      props.onTagsChanged()
    }
    setIsUploadingImage(false)
    return uploaded
  }


  /**
   * Stages a gallery image as the new cover. The change is only persisted when
   * the user clicks Save (see {@link saveDesign}); until then it's preview-only.
   */
  const stageCover = (imageId: number) => {
    setPendingCoverId(imageId)
  }

  /** Uploads the selected archive file as a new file version. */
  const uploadDesignFile = async () => {
    if (uploadFileObject().length === 0) { setUploadError(translate('validation.file_required')); return }
    const version = uploadVersion().trim()
    // Numbers only (a decimal is fine), and not a version that already exists.
    if (!/^\d+(\.\d+)?$/.test(version)) { setUploadError(t('error.version_invalid')); return }
    if (fileVersions().some(v => v.version === version)) { setUploadError(t('error.version_exists')); return }
    setIsUploading(true); setUploadError('')
    try {
      const formData = new FormData()
      for (const f of uploadFileObject()) formData.append('file', f)
      formData.append('version', version)
      if (uploadNotes().trim()) formData.append('notes', uploadNotes().trim())
      await api.uploadFile(props.designId, formData)
      props.showToast(translate('toast_file_uploaded'))
      setUploadFileObject([]); setUploadVersion(''); setUploadNotes('')
      if (fileInputRef) fileInputRef.value = ''
      loadFiles() // belegt uploadVersion neu mit nextVersion()
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : 'error.internal'
      setUploadError(t(message) !== message ? t(message) : message)
    } finally { setIsUploading(false) }
  }

  /** Appends one or more files to an existing version. */
  const addFilesToVersion = async (fileVersionId: number, files: FileList | null) => {
    if (!files || files.length === 0) return
    try {
      const formData = new FormData()
      for (const f of Array.from(files)) formData.append('file', f)
      await api.addEntries(props.designId, fileVersionId, formData)
      props.showToast(translate('toast_file_uploaded'))
      loadFiles()
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : 'error.internal'
      props.showToast(t(message) !== message ? t(message) : message, 'error')
    }
  }

  /** Deletes a single file from a version, after asking. */
  const deleteFileEntry = async (fileVersionId: number, entry: DesignFileEntry) => {
    if (!confirm(translate('confirm_delete_file').replace('{name}', entry.filename))) return
    try {
      await api.deleteFileEntry(props.designId, fileVersionId, entry.id)
      props.showToast(translate('toast_file_deleted'))
      loadFiles()
      loadDesign()
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : 'error.internal'
      props.showToast(t(message) !== message ? t(message) : message, 'error')
    }
  }

  /**
   * Adds the design to a collection picked from the dropdown. Persisted right
   * away rather than on save.
   */
  const addCollectionSel = async (col: Collection) => {
    setColQuery(''); setColOpen(false)
    if (!notColSelected(col)) return
    try {
      await api.addToCollection(col.id, [props.designId])
      setSelectedCollections(cs => [...cs, col])
      props.showToast(translate('toast_added_to_collection'))
      props.onCollectionsChanged()
    } catch {}
  }

  /** Removes the design from a collection (the chip's ×). Persisted right away. */
  const removeCollectionSel = async (id: number) => {
    try {
      await api.removeFromCollection(id, props.designId)
      setSelectedCollections(cs => cs.filter(c => c.id !== id))
      props.showToast(translate('toast_removed_from_collection'))
      props.onCollectionsChanged()
    } catch {}
  }

  /**
   * Creates a collection named after what is in the search box and puts the
   * design in it right away, like createNewTag. Refreshes the parent list.
   */
  const createNewCollection = async () => {
    const name = colQuery().trim()
    if (!name || isCreatingCol()) return
    setIsCreatingCol(true)
    try {
      const res = await api.createCollection({ name }) as { data: Collection }
      const col = res.data
      await api.addToCollection(col.id, [props.designId])
      setSelectedCollections(cs => [...cs, col])
      props.showToast(translate('toast_collection_created'))
      props.onCollectionsChanged()
    } catch (failure: unknown) { props.showToast(translate(errorKey(failure)), 'error') }
    finally { setIsCreatingCol(false); setColQuery(''); setColOpen(false) }
  }

  /**
   * Shares the design with a comma-separated list of email addresses.
   * Reloads the design after sharing to refresh the shares list.
   */
  const shareWithUsers = async () => {
    const recipients = sharePicked().map(user => user.email || user.name).filter(Boolean)
    if (!recipients.length) return
    setIsSharing(true)
    try {
      await api.shareDesign(props.designId, recipients)
      props.showToast(translate('toast_shared'))
      setSharePicked([])
      setShareEmailInput('')
      setShareUserSuggestions([])
      loadDesign()
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : 'error.internal'
      props.showToast(t(message) !== message ? t(message) : message, 'error')
    } finally { setIsSharing(false) }
  }

  /** Adds a suggestion to the pending recipients (no duplicates). */
  const pickShareUser = (user: {id:string,name:string,email:string}) => {
    setSharePicked(current => current.some(picked => picked.id === user.id) ? current : [...current, user])
    setShareEmailInput('')
    setShareUserSuggestions(current => current.filter(entry => entry.id !== user.id))
  }

  const unshareUser = async (shareId: number) => {
    try { await api.unshareDesign(props.designId, shareId); props.showToast(translate('toast_unshared')); loadDesign() } catch {}
  }

  /**
   * Creates a new tag with the current name/colour inputs and immediately
   * selects it for the design. Notifies the parent via `onTagsChanged`.
   */
  const createNewTag = async () => {
    const name = tagQuery().trim()
    if (!name) return
    setIsCreatingTag(true)
    try {
      const response = await api.createTag({ name, color: newTagColor() }) as { data: Tag }
      addSelectedTag(response.data)
      props.onTagsChanged()
    } catch {} finally { setIsCreatingTag(false) }
  }

  const GRADIENT_FALLBACK = `linear-gradient(135deg, #1a1a2e, #16213e, #0f3460)`

  return (
    <div style={{ 'min-height': '100vh', background: 'var(--bg)', 'padding-bottom': '70px' }}>
      <style>{`@keyframes spin{to{transform:rotate(360deg)}}`}</style>

      {/* Breadcrumb bar. Gone once the design turned out to be unavailable:
          with no name and no actions left, all it still carried was a second
          way back, and the error panel already offers one. */}
      <Show when={design() || isLoading()}>
      <div class="stlv-breadcrumb" style={{ background: 'var(--nav-bg)', 'backdrop-filter': 'blur(16px)', 'border-bottom': '1px solid var(--border)', padding: '0 36px', height: '56px', display: 'flex', 'align-items': 'center', gap: '16px', position: 'sticky', top: '85px', 'z-index': '90' }}>
        <button onClick={() => guardClose(props.onBack)}
          style={{ background: 'none', border: 'none', color: 'var(--text2)', cursor: 'pointer', ...sansFont, 'font-size': '14px', 'font-weight': '600', display: 'flex', 'align-items': 'center', gap: '5px' }}>
          <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><polyline points="15 18 9 12 15 6"/></svg>
          {translate('btn_back')}
        </button>
        <div style={{ height: '24px', width: '1px', background: 'var(--border)' }} />
        <span style={{ ...sansFont, 'font-size': '14px', color: 'var(--text2)', overflow: 'hidden', 'text-overflow': 'ellipsis', 'white-space': 'nowrap', flex: '1' }}>
          {/* The ellipsis means "still loading"; a design that will never arrive
              must not keep pretending it is on its way. */}
          {design() ? shownName() : isLoading() ? '…' : ''}
        </span>
        <Show when={design()?.is_shared}>
          <span style={{ ...monoFont, 'font-size': '11px', background: 'rgba(69,123,157,0.2)', color: 'var(--accent-light)', 'border-radius': '5px', padding: '3px 9px' }}>
            {translate('shared_with_you')}
          </span>
        </Show>
        {/* Every action here operates on the design; with none loaded they were
            offered anyway, so a design the user may not see still showed
            "delete" and "edit" next to an empty page. */}
        <div style={{ display: 'flex', gap: '8px', 'margin-left': 'auto' }}>
          <Show when={!props.isReadOnly && design()?.source_url}>
            {(() => {
              const active = props.syncStatus === 'queued' || props.syncStatus === 'syncing'
              return (
                <button onClick={active ? undefined : startSyncStream} disabled={active}
                  style={{ padding: '7px 15px', background: 'var(--bg4)', border: '1px solid var(--border2)', 'border-radius': '9px', color: active ? 'var(--muted)' : 'var(--text)', 'font-size': '13px', cursor: active ? 'default' : 'pointer', ...sansFont, 'font-weight': '500', display: 'flex', 'align-items': 'center', gap: '7px', opacity: active ? '0.7' : '1' }}>
                  <Show when={active}>
                    <div style={{ width: '12px', height: '12px', 'flex-shrink': '0', border: '2px solid var(--muted)', 'border-top-color': 'transparent', 'border-radius': '50%', animation: 'spin 0.7s linear infinite' }} />
                  </Show>
                  {active ? translate('btn_syncing') : translate('btn_sync')}
                </button>
              )
            })()}
          </Show>
          <Show when={!props.isReadOnly && design()}>
            <button onClick={() => setConfirmDelete(true)}
              style={{ padding: '7px 13px', background: 'var(--danger-bg)', border: '1px solid var(--danger-border)', 'border-radius': '9px', color: 'var(--danger)', 'font-size': '13px', cursor: 'pointer', ...sansFont }}>
              {translate('btn_delete_design')}
            </button>
            <button onClick={() => {
              if (!isEditing()) { enterEditMode() }
              else { guardClose(exitEditMode) }
            }}
              style={{ padding: '7px 15px', background: isEditing() ? 'var(--surface)' : 'var(--accent)', border: `1px solid ${isEditing() ? 'var(--border)' : 'var(--accent)'}`, 'border-radius': '9px', color: isEditing() ? 'var(--muted)' : '#fff', 'font-size': '13px', cursor: 'pointer', ...sansFont, 'font-weight': '600' }}>
              {isEditing() ? translate('btn_cancel') : translate('btn_edit')}
            </button>
          </Show>
        </div>
      </div>
      </Show>

      {/* Sync status banner */}
      <Show when={props.syncStatus === 'queued' || props.syncStatus === 'syncing' || props.syncStatus === 'updated' || props.syncStatus === 'error'}>
        <div style={{
          background: props.syncStatus === 'error' ? 'rgba(239,68,68,0.1)' : props.syncStatus === 'updated' ? 'rgba(34,197,94,0.1)' : 'rgba(69,123,157,0.1)',
          'border-bottom': `1px solid ${props.syncStatus === 'error' ? 'rgba(239,68,68,0.3)' : props.syncStatus === 'updated' ? 'rgba(34,197,94,0.3)' : 'rgba(69,123,157,0.3)'}`,
          padding: '10px 36px', display: 'flex', 'align-items': 'center', gap: '10px'
        }}>
          <Show when={props.syncStatus === 'queued' || props.syncStatus === 'syncing'}>
            <div style={{ width: '14px', height: '14px', 'flex-shrink': '0', border: '2px solid var(--accent)', 'border-top-color': 'transparent', 'border-radius': '50%', animation: 'spin 0.7s linear infinite' }} />
          </Show>
          <Show when={props.syncStatus === 'updated'}>
            <div style={{ width: '14px', height: '14px', 'flex-shrink': '0', background: '#22c55e', 'border-radius': '50%', display: 'flex', 'align-items': 'center', 'justify-content': 'center', 'font-size': '9px', color: '#fff' }}>✓</div>
          </Show>
          <Show when={props.syncStatus === 'error'}>
            <div style={{ width: '14px', height: '14px', 'flex-shrink': '0', background: '#ef4444', 'border-radius': '50%', display: 'flex', 'align-items': 'center', 'justify-content': 'center', 'font-size': '9px', color: '#fff' }}>✕</div>
          </Show>
          <span style={{ ...sansFont, 'font-size': '13px', color: 'var(--text)', 'font-weight': '500' }}>
            {props.syncStatus === 'queued'  && translate('sync_status_queued')}
            {props.syncStatus === 'syncing' && (
              props.syncStep?.step
                ? (props.syncStep.tot > 0
                    ? `${translate(`sync_step_${props.syncStep.step}` as any)} (${props.syncStep.cur}/${props.syncStep.tot})`
                    : translate(`sync_step_${props.syncStep.step}` as any))
                : (props.syncProgress && props.syncProgress > 0
                    ? `${translate('sync_status_syncing')} ${props.syncProgress}%`
                    : translate('sync_status_syncing'))
            )}
            {props.syncStatus === 'updated' && translate('sync_status_updated')}
            {props.syncStatus === 'error'   && translate('sync_status_error')}
          </span>
        </div>
      </Show>

      <Show when={isLoading()} fallback={
        <Show when={design()} fallback={
          /* Nothing loaded: the error box lived inside the content, so a design
             the server refused left the page blank apart from the breadcrumb -
             no reason, and nothing to do about it. */
          <div style={{ padding: '80px 36px', 'max-width': '620px', margin: '0 auto', 'text-align': 'center' }}>
            <div style={{ 'font-size': '44px', 'margin-bottom': '16px', opacity: '0.35' }}>🔒</div>
            <div style={{ ...sansFont, 'font-size': '18px', 'font-weight': '700', color: 'var(--text)', 'margin-bottom': '8px' }}>
              {translate('design_unavailable_title')}
            </div>
            <div style={{ ...sansFont, 'font-size': '14px', color: 'var(--muted)', 'line-height': '1.6', 'margin-bottom': '22px' }}>
              {errorMessage() || translate('design_unavailable_hint')}
            </div>
            <button onClick={props.onBack}
              style={{ padding: '10px 22px', background: 'var(--accent)', border: 'none', 'border-radius': '10px', color: '#fff', ...sansFont, 'font-size': '14px', 'font-weight': '600', cursor: 'pointer' }}>
              {translate('btn_back')}
            </button>
          </div>
        }>
          <div style={{ padding: '32px 36px', 'max-width': '1500px', margin: '0 auto' }}>
            <ErrorBox message={errorMessage()} />

            {/* ── Main 2-column layout ── */}
            <div class="stlv-detail-grid" style={{ display: 'grid', 'grid-template-columns': '420px 1fr', gap: '32px', 'margin-bottom': '32px' }}>

              {/* ─── LEFT: Image gallery ─── */}
              <div>
                {/* Main image */}
                <div style={{ 'border-radius': '18px', overflow: 'hidden', height: '400px', background: currentImageUrl() ? 'var(--bg2)' : GRADIENT_FALLBACK, position: 'relative', border: '1px solid var(--border)' }}>
                  <Show when={currentImageUrl()} fallback={
                    <div style={{ display: 'flex', 'align-items': 'center', 'justify-content': 'center', height: '100%', 'font-size': '72px', opacity: '0.2' }}>🖨️</div>
                  }>
                    <img src={currentImageUrl()!} alt={design()!.name} style={{ width: '100%', height: '100%', 'object-fit': 'cover' }}
                      onError={e => { (e.target as HTMLImageElement).style.display = 'none' }} />
                  </Show>

                  {/* Left/right navigation arrows */}
                  <Show when={galleryImages().length > 1}>
                    <button
                      onClick={e => { e.stopPropagation(); setActiveImageIndex(i => (i - 1 + galleryImages().length) % galleryImages().length) }}
                      style={{ position: 'absolute', left: '10px', top: '50%', transform: 'translateY(-50%)', background: 'rgba(0,0,0,0.6)', border: '1px solid rgba(255,255,255,0.25)', 'border-radius': '50%', width: '38px', height: '38px', display: 'flex', 'align-items': 'center', 'justify-content': 'center', cursor: 'pointer', 'z-index': '5', 'backdrop-filter': 'blur(4px)' }}>
                      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="#fff" stroke-width="2.8"><polyline points="15 18 9 12 15 6"/></svg>
                    </button>
                    <button
                      onClick={e => { e.stopPropagation(); setActiveImageIndex(i => (i + 1) % galleryImages().length) }}
                      style={{ position: 'absolute', right: '10px', top: '50%', transform: 'translateY(-50%)', background: 'rgba(0,0,0,0.6)', border: '1px solid rgba(255,255,255,0.25)', 'border-radius': '50%', width: '38px', height: '38px', display: 'flex', 'align-items': 'center', 'justify-content': 'center', cursor: 'pointer', 'z-index': '5', 'backdrop-filter': 'blur(4px)' }}>
                      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="#fff" stroke-width="2.8"><polyline points="9 18 15 12 9 6"/></svg>
                    </button>
                  </Show>

                  {/* 3D view overlay button */}
                  <Show when={firstPreviewEntry() && currentVersion()}>
                    <button
                      onClick={() => {
                        const cv = currentVersion()!
                        const entry = firstPreviewEntry()!
                        setStlViewUrl(api.entryUrl(props.designId, cv.id, entry.id))
                        setStlViewName(entry.filename)
                        setStlViewFilename(entry.filename)
                      }}
                      style={{ position: 'absolute', bottom: '14px', left: '14px', background: 'rgba(0,0,0,0.65)', border: '1px solid rgba(255,255,255,0.2)', 'border-radius': '10px', padding: '7px 14px', color: '#fff', 'font-size': '13px', cursor: 'pointer', ...monoFont, display: 'flex', 'align-items': 'center', gap: '7px', 'font-weight': '500', 'backdrop-filter': 'blur(4px)' }}>
                      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="#fff" stroke-width="2">
                        <path d="M12 2L2 7l10 5 10-5-10-5z"/><path d="M2 17l10 5 10-5"/><path d="M2 12l10 5 10-5"/>
                      </svg>
                      {translate('btn_view_3d')}
                    </button>
                  </Show>

                  {/* Add photo button */}
                  <Show when={!props.isReadOnly && isEditing()}>
                    <button onClick={() => imageInputRef?.click()} disabled={isUploadingImage()}
                      style={{ position: 'absolute', bottom: '14px', right: '14px', background: 'rgba(0,0,0,0.65)', border: '1px solid rgba(255,255,255,0.2)', 'border-radius': '10px', padding: '7px 13px', color: '#fff', 'font-size': '12px', cursor: 'pointer', ...monoFont, display: 'flex', 'align-items': 'center', gap: '6px', 'backdrop-filter': 'blur(4px)' }}>
                      <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="#fff" stroke-width="2.5">
                        <rect x="3" y="3" width="18" height="18" rx="2"/><circle cx="8.5" cy="8.5" r="1.5"/><polyline points="21 15 16 10 5 21"/>
                      </svg>
                      {isUploadingImage() ? '…' : translate('btn_add_photo')}
                    </button>
                    <input ref={imageInputRef} type="file" accept="image/*" multiple style={{ display: 'none' }}
                      onChange={e => { const files = e.currentTarget.files; if (files && files.length > 0) uploadDesignImage(files); e.currentTarget.value = '' }} />
                  </Show>
                </div>

                {/* Thumbnail strip */}
                <Show when={galleryImages().length > 1}>
                  <div style={{ display: 'flex', gap: '9px', 'margin-top': '11px', 'overflow-x': 'auto', 'padding-bottom': '4px' }}>
                    <For each={galleryImages()}>{(img, index) => (
                      <div onClick={() => setActiveImageIndex(index())}
                        style={{ width: '80px', height: '80px', 'flex-shrink': '0', 'border-radius': '10px', overflow: 'hidden', cursor: 'pointer', border: activeImageIndex() === index() ? '2px solid var(--accent)' : '2px solid var(--border)', 'transition': 'border 0.15s', position: 'relative' }}>
                        <img src={api.coverUrl(img.path)} alt="" style={{ width: '100%', height: '100%', 'object-fit': 'cover', opacity: ('id' in img && isStagedDelete((img as any).id)) ? '0.3' : '1', transition: 'opacity 0.15s' }} />
                        {/* Cover-Kennzeichnung / „Als Titelbild"-Knopf (nur echte Bilder, nicht bei zum Löschen markierten). */}
                        <Show when={'id' in img && (img as any).id > 0 && !isStagedDelete((img as any).id)}>
                          <Show when={effectiveCoverId() === (img as any).id} fallback={
                            <Show when={isEditing() && !props.isReadOnly}>
                              <button onClick={e => { e.stopPropagation(); stageCover((img as any).id) }} title={translate('btn_set_cover')}
                                style={{ position: 'absolute', top: '3px', left: '3px', background: 'rgba(0,0,0,0.6)', border: 'none', 'border-radius': '4px', color: '#fff', cursor: 'pointer', width: '20px', height: '20px', display: 'flex', 'align-items': 'center', 'justify-content': 'center', padding: '0' }}>
                                <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="#fff" stroke-width="2"><polygon points="12 2 15.09 8.26 22 9.27 17 14.14 18.18 21.02 12 17.77 5.82 21.02 7 14.14 2 9.27 8.91 8.26 12 2"/></svg>
                              </button>
                            </Show>
                          }>
                            <div title={translate('label_cover')}
                              style={{ position: 'absolute', top: '3px', left: '3px', background: 'var(--accent)', 'border-radius': '4px', color: '#fff', width: '20px', height: '20px', display: 'flex', 'align-items': 'center', 'justify-content': 'center' }}>
                              <svg width="12" height="12" viewBox="0 0 24 24" fill="#fff" stroke="#fff" stroke-width="1"><polygon points="12 2 15.09 8.26 22 9.27 17 14.14 18.18 21.02 12 17.77 5.82 21.02 7 14.14 2 9.27 8.91 8.26 12 2"/></svg>
                            </div>
                          </Show>
                        </Show>
                        <Show when={isEditing() && !props.isReadOnly && 'id' in img && (img as any).id > 0}>
                          <Show when={isStagedDelete((img as any).id)} fallback={
                            <button onClick={e => { e.stopPropagation(); setConfirmDeleteImageId((img as any).id) }} title={translate('btn_delete_image')}
                              style={{ position: 'absolute', top: '3px', right: '3px', background: 'rgba(230,57,70,0.85)', border: 'none', 'border-radius': '4px', color: '#fff', 'font-size': '11px', cursor: 'pointer', width: '20px', height: '20px', display: 'flex', 'align-items': 'center', 'justify-content': 'center' }}>×</button>
                          }>
                            {/* Zum Löschen markiert: „Rückgängig"-Knopf; endgültig gelöscht wird erst beim Speichern. */}
                            <button onClick={e => { e.stopPropagation(); toggleDeleteImage((img as any).id) }} title={translate('btn_undo_delete')}
                              style={{ position: 'absolute', inset: '0', background: 'rgba(230,57,70,0.28)', border: '2px solid var(--danger)', 'border-radius': '10px', color: '#fff', cursor: 'pointer', display: 'flex', 'align-items': 'center', 'justify-content': 'center', gap: '4px', ...monoFont, 'font-size': '10px', 'font-weight': '700' }}>
                              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="#fff" stroke-width="2.2"><polyline points="1 4 1 10 7 10"/><path d="M3.51 15a9 9 0 1 0 2.13-9.36L1 10"/></svg>
                            </button>
                          </Show>
                        </Show>
                      </div>
                    )}</For>
                  </div>
                </Show>

                {/* Slicer buttons */}
                <Show when={slicerEntries()}>
                  <div style={{ 'margin-top': '18px' }}>
                    <div style={{ ...monoFont, 'font-size': '11px', color: 'var(--muted)', 'text-transform': 'uppercase', 'letter-spacing': '0.08em', 'margin-bottom': '9px' }}>{translate('btn_open_slicer')}</div>
                    <SlicerButtons designId={props.designId} fileVersionId={slicerEntries()!.fileVersionId} entries={slicerEntries()!.entries} />
                  </div>
                </Show>

                {/* Translation notice + original/translation toggle */}
                <Show when={isTranslated()}>
                  <div style={{ display: 'flex', 'align-items': 'center', gap: '9px', 'margin-top': '32px', 'flex-wrap': 'wrap' }}>
                    <span style={{ ...monoFont, 'font-size': '10px', 'font-weight': '700', color: '#fff', background: 'var(--accent)', border: 'none', 'border-radius': '6px', padding: '3px 8px', display: 'inline-flex', 'align-items': 'center', gap: '5px' }}>
                      <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="10"/><line x1="2" y1="12" x2="22" y2="12"/><path d="M12 2a15.3 15.3 0 0 1 4 10 15.3 15.3 0 0 1-4 10 15.3 15.3 0 0 1-4-10 15.3 15.3 0 0 1 4-10z"/></svg>
                      {translate(showOriginal() ? 'design_showing_original' : 'design_translated_badge')}
                    </span>
                    <button onClick={() => setShowOriginal(o => !o)}
                      style={{ background: 'none', border: 'none', color: 'var(--accent)', cursor: 'pointer', ...sansFont, 'font-size': '12px', 'text-decoration': 'underline', padding: '0' }}>
                      {translate(showOriginal() ? 'design_show_translation' : 'design_show_original')}
                    </button>
                  </div>
                </Show>
              </div>

              {/* ─── RIGHT: Metadata panel ─── */}
              <div style={{ display: 'flex', 'flex-direction': 'column', gap: '22px' }}>

                {/* Name + platform */}
                <div>
                  <h1 style={{ ...sansFont, 'font-size': '28px', 'font-weight': '700', color: 'var(--text)', 'line-height': '1.2', margin: '0 0 10px' }}>
                    {shownName()}
                  </h1>
                  <div style={{ display: 'flex', gap: '9px', 'align-items': 'center', 'flex-wrap': 'wrap' }}>
                    <Show when={design()!.source_platform}>
                      <span style={{ ...monoFont, 'font-size': '11px', 'font-weight': '700', color: '#fff', background: PLATFORM_COLORS[design()!.source_platform!] || 'rgba(0,0,0,0.45)', padding: '4px 10px', 'border-radius': '8px' }}>
                        {platformLabel(design()!.source_platform!, translate)}
                      </span>
                    </Show>
                    <Show when={shownAuthor()}>
                      <span style={{ ...sansFont, 'font-size': '13px', color: 'var(--muted)' }}>{translate('card_by')} {shownAuthor()}</span>
                    </Show>
                  </div>
                </div>

                {/* Rating */}
                <div>
                  <div style={labelStyle}>{translate('field_rating')}</div>
                  <StarRating
                    value={design()!.rating || 0}
                    onChange={!props.isReadOnly ? async (rating) => { setDesign(d => d ? { ...d, rating } : d); setEditForm(f => ({ ...f, rating })); try { await api.updateDesign(props.designId, { rating }) } catch {} } : undefined}
                  />
                </div>

                {/* Metadata grid */}
                <div style={{ display: 'grid', 'grid-template-columns': '1fr 1fr', gap: '12px' }}>
                  {([
                    { label: translate('field_category'),     value: design()!.category },
                    { label: translate('field_license'),      value: design()!.license },
                    { label: translate('label_print_time'),   value: design()!.print_time_minutes ? formatPrintTime(design()!.print_time_minutes!) : null },
                    { label: translate('tab_files'),          value: design()!.file_count ? translate('label_files_in_version').replace('{count}', String(design()!.file_count!)) : null },
                    { label: translate('label_size'),         value: design()!.size_bytes ? formatBytes(design()!.size_bytes!) : null },
                    { label: translate('label_version'),      value: currentVersion()?.version ? `v${currentVersion()!.version}` : null },
                    { label: translate('label_updated'),      value: design()!.updated_at ? (() => { const date = new Date(design()!.updated_at!); const dd = String(date.getDate()).padStart(2,'0'); const mm = String(date.getMonth()+1).padStart(2,'0'); const yy = date.getFullYear(); const hh = String(date.getHours()).padStart(2,'0'); const min = String(date.getMinutes()).padStart(2,'0'); return `${dd}.${mm}.${yy} ${hh}:${min}`; })() : null },
                  ] as const).filter(item => item.value).map(item => (
                    <div style={{ background: 'var(--surface)', border: '1px solid var(--border)', 'border-radius': '11px', padding: '11px 15px' }}>
                      <div style={{ ...monoFont, 'font-size': '10px', color: 'var(--muted)', 'margin-bottom': '5px', 'text-transform': 'uppercase', 'letter-spacing': '0.05em' }}>{item.label}</div>
                      <div style={{ ...sansFont, 'font-size': '14px', color: 'var(--text2)', 'font-weight': '500' }}>{item.value}</div>
                    </div>
                  ))}
                </div>

                {/* Tags (read-only display when not editing) */}
                <div>
                  <div style={labelStyle}>{translate('label_tags')}</div>
                  <div style={{ display: 'flex', 'flex-wrap': 'wrap', gap: '7px' }}>
                    <Show when={(!design()?.tags || design()!.tags!.length === 0)}>
                      <span style={{ 'font-size': '13px', color: 'var(--muted)', 'font-style': 'italic' }}>{translate('label_no_tags')}</span>
                    </Show>
                    <For each={design()!.tags || []}>{tag => (
                      <span style={{ 'font-size': '12px', padding: '4px 11px', 'border-radius': '7px', background: tag.color + '33', color: tag.color, ...sansFont, 'font-weight': '600' }}>
                        {tag.name}
                      </span>
                    )}</For>
                  </div>
                </div>

                {/* Collections - read-only display only. Edit via the edit form. */}
                <div>
                  <div style={labelStyle}>{translate('collection_title')}</div>
                  <div style={{ display: 'flex', 'flex-wrap': 'wrap', gap: '7px' }}>
                    <Show when={designCollectionIds().length === 0}>
                      <span style={{ 'font-size': '13px', color: 'var(--muted)', 'font-style': 'italic' }}>{translate('label_not_in_collection')}</span>
                    </Show>
                    <For each={props.allCollections.filter(c => designCollectionIds().includes(c.id))}>{col => (
                      <button onClick={() => props.onOpenCollection?.(col.id)}
                        style={{ 'font-size': '12px', padding: '4px 11px', 'border-radius': '7px', background: 'rgba(69,123,157,0.15)', color: 'var(--accent-light)', ...sansFont, 'font-weight': '600', display: 'flex', 'align-items': 'center', gap: '5px', border: 'none', cursor: props.onOpenCollection ? 'pointer' : 'default' }}>
                        <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z"/></svg>
                        {col.name}
                      </button>
                    )}</For>
                    {/* A collection the design is in but that is hidden: show a
                        neutral placeholder instead of its name, so the design
                        still reads as "in a collection" without revealing which. */}
                    <For each={selectedCollections().filter(c => c.is_hidden)}>{() => (
                      <span style={{ 'font-size': '12px', padding: '4px 11px', 'border-radius': '7px', background: 'var(--bg3)', color: 'var(--muted)', ...sansFont, 'font-weight': '600', 'font-style': 'italic', display: 'flex', 'align-items': 'center', gap: '5px' }}>
                        <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M17.94 17.94A10.07 10.07 0 0 1 12 20c-7 0-11-8-11-8a18.45 18.45 0 0 1 5.06-5.94M9.9 4.24A9.12 9.12 0 0 1 12 4c7 0 11 8 11 8a18.5 18.5 0 0 1-2.16 3.19m-6.72-1.07a3 3 0 1 1-4.24-4.24"/><line x1="1" y1="1" x2="23" y2="23"/></svg>
                        {translate('col_hidden_placeholder')}
                      </span>
                    )}</For>
                  </div>
                </div>

                {/* Source URL */}
                <Show when={design()!.source_url}>
                  <div>
                    <div style={labelStyle}>{translate('label_source')}</div>
                    <a href={design()!.source_url} target="_blank" rel="noopener"
                      style={{ ...monoFont, 'font-size': '12px', color: 'var(--accent-light)', 'word-break': 'break-all', 'text-decoration': 'none' }}>
                      {design()!.source_url}
                    </a>
                  </div>
                </Show>
              </div>
            </div>

            {/* ── Edit form ── */}
            <Show when={isEditing() && !props.isReadOnly}>
              <div style={{ background: 'var(--bg2)', border: '1px solid var(--border)', 'border-radius': '18px', padding: '26px', 'margin-bottom': '30px' }}
                onInput={markDirty} onChange={markDirty}>
                <div style={{ ...sansFont, 'font-weight': '700', 'font-size': '16px', color: 'var(--text)', 'margin-bottom': '20px' }}>{translate('label_edit_design')}</div>
                <ErrorBox message={errorMessage()} />
                <div style={{ display: 'grid', 'grid-template-columns': '1fr 1fr', gap: '14px' }}>
                  <div style={{ 'grid-column': '1 / -1' }}>
                    <label style={labelStyle}>{translate('field_name')} *</label>
                    <input style={inputStyle} value={editForm().name} onInput={e => setEditForm(f => ({...f, name: e.currentTarget.value}))} />
                  </div>
                  <div style={{ 'grid-column': '1 / -1' }}>
                    <label style={labelStyle}>{translate('field_description')}</label>
                    <textarea rows={3} style={{...inputStyle, resize: 'vertical'}} value={editForm().description} onInput={e => setEditForm(f => ({...f, description: e.currentTarget.value}))} />
                  </div>
                  <div><label style={labelStyle}>{translate('field_category')}</label><input style={inputStyle} value={editForm().category} onInput={e => setEditForm(f => ({...f, category: e.currentTarget.value}))} /></div>
                  <div><label style={labelStyle}>{translate('field_author')}</label><input style={inputStyle} value={editForm().author} onInput={e => setEditForm(f => ({...f, author: e.currentTarget.value}))} /></div>
                  <div><label style={labelStyle}>{translate('field_license')}</label><input style={inputStyle} value={editForm().license} onInput={e => setEditForm(f => ({...f, license: e.currentTarget.value}))} /></div>
                  <div style={{ 'grid-column': '1 / -1' }}>
                    <label style={labelStyle}>{translate('field_source_url')}</label>
                    <input style={inputStyle} value={editForm().source_url} onInput={e => setEditForm(f => ({...f, source_url: e.currentTarget.value}))} />
                  </div>
                  <div style={{ 'grid-column': '1 / -1' }}>
                    <label style={labelStyle}>{translate('field_visibility')}</label>
                    <button type="button" onClick={() => setEditForm(f => ({...f, is_hidden: f.is_hidden ? 0 : 1}))}
                      style={{ padding: '9px 17px', 'border-radius': '9px', border: `1px solid ${editForm().is_hidden ? 'var(--accent)' : 'var(--border)'}`, background: editForm().is_hidden ? 'rgba(69,123,157,0.15)' : 'var(--surface)', color: editForm().is_hidden ? 'var(--accent-light)' : 'var(--text3)', 'font-size': '14px', cursor: 'pointer', ...sansFont, 'font-weight': '500', display: 'flex', 'align-items': 'center', gap: '7px' }}>
                      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2">
                        <Show when={!editForm().is_hidden}><path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z"/><circle cx="12" cy="12" r="3"/></Show>
                        <Show when={!!editForm().is_hidden}><path d="M17.94 17.94A10.07 10.07 0 0 1 12 20c-7 0-11-8-11-8a18.45 18.45 0 0 1 5.06-5.94"/><path d="M9.9 4.24A9.12 9.12 0 0 1 12 4c7 0 11 8 11 8a18.5 18.5 0 0 1-2.16 3.19"/><line x1="1" y1="1" x2="23" y2="23"/></Show>
                      </svg>
                      {editForm().is_hidden ? translate('btn_unhide_design') : translate('btn_hide_design')}
                    </button>
                  </div>
                </div>

                {/* Collection editor - durchsuchbare Combobox (Select2-Stil), nur beim Bearbeiten */}
                <Show when={!props.isReadOnly}>
                  <div style={{ 'margin-top': '16px' }}>
                    <label style={labelStyle}>{translate('collection_title')}</label>
                    {/* Ausgewählte Sammlungen als entfernbare Chips */}
                    <div style={{ display: 'flex', 'flex-wrap': 'wrap', gap: '7px', 'margin-bottom': '9px' }}>
                      <For each={selectedCollections()}>{col => (
                        <span style={{ display: 'inline-flex', 'align-items': 'center', gap: '6px', 'font-size': '12px', padding: '4px 6px 4px 11px', 'border-radius': '7px', background: 'rgba(69,123,157,0.18)', color: 'var(--accent-light)', ...sansFont, 'font-weight': '600' }}>
                          {col.name}
                          <button onClick={() => removeCollectionSel(col.id)} title={translate('btn_delete')}
                            style={{ background: 'none', border: 'none', color: 'var(--accent-light)', cursor: 'pointer', 'font-size': '15px', 'line-height': '1', padding: '0' }}>×</button>
                        </span>
                      )}</For>
                      <Show when={selectedCollections().length === 0}>
                        <span style={{ 'font-size': '13px', color: 'var(--muted)', 'font-style': 'italic' }}>{translate('label_no_collections')}</span>
                      </Show>
                    </div>
                    {/* Sucheingabe + Dropdown */}
                    <div style={{ position: 'relative' }}>
                      <input value={colQuery()} placeholder={translate('collection_search_placeholder')}
                        onInput={e => { setColQuery(e.currentTarget.value); setColOpen(true) }}
                        onFocus={() => setColOpen(true)}
                        onClick={() => setColOpen(true)}
                        onBlur={() => setTimeout(() => setColOpen(false), 150)}
                        onKeyDown={onColKeyDown}
                        style={{ ...inputStyle, width: '100%', 'font-size': '13px', padding: '7px 11px' }} />
                      <Show when={colOpen() && (colResults().length > 0 || showCreateCol())}>
                        <div style={{ position: 'absolute', top: 'calc(100% + 4px)', left: '0', right: '0', 'z-index': '30', background: 'var(--bg2)', border: '1px solid var(--border)', 'border-radius': '10px', 'box-shadow': '0 12px 30px rgba(0,0,0,0.35)', 'max-height': '240px', 'overflow-y': 'auto', padding: '5px' }}>
                          <For each={colResults()}>{col => (
                            <button onMouseDown={e => e.preventDefault()} onClick={() => addCollectionSel(col)}
                              style={{ display: 'flex', 'align-items': 'center', gap: '8px', width: '100%', 'text-align': 'left', padding: '7px 9px', background: 'none', border: 'none', 'border-radius': '7px', cursor: 'pointer', ...sansFont, 'font-size': '13px', color: 'var(--text2)' }}>
                              {col.name}
                            </button>
                          )}</For>
                          <Show when={showCreateCol()}>
                            <div style={{ padding: '7px 9px', 'border-top': colResults().length > 0 ? '1px solid var(--border)' : 'none' }}>
                              <button onMouseDown={e => e.preventDefault()} onClick={createNewCollection} disabled={isCreatingCol()}
                                style={{ 'max-width': '100%', padding: '5px 11px', background: 'var(--accent)', border: 'none', 'border-radius': '7px', color: '#fff', 'font-size': '13px', cursor: 'pointer', ...sansFont, opacity: isCreatingCol() ? '0.6' : '1', overflow: 'hidden', 'text-overflow': 'ellipsis', 'white-space': 'nowrap' }}>
                                {isCreatingCol() ? '…' : translate('collection_create', { name: colQuery().trim() })}
                              </button>
                            </div>
                          </Show>
                        </div>
                      </Show>
                    </div>
                  </div>
                </Show>

                {/* Tag editor - durchsuchbare Combobox (Select2-Stil) */}
                <div style={{ 'margin-top': '16px' }}>
                  <label style={labelStyle}>{translate('label_tags')}</label>
                  {/* Ausgewählte Tags als entfernbare Chips */}
                  <div style={{ display: 'flex', 'flex-wrap': 'wrap', gap: '7px', 'margin-bottom': '9px' }}>
                    <For each={selectedTags()}>{tag => (
                      <span style={{ display: 'inline-flex', 'align-items': 'center', gap: '6px', 'font-size': '12px', padding: '4px 6px 4px 11px', 'border-radius': '7px', background: tag.color + '33', color: tag.color, ...sansFont, 'font-weight': '600' }}>
                        {tag.name}
                        <button onClick={() => removeSelectedTag(tag.id)} title={translate('btn_delete')}
                          style={{ background: 'none', border: 'none', color: tag.color, cursor: 'pointer', 'font-size': '15px', 'line-height': '1', padding: '0' }}>×</button>
                      </span>
                    )}</For>
                    <Show when={selectedTags().length === 0}>
                      <span style={{ 'font-size': '13px', color: 'var(--muted)', 'font-style': 'italic' }}>{translate('label_no_tags')}</span>
                    </Show>
                  </div>
                  {/* Sucheingabe + Dropdown */}
                  <div style={{ position: 'relative' }}>
                    <input value={tagQuery()} placeholder={translate('tag_search_placeholder')}
                      onInput={e => onTagInput(e.currentTarget.value)}
                      onFocus={() => { setTagOpen(true); runTagSearch(tagQuery()) }}
                      onClick={() => { if (!tagOpen()) { setTagOpen(true); runTagSearch(tagQuery()) } }}
                      onBlur={() => setTimeout(() => setTagOpen(false), 150)}
                      onKeyDown={onTagKeyDown}
                      style={{ ...inputStyle, width: '100%', 'font-size': '13px', padding: '7px 11px' }} />
                    <Show when={tagOpen() && (tagResults().filter(notSelected).length > 0 || showCreateTag())}>
                      <div style={{ position: 'absolute', top: 'calc(100% + 4px)', left: '0', right: '0', 'z-index': '30', background: 'var(--bg2)', border: '1px solid var(--border)', 'border-radius': '10px', 'box-shadow': '0 12px 30px rgba(0,0,0,0.35)', 'max-height': '240px', 'overflow-y': 'auto', padding: '5px' }}>
                        <For each={tagResults().filter(notSelected)}>{tag => (
                          <button onMouseDown={e => e.preventDefault()} onClick={() => addSelectedTag(tag)}
                            style={{ display: 'flex', 'align-items': 'center', gap: '8px', width: '100%', 'text-align': 'left', padding: '7px 9px', background: 'none', border: 'none', 'border-radius': '7px', cursor: 'pointer', ...sansFont, 'font-size': '13px', color: 'var(--text2)' }}>
                            <span style={{ width: '10px', height: '10px', 'border-radius': '3px', background: tag.color, 'flex-shrink': '0' }} />
                            {tag.name}
                          </button>
                        )}</For>
                        <Show when={showCreateTag()}>
                          <div style={{ display: 'flex', 'align-items': 'center', gap: '8px', padding: '7px 9px', 'border-top': tagResults().filter(notSelected).length > 0 ? '1px solid var(--border)' : 'none' }}>
                            <div style={{ display: 'flex', gap: '3px', 'flex-shrink': '0' }}>
                              <For each={TAG_COLOR_PRESETS.slice(0, 7)}>{color => (
                                <div onMouseDown={e => e.preventDefault()} onClick={() => setNewTagColor(color)} style={{ width: '18px', height: '18px', 'border-radius': '4px', background: color, cursor: 'pointer', border: newTagColor() === color ? '2px solid #fff' : '2px solid transparent' }} />
                              )}</For>
                            </div>
                            <button onMouseDown={e => e.preventDefault()} onClick={createNewTag} disabled={isCreatingTag()}
                              style={{ 'flex-shrink': '0', 'max-width': '100%', padding: '5px 11px', background: 'var(--accent)', border: 'none', 'border-radius': '7px', color: '#fff', 'font-size': '13px', cursor: 'pointer', ...sansFont, opacity: isCreatingTag() ? '0.6' : '1', overflow: 'hidden', 'text-overflow': 'ellipsis', 'white-space': 'nowrap' }}>
                              {isCreatingTag() ? '…' : translate('tag_create', { name: tagQuery().trim() })}
                            </button>
                          </div>
                        </Show>
                      </div>
                    </Show>
                  </div>
                </div>

                {/* Edit actions */}
                <div style={{ display: 'flex', gap: '11px', 'margin-top': '20px', 'justify-content': 'flex-end', 'align-items': 'center' }}>
                  <button onClick={() => guardClose(exitEditMode)}
                    style={{ padding: '9px 20px', background: 'var(--surface)', border: '1px solid var(--border)', 'border-radius': '10px', color: 'var(--text3)', ...sansFont, 'font-size': '14px', cursor: 'pointer' }}>
                    {translate('btn_cancel')}
                  </button>
                  <button onClick={saveDesign} disabled={isSaving()}
                    style={{ padding: '9px 26px', background: isSaving() ? 'var(--bg4)' : 'var(--accent)', border: 'none', 'border-radius': '10px', color: '#fff', ...sansFont, 'font-size': '14px', 'font-weight': '700', cursor: isSaving() ? 'not-allowed' : 'pointer' }}>
                    {isSaving() ? translate('btn_saving') : translate('btn_save')}
                  </button>
                </div>
              </div>
            </Show>

            {/* ── Tab bar ── */}
            <div style={{ display: 'flex', 'border-bottom': '1px solid var(--border)', 'margin-bottom': '26px' }}>
              {(['details', 'files', ...(currentPrintEntries().length ? ['gcode'] : []), 'notes', 'share'] as ('details' | 'files' | 'gcode' | 'notes' | 'share')[]).map(tab => (
                <button onClick={() => setActiveTab(tab)}
                  style={{ padding: '11px 22px', background: 'none', border: 'none', 'border-bottom': activeTab() === tab ? '2px solid var(--accent)' : '2px solid transparent', color: activeTab() === tab ? 'var(--accent-light)' : 'var(--muted)', ...sansFont, 'font-size': '14px', 'font-weight': activeTab() === tab ? '600' : '400', cursor: 'pointer' }}>
                  {tab === 'details' ? translate('tab_details')
                    : tab === 'files' ? `${translate('tab_files')} (${(() => {
        const cur = fileVersions().find(v => v.is_current) || fileVersions()[0]
        return cur?.entries?.length ?? cur?.file_count ?? fileVersions().length
      })()})`
                    : tab === 'gcode' ? translate('label_print_settings')
                    : tab === 'notes' ? translate('tab_notes')
                    : translate('tab_share')}
                </button>
              ))}
            </div>

            {/* ── Details tab ── */}
            <Show when={activeTab() === 'details'}>
              <Show when={shownDescription()} fallback={
                <div style={{ ...sansFont, 'font-size': '14px', color: 'var(--muted)', 'font-style': 'italic' }}>{translate('label_no_description')}</div>
              }>
                <div style={{ background: 'var(--bg2)', border: '1px solid var(--border)', 'border-radius': '14px', padding: '22px' }}>
                  <div style={labelStyle}>{translate('field_description')}</div>
                  <style>{descriptionCss}</style>
                  <div class="design-description" style={{ ...sansFont, 'font-size': '14px', color: 'var(--text2)', 'line-height': '1.75', 'margin-top': '9px', 'overflow': 'hidden' }}
                    ref={descriptionElement => createEffect(() => descriptionElement.replaceChildren(descriptionFragment()))} />
                </div>
              </Show>
            </Show>

            {/* ── Files tab ── */}
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
                          {(() => { const date = new Date(fileVersion.created_at); return `${String(date.getDate()).padStart(2,'0')}.${String(date.getMonth()+1).padStart(2,'0')}.${date.getFullYear()}` })()}
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
                          <label title={translate('btn_add_files_title')} onClick={e => e.stopPropagation()}
                            style={{ padding: '6px 12px', background: 'var(--bg4)', border: '1px solid var(--border2)', 'border-radius': '8px', color: 'var(--text)', 'font-size': '12px', cursor: 'pointer', ...monoFont, display: 'flex', 'align-items': 'center', gap: '5px' }}>
                            <input type="file" multiple accept={FILE_ACCEPT} style={{ display: 'none' }}
                              onChange={e => { addFilesToVersion(fileVersion.id, e.currentTarget.files); e.currentTarget.value = '' }} />
                            ＋ {translate('btn_add_files')}
                          </label>
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

                {/* Upload new version */}
                <Show when={!props.isReadOnly}>
                  <div style={{ background: 'var(--bg2)', border: '1px solid var(--border)', 'border-radius': '16px', padding: '22px' }}>
                    <div style={{ ...monoFont, 'font-size': '11px', color: 'var(--muted)', 'text-transform': 'uppercase', 'letter-spacing': '0.08em', 'margin-bottom': '16px' }}>{translate('label_upload_new_version')}</div>
                    <ErrorBox message={uploadError()} />
                    <div style={{ display: 'flex', 'flex-direction': 'column', gap: '13px' }}>
                      <div onClick={() => fileInputRef?.click()}
                        style={{ border: `2px dashed ${uploadFileObject().length ? 'var(--accent)' : 'var(--border2)'}`, 'border-radius': '11px', padding: '22px', 'text-align': 'center', cursor: 'pointer', background: uploadFileObject().length ? 'rgba(69,123,157,0.05)' : 'transparent' }}>
                        <input ref={fileInputRef} type="file" multiple accept={FILE_ACCEPT} style={{ display: 'none' }}
                          onChange={e => setUploadFileObject(Array.from(e.currentTarget.files ?? []))} />
                        <Show when={uploadFileObject().length} fallback={
                          <div style={{ ...monoFont, 'font-size': '13px', color: 'var(--muted)', display: 'flex', 'align-items': 'center', gap: '7px', 'justify-content': 'center' }}>
                            <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z"/></svg>
                            {translate('upload_file_label')}
                          </div>
                        }>
                          <div style={{ ...sansFont, 'font-size': '14px', color: 'var(--accent)', 'font-weight': '600' }}>
                            ✓ {uploadFileObject().length === 1
                                ? `${uploadFileObject()[0].name} (${formatBytes(uploadFileObject()[0].size)})`
                                : translate('label_files_selected').replace('{count}', String(uploadFileObject().length))}
                          </div>
                        </Show>
                      </div>
                      <div style={{ display: 'grid', 'grid-template-columns': '1fr 2fr', gap: '11px' }}>
                        <div><label style={labelStyle}>{translate('field_version')} *</label><input style={inputStyle} value={uploadVersion()} onInput={e => setUploadVersion(e.currentTarget.value)} placeholder={nextVersion()} /></div>
                        <div><label style={labelStyle}>{translate('field_notes')}</label><input style={inputStyle} value={uploadNotes()} onInput={e => setUploadNotes(e.currentTarget.value)} placeholder="optional" /></div>
                      </div>
                      <button onClick={uploadDesignFile} disabled={isUploading() || !uploadFileObject().length}
                        style={{ padding: '11px', background: (isUploading() || !uploadFileObject().length) ? 'var(--bg4)' : 'var(--accent)', border: (isUploading() || !uploadFileObject().length) ? '1px solid var(--border2)' : 'none', 'border-radius': '10px', color: (isUploading() || !uploadFileObject().length) ? 'var(--muted)' : '#fff', ...sansFont, 'font-size': '14px', 'font-weight': '700', cursor: (isUploading() || !uploadFileObject().length) ? 'not-allowed' : 'pointer' }}>
                        {isUploading() ? translate('btn_uploading') : translate('btn_upload')}
                      </button>
                    </div>
                  </div>
                </Show>
              </div>
            </Show>

            {/* ── Print settings tab - one section per sliced file of the current version ── */}
            <Show when={activeTab() === 'gcode'}>
              <div style={{ display: 'flex', 'flex-direction': 'column', gap: '18px' }}>
                <For each={currentPrintEntries()}>
                  {entry => {
                    const meta = entry.gcode_meta!
                    return (
                      <div style={{ background: 'var(--bg2)', border: '1px solid var(--border)', 'border-radius': '16px', padding: '22px' }}>
                        {/* The filename heads every section, a single one included: without
                            it the values float free and nothing says which file they
                            belong to. */}
                        <div>
                          <div style={{ display: 'flex', 'align-items': 'center', gap: '8px', 'margin-bottom': '16px', 'padding-bottom': '14px', 'border-bottom': '1px solid var(--border)' }}>
                            <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="var(--accent-light)" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><polyline points="14 2 14 8 20 8"/></svg>
                            <span style={{ ...monoFont, 'font-size': '13px', 'font-weight': '600', color: 'var(--text2)', 'word-break': 'break-all' }}>{entry.filename}</span>
                          </div>
                        </div>
                        <div style={{ display: 'grid', 'grid-template-columns': 'repeat(auto-fill, minmax(190px, 1fr))', gap: '12px' }}>
                          {printFields(meta).filter(item => item.value).map(item => (
                            <div style={{ background: 'var(--surface)', border: '1px solid var(--border)', 'border-radius': '11px', padding: '11px 15px' }}>
                              <div style={{ ...monoFont, 'font-size': '10px', color: 'var(--muted)', 'margin-bottom': '5px', 'text-transform': 'uppercase', 'letter-spacing': '0.05em' }}>{item.label}</div>
                              <div style={{ ...sansFont, 'font-size': '14px', color: 'var(--text2)', 'font-weight': '500' }}>{item.value}</div>
                            </div>
                          ))}
                        </div>
                      </div>
                    )
                  }}
                </For>
              </div>
            </Show>

            {/* ── Notes tab ── */}
            <Show when={activeTab() === 'notes'}>
              <div style={{ background: 'var(--bg2)', border: '1px solid var(--border)', 'border-radius': '16px', padding: '22px' }}>
                <Show when={!props.isReadOnly} fallback={
                  <p style={{ ...sansFont, 'font-size': '14px', color: 'var(--text2)', 'line-height': '1.7' }}>{design()!.notes || translate('notes_no_notes')}</p>
                }>
                  <textarea rows={10} value={editForm().notes} onInput={e => setEditForm(f => ({...f, notes: e.currentTarget.value}))}
                    placeholder={translate('notes_placeholder')}
                    style={{...inputStyle, resize: 'vertical', 'min-height': '220px', 'line-height': '1.65'}} />
                  <div style={{ 'margin-top': '13px', display: 'flex', 'justify-content': 'flex-end' }}>
                    <button onClick={async () => { await api.updateDesign(props.designId, { notes: editForm().notes }); props.showToast(translate('toast_design_saved')); loadDesign() }}
                      style={{ padding: '9px 22px', background: 'var(--accent)', border: 'none', 'border-radius': '10px', color: '#fff', ...sansFont, 'font-size': '14px', 'font-weight': '700', cursor: 'pointer' }}>
                      {translate('btn_save_notes')}
                    </button>
                  </div>
                </Show>
              </div>
            </Show>

            {/* ── Share tab ── */}
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
          </div>
        </Show>
      }>
        {/* Loading spinner */}
        <div style={{ display: 'flex', 'justify-content': 'center', 'margin-top': '90px' }}>
          <div style={{ width: '36px', height: '36px', border: '3px solid var(--border)', 'border-top': '3px solid var(--accent)', 'border-radius': '50%', 'animation': 'spin 0.8s linear infinite' }} />
        </div>
      </Show>

      {/* 3D Viewer */}
      <Show when={stlViewUrl()}>
        <StlViewerModal url={stlViewUrl()!} filename={stlViewFilename()} designName={stlViewName()} zUp={stlViewZUp()}
          onSaveImage={async (blob) => {
            const n = await uploadDesignImage([new File([blob], `${(stlViewName() || 'modell').replace(/[^\w.-]+/g, '_')}-foto.png`, { type: 'image/png' })])
            // The upload helper swallows errors itself (toast); without re-raising, the viewer would
            // acknowledge a failure as "added".
            if (n === 0) throw new Error('upload failed')
          }}
          onClose={() => {
          setStlViewUrl(null)
          // Drop a ?viewer= deep link so a reload doesn't reopen the viewer
          if (new URLSearchParams(window.location.search).has('viewer')) {
            history.replaceState(null, '', `${window.location.pathname}?design=${props.designId}`)
          }
        }} />
      </Show>

      {/* Design delete confirmation modal */}
      <Show when={confirmDelete()}>
        <div style={{ position: 'fixed', inset: '0', background: 'rgba(0,0,0,0.65)', display: 'flex', 'align-items': 'center', 'justify-content': 'center', 'z-index': '600', 'backdrop-filter': 'blur(5px)' }}
          onClick={() => setConfirmDelete(false)}>
          <div onClick={e => e.stopPropagation()}
            style={{ background: 'var(--bg2)', 'border-radius': '18px', padding: '28px 32px', width: '420px', border: '1px solid var(--border)', 'box-shadow': '0 24px 64px rgba(0,0,0,0.5)' }}>
            <div style={{ ...sansFont, 'font-size': '18px', 'font-weight': '700', color: 'var(--text)', 'margin-bottom': '10px' }}>{translate('btn_delete_design')}</div>
            <div style={{ ...sansFont, 'font-size': '14px', color: 'var(--text2)', 'margin-bottom': '24px', 'line-height': '1.6' }}
              innerHTML={translate('confirm_delete_design_body').replace('{name}', design() ? escapeHtml(displayName(design()!, lang(), translateDesigns())) : '')} />
            <Show when={design()?.source_url}>
              <label style={{ display: 'flex', 'align-items': 'flex-start', gap: '9px', cursor: 'pointer', 'margin': '-12px 0 20px',
                ...sansFont, 'font-size': '13px', color: 'var(--text2)', 'line-height': '1.5' }}>
                <input type="checkbox" checked={excludeFromSync()}
                  onChange={e => setExcludeFromSync(e.currentTarget.checked)}
                  style={{ 'margin-top': '2px', cursor: 'pointer', 'accent-color': 'var(--accent)' }} />
                <span>{translate('delete_exclude_from_sync')}</span>
              </label>
            </Show>
            <div style={{ display: 'flex', gap: '11px', 'justify-content': 'flex-end' }}>
              <button onClick={() => setConfirmDelete(false)}
                style={{ padding: '9px 20px', background: 'var(--surface)', border: '1px solid var(--border)', 'border-radius': '10px', color: 'var(--muted)', 'font-size': '14px', cursor: 'pointer', ...sansFont }}>
                {translate('btn_cancel')}
              </button>
              <button onClick={deleteDesign} disabled={isDeleting()}
                style={{ padding: '9px 20px', background: 'var(--danger-bg)', border: '1px solid var(--danger)', 'border-radius': '10px', color: 'var(--danger)', 'font-size': '14px', 'font-weight': '700', cursor: 'pointer', ...sansFont }}>
                {isDeleting() ? '…' : translate('btn_confirm_delete')}
              </button>
            </div>
          </div>
        </div>
      </Show>

      {/* File version delete confirmation modal */}
      <Show when={confirmDeleteFileId() !== null}>
        <div style={{ position: 'fixed', inset: '0', background: 'rgba(0,0,0,0.65)', display: 'flex', 'align-items': 'center', 'justify-content': 'center', 'z-index': '600', 'backdrop-filter': 'blur(5px)' }}
          onClick={() => setConfirmDeleteFileId(null)}>
          <div onClick={e => e.stopPropagation()}
            style={{ background: 'var(--bg2)', 'border-radius': '18px', padding: '28px 32px', width: '420px', border: '1px solid var(--border)', 'box-shadow': '0 24px 64px rgba(0,0,0,0.5)' }}>
            <div style={{ ...sansFont, 'font-size': '18px', 'font-weight': '700', color: 'var(--text)', 'margin-bottom': '10px' }}>{translate('delete_file_version_title')}</div>
            <div style={{ ...sansFont, 'font-size': '14px', color: 'var(--text2)', 'margin-bottom': '24px', 'line-height': '1.6' }}>
              {translate('delete_file_version_body')}
            </div>
            <div style={{ display: 'flex', gap: '11px', 'justify-content': 'flex-end' }}>
              <button onClick={() => setConfirmDeleteFileId(null)}
                style={{ padding: '9px 20px', background: 'var(--surface)', border: '1px solid var(--border)', 'border-radius': '10px', color: 'var(--muted)', 'font-size': '14px', cursor: 'pointer', ...sansFont }}>
                {translate('btn_cancel')}
              </button>
              <button onClick={async () => {
                const id = confirmDeleteFileId()
                if (id !== null) { await api.deleteFile(props.designId, id); setConfirmDeleteFileId(null); loadFiles() }
              }}
                style={{ padding: '9px 20px', background: 'var(--danger-bg)', border: '1px solid var(--danger)', 'border-radius': '10px', color: 'var(--danger)', 'font-size': '14px', 'font-weight': '700', cursor: 'pointer', ...sansFont }}>
                {translate('btn_confirm_delete')}
              </button>
            </div>
          </div>
        </div>
      </Show>

      {/* Gallery image delete confirmation modal (staged - applied on Save) */}
      <Show when={confirmDeleteImageId() !== null}>
        <div style={{ position: 'fixed', inset: '0', background: 'rgba(0,0,0,0.65)', display: 'flex', 'align-items': 'center', 'justify-content': 'center', 'z-index': '600', 'backdrop-filter': 'blur(5px)' }}
          onClick={() => setConfirmDeleteImageId(null)}>
          <div onClick={e => e.stopPropagation()}
            style={{ background: 'var(--bg2)', 'border-radius': '18px', padding: '28px 32px', width: '420px', border: '1px solid var(--border)', 'box-shadow': '0 24px 64px rgba(0,0,0,0.5)' }}>
            <div style={{ ...sansFont, 'font-size': '18px', 'font-weight': '700', color: 'var(--text)', 'margin-bottom': '10px' }}>{translate('delete_image_title')}</div>
            <div style={{ ...sansFont, 'font-size': '14px', color: 'var(--text2)', 'margin-bottom': '24px', 'line-height': '1.6' }}>
              {translate('delete_image_body')}
            </div>
            <div style={{ display: 'flex', gap: '11px', 'justify-content': 'flex-end' }}>
              <button onClick={() => setConfirmDeleteImageId(null)}
                style={{ padding: '9px 20px', background: 'var(--surface)', border: '1px solid var(--border)', 'border-radius': '10px', color: 'var(--muted)', 'font-size': '14px', cursor: 'pointer', ...sansFont }}>
                {translate('btn_cancel')}
              </button>
              <button onClick={() => { const id = confirmDeleteImageId(); if (id !== null) toggleDeleteImage(id); setConfirmDeleteImageId(null) }}
                style={{ padding: '9px 20px', background: 'var(--danger-bg)', border: '1px solid var(--danger)', 'border-radius': '10px', color: 'var(--danger)', 'font-size': '14px', 'font-weight': '700', cursor: 'pointer', ...sansFont }}>
                {translate('btn_mark_for_deletion')}
              </button>
            </div>
          </div>
        </div>
      </Show>

    </div>
  )
}
