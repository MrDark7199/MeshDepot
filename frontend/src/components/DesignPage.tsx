import { createSignal, createEffect, onCleanup, onMount, Show, For } from 'solid-js'
import { api } from '../services/api'
import { useAuth } from '../services/AuthContext'
import { useI18n } from '../i18n/index'
import { useUnsavedChanges } from '../utils/unsavedChanges'
import { StlViewerModal } from './StlViewer'
import { is3dFile } from '../utils/meshTools'
import { displayName, displayDescription } from '../utils/designText'
import { formatDate, formatDateTime } from '../utils/datetime'
import { ToggleSwitch } from './ToggleSwitch'
import { UploadFilesModal } from './design/UploadFilesModal'
import { escapeHtml } from '../utils/sanitizeHtml'
import { parseChoices, serialiseChoices } from '../utils/customFieldValue'
import { errorKey } from '../utils/errorMessage'
import type { CustomField, Design, DesignFile, DesignFileEntry, DesignID, DesignImage, Tag, Collection, DesignShare, GcodeMeta, ShareLink } from '../types'
import { PLATFORM_COLORS, platformLabel } from '../constants/platforms'
import { buildDescriptionFragment, descriptionCss } from '../utils/description'
import { browserHandlesClick, gridHref } from '../utils/navlink'
import { sansFont, monoFont, labelStyle, inputStyle, TAG_COLOR_PRESETS } from '../styles/formStyles'
import { GCODE_FORMATS, FDM_JOB_FORMATS, RESIN_FORMATS, RESIN_VIEWER_FORMATS, SLICER_FORMATS, SLICERS, lowerExt } from '../constants/fileFormats'
import { isResolvableHost, normalizeSourceUrl } from '../utils/sourceUrl'
import { formatBytes, formatPrintTime } from '../utils/format'
import { StarRating } from './StarRating'
import { SlicerButtons } from './SlicerButtons'
import { ShareLinkSection } from './ShareLinkSection'
import { ErrorBox } from './ErrorBox'
import { designBreadcrumb, type DesignBreadcrumbDeps } from '../app/designBreadcrumb'
import { designSyncBanner, type DesignSyncBannerDeps } from '../app/designSyncBanner'
import { designFilesTab, type DesignFilesTabDeps } from '../app/designFilesTab'
import { designShareTab, type DesignShareTabDeps } from '../app/designShareTab'

export interface DesignPageProps {
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
  syncError?:           string
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
  /** Version the viewer was opened from - where its split tool writes the parts back to. */
  const [stlViewVersionId, setStlViewVersionId] = createSignal<number | null>(null)
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

  const customFields = () => (design()?.custom_fields ?? []) as CustomField[]

  const enterEditMode = () => {
    setCustomValues(Object.fromEntries(customFields()
      .filter(field => field.present)
      .map(field => [String(field.id), field.value ?? ''])))
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
  // Off by default. Excluding a design from the sync outlives the deletion - the
  // platform keeps it and MeshDepot deliberately does not fetch it again - and a
  // lasting decision should be taken on purpose, not by leaving a switch alone.
  const [excludeFromSync, setExcludeFromSync] = createSignal(false)

  // syncTick: reload design+files when a background sync finishes
  createEffect(() => {
    const tick = props.syncTick
    if (tick && tick > 0) { loadDesign(); loadFiles() }
  })

  const [confirmDeleteFileId, setConfirmDeleteFileId] = createSignal<number | null>(null)
  const [uploadVersionOpen, setUploadVersionOpen] = createSignal(false)
  // The fields this design carries while editing, keyed by field id as a string -
  // which is how they travel to the server as well. A field missing from here is
  // taken off the design; that is why it is the complete set rather than a patch.
  const [customValues, setCustomValues] = createSignal<Record<string, string>>({})
  const [addFilesToVersionId, setAddFilesToVersionId] = createSignal<number | null>(null)
  const [pendingFileEntryDelete, setPendingFileEntryDelete] =
    createSignal<{ fileVersionId: number; entry: DesignFileEntry } | null>(null)

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
    setStlViewVersionId(fileVersionId)
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

  // - Actions ---------------------------------

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
      await api.updateDesign(props.designId, { ...editForm(), source_url: sourceUrl, custom_values: customValues() })
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
  const uploadDesignFile = async (files?: File[]) => {
    if (files) setUploadFileObject(files)
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
      setUploadVersionOpen(false)
      loadFiles() // belegt uploadVersion neu mit nextVersion()
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : 'error.internal'
      setUploadError(t(message) !== message ? t(message) : message)
    } finally { setIsUploading(false) }
  }

  /** Appends one or more files to an existing version. */
  const addFilesToVersion = async (fileVersionId: number, files: File[]) => {
    if (files.length === 0) return
    try {
      const formData = new FormData()
      for (const f of files) formData.append('file', f)
      await api.addEntries(props.designId, fileVersionId, formData)
      props.showToast(translate('toast_file_uploaded'))
      setAddFilesToVersionId(null)
      loadFiles()
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : 'error.internal'
      props.showToast(t(message) !== message ? t(message) : message, 'error')
    }
  }

  /** Asks first: the modal calls performFileEntryDelete once it is confirmed. */
  const deleteFileEntry = (fileVersionId: number, entry: DesignFileEntry) => {
    setPendingFileEntryDelete({ fileVersionId, entry })
  }

  const performFileEntryDelete = async (fileVersionId: number, entry: DesignFileEntry) => {
    setPendingFileEntryDelete(null)
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

  const setFileInputRef = (element: HTMLInputElement) => { fileInputRef = element }

  const designBreadcrumbDeps: DesignBreadcrumbDeps = {
    props, translate, user, design, shownName, isLoading, isEditing, enterEditMode, exitEditMode,
    openUploadVersion: () => { setUploadError(''); setUploadVersionOpen(true) },
    guardClose, setConfirmDelete, startSyncStream,
  }

  const designSyncBannerDeps: DesignSyncBannerDeps = { props, translate }

  const designFilesTabDeps: DesignFilesTabDeps = {
    props, translate, lang, user, design, activeTab, fileVersions, isLoadingFiles, expandedVersionIds,
    setExpandedVersionIds, collapsedFolders, setCollapsedFolders, deleteFileEntry, setConfirmDeleteFileId,
    openAddFiles: (fileVersionId: number) => setAddFilesToVersionId(fileVersionId),
    showEntryInViewer, isGcodeFile, isResinFile, isResinViewable, gcodeSummary,
  }

  const designShareTabDeps: DesignShareTabDeps = {
    props, translate, lang, design, activeTab, shareOpen, setShareOpen, shareEmailInput,
    setShareEmailInput, shareUserSuggestions, loadShareUserSuggestions, sharePicked, setSharePicked,
    pickShareUser, shareWithUsers, unshareUser, isSharing, shareLinks, createShareLink,
    deleteShareLink, copyShareLink, copiedToken, creatingLink, linkDays, setLinkDays,
  }


  return (
    <div style={{ 'min-height': '100vh', background: 'var(--bg)', 'padding-bottom': '70px' }}>
      <style>{`@keyframes spin{to{transform:rotate(360deg)}}`}</style>

      {designBreadcrumb(designBreadcrumbDeps)}

      {designSyncBanner(designSyncBannerDeps)}

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

            {/* - Main 2-column layout - */}
            <div class="stlv-detail-grid" style={{ display: 'grid', 'grid-template-columns': '420px 1fr', gap: '32px', 'margin-bottom': '32px' }}>

              {/* -- LEFT: Image gallery -- */}
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
                        setStlViewVersionId(cv.id)
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

              {/* -- RIGHT: Metadata panel -- */}
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
                    { label: translate('label_updated'),      value: design()!.updated_at ? formatDateTime(design()!.updated_at!, lang()) : null },
                  ] as const).filter(item => item.value).map(item => (
                    <div style={{ background: 'var(--surface)', border: '1px solid var(--border)', 'border-radius': '11px', padding: '11px 15px' }}>
                      <div style={{ ...monoFont, 'font-size': '10px', color: 'var(--muted)', 'margin-bottom': '5px', 'text-transform': 'uppercase', 'letter-spacing': '0.05em' }}>{item.label}</div>
                      <div style={{ ...sansFont, 'font-size': '14px', color: 'var(--text2)', 'font-weight': '500' }}>{item.value}</div>
                    </div>
                  ))}
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
                {/* Fields of the reader's own, at the end of the panel. Hidden when
                    none has a value, so a design without them looks as before. */}
                <Show when={customFields().some(field => field.present)}>
                  <div style={{ 'border-top': '1px solid var(--border)', 'padding-top': '16px' }}>
                    <div style={{ ...monoFont, 'font-size': '11px', color: 'var(--muted)', 'text-transform': 'uppercase', 'letter-spacing': '0.08em', 'margin-bottom': '12px' }}>
                      {translate('section_custom_fields')}
                    </div>
                    <div style={{ display: 'grid', 'grid-template-columns': '1fr 1fr', gap: '12px' }}>
                      <For each={customFields().filter(field => field.present)}>{field => (
                        <div style={{ 'grid-column': (field.value ?? '').length > 28 ? '1 / -1' : 'auto' }}>
                          <div style={labelStyle}>{field.name}</div>
                          <div style={{ ...sansFont, 'font-size': '13px', color: 'var(--text2)', 'word-break': 'break-word' }}>
                            {field.field_type === 'boolean'
                              ? translate(field.value === '1' || field.value === 'true' ? 'label_yes' : 'label_no')
                              : field.field_type === 'multiselect'
                                ? (parseChoices(field.value).join(', ') || '-')
                                : (field.value || '-')}
                          </div>
                        </div>
                      )}</For>
                    </div>
                  </div>
                </Show>

              </div>
            </div>

            {/* - Edit form - */}
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

                {/* The user's own fields, and only the ones put on this design: a field
                    that is not here is not on the design at all, so a yes/no does not
                    read as "no" on everything ever defined. */}
                <Show when={customFields().length > 0}>
                  <div style={{ 'grid-column': '1 / -1', 'border-top': '1px solid var(--border)', 'padding-top': '18px', 'margin-top': '20px' }}>
                    <div style={{ ...monoFont, 'font-size': '11px', color: 'var(--muted)', 'text-transform': 'uppercase', 'letter-spacing': '0.08em', 'margin-bottom': '14px' }}>
                      {translate('section_custom_fields')}
                    </div>
                    <div style={{ display: 'flex', 'flex-direction': 'column', gap: '14px' }}>
                      <For each={customFields().filter(field => String(field.id) in customValues())}>{field => {
                        const value = () => customValues()[String(field.id)] ?? ''
                        const set = (next: string) =>
                          setCustomValues(current => ({ ...current, [String(field.id)]: next }))
                        const drop = () => setCustomValues(current => {
                          const { [String(field.id)]: _removed, ...rest } = current
                          return rest
                        })
                        return (
                          <div>
                            <div style={{ display: 'flex', 'align-items': 'center', 'justify-content': 'space-between', gap: '10px' }}>
                              <label style={labelStyle}>{field.name}</label>
                              <button onClick={drop} title={translate('custom_field_remove_hint')}
                                style={{ padding: '3px 10px', background: 'var(--danger-bg)', border: '1px solid var(--danger-border)', 'border-radius': '7px', color: 'var(--danger)', 'font-size': '11px', cursor: 'pointer', ...sansFont, 'margin-bottom': '6px' }}>
                                {translate('btn_delete')}
                              </button>
                            </div>
                            <Show when={field.field_type === 'multiselect'}>
                              <div style={{ display: 'flex', 'flex-wrap': 'wrap', gap: '8px' }}>
                                <For each={field.options ?? []}>{choice => {
                                  const picked = () => parseChoices(value()).includes(choice)
                                  return (
                                    <button onClick={() => {
                                      const current = parseChoices(value())
                                      set(serialiseChoices(picked()
                                        ? current.filter(entry => entry !== choice)
                                        : [...current, choice]))
                                    }}
                                      style={{ padding: '5px 12px', 'border-radius': '8px', border: `1px solid ${picked() ? 'var(--accent)' : 'var(--border2)'}`, background: picked() ? 'rgba(69,123,157,0.18)' : 'var(--surface)', color: picked() ? 'var(--accent-light)' : 'var(--muted)', 'font-size': '12px', cursor: 'pointer', ...sansFont, 'font-weight': picked() ? '600' : '400' }}>
                                      {choice}
                                    </button>
                                  )
                                }}</For>
                              </div>
                            </Show>
                            <Show when={field.field_type !== 'multiselect'}>
                            <Show when={field.field_type === 'select'} fallback={
                              <Show when={field.field_type === 'boolean'} fallback={
                                <input style={inputStyle}
                                  type={field.field_type === 'int' || field.field_type === 'float' ? 'number' : 'text'}
                                  step={field.field_type === 'float' ? 'any' : '1'}
                                  placeholder={translate(`custom_field_type_${field.field_type}` as any)}
                                  value={value()} onInput={event => set(event.currentTarget.value)} />
                              }>
                                <div style={{ display: 'flex', 'align-items': 'center', gap: '10px', 'padding-top': '2px' }}>
                                  <ToggleSwitch checked={value() === '1'} onChange={on => set(on ? '1' : '0')} />
                                  <span style={{ ...sansFont, 'font-size': '13px', color: 'var(--text2)' }}>
                                    {translate(value() === '1' ? 'label_yes' : 'label_no')}
                                  </span>
                                </div>
                              </Show>
                            }>
                              <select style={inputStyle} value={value()} onChange={event => set(event.currentTarget.value)}>
                                <option value="">-</option>
                                <For each={field.options ?? []}>{choice => (
                                  <option value={choice}>{choice}</option>
                                )}</For>
                              </select>
                            </Show>
                            </Show>
                          </div>
                        )
                      }}</For>

                      <Show when={customFields().some(field => !(String(field.id) in customValues()))}>
                        <select style={{ ...inputStyle, 'align-self': 'flex-start', width: 'auto', 'min-width': '220px' }}
                          value=""
                          onChange={event => {
                            const chosen = customFields().find(field => String(field.id) === event.currentTarget.value)
                            event.currentTarget.value = ''
                            if (!chosen) return
                            // A yes/no starts at no, which is a value like any other now that
                            // the field is deliberately on this design.
                            setCustomValues(current => ({
                              ...current,
                              [String(chosen.id)]: chosen.field_type === 'boolean' ? '0' : '',
                            }))
                          }}>
                          <option value="">＋ {translate('custom_field_add_to_design')}</option>
                          <For each={customFields().filter(field => !(String(field.id) in customValues()))}>{field => (
                            <option value={String(field.id)}>{field.name}</option>
                          )}</For>
                        </select>
                      </Show>
                    </div>
                  </div>
                </Show>

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

            {/* A note is written to be seen again; behind a tab it was not. */}
            <Show when={design()?.notes?.trim()}>
              <div onClick={() => setActiveTab('notes')}
                style={{ display: 'flex', gap: '11px', 'align-items': 'flex-start', padding: '13px 16px', 'margin-bottom': '20px', background: 'var(--surface)', border: '1px solid var(--border)', 'border-left': '3px solid var(--accent)', 'border-radius': '10px', cursor: 'pointer' }}>
                <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="var(--muted)" stroke-width="2"
                  stroke-linecap="round" stroke-linejoin="round" style={{ 'flex-shrink': '0', 'margin-top': '2px' }}>
                  <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/>
                  <polyline points="14 2 14 8 20 8"/><line x1="8" y1="13" x2="16" y2="13"/>
                  <line x1="8" y1="17" x2="13" y2="17"/>
                </svg>
                <div style={{ 'min-width': '0' }}>
                  <div style={{ ...sansFont, 'font-size': '11px', 'font-weight': '700', color: 'var(--muted)', 'letter-spacing': '0.04em', 'text-transform': 'uppercase', 'margin-bottom': '3px' }}>
                    {translate('tab_notes')}
                  </div>
                  <div style={{ ...sansFont, 'font-size': '14px', color: 'var(--text2)', 'line-height': '1.6', 'white-space': 'pre-wrap', display: '-webkit-box', '-webkit-line-clamp': '3', '-webkit-box-orient': 'vertical', overflow: 'hidden' }}>
                    {design()!.notes}
                  </div>
                </div>
              </div>
            </Show>

            {/* - Tab bar - */}
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

            {/* - Details tab - */}
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

            {designFilesTab(designFilesTabDeps)}

            {/* - Print settings tab - one section per sliced file of the current version - */}
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

            {/* - Notes tab - */}
            <Show when={activeTab() === 'notes'}>
              <div style={{ background: 'var(--bg2)', border: '1px solid var(--border)', 'border-radius': '16px', padding: '22px' }}>
                <Show when={!props.isReadOnly} fallback={
                  <p style={{ ...sansFont, 'font-size': '14px', color: 'var(--text2)', 'line-height': '1.7' }}>{design()!.notes || translate('notes_no_notes')}</p>
                }>
                  <textarea rows={10} value={editForm().notes} onInput={e => setEditForm(f => ({...f, notes: e.currentTarget.value}))}
                    placeholder={translate('notes_placeholder')}
                    style={{...inputStyle, resize: 'vertical', 'min-height': '220px', 'line-height': '1.65'}} />
                  <div style={{ 'margin-top': '13px', display: 'flex', 'justify-content': 'flex-end' }}>
                    {/* Updates the loaded design in place instead of calling loadDesign():
                        that one flips isLoading and refetches everything, which threw the
                        whole detail view away to apply a field we just sent ourselves. */}
                    <button onClick={async () => {
                      const notes = editForm().notes
                      await api.updateDesign(props.designId, { notes })
                      setDesign(current => (current ? { ...current, notes } : current))
                      props.showToast(translate('toast_design_saved'))
                    }}
                      style={{ padding: '9px 22px', background: 'var(--accent)', border: 'none', 'border-radius': '10px', color: '#fff', ...sansFont, 'font-size': '14px', 'font-weight': '700', cursor: 'pointer' }}>
                      {translate('btn_save_notes')}
                    </button>
                  </div>
                </Show>
              </div>
            </Show>

            {designShareTab(designShareTabDeps)}
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
          onSaveFiles={async (files) => {
            // Lands in the version the viewer was opened from. The request has to
            // throw on failure: that is the only way the viewer learns of it.
            const versionId = stlViewVersionId()
            if (!versionId) throw new Error('no version')

            // Collisions are resolved here rather than in the viewer, because this
            // is the side that knows what the version already holds. A second run
            // of the split tool produces the same names as the first, and the
            // server rejects the whole batch on the first duplicate - so the parts
            // get the next free name instead of an error the user cannot act on.
            const taken = new Set(
              (fileVersions().find(version => version.id === versionId)?.entries ?? [])
                .map(entry => entry.filename.toLowerCase()),
            )
            const freeName = (filename: string) => {
              if (!taken.has(filename.toLowerCase())) { taken.add(filename.toLowerCase()); return filename }
              const dot = filename.lastIndexOf('.')
              const stem = dot > 0 ? filename.slice(0, dot) : filename
              const extension = dot > 0 ? filename.slice(dot) : ''
              for (let suffix = 2; ; suffix++) {
                const candidate = `${stem}-${suffix}${extension}`
                if (!taken.has(candidate.toLowerCase())) { taken.add(candidate.toLowerCase()); return candidate }
              }
            }

            const formData = new FormData()
            for (const file of files) formData.append('file', new File([file], freeName(file.name), { type: file.type }))
            await api.addEntries(props.designId, versionId, formData)
            props.showToast(translate('toast_file_uploaded'))
            loadFiles()
          }}
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
              <div style={{ display: 'flex', 'align-items': 'center', gap: '11px', 'margin': '-12px 0 20px',
                ...sansFont, 'font-size': '13px', color: 'var(--text2)', 'line-height': '1.5' }}>
                <ToggleSwitch checked={excludeFromSync()} onChange={setExcludeFromSync} />
                <span style={{ cursor: 'pointer' }} onClick={() => setExcludeFromSync(!excludeFromSync())}>
                  {translate('delete_exclude_from_sync')}
                </span>
              </div>
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
      <Show when={pendingFileEntryDelete()}>
        {pending => (
          <div style={{ position: 'fixed', inset: '0', background: 'rgba(0,0,0,0.65)', display: 'flex', 'align-items': 'center', 'justify-content': 'center', 'z-index': '600', 'backdrop-filter': 'blur(5px)' }}
            onClick={() => setPendingFileEntryDelete(null)}>
            <div onClick={e => e.stopPropagation()}
              style={{ background: 'var(--bg2)', 'border-radius': '18px', padding: '28px 32px', width: '420px', border: '1px solid var(--border)', 'box-shadow': '0 24px 64px rgba(0,0,0,0.5)' }}>
              <div style={{ ...sansFont, 'font-size': '18px', 'font-weight': '700', color: 'var(--text)', 'margin-bottom': '10px' }}>{translate('btn_delete')}</div>
              <div style={{ ...sansFont, 'font-size': '14px', color: 'var(--text2)', 'margin-bottom': '24px', 'line-height': '1.6', 'word-break': 'break-word' }}>
                {translate('confirm_delete_file').replace('{name}', pending().entry.filename)}
              </div>
              <div style={{ display: 'flex', gap: '11px', 'justify-content': 'flex-end' }}>
                <button onClick={() => setPendingFileEntryDelete(null)}
                  style={{ padding: '9px 20px', background: 'var(--surface)', border: '1px solid var(--border)', 'border-radius': '10px', color: 'var(--muted)', 'font-size': '14px', cursor: 'pointer', ...sansFont }}>
                  {translate('btn_cancel')}
                </button>
                <button onClick={() => performFileEntryDelete(pending().fileVersionId, pending().entry)}
                  style={{ padding: '9px 20px', background: 'var(--danger-bg)', border: '1px solid var(--danger)', 'border-radius': '10px', color: 'var(--danger)', 'font-size': '14px', 'font-weight': '700', cursor: 'pointer', ...sansFont }}>
                  {translate('btn_confirm_delete')}
                </button>
              </div>
            </div>
          </div>
        )}
      </Show>

      <Show when={uploadVersionOpen()}>
        <UploadFilesModal
          title={translate('label_upload_new_version')}
          withVersionFields
          version={uploadVersion()}
          onVersionInput={setUploadVersion}
          versionPlaceholder={nextVersion()}
          notes={uploadNotes()}
          onNotesInput={setUploadNotes}
          error={uploadError()}
          busy={isUploading()}
          onClose={() => setUploadVersionOpen(false)}
          onSubmit={files => uploadDesignFile(files)} />
      </Show>

      <Show when={addFilesToVersionId() !== null}>
        <UploadFilesModal
          title={translate('title_add_files_to_version')}
          takenNames={(fileVersions().find(version => version.id === addFilesToVersionId())?.entries ?? [])
            .map(entry => entry.filename)}
          onClose={() => setAddFilesToVersionId(null)}
          onSubmit={files => addFilesToVersion(addFilesToVersionId()!, files)} />
      </Show>

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
