import { createSignal, createEffect, onCleanup, onMount, Show, For } from 'solid-js'
import type { JSX } from 'solid-js'
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
import { PAGE_X } from '../constants/layout'
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
import { designEditScreen, type DesignEditScreenDeps } from '../app/designEditScreen'

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
  /**
   * The pieces of the navigation bar this page carries itself. It replaces that
   * bar rather than standing under it, so the way home and the two global
   * actions are handed in instead of being drawn twice.
   */
  chrome?: { logo: () => JSX.Element; globalActions: () => JSX.Element }
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
export interface DesignEditForm {
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

/** The form as the design stands on the server: nullable columns as '', the
 *  hidden flag as 0/1, which is what the PUT endpoint expects. */
function formFromDesign(loaded: Design): DesignEditForm {
  return {
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
  }
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
  /** The older fassung the viewer holds against the one on screen, if any. */
  const [compareUrl, setCompareUrl] = createSignal<string | null>(null)
  const [compareLabel, setCompareLabel] = createSignal('')
  const [baseLabel, setBaseLabel] = createSignal('')
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
  const [isSaving, setIsSaving] = createSignal(false)
  const [errorMessage, setErrorMessage] = createSignal('')
  const { resetDirty, guardClose, setShouldBlock } = useUnsavedChanges(translate('confirm_discard_changes'))
  let editSnapshot = ''
  let initialEditModeDone = false

  const editState = () => JSON.stringify({ form: editForm(), tags: selectedTagIds(), cols: designCollectionIds(), cover: pendingCoverId(), del: pendingDeleteImageIds(), fields: customValues(), added: pendingImages().length })

  const customFields = () => (design()?.custom_fields ?? []) as CustomField[]

  // What the lists looked like before the form touched them. Cancel puts them
  // back: the detail view reads the same signals, so a tag added and then
  // dropped would otherwise keep showing there.
  let tagsBeforeEdit: Tag[] = []
  let collectionsBeforeEdit: Collection[] = []

  const enterEditMode = () => {
    setCustomValues(Object.fromEntries(customFields()
      .filter(field => field.present)
      .map(field => [String(field.id), field.value ?? ''])))
    setPendingCoverId(null)
    setPendingDeleteImageIds([])
    clearPendingImages()
    setErrorMessage('')
    tagsBeforeEdit = selectedTags()
    collectionsBeforeEdit = selectedCollections()
    editSnapshot = editState()
    setShouldBlock(() => editState() !== editSnapshot)
    setIsEditing(true)
  }

  /**
   * Leaves the form and drops what it staged. A save passes false: what was
   * staged is the server's state by then, and the reload that follows brings it
   * back with real ids.
   */
  const exitEditMode = (restoreStaged = true) => {
    resetDirty()
    setPendingCoverId(null)
    setPendingDeleteImageIds([])
    clearPendingImages()
    if (restoreStaged) {
      // The typed fields go back as well. Without this a name, a note or the
      // visibility switch kept the abandoned value and stood there again the
      // next time the screen was opened, looking saved.
      const loaded = design()
      if (loaded) setEditForm(formFromDesign(loaded))
      setSelectedTags(tagsBeforeEdit)
      setSelectedCollections(collectionsBeforeEdit)
    }
    // The error box belongs to the form that was abandoned. It is shown on the
    // detail view as well, where "Please enter a name." stood over a design
    // that has one.
    setErrorMessage('')
    setIsEditing(false)
  }

  // Photos picked but never saved are only in memory; their preview URLs are
  // released rather than left behind.
  onCleanup(() => clearPendingImages())

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
  /** The version a new folder is being named for, and the name so far. */
  const [newFolderVersionId, setNewFolderVersionId] = createSignal<number | null>(null)
  const [newFolderName, setNewFolderName] = createSignal('')
  /** The folder waiting for the delete dialog, with what is in it. */
  const [folderToDelete, setFolderToDelete] =
    createSignal<{ versionId: number; folder: string; fileCount: number } | null>(null)
  /** Where the files of the "+ Files" dialog should land. */
  const [uploadFolder, setUploadFolder] = createSignal('')
  const [pendingFileEntryDelete, setPendingFileEntryDelete] =
    createSignal<{ fileVersionId: number; entry: DesignFileEntry } | null>(null)

  const [collapsedFolders, setCollapsedFolders] = createSignal<Set<string>>(new Set())
  // Which file-version cards are expanded. Newest (current) is expanded by default.
  const [expandedVersionIds, setExpandedVersionIds] = createSignal<Set<number>>(new Set())

  const [isUploadingImage, setIsUploadingImage] = createSignal(false)

  /**
   * Photos picked on the edit screen. They are kept as files with a preview URL
   * and only sent when Save is pressed, so leaving the screen takes them with
   * it instead of leaving them in the gallery.
   */
  // Each carries a provisional id, negative like a new tag's, so it can be
  // picked as the cover before it exists: the save maps that id to the row the
  // upload creates. Without it a freshly added design - which has no stored
  // image at all - offered no way to say which picture is the cover.
  const [pendingImages, setPendingImages] = createSignal<{ key: number; file: File; url: string }[]>([])
  const addPendingImages = (files: FileList | File[]) => {
    const picked = Array.from(files).filter(file => file.type.startsWith('image/'))
    if (picked.length === 0) return
    setPendingImages(current => [...current,
      ...picked.map(file => ({ key: takeProvisionalId(), file, url: URL.createObjectURL(file) }))])
  }
  const removePendingImage = (index: number) => setPendingImages(current => {
    const dropped = current[index]
    URL.revokeObjectURL(dropped.url)
    // A picture that is going away cannot stay the chosen cover.
    if (pendingCoverId() === dropped.key) setPendingCoverId(null)
    return current.filter((_, position) => position !== index)
  })
  const clearPendingImages = () => {
    for (const picked of pendingImages()) URL.revokeObjectURL(picked.url)
    setPendingImages([])
  }

  /**
   * Ids a tag or collection the user invented while editing carries until Save
   * creates it. Negative, so a real id is never mistaken for one.
   */
  let lastProvisionalId = 0
  const takeProvisionalId = () => --lastProvisionalId

  // Tags as a select2-style combobox: the picked ones are held as full objects
  // (chips), and matches are searched on the server rather than loaded up front.
  const [selectedTags, setSelectedTags] = createSignal<Tag[]>([])
  const selectedTagIds = () => selectedTags().map(t => t.id)
  const [tagQuery, setTagQuery] = createSignal('')
  const [tagResults, setTagResults] = createSignal<Tag[]>([])
  const [tagOpen, setTagOpen] = createSignal(false)
  const [newTagColor, setNewTagColor] = createSignal(TAG_COLOR_PRESETS[0])

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
  // The picker offers hidden collections too, so a design can still be put into
  // one. The read-only view further down keeps hiding them (it intersects
  // props.allCollections, which never contains hidden ones).
  const [pickerCollections, setPickerCollections] = createSignal<Collection[]>([])
  /** The collections the server has this design in - what the staged list is
   *  compared against when saving. */
  const [savedCollectionIds, setSavedCollectionIds] = createSignal<number[]>([])

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
      && !selectedCollections().some(c => c.name.toLowerCase() === q)
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
    // Reloading while the edit screen is open must leave it standing: uploading
    // an image reloads the design, and the spinner would tear the form down
    // mid-edit, form contents and scroll position with it.
    const quiet = isEditing()
    if (!quiet) setIsLoading(true)
    try {
      const response = await api.getDesign(props.designId)
      const loaded = response.data
      setDesign(loaded)
      setShowOriginal(false)
      // For the same reason the form is only filled from the server when nobody
      // is typing into it - otherwise an image upload discards the edits.
      if (!quiet) {
        setEditForm(formFromDesign(loaded))
        setSelectedTags(loaded.tags || [])
      }
      const [colResponse, pickerResponse]: [any, any] = await Promise.all([
        api.getDesignCollections(props.designId, true),
        api.getCollections(true),
      ])
      if (!quiet) {
        setSelectedCollections(colResponse.data)
        setSavedCollectionIds((colResponse.data || []).map((collection: Collection) => collection.id))
      }
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
    } finally { if (!quiet) setIsLoading(false) }
  }

  /**
   * Opens a file entry in the 3D viewer. A resin file (.pwmx) carries no
   * geometry, so what is loaded is the mesh the server rebuilt from the layer
   * stack, parsed as an STL (hence the "model.stl" format hint).
   */
  /**
   * Opens one file with its older fassung laid over it. Everything the viewer
   * does to a single model - splitting, measuring, photographing - is turned off
   * there; two models in one scene make those questions ambiguous.
   */
  const compareInViewer = (olderVersionId: number, olderEntry: DesignFileEntry,
                           newerVersionId: number, newerEntry: DesignFileEntry) => {
    const versionNumber = (id: number) => fileVersions().find(version => version.id === id)?.version ?? ''
    setCompareUrl(api.entryUrl(props.designId, olderVersionId, olderEntry.id))
    setCompareLabel('v' + versionNumber(olderVersionId))
    setBaseLabel('v' + versionNumber(newerVersionId))
    setStlViewUrl(api.entryUrl(props.designId, newerVersionId, newerEntry.id))
    setStlViewFilename(newerEntry.filename)
    setStlViewName(newerEntry.filename)
    setStlViewZUp(false)
    setStlViewVersionId(newerVersionId)
  }

  const showEntryInViewer = (fileVersionId: number, entry: DesignFileEntry) => {
    setCompareUrl(null)
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
        // The newest (current) version is expanded to begin with. What is open
        // stays open afterwards: every move of a file reloads the list, and
        // collapsing the card being worked in would close it under the hand.
        const newest = versions.find(v => v.is_current) || versions[0]
        setExpandedVersionIds(current => {
          const known = new Set(versions.map(version => version.id))
          const kept = new Set([...current].filter(id => known.has(id)))
          if (kept.size === 0 && newest) kept.add(newest.id)
          return kept
        })
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

      // Tags and collections the user invented exist only in the form until
      // here; they are created first, so the links below have real ids to use.
      const tagIds: number[] = []
      for (const tag of selectedTags()) {
        if (tag.id > 0) { tagIds.push(tag.id); continue }
        const created = await api.createTag({ name: tag.name, color: tag.color }) as { data: Tag }
        tagIds.push(created.data.id)
      }
      await api.setDesignTags(props.designId, tagIds)

      const collectionIds: number[] = []
      let collectionsChanged = false
      for (const collection of selectedCollections()) {
        if (collection.id > 0) { collectionIds.push(collection.id); continue }
        const created = await api.createCollection({ name: collection.name }) as { data: Collection }
        collectionIds.push(created.data.id)
        collectionsChanged = true
      }
      // Only the difference is written: the rest the design is already in.
      for (const id of collectionIds) {
        if (savedCollectionIds().includes(id)) continue
        await api.addToCollection(id, [props.designId])
        collectionsChanged = true
      }
      for (const id of savedCollectionIds()) {
        if (collectionIds.includes(id)) continue
        await api.removeFromCollection(id, props.designId)
        collectionsChanged = true
      }

      // Photos picked on the screen are sent only now, which is what lets
      // Cancel leave the gallery as it was. Each answer says which row it
      // created, so a cover chosen among them can be pointed at it below.
      const uploadedIds = new Map<number, number>()
      for (const picked of pendingImages()) {
        const formData = new FormData()
        formData.append('image', picked.file)
        const answer = await api.uploadImage(props.designId, formData) as { data?: { image_id?: number } }
        const newId = answer?.data?.image_id
        if (newId) uploadedIds.set(picked.key, newId)
      }

      // Staged image deletions (Galerie) - only now removed on the server.
      const toDelete = pendingDeleteImageIds()
      for (const id of toDelete) { try { await api.deleteImage(props.designId, id) } catch {} }
      if (toDelete.length) setActiveImageIndex(0)
      // Staged cover selection (Titelbild) - only now committed to the server.
      // A negative one names a picture from this same save, whose real id the
      // upload just handed back.
      const pendingCover = pendingCoverId()
      const coverId = pendingCover !== null && pendingCover < 0 ? uploadedIds.get(pendingCover) : pendingCover
      if (coverId) await api.setDesignImageCover(props.designId, coverId)
      props.showToast(translate('toast_design_saved'))
      props.onTagsChanged()
      if (collectionsChanged) props.onCollectionsChanged()
      exitEditMode(false)
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

  /** The folders of one version, for the pickers. */
  const foldersOfVersion = (fileVersionId: number | null) =>
    fileVersions().find(version => version.id === fileVersionId)?.folders ?? []

  const createFolder = async () => {
    const versionId = newFolderVersionId()
    const name = newFolderName().trim()
    if (versionId === null || !name) return
    try {
      await api.createFolder(props.designId, versionId, name)
      setNewFolderVersionId(null)
      setNewFolderName('')
      loadFiles()
    } catch (failure: unknown) {
      props.showToast(t(errorKey(failure)), 'error')
    }
  }

  /**
   * Moves one file into a folder of its version - dropped there, usually. With
   * an order, the folder is also arranged the way it was dropped; the move runs
   * first, so the file is in that folder by the time the order is written.
   */
  const moveEntryToFolder = async (fileVersionId: number, entryId: number, folder: string, order?: number[]) => {
    try {
      await api.moveEntry(props.designId, fileVersionId, entryId, folder)
      if (order && order.length > 0) await api.reorderEntries(props.designId, fileVersionId, order)
      loadFiles()
    } catch (failure: unknown) {
      props.showToast(t(errorKey(failure)), 'error')
    }
  }

  /** Arranges one folder's files, without any of them changing folder. */
  const reorderEntries = async (fileVersionId: number, entryIds: number[]) => {
    try {
      await api.reorderEntries(props.designId, fileVersionId, entryIds)
      loadFiles()
    } catch (failure: unknown) {
      props.showToast(t(errorKey(failure)), 'error')
    }
  }

  /** Removes a folder, with or without what is in it - the dialog asks which. */
  const removeFolder = async (deleteFiles: boolean) => {
    const pending = folderToDelete()
    if (!pending) return
    setFolderToDelete(null)
    try {
      await api.deleteFolder(props.designId, pending.versionId, pending.folder, deleteFiles)
      loadFiles()
      if (deleteFiles) loadDesign()
    } catch (failure: unknown) {
      props.showToast(t(errorKey(failure)), 'error')
    }
  }

  /** Appends one or more files to an existing version. */
  const addFilesToVersion = async (fileVersionId: number, files: File[]) => {
    if (files.length === 0) return
    try {
      const formData = new FormData()
      for (const f of files) formData.append('file', f)
      if (uploadFolder().trim()) formData.append('folder', uploadFolder().trim())
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

  /** Puts the design in a collection picked from the dropdown - staged, like
   *  everything else on the edit screen, and written when Save is pressed. */
  const addCollectionSel = (col: Collection) => {
    setColQuery(''); setColOpen(false)
    if (!notColSelected(col)) return
    setSelectedCollections(cs => [...cs, col])
  }

  /** Takes the design out of a collection (the chip's ×). Staged as well. */
  const removeCollectionSel = (id: number) => setSelectedCollections(cs => cs.filter(c => c.id !== id))

  /**
   * A collection named after what is in the search box. It does not exist yet:
   * it carries a provisional id until Save creates it, so cancelling leaves no
   * empty collection behind.
   */
  const createNewCollection = () => {
    const name = colQuery().trim()
    if (!name) return
    setSelectedCollections(cs => [...cs, { id: takeProvisionalId(), name }])
    setColQuery(''); setColOpen(false)
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
   * A tag named after what is in the search box, in the chosen colour. Like a
   * new collection it is only promised here and created on save, so a tag typed
   * and then abandoned does not end up in the library.
   */
  const createNewTag = () => {
    const name = tagQuery().trim()
    if (!name) return
    addSelectedTag({ id: takeProvisionalId(), name, color: newTagColor() })
  }

  const GRADIENT_FALLBACK = `linear-gradient(135deg, #1a1a2e, #16213e, #0f3460)`

  const setFileInputRef = (element: HTMLInputElement) => { fileInputRef = element }

  const designBreadcrumbDeps: DesignBreadcrumbDeps = {
    props, translate, design, shownName, isLoading, enterEditMode,
    openUploadVersion: () => { setUploadError(''); setUploadVersionOpen(true) },
    guardClose, setConfirmDelete, startSyncStream,
  }

  const designSyncBannerDeps: DesignSyncBannerDeps = { props, translate }

  const designFilesTabDeps: DesignFilesTabDeps = {
    props, translate, lang, user, design, activeTab, fileVersions, isLoadingFiles, expandedVersionIds,
    setExpandedVersionIds, collapsedFolders, setCollapsedFolders, deleteFileEntry, setConfirmDeleteFileId,
    openAddFiles: (fileVersionId: number) => { setUploadFolder(''); setAddFilesToVersionId(fileVersionId) },
    openNewFolder: (fileVersionId: number) => { setNewFolderName(''); setNewFolderVersionId(fileVersionId) },
    moveEntryToFolder,
    reorderEntries,
    askDeleteFolder: (fileVersionId: number, folder: string, fileCount: number) =>
      setFolderToDelete({ versionId: fileVersionId, folder, fileCount }),
    compareInViewer,
    showEntryInViewer, isGcodeFile, isResinFile, isResinViewable, gcodeSummary,
  }

  const designEditScreenDeps: DesignEditScreenDeps = {
    translate, logo: props.chrome?.logo, shownName, editForm, setEditForm, errorMessage, isSaving, saveDesign,
    cancelEdit: () => guardClose(exitEditMode),
    galleryImages, effectiveCoverId, stageCover, isStagedDelete, toggleDeleteImage,
    pendingImages, addPendingImages, removePendingImage,
    selectedTags, notSelected, addSelectedTag, removeSelectedTag, tagQuery, onTagInput, runTagSearch,
    tagResults, tagOpen, setTagOpen, onTagKeyDown, showCreateTag, createNewTag,
    newTagColor, setNewTagColor,
    selectedCollections, colResults, addCollectionSel, removeCollectionSel, colQuery, setColQuery,
    colOpen, setColOpen, onColKeyDown, showCreateCol, createNewCollection,
    customFields, customValues, setCustomValues,
  }

  const designShareTabDeps: DesignShareTabDeps = {
    props, translate, lang, design, activeTab, shareOpen, setShareOpen, shareEmailInput,
    setShareEmailInput, shareUserSuggestions, loadShareUserSuggestions, sharePicked, setSharePicked,
    pickShareUser, shareWithUsers, unshareUser, isSharing, shareLinks, createShareLink,
    deleteShareLink, copyShareLink, copiedToken, creatingLink, linkDays, setLinkDays,
  }


  // The room at the bottom is for the detail view, whose last tab should not end
  // flush with the window. The edit screen brings its own and would only be
  // pushed into a scrollbar by it.
  return (
    <div style={{ 'min-height': '100vh', background: 'var(--bg)', 'padding-bottom': isEditing() ? '0' : '70px' }}>
      <style>{`@keyframes spin{to{transform:rotate(360deg)}}`}</style>

      <Show when={!isEditing()}>{designBreadcrumb(designBreadcrumbDeps)}</Show>

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
          {/* Editing takes the whole page. The form used to sit below the detail
              view, which meant reading one layout while typing into another, with
              the tabs underneath showing what was not being edited. */}
          <Show when={isEditing() && !props.isReadOnly} fallback={
          <div style={{ padding: `32px ${PAGE_X}`, 'max-width': '1500px', margin: '0 auto' }}>
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

                </div>

                {/* Thumbnail strip */}
                <Show when={galleryImages().length > 1}>
                  <div style={{ display: 'flex', gap: '9px', 'margin-top': '11px', 'overflow-x': 'auto', 'padding-bottom': '4px' }}>
                    <For each={galleryImages()}>{(img, index) => (
                      <div onClick={() => setActiveImageIndex(index())}
                        style={{ width: '80px', height: '80px', 'flex-shrink': '0', 'border-radius': '10px', overflow: 'hidden', cursor: 'pointer', border: activeImageIndex() === index() ? '2px solid var(--accent)' : '2px solid var(--border)', 'transition': 'border 0.15s', position: 'relative' }}>
                        <img src={api.coverUrl(img.path)} alt="" style={{ width: '100%', height: '100%', 'object-fit': 'cover' }} />
                        {/* Which one is the cover. Choosing another happens on
                            the edit screen, so there is no button here. */}
                        <Show when={'id' in img && (img as any).id > 0 && effectiveCoverId() === (img as any).id}>
                          <div title={translate('label_cover')}
                            style={{ position: 'absolute', top: '3px', left: '3px', background: 'var(--accent)', 'border-radius': '4px', color: '#fff', width: '20px', height: '20px', display: 'flex', 'align-items': 'center', 'justify-content': 'center' }}>
                            <svg width="12" height="12" viewBox="0 0 24 24" fill="#fff" stroke="#fff" stroke-width="1"><polygon points="12 2 15.09 8.26 22 9.27 17 14.14 18.18 21.02 12 17.77 5.82 21.02 7 14.14 2 9.27 8.91 8.26 12 2"/></svg>
                          </div>
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
          }>
            {designEditScreen(designEditScreenDeps)}
          </Show>
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
          compareUrl={compareUrl() ?? undefined} compareLabel={compareLabel()} baseLabel={baseLabel()}
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
          setCompareUrl(null)
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
          folders={foldersOfVersion(addFilesToVersionId())}
          folder={uploadFolder()}
          onFolderChange={setUploadFolder}
          // Only the names of the chosen folder clash: the same name in two
          // folders is two files, on disk as in the ZIP export.
          takenNames={(fileVersions().find(version => version.id === addFilesToVersionId())?.entries ?? [])
            .filter(entry => {
              const path = entry.relative_path || entry.filename
              const folder = path.includes('/') ? path.slice(0, path.lastIndexOf('/')) : ''
              return folder === uploadFolder().trim()
            })
            .map(entry => entry.filename)}
          onClose={() => setAddFilesToVersionId(null)}
          onSubmit={files => addFilesToVersion(addFilesToVersionId()!, files)} />
      </Show>

      {/* Naming a new folder */}
      <Show when={newFolderVersionId() !== null}>
        <div style={{ position: 'fixed', inset: '0', background: 'rgba(0,0,0,0.65)', display: 'flex', 'align-items': 'center', 'justify-content': 'center', 'z-index': '600', 'backdrop-filter': 'blur(5px)' }}
          onClick={() => setNewFolderVersionId(null)}>
          <div onClick={e => e.stopPropagation()}
            style={{ background: 'var(--bg2)', 'border-radius': '18px', padding: '28px 32px', width: '420px', border: '1px solid var(--border)', 'box-shadow': '0 24px 64px rgba(0,0,0,0.5)' }}>
            <div style={{ ...sansFont, 'font-size': '18px', 'font-weight': '700', color: 'var(--text)', 'margin-bottom': '16px' }}>{translate('new_folder_title')}</div>
            <input style={inputStyle} value={newFolderName()} placeholder={translate('new_folder_placeholder')} autofocus
              onInput={e => setNewFolderName(e.currentTarget.value)}
              onKeyDown={(e: KeyboardEvent) => { if (e.key === 'Enter') createFolder() }} />
            <div style={{ display: 'flex', gap: '11px', 'justify-content': 'flex-end', 'margin-top': '22px' }}>
              <button onClick={() => setNewFolderVersionId(null)}
                style={{ padding: '9px 20px', background: 'var(--surface)', border: '1px solid var(--border)', 'border-radius': '10px', color: 'var(--muted)', 'font-size': '14px', cursor: 'pointer', ...sansFont }}>
                {translate('btn_cancel')}
              </button>
              <button onClick={createFolder} disabled={!newFolderName().trim()}
                style={{ padding: '9px 20px', background: newFolderName().trim() ? 'var(--accent)' : 'var(--bg4)', border: 'none', 'border-radius': '10px', color: newFolderName().trim() ? '#fff' : 'var(--muted)', 'font-size': '14px', 'font-weight': '700', cursor: newFolderName().trim() ? 'pointer' : 'not-allowed', ...sansFont }}>
                {translate('btn_create')}
              </button>
            </div>
          </div>
        </div>
      </Show>

      {/* Deleting a folder. Two ways out, because both are reasonable and only
          one of them can be undone - which is to say, neither, so the one that
          destroys nothing is the default. */}
      <Show when={folderToDelete()}>
        {pending => (
          /* No closing on a click beside it: the three answers differ in what they
             destroy, and the way out of that question is one of them, not a
             stray click. */
          <div style={{ position: 'fixed', inset: '0', background: 'rgba(0,0,0,0.65)', display: 'flex', 'align-items': 'center', 'justify-content': 'center', 'z-index': '600', 'backdrop-filter': 'blur(5px)' }}>
            <div style={{ background: 'var(--bg2)', 'border-radius': '18px', padding: '28px 32px', width: '720px', 'max-width': '94vw', border: '1px solid var(--border)', 'box-shadow': '0 24px 64px rgba(0,0,0,0.5)' }}>
              <div style={{ ...sansFont, 'font-size': '18px', 'font-weight': '700', color: 'var(--text)', 'margin-bottom': '10px' }}>
                {translate('delete_folder_title').replace('{name}', pending().folder)}
              </div>
              <div style={{ ...sansFont, 'font-size': '14px', color: 'var(--text2)', 'margin-bottom': '24px', 'line-height': '1.6' }}>
                <Show when={pending().fileCount > 0} fallback={translate('delete_folder_empty')}>
                  {translate('delete_folder_body').replace('{count}', String(pending().fileCount))}
                </Show>
              </div>
              <div style={{ display: 'flex', gap: '11px', 'justify-content': 'flex-end', 'flex-wrap': 'wrap' }}>
                <button onClick={() => setFolderToDelete(null)}
                  style={{ padding: '9px 20px', background: 'var(--surface)', border: '1px solid var(--border)', 'border-radius': '10px', color: 'var(--muted)', 'font-size': '14px', cursor: 'pointer', ...sansFont }}>
                  {translate('btn_cancel')}
                </button>
                <button onClick={() => removeFolder(false)}
                  style={{ padding: '9px 20px', background: 'var(--accent)', border: 'none', 'border-radius': '10px', color: '#fff', 'font-size': '14px', 'font-weight': '700', cursor: 'pointer', ...sansFont }}>
                  {pending().fileCount > 0 ? translate('btn_folder_keep_files') : translate('btn_confirm_delete')}
                </button>
                <Show when={pending().fileCount > 0}>
                  <button onClick={() => removeFolder(true)}
                    style={{ padding: '9px 20px', background: 'var(--danger-bg)', border: '1px solid var(--danger)', 'border-radius': '10px', color: 'var(--danger)', 'font-size': '14px', 'font-weight': '700', cursor: 'pointer', ...sansFont }}>
                    {translate('btn_folder_delete_files').replace('{count}', String(pending().fileCount))}
                  </button>
                </Show>
              </div>
            </div>
          </div>
        )}
      </Show>

    </div>
  )
}
