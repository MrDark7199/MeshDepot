import { createSignal, createEffect, onMount, onCleanup, Show, For, type JSX } from 'solid-js'
import { useI18n } from '../i18n/index'
import { useTheme } from '../ThemeContext'
import { splitConnectedComponents, writeBinaryStl, buildZip, type MeshPart } from '../utils/meshSplit'
import type { Model3mfRequest, Model3mfResponse } from '../workers/model3mf'
import {
  Engine, Scene, ArcRotateCamera, Vector3, Matrix,
  HemisphericLight, DirectionalLight, Color3, Color4,
  StandardMaterial, Mesh, type LinesMesh, VertexData, PointerEventTypes, AbstractMesh,
  MeshBuilder, TransformNode, Quaternion, DynamicTexture, Texture,
  MultiMaterial, SubMesh, VertexBuffer, type PickingInfo,
} from '@babylonjs/core'
// Side-effect import: registers AbstractMesh.createOrUpdateSubmeshesOctree (octree-accelerated
// triangle picking), which the barrel import can otherwise tree-shake away.
import '@babylonjs/core/Culling/Octrees/octreeSceneComponent'

export function is3dFile(filename: string): boolean {
  const ext = filename.toLowerCase().split('.').pop() || ''
  return ['stl', 'obj', '3mf'].includes(ext)
}

/** Default colour for single-group models (STL/OBJ/plain 3MF) - #4a90d9. */
const DEFAULT_HEX = '#4a90d9'

const decodeText = (bytes: Uint8Array): string => new TextDecoder('utf-8', { fatal: false }).decode(bytes)

/** Base64-encodes a byte buffer in chunks (avoids blowing the call stack on large PNGs). */
const bytesToBase64 = (bytes: Uint8Array): string => {
  let bin = ''
  const CHUNK = 0x8000
  for (let i = 0; i < bytes.length; i += CHUNK) {
    bin += String.fromCharCode(...bytes.subarray(i, i + CHUNK))
  }
  return btoa(bin)
}

/**
 * Coerces an arbitrary colour string into a strict `#rrggbb` form that both
 * Babylon's `Color3.FromHexString` and an `<input type="color">` accept.
 * Strips alpha (8-digit Bambu colours) and any stray characters.
 */
const normalizeHex = (hex: string): string => {
  let h = (hex || '').trim().toLowerCase()
  if (h.startsWith('#')) h = h.slice(1)
  h = h.replace(/[^0-9a-f]/g, '')
  return ('#' + (h + '000000').slice(0, 6))
}

/**
 * WCAG 2.1 relative luminance of linear-light RGB components (0..1).
 *
 * Gamma-linearized and weighted by cone sensitivity - not the naive
 * 0.299/0.587/0.114 average, which works on sRGB values and is well off for
 * saturated colours.
 */
const relativeLuminance = (r: number, g: number, b: number): number => {
  const lin = (v: number) => (v <= 0.04045 ? v / 12.92 : Math.pow((v + 0.055) / 1.055, 2.4))
  return 0.2126 * lin(r) + 0.7152 * lin(g) + 0.0722 * lin(b)
}

/**
 * The darkest a model may be drawn before its shape stops being readable.
 *
 * Roughly #3a3a3a. Below this the lighting has nothing to work with: Babylon
 * multiplies the diffuse colour by the light, and anything times a value near
 * zero stays near zero - so a black part arrives as a flat silhouette with no
 * edges, no curvature and no accents.
 */
const MIN_MODEL_LUMINANCE = 0.04

/**
 * Lifts a colour read from a file until the model is actually legible.
 *
 * Applies only to colours the file dictates, never to one somebody picked: a
 * member who deliberately sets a part to black gets black.
 *
 * How it lifts depends on what is there. A colour with some hue left is scaled
 * up, which keeps it recognisably the same colour - a very dark red becomes a
 * dark red rather than pink. A colour that is essentially black has no hue to
 * preserve and is mixed towards white instead, because scaling zero by anything
 * is still zero.
 */
const legibleModelHex = (hex: string): string => {
  const normalized = normalizeHex(hex)
  const r = parseInt(normalized.slice(1, 3), 16) / 255
  const g = parseInt(normalized.slice(3, 5), 16) / 255
  const b = parseInt(normalized.slice(5, 7), 16) / 255
  if (relativeLuminance(r, g, b) >= MIN_MODEL_LUMINANCE) return normalized

  const toHex = (value: number) =>
    Math.round(Math.min(1, Math.max(0, value)) * 255).toString(16).padStart(2, '0')

  const brightest = Math.max(r, g, b)
  if (brightest > 0.06) {
    // Enough colour to scale. Stepping rather than solving for the factor: the
    // luminance curve is not linear, and a loop of at most a few dozen steps is
    // both exact enough and obvious to read.
    for (let factor = 1.05; factor <= 20; factor += 0.05) {
      const lifted = [r * factor, g * factor, b * factor]
      if (relativeLuminance(lifted[0], lifted[1], lifted[2]) >= MIN_MODEL_LUMINANCE) {
        return '#' + lifted.map(toHex).join('')
      }
    }
  }
  for (let mix = 0.02; mix < 1; mix += 0.01) {
    const lifted = [r + (1 - r) * mix, g + (1 - g) * mix, b + (1 - b) * mix]
    if (relativeLuminance(lifted[0], lifted[1], lifted[2]) >= MIN_MODEL_LUMINANCE) {
      return '#' + lifted.map(toHex).join('')
    }
  }
  return normalized
}

/**
 * True if the normal buffer carries at least one non-degenerate normal. STL files may
 * ship zero-length facet normals (e.g. our marching-cubes resin reconstruction), which
 * would leave the mesh unlit; callers recompute normals from geometry when this is false.
 */
const hasUsableNormals = (normals: ArrayLike<number>): boolean => {
  for (let i = 0; i + 2 < normals.length; i += 3) {
    if (normals[i] !== 0 || normals[i + 1] !== 0 || normals[i + 2] !== 0) return true
  }
  return false
}

/**
 * Welds coincident vertices of a non-indexed triangle soup and returns indexed geometry with
 * averaged (smooth) normals. Marching-cubes STL emits independent triangles → every facet is
 * flat-shaded and the coarse voxel surface looks blocky; sharing vertices at identical positions
 * and averaging the adjoining face normals makes the same geometry read as a smooth surface,
 * with no change to triangle detail. Positions are quantized to a fine grid so tiny float
 * differences still weld. Also shrinks the vertex count (faster picking/rendering).
 */
const weldSmooth = (positions: ArrayLike<number>): { positions: number[]; indices: number[]; normals: number[] } => {
  const map = new Map<string, number>()
  const outPos: number[] = []
  const indices: number[] = new Array(positions.length / 3)
  const q = 1e4 // weld tolerance: 1e-4 mm grid
  for (let i = 0, v = 0; i < positions.length; i += 3, v++) {
    const key = Math.round(positions[i] * q) + ',' + Math.round(positions[i + 1] * q) + ',' + Math.round(positions[i + 2] * q)
    let idx = map.get(key)
    if (idx === undefined) {
      idx = outPos.length / 3
      map.set(key, idx)
      outPos.push(positions[i], positions[i + 1], positions[i + 2])
    }
    indices[v] = idx
  }
  const normals: number[] = []
  VertexData.ComputeNormals(outPos, indices, normals)
  return { positions: outPos, indices, normals }
}

/** Theme-dependent default viewer backgrounds (used until the user picks their own). */
const DEFAULT_VIEWER_BG_DARK = '#202423'
const DEFAULT_VIEWER_BG_LIGHT = '#8b8383'
/** localStorage keys persisting the viewer's display settings across sessions. */
const VIEWER_BG_KEY = 'meshdepot_viewer_bg'
const VIEWER_GRID_KEY = 'meshdepot_viewer_grid'
const VIEWER_BED_KEY = 'meshdepot_viewer_bed'
const VIEWER_BED_W_KEY = 'meshdepot_viewer_bed_w'
const VIEWER_BED_L_KEY = 'meshdepot_viewer_bed_l'
const VIEWER_BED_COLOR_KEY = 'meshdepot_viewer_bed_color'
/** G-code (generated) display settings: line vs solid, line colour mode, shared custom colour. */
const VIEWER_GCODE_MODE_KEY = 'meshdepot_viewer_gcode_mode'          // 'lines' | 'solid'
const VIEWER_GCODE_LINECOLOR_KEY = 'meshdepot_viewer_gcode_linecolor' // 'heat' | 'custom'
const VIEWER_GCODE_COLOR_KEY = 'meshdepot_viewer_gcode_color'         // #rrggbb, shared by solid + custom lines
/** Default print-bed footprint in mm (Bambu-class 256×256); user-editable. */
const DEFAULT_BED_W = 256
const DEFAULT_BED_L = 256
/** Build-plate grain: texture resolution, and the edge length in mm that one tile covers. */
const BED_GRAIN_PX = 256
const BED_GRAIN_MM = 40
/** Default bed tint - a MakerWorld-style slate grey that reads as a plate against the dark viewer. */
const DEFAULT_BED_COLOR = '#2b2f37'
const DEFAULT_GCODE_COLOR = '#4a90d9'
/** Clamps a bed dimension (mm) to a sane range. */
const clampBedDim = (v: number) => Math.min(2000, Math.max(10, Math.round(v)))

/** Default camera framing on load / reset: a 45° downward look at the build plate, a bit zoomed out. */
const VIEW_ALPHA = -Math.PI / 2
const VIEW_BETA = Math.PI / 4          // 45° above the horizontal plate
const VIEW_RADIUS_FACTOR = 2.0         // distance = model size × this (a bit further away)
/** Reference grid half-extent = camera distance × this - large enough to run past the screen edges, even tilted. */
const GRID_REACH_FACTOR = 8

const hexToClearColor = (hex: string): Color4 => {
  const c = Color3.FromHexString(normalizeHex(hex))
  return new Color4(c.r, c.g, c.b, 1)
}

interface StlViewerModalProps {
  /** Authenticated URL of the 3D file to load. */
  url: string
  /** Original filename (used for format detection). */
  filename: string
  designName: string
  onClose: () => void
  /** Optional: persists a captured viewer photo to the design's image gallery. */
  onSaveImage?: (blob: Blob) => Promise<void>
  /** Optional: adds the parts of a split model to the design's current version. */
  onSaveFiles?: (files: File[]) => Promise<void>
  /** Model is authored Z-up (e.g. reconstructed resin mesh) → re-orient to the viewer's Y-up world. */
  zUp?: boolean
}

/**
 * Full-screen 3D model viewer modal powered by Babylon.js.
 * Supports STL (binary + ASCII), OBJ, and 3MF formats.
 * Left-drag: rotate · Scroll: zoom · Right-drag: pan
 */
export function StlViewerModal(props: StlViewerModalProps) {
  const { translate } = useI18n()
  let canvasRef: HTMLCanvasElement | undefined
  let engine: Engine | undefined
  let scene: Scene | undefined
  let camera: ArcRotateCamera | undefined
  let initialRadius = 10
  let initialTarget = Vector3.Zero()
  /** One render group per filament/extruder colour; kept outside reactivity for the Babylon refs. */
  let builtGroups: BuiltGroup[] = []
  /** Build plates of a multi-plate 3MF (empty for single-plate / STL / OBJ); holds the Babylon refs. */
  let builtPlates: PlateInfo[] = []
  /** Root node that re-orients slicer Z-up STL/3MF meshes to the viewer's Y-up world. */
  let modelRoot: TransformNode | undefined
  /** Split tool: separated parts, kept outside reactivity - the arrays are large. */
  let splitParts: MeshPart[] = []
  /** Overlay pulsed over the model to point out a single part. */
  let splitHighlight: Mesh | undefined
  /** Render callback driving that pulse, kept so it can be detached again. */
  let splitFlashTick: (() => void) | undefined

  /** The rendered G-code toolpath mesh (lines or solid), if the current file is G-code. */
  let gcodePath: Mesh | undefined
  /** Parsed G-code once, so line/solid switches and recolours never re-fetch or re-parse. */
  let gcodeData: { polylines: Vector3[][]; minH: number; maxH: number; layerH: number } | null = null
  /** Material of the solid G-code mesh, kept so colour changes update live without a rebuild. */
  let gcodeMat: StandardMaterial | undefined
  /** Disposable nodes of the reference grid / build-plate overlays (rebuilt on demand). */
  let gridNodes: { dispose: () => void }[] = []
  let bedNodes: { dispose: () => void }[] = []
  /** Plate grain textures, generated on first use and reused by every rebuild (see bedGrain()). */
  let bedGrainTextures: { normal: DynamicTexture; diffuse: DynamicTexture } | null = null
  /** Camera distance the reference grid was last sized for - lets us rebuild it when the user zooms far. */
  let gridRadiusAtBuild = 0
  /** World-space bounding radius of the framed content; drives the adaptive depth-clip range. */
  let sceneRadius = 0

  const [isLoading, setIsLoading] = createSignal(true)
  /**
   * One figure across the whole load, 0..1, or null when nothing can be
   * measured. Downloading is only the first stretch of the wait - reading the
   * file and handing it to the GPU are the rest - so each phase advances its
   * own slice of a single bar instead of restarting from zero and leaving the
   * previous one parked at some arbitrary value.
   */
  const [loadProgress, setLoadProgress] = createSignal<number | null>(null)
  /** What the loading screen is currently busy with. */
  const [loadPhase, setLoadPhase] = createSignal<'download' | 'parse' | 'build'>('download')

  /**
   * Share of the bar each phase owns. Weighted by what actually takes the time:
   * from a local server the download is over in milliseconds, while reading a
   * multi-million-triangle mesh is the bulk of the wait.
   */
  const LOAD_PHASE_RANGE: Record<'download' | 'parse' | 'build', [number, number]> = {
    download: [0, 0.15],
    parse: [0.15, 0.85],
    build: [0.85, 1],
  }

  /** Maps a phase-local 0..1 onto that phase's slice of the overall bar. */
  const reportLoad = (phase: 'download' | 'parse' | 'build', fraction: number) => {
    const [from, to] = LOAD_PHASE_RANGE[phase]
    setLoadPhase(phase)
    setLoadProgress(from + (to - from) * Math.max(0, Math.min(1, fraction)))
  }
  const [errorMessage, setErrorMessage] = createSignal('')
  const [formatLabel, setFormatLabel] = createSignal('')
  const [colorsOpen, setColorsOpen] = createSignal(false)
  /** Settings panel (background colour, grid, build plate). */
  const [settingsOpen, setSettingsOpen] = createSignal(false)
  /** Reference-grid overlay (XYZ axes, Blender-style), off by default; persisted. */
  const [gridOn, setGridOn] = createSignal(localStorage.getItem(VIEWER_GRID_KEY) === '1')
  /** Build-plate overlay (model shown sitting on a print bed), off by default; persisted. */
  const [bedOn, setBedOn] = createSignal(localStorage.getItem(VIEWER_BED_KEY) === '1')
  /** User's print-bed footprint (mm); the shown plate is drawn to exactly these dimensions. */
  const [bedW, setBedW] = createSignal(clampBedDim(parseFloat(localStorage.getItem(VIEWER_BED_W_KEY) || '') || DEFAULT_BED_W))
  const [bedL, setBedL] = createSignal(clampBedDim(parseFloat(localStorage.getItem(VIEWER_BED_L_KEY) || '') || DEFAULT_BED_L))
  /** Build-plate tint (semi-transparent); persisted, defaults to the previous dark bed colour. */
  const [bedColor, setBedColor] = createSignal(normalizeHex(localStorage.getItem(VIEWER_BED_COLOR_KEY) || DEFAULT_BED_COLOR))

  /** True while the current file is a *generated* structure (G-code) rather than a mesh (STL/OBJ/3MF). */
  const [isGcode, setIsGcode] = createSignal(false)
  /** G-code rendering: coloured tool-path lines, or an opaque swept-bead "solid" body (view only). */
  const [gcodeMode, setGcodeMode] = createSignal<'lines' | 'solid'>(localStorage.getItem(VIEWER_GCODE_MODE_KEY) === 'solid' ? 'solid' : 'lines')
  /** Line colouring: height gradient ("heat") or a flat custom colour. */
  const [gcodeLineColor, setGcodeLineColor] = createSignal<'heat' | 'custom'>(localStorage.getItem(VIEWER_GCODE_LINECOLOR_KEY) === 'custom' ? 'custom' : 'heat')
  /** Shared custom colour used by the solid body and by custom-coloured lines. */
  const [gcodeColor, setGcodeColor] = createSignal(normalizeHex(localStorage.getItem(VIEWER_GCODE_COLOR_KEY) || DEFAULT_GCODE_COLOR))

  /**
   * Split tool. 'intro' explains what the tool does and waits for a deliberate
   * start - separating a dense mesh takes seconds, so it must not begin on the
   * same click that opens the menu.
   */
  const [splitPhase, setSplitPhase] = createSignal<'idle' | 'intro' | 'busy' | 'done'>('idle')
  const [splitProgress, setSplitProgress] = createSignal(0)
  /** One entry per separated part, in the order they are listed. */
  const [splitNames, setSplitNames] = createSignal<string[]>([])
  /** Which parts the action buttons apply to. */
  const [splitChosen, setSplitChosen] = createSignal<boolean[]>([])
  /** Part currently being pointed out in the model; clears itself after a moment. */
  const [splitFlash, setSplitFlash] = createSignal<number | null>(null)
  /** Which plate to separate: an index into builtPlates, or 'all'. */
  const [splitPlate, setSplitPlate] = createSignal<number | 'all'>('all')
  const [splitSaving, setSplitSaving] = createSignal(false)
  /**
   * Which parts are already in the design. The selection is cumulative, so a
   * second "add" would otherwise resend the ones from the first and the server
   * rejects them by name (error.filename_exists). Tracked per part rather than
   * as one "saved" flag so adding more later stays possible.
   */
  const [splitAdded, setSplitAdded] = createSignal<boolean[]>([])
  /** The separation itself failed - there is no result to show. */
  const [splitError, setSplitError] = createSignal(false)
  /**
   * Adding the parts to the design failed. Deliberately separate from
   * splitError: one signal for both meant a failed upload was reported as
   * "the model could not be separated", which is not what happened and sent
   * the reader looking in the wrong place. Carries the server's message.
   */
  const [splitAddError, setSplitAddError] = createSignal('')
  /** Panel position; null until the result places it, then dragged by the header. */
  const [splitPos, setSplitPos] = createSignal<{ x: number; y: number } | null>(null)

  /** Tools menu + measure tool (pick two surface points → distance). */
  const [toolsOpen, setToolsOpen] = createSignal(false)
  const [measureActive, setMeasureActive] = createSignal(false)
  /** Number of points placed in the current measurement (0/1/2) - drives the on-screen hint. */
  const [measureCount, setMeasureCount] = createSignal(0)
  /** Distance between the two picked points in model units (mm), or null while incomplete. */
  const [measureDist, setMeasureDist] = createSignal<number | null>(null)
  let measurePoints: Vector3[] = []
  let measureNodes: TransformNode[] = []   // placed teardrop markers (each a root node) + connecting line
  let measureLabelEl: HTMLDivElement | undefined  // floating "xx mm" label on the line
  /** Live teardrop that tracks the cursor over the surface while the measure tool is on. */
  let measureHover: TransformNode | undefined
  /** True while a mouse button is held (orbit/pan): suppresses the measure hover raycast so
   *  dragging stays smooth. Tracked explicitly via pointer down/up because event.buttons is
   *  unreliable for synthetic move events. */
  let pointerDown = false

  /** Photo tool: 'off' idle · 'aim' framing (camera locked, drag = crop) · 'review' captured image. */
  const [photoMode, setPhotoMode] = createSignal<'off' | 'aim' | 'review'>('off')
  /** Captured image as a data URL (preview + download + upload source). */
  const [photoUrl, setPhotoUrl] = createSignal<string | null>(null)
  const [photoSaving, setPhotoSaving] = createSignal(false)
  const [photoSaved, setPhotoSaved] = createSignal(false)
  /** Last upload attempt failed → notice shown right in the photo dialog. */
  const [photoError, setPhotoError] = createSignal(false)
  /** Optional crop rectangle in CSS pixels relative to the canvas, drawn by dragging in 'aim' mode. */
  const [photoRegion, setPhotoRegion] = createSignal<{ x: number; y: number; w: number; h: number } | null>(null)
  let photoDragStart: { x: number; y: number } | undefined  // drag origin while marqueeing a crop
  /** Reactive mirror of {@link builtGroups} for the colour panel (label + current hex). */
  const [colorGroups, setColorGroups] = createSignal<{ label: string; hex: string }[]>([])
  /** Reactive plate list for the left sidebar (multi-plate 3MF only); `colorIdx` points into {@link colorGroups}. */
  const [platesUI, setPlatesUI] = createSignal<{ name: string; thumbnail?: string; colorIdx: number[] }[]>([])
  /** Index of the plate currently shown; other plates' meshes are hidden. */
  const [activePlate, setActivePlate] = createSignal(0)
  const theme = useTheme()
  /** Theme-based default background (dark vs light), used when the user has no saved override. */
  const themeDefaultBg = () => (theme.resolvedTheme() === 'light' ? DEFAULT_VIEWER_BG_LIGHT : DEFAULT_VIEWER_BG_DARK)
  /** The user's saved background override, or null to follow {@link themeDefaultBg}. */
  const storedBg = localStorage.getItem(VIEWER_BG_KEY)
  const [customBg, setCustomBg] = createSignal<string | null>(storedBg ? normalizeHex(storedBg) : null)
  /** Effective viewer background: the user's override if set, otherwise the theme default. */
  const bgColor = () => customBg() ?? themeDefaultBg()

  /** Applies a user-chosen background colour to the live scene and persists it for next time. */
  const applyBgColor = (hex: string) => {
    const norm = normalizeHex(hex)
    setCustomBg(norm)
    localStorage.setItem(VIEWER_BG_KEY, norm)
    if (scene) scene.clearColor = hexToClearColor(norm)
    if (gridOn()) buildGrid()   // grid colour is derived from the background - re-derive it
  }

  // Follow theme changes while open when the user hasn't overridden the background.
  createEffect(() => {
    const def = themeDefaultBg()
    if (!customBg() && scene) {
      scene.clearColor = hexToClearColor(def)
      if (gridOn()) buildGrid()
    }
  })

  /** Applies a picked colour to a group's material and reflects it in the panel. */
  const setGroupColor = (index: number, hex: string) => {
    const g = builtGroups[index]
    if (g) g.material.diffuseColor = Color3.FromHexString(normalizeHex(hex))
    setColorGroups(prev => prev.map((cg, i) => (i === index ? { ...cg, hex } : cg)))
  }

  /** Restores every group to the colour read from the file. */
  const resetColors = () => {
    for (const g of builtGroups) g.material.diffuseColor = Color3.FromHexString(normalizeHex(g.defaultHex))
    setColorGroups(builtGroups.map(g => ({ label: g.label, hex: g.defaultHex })))
  }

  const fileExtension = () => (props.filename || props.url.split('?')[0]).split('.').pop()?.toLowerCase() || ''
  const resizeHandler = () => engine?.resize()
  /** Hides the measure hover drop when the cursor leaves the canvas (e.g. moving up to the toolbar). */
  const hoverLeaveHandler = () => measureHover?.setEnabled(false)
  /** Escape leaves whichever tool is active (measure first, then photo) without closing the viewer. */
  const keydownHandler = (e: KeyboardEvent) => {
    if (e.key !== 'Escape') return
    if (measureActive()) { setMeasure(false); e.stopPropagation() }
    else if (photoMode() !== 'off') { exitPhoto(); e.stopPropagation() }
  }

  /** Combined world-space axis-aligned bounding box of the given meshes. */
  const worldBoundsOf = (meshes: AbstractMesh[]) => {
    const min = new Vector3(Infinity, Infinity, Infinity)
    const max = new Vector3(-Infinity, -Infinity, -Infinity)
    for (const mesh of meshes) {
      mesh.computeWorldMatrix(true)
      const bb = mesh.getBoundingInfo().boundingBox
      min.minimizeInPlace(bb.minimumWorld)
      max.maximizeInPlace(bb.maximumWorld)
    }
    return { min, max }
  }

  /** Model meshes currently on screen (active plate for multi-plate 3MF, else everything). */
  const visibleModelMeshes = (): AbstractMesh[] => {
    if (gcodePath) return [gcodePath]
    if (builtPlates.length > 1) return builtPlates[activePlate()].meshes
    return builtGroups.flatMap(g => g.meshes)
  }

  /**
   * Bounding box to frame the camera on: the visible model, expanded to the full print-bed
   * footprint when the bed overlay is on (so the whole plate is in view - is it fitting?).
   */
  const boundsForView = () => {
    const { min, max } = worldBoundsOf(visibleModelMeshes())
    if (bedOn()) {
      const cx = (min.x + max.x) / 2, cz = (min.z + max.z) / 2
      const halfW = bedW() / 2, halfD = bedL() / 2
      min.x = Math.min(min.x, cx - halfW); max.x = Math.max(max.x, cx + halfW)
      min.z = Math.min(min.z, cz - halfD); max.z = Math.max(max.z, cz + halfD)
    }
    return { min, max }
  }

  /** Frames the camera on {@link boundsForView} (distance/target only) and records the reset pose. */
  const fitView = () => {
    if (!camera || visibleModelMeshes().length === 0) return
    const { min, max } = boundsForView()
    const center = Vector3.Center(min, max)
    const size = Math.max(max.x - min.x, max.y - min.y, max.z - min.z) || 1
    // Bounding radius (diagonal/2) around the target - used to size the depth-clip range each frame.
    sceneRadius = Math.max(max.subtract(min).length() / 2, 1)
    camera.target = center
    camera.radius = size * VIEW_RADIUS_FACTOR
    camera.lowerRadiusLimit = size * 0.05
    initialRadius = camera.radius
    initialTarget = center.clone()
  }

  /** A "nice" grid spacing (1/2/5 × 10ⁿ) so a model spans roughly ten cells. */
  const niceStep = (span: number) => {
    const raw = (span || 1) / 10
    const pow = Math.pow(10, Math.floor(Math.log10(raw)))
    const norm = raw / pow
    return (norm >= 5 ? 5 : norm >= 2 ? 2 : 1) * pow
  }

  const disposeGrid = () => { for (const n of gridNodes) n.dispose(); gridNodes = [] }
  const disposeBed = () => { for (const n of bedNodes) n.dispose(); bedNodes = [] }

  /**
   * Builds a floor reference grid with coloured XYZ axes (Blender-style) under the visible model.
   * The grid reaches far past the model - its extent scales with the camera distance so it always
   * runs to (and past) the screen edges, even when the view is tilted low - while a "nice" adaptive
   * step keeps the cell count bounded no matter how far it stretches.
   */
  const buildGrid = () => {
    disposeGrid()
    if (!scene || !camera) return
    const meshes = visibleModelMeshes()
    if (meshes.length === 0) return
    const { min, max } = worldBoundsOf(meshes)
    const cx = (min.x + max.x) / 2, cz = (min.z + max.z) / 2, y = min.y
    const span = Math.max(max.x - min.x, max.z - min.z, 20)
    // Reach far beyond the model, driven by view distance so it fills the viewport at any zoom;
    // never smaller than the model itself. GRID_REACH_FACTOR is generous so a tilted view still
    // sees grid out to the horizon rather than an abrupt edge.
    gridRadiusAtBuild = camera.radius
    const reach = Math.max(camera.radius * GRID_REACH_FACTOR, span)
    const step = niceStep(reach / 3)          // ~60 cells across the whole grid, snapped to 1/2/5×10ⁿ
    const half = Math.ceil(reach / step) * step
    const lines: Vector3[][] = []
    for (let x = -half; x <= half + 1e-6; x += step) lines.push([new Vector3(cx + x, y, cz - half), new Vector3(cx + x, y, cz + half)])
    for (let z = -half; z <= half + 1e-6; z += step) lines.push([new Vector3(cx - half, y, cz + z), new Vector3(cx + half, y, cz + z)])
    const grid = MeshBuilder.CreateLineSystem('vgrid', { lines }, scene)
    // Derived from the background instead of a fixed grey, so the grid stays readable whichever
    // background the user configures. Drawn fully opaque on purpose: alpha would blend the line
    // back towards the background and undo exactly the contrast that was just computed.
    grid.color = contrastLineColor(Color3.FromHexString(normalizeHex(bgColor())))
    grid.alpha = 1; grid.isPickable = false
    gridNodes.push(grid)
    // Coloured XYZ axes at the grid origin (Babylon Y is up): X red, Y green, Z blue.
    // Kept at model scale (not the full grid reach) so they stay a readable orientation gizmo.
    const axisLen = Math.max(span * 0.75, step * 2)
    // The X and Z axes run along the grid lines through the origin - same plane, same direction, so
    // they z-fight and flicker while orbiting. (The Y axis points away from the plane and never did.)
    // A hundredth of a cell separates them; relative to the cell size because the grid rescales with
    // the camera, and a fixed epsilon would fall below the depth resolution once zoomed out.
    const ay = y + step * 0.01
    const axis = (to: Vector3, col: Color3, name: string) => {
      const l = MeshBuilder.CreateLines(name, { points: [new Vector3(cx, ay, cz), to] }, scene)
      l.color = col; l.isPickable = false; gridNodes.push(l)
    }
    axis(new Vector3(cx + axisLen, ay, cz), new Color3(0.9, 0.28, 0.28), 'axisX')
    axis(new Vector3(cx, y + axisLen, cz), new Color3(0.35, 0.8, 0.35), 'axisY')
    axis(new Vector3(cx, ay, cz + axisLen), new Color3(0.38, 0.52, 1), 'axisZ')
  }

  /** WCAG 2.1 relative luminance of a Babylon colour; see relativeLuminance above. */
  const relLuminance = (c: Color3): number => relativeLuminance(c.r, c.g, c.b)

  /**
   * Picks a line colour that stays visible on a freely configurable background.
   *
   * Standard approach, borrowed from accessibility: the WCAG contrast ratio
   * (L_light + 0.05) / (L_dark + 0.05), where WCAG 2.1 (1.4.11 "Non-text Contrast") asks for at
   * least 3:1 on graphical objects such as grid lines. Rather than snapping to hard black/white,
   * the background colour is mixed towards white or black step by step and the first step that
   * meets the target wins - the line keeps a family resemblance to its background and gets only as
   * strong as it has to be.
   *
   * Luminance decides the direction: below 0.179 (where white and black contrast equally) there is
   * more headroom upwards, above it downwards.
   */
  const contrastLineColor = (bg: Color3, minRatio = 3): Color3 => {
    const bgLum = relLuminance(bg)
    const target = bgLum < 0.179 ? new Color3(1, 1, 1) : new Color3(0, 0, 0)
    for (let t = 0.1; t < 1; t += 0.05) {
      const c = new Color3(bg.r + (target.r - bg.r) * t, bg.g + (target.g - bg.g) * t, bg.b + (target.b - bg.b) * t)
      const l = relLuminance(c)
      const ratio = (Math.max(l, bgLum) + 0.05) / (Math.min(l, bgLum) + 0.05)
      if (ratio >= minRatio) return c
    }
    return target
  }

  /**
   * Seamlessly tileable height field for the plate's surface grain, in [0, 1].
   *
   * Two layers, like a real textured PEI sheet: broad, soft blotches from bilinear value noise on a
   * wrapping lattice (the lattice indices wrap, which is what makes the tile seam-free), plus a
   * fine per-pixel speckle for the powder-coated roughness. The speckle needs no wrapping - at one
   * pixel per sample there is no structure that could break at the edge.
   */
  const makeGrainHeight = (): Float32Array => {
    const n = BED_GRAIN_PX, lat = 16
    const h = new Float32Array(n * n)
    const lattice = new Float32Array(lat * lat)
    for (let i = 0; i < lattice.length; i++) lattice[i] = Math.random()
    const smooth = (t: number) => t * t * (3 - 2 * t)   // smoothstep: hides the lattice edges
    for (let y = 0; y < n; y++) {
      for (let x = 0; x < n; x++) {
        const fx = (x / n) * lat, fy = (y / n) * lat
        const x0 = Math.floor(fx) % lat, y0 = Math.floor(fy) % lat
        const x1 = (x0 + 1) % lat, y1 = (y0 + 1) % lat
        const tx = smooth(fx - Math.floor(fx)), ty = smooth(fy - Math.floor(fy))
        const top = lattice[y0 * lat + x0] + (lattice[y0 * lat + x1] - lattice[y0 * lat + x0]) * tx
        const bot = lattice[y1 * lat + x0] + (lattice[y1 * lat + x1] - lattice[y1 * lat + x0]) * tx
        h[y * n + x] = (top + (bot - top) * ty) * 0.45 + Math.random() * 0.55
      }
    }
    return h
  }

  /**
   * Builds the plate's grain textures once and caches them: a normal map (the grain is mostly a
   * lighting effect - it catches the head light as the view orbits, which is what sells a rough
   * plate) plus a near-white diffuse map that multiplies the user's plate tint, so the surface
   * varies slightly in brightness without taking on a colour of its own.
   *
   * Cached because buildBed() re-runs on every colour-picker input event; regenerating two textures
   * per drag frame would churn GPU memory for no visual gain.
   */
  const bedGrain = (): { normal: DynamicTexture; diffuse: DynamicTexture } | null => {
    if (!scene) return null
    if (bedGrainTextures) return bedGrainTextures
    const n = BED_GRAIN_PX
    const h = makeGrainHeight()
    const at = (x: number, y: number) => h[((y + n) % n) * n + ((x + n) % n)]   // wrap = seamless slopes

    // Mip maps are not optional here: a fine noise tiled several times across the plate aliases into
    // a crawling moiré as soon as the view zooms out. Anisotropic filtering does the same job for the
    // flat viewing angle the plate is normally seen at.
    const normal = new DynamicTexture('bedGrainNormal', { width: n, height: n }, scene, true)
    const diffuse = new DynamicTexture('bedGrainDiffuse', { width: n, height: n }, scene, true)
    for (const t of [normal, diffuse]) {
      t.anisotropicFilteringLevel = 8
      // DynamicTexture defaults to CLAMP, unlike Texture. Left at that, uScale/vScale draw the grain
      // once into the first cell and smear the edge pixels across the rest of the plate as stripes
      // along X and Y - tiling needs WRAP explicitly.
      t.wrapU = Texture.WRAP_ADDRESSMODE
      t.wrapV = Texture.WRAP_ADDRESSMODE
    }
    const nCtx = normal.getContext(), dCtx = diffuse.getContext()
    // getImageData rather than createImageData: Babylon's ICanvasRenderingContext only declares the
    // former. Every byte including alpha is overwritten below, so the initial content is irrelevant.
    const nImg = nCtx.getImageData(0, 0, n, n), dImg = dCtx.getImageData(0, 0, n, n)
    // Slope-to-normal: central differences give the height gradient, STRENGTH sets how steep the
    // bumps read. The z component stays 1 before normalising, so a flat area maps to (0.5,0.5,1) -
    // the neutral "no perturbation" colour of a tangent-space normal map.
    const STRENGTH = 2.2
    for (let y = 0; y < n; y++) {
      for (let x = 0; x < n; x++) {
        const dx = (at(x - 1, y) - at(x + 1, y)) * STRENGTH
        const dy = (at(x, y - 1) - at(x, y + 1)) * STRENGTH
        const len = Math.hypot(dx, dy, 1)
        const i = (y * n + x) * 4
        nImg.data[i] = Math.round((dx / len * 0.5 + 0.5) * 255)
        nImg.data[i + 1] = Math.round((dy / len * 0.5 + 0.5) * 255)
        nImg.data[i + 2] = Math.round((1 / len * 0.5 + 0.5) * 255)
        nImg.data[i + 3] = 255
        // Keep the brightness modulation shallow (0.90–1.00): the plate should look textured, not
        // dirty, and the tint the user picked has to survive it.
        const v = Math.round((0.9 + at(x, y) * 0.1) * 255)
        dImg.data[i] = dImg.data[i + 1] = dImg.data[i + 2] = v
        dImg.data[i + 3] = 255
      }
    }
    nCtx.putImageData(nImg, 0, 0); normal.update(false)
    dCtx.putImageData(dImg, 0, 0); diffuse.update(false)
    bedGrainTextures = { normal, diffuse }
    return bedGrainTextures
  }

  /**
   * Builds a semi-transparent print bed of the user's configured footprint (width × length in mm)
   * under the model, centred on its XZ footprint so it's easy to see whether the print fits. The
   * plate is drawn double-sided (visible from below) and tinted with the user's chosen colour; a
   * raster, an outer frame and a coloured front edge give it a build-plate look.
   */
  const buildBed = () => {
    disposeBed()
    if (!scene) return
    const meshes = visibleModelMeshes()
    if (meshes.length === 0) return
    const { min, max } = worldBoundsOf(meshes)
    const cx = (min.x + max.x) / 2, cz = (min.z + max.z) / 2, y = min.y
    const width = bedW(), depth = bedL()      // width → X, depth (length) → Z; footprint = user setting
    const base = Color3.FromHexString(bedColor())
    // The plate is a flat box WITH THICKNESS (not just a plane) so it reads as a physical build
    // plate (MakerWorld look) rather than a drawn rectangle.
    const thickness = Math.max(Math.min(width, depth) * 0.02, 4)
    // Gap between the plate's top face and the model sitting on it. A fixed tiny value (0.02 mm
    // before) falls below the depth-buffer resolution once zoomed out - that resolution degrades
    // with view distance - so a flat model underside and the plate z-fight and flicker while
    // orbiting. Scaled to the model instead (0.3 % of the shorter edge, ~0.8 mm at 256 mm) the gap
    // is invisible yet stays well above the resolution at every zoom level.
    const lift = Math.max(Math.min(width, depth) * 0.003, 0.05)
    const topY = y - lift          // top face sits just below the model
    const bed = MeshBuilder.CreateBox('bed', { width, depth, height: thickness }, scene)
    bed.position.set(cx, topY - thickness / 2, cz)

    /** Shared plate look; the grain is added to the top face only (see below). */
    const plateMaterial = (name: string) => {
      const m = new StandardMaterial(name, scene!)
      m.diffuseColor = base
      // Emissive keeps the plate readable regardless of the light angle, but every bit of it also
      // flattens the grain (it is added after shading). 0.22 is the compromise that still lets the
      // texture show while the plate never goes black in a low view.
      m.emissiveColor = base.scale(0.22)
      // A slightly stronger, tightly focused highlight: the rough surface should glint as it turns,
      // which is most of what makes it read as textured rather than painted.
      m.specularColor = new Color3(0.2, 0.2, 0.22)
      m.specularPower = 24
      m.alpha = 0.9                // slightly translucent, but still reads as a solid plate
      // Polygon offset: pushes ONLY the plate surface a few depth units back. Works independently of
      // zoom and depth resolution and keeps whatever rests on the plate stably on top even where the
      // geometric gap above gets tight (very small plates, extreme zoom-out).
      m.zOffset = 4
      m.zOffsetUnits = 4
      return m
    }
    const matTop = plateMaterial('bedMatTop'), matBody = plateMaterial('bedMatBody')

    // Surface grain. Tiled to a fixed physical size (BED_GRAIN_MM), so the roughness keeps the same
    // scale whether the plate is set to 120 mm or 350 mm instead of being stretched with it.
    const grain = bedGrain()
    if (grain) {
      const uS = Math.max(width / BED_GRAIN_MM, 1), vS = Math.max(depth / BED_GRAIN_MM, 1)
      grain.diffuse.uScale = uS; grain.diffuse.vScale = vS
      grain.normal.uScale = uS; grain.normal.vScale = vS
      matTop.diffuseTexture = grain.diffuse // multiplies diffuseColor → tint survives, brightness varies
      matTop.bumpTexture = grain.normal
      matTop.bumpTexture.level = 0.55       // subtle: a fine tooth, not a hammered finish
    }

    // Only the printing surface is textured - the underside and the rim are plain, like the milled
    // carrier of a real plate. That needs per-face materials: the box is split into one submesh per
    // face, each pointing at an entry of a MultiMaterial.
    //
    // Which face is the top one is read from the vertex normals rather than assumed from Babylon's
    // face order - that order is an implementation detail and silently wrong results (grain on the
    // bottom) would be easy to miss. A box has 4 vertices and 6 indices per face, laid out
    // contiguously, so a face's normal is that of its first vertex.
    const normals = bed.getVerticesData(VertexBuffer.NormalKind)
    const multi = new MultiMaterial('bedMulti', scene)
    multi.subMaterials = [matTop, matBody]
    bed.subMeshes = []
    for (let f = 0; f < 6; f++) {
      const isTop = !!normals && normals[f * 4 * 3 + 1] > 0.9   // normal.y ≈ 1
      new SubMesh(isTop ? 0 : 1, f * 4, 4, f * 6, 6, bed)
    }
    bed.material = multi; bed.isPickable = false
    // Materials are disposed with the plate - buildBed() runs on every colour-picker event, and
    // mesh.dispose() leaves materials behind. The grain textures are shared and must NOT go with
    // them, which is why the materials are disposed on their own rather than via dispose(false, true).
    bedNodes.push(multi, matTop, matBody, bed)

    // No overlays on the plate at all - no raster of its own (that is what the "grid" toggle is for,
    // and two rasters of different spacing on top of each other help nobody), no outline and no
    // front-edge marker. The plate is its surface and its silhouette; orientation comes from the
    // XYZ axes of the grid.
  }

  /** Live update of the plate tint: persists it and rebuilds the plate if it is visible. */
  const applyBedColor = (raw: string) => {
    const hex = normalizeHex(raw)
    setBedColor(hex)
    localStorage.setItem(VIEWER_BED_COLOR_KEY, hex)
    if (bedOn()) buildBed()
  }

  /** Segmentierter Umschalt-Button (Linien/Solid, Heat/Eigene) im Einstellungen-Panel. */
  const segBtnStyle = (active: boolean): JSX.CSSProperties => ({
    flex: '1', background: active ? 'rgba(74,144,217,0.25)' : 'rgba(255,255,255,0.05)',
    border: `1px solid ${active ? 'rgba(74,144,217,0.6)' : 'rgba(255,255,255,0.12)'}`,
    'border-radius': '7px', padding: '6px 8px', color: '#fff', 'font-size': '11px',
    cursor: 'pointer', 'font-family': "'DM Sans',sans-serif",
  })
  /** The compact colour picker, as used for the background. */
  const colorInputStyle: JSX.CSSProperties = {
    width: '26px', height: '26px', 'min-width': '26px', padding: '0',
    border: '1px solid rgba(255,255,255,0.2)', 'border-radius': '6px', background: 'none', cursor: 'pointer',
  }

  /** Rebuilds whichever overlays are enabled (after load or a plate switch). */
  const refreshHelpers = () => {
    if (gridOn()) buildGrid()
    if (bedOn()) buildBed()
  }

  const setGrid = (on: boolean) => {
    setGridOn(on)
    localStorage.setItem(VIEWER_GRID_KEY, on ? '1' : '0')
    if (on) buildGrid(); else disposeGrid()
  }
  const setBed = (on: boolean) => {
    setBedOn(on)
    localStorage.setItem(VIEWER_BED_KEY, on ? '1' : '0')
    if (on) buildBed(); else disposeBed()
    // Deliberately no re-framing: position and zoom stay put, as with the grid toggle.
  }
  /** Updates a print-bed dimension (mm), persists it, and redraws + reframes the bed if it's shown. */
  const applyBedSize = (which: 'w' | 'l', raw: number) => {
    if (!isFinite(raw) || raw <= 0) return
    const v = clampBedDim(raw)
    if (which === 'w') { setBedW(v); localStorage.setItem(VIEWER_BED_W_KEY, String(v)) }
    else { setBedL(v); localStorage.setItem(VIEWER_BED_L_KEY, String(v)) }
    if (bedOn()) { buildBed(); fitView() }
  }

  /** Removes the current measurement (markers, line, floating label) and resets its state. */
  const clearMeasure = () => {
    for (const n of measureNodes) n.dispose()
    measureNodes = []
    measurePoints = []
    setMeasureCount(0)
    setMeasureDist(null)
    if (measureLabelEl) measureLabelEl.style.display = 'none'
  }

  /** Enables/disables the measure tool; clears any in-progress measurement when turning off. */
  const setMeasure = (on: boolean) => {
    setMeasureActive(on)
    if (canvasRef) canvasRef.style.cursor = on ? 'crosshair' : ''
    if (on) updateMeasureHover()                    // show the hover drop immediately if the cursor is on the object
    else { clearMeasure(); measureHover?.setEnabled(false) }
  }

  /**
   * Builds a small black teardrop as a root {@link TransformNode}: a cone whose tip sits at the
   * node's local origin, pointing along +Y, capped by a spherical bulb. Position the root on a
   * surface point and align its +Y with the surface normal to make the drop stand 90° off the face.
   */
  /**
   * Colour of the surface a pick landed on, as the marker sees it. Falls back to the viewer
   * background for anything without a plain diffuse colour (vertex-coloured G-code lines, say),
   * which is what the marker is read against there anyway.
   */
  const pickedSurfaceColor = (mesh?: AbstractMesh | null): Color3 => {
    const mat = mesh?.material
    if (mat instanceof StandardMaterial) return mat.diffuseColor
    return Color3.FromHexString(normalizeHex(bgColor()))
  }

  /**
   * Recolours a teardrop so it contrasts with whatever it sits on - same WCAG approach as the
   * reference grid, since the model colour is just as freely configurable as the background.
   * Part of the colour goes in as emissive: the marker is a read-off aid, and without it the
   * computed contrast would hold only on the sides that happen to face the light.
   */
  const tintTeardrop = (root: TransformNode, surface: Color3) => {
    const mat = root.getChildMeshes()[0]?.material
    if (!(mat instanceof StandardMaterial)) return
    const c = contrastLineColor(surface, 4.5)   // solid marker: aim higher than the 3:1 for lines
    mat.diffuseColor = c
    mat.emissiveColor = c.scale(0.45)
  }

  const buildTeardrop = (name: string, scene: Scene, surface: Color3): TransformNode => {
    const h = Math.max(initialRadius * 0.0056, 0.08)
    const d = h * 0.5
    const mat = new StandardMaterial(`${name}Mat`, scene)
    mat.specularColor = new Color3(0.15, 0.15, 0.15)
    const root = new TransformNode(name, scene)
    const cone = MeshBuilder.CreateCylinder(`${name}Cone`, { height: h, diameterTop: d, diameterBottom: 0, tessellation: 16 }, scene)
    cone.position = new Vector3(0, h / 2, 0)        // tip at the local origin (→ on the surface point)
    cone.material = mat; cone.isPickable = false; cone.renderingGroupId = 1; cone.parent = root
    const bulb = MeshBuilder.CreateSphere(`${name}Bulb`, { diameter: d, segments: 12 }, scene)
    bulb.position = new Vector3(0, h, 0)
    bulb.material = mat; bulb.isPickable = false; bulb.renderingGroupId = 1; bulb.parent = root
    tintTeardrop(root, surface)
    return root
  }

  /**
   * Surface normal under the cursor, always pointing out of the solid towards the viewer.
   *
   * The raw pick normal is not usable on its own. Vertex normals are missing on some meshes (then
   * Babylon returns null), and STL/3MF files routinely ship triangles with inconsistent winding, so
   * the normal can point *into* the body - the marker would stand on its head, buried in the
   * surface. Fall back to the geometric face normal, then flip whatever we got towards the camera:
   * the face we are looking at is by definition the outward one.
   */
  const outwardNormal = (pick: PickingInfo): Vector3 | null => {
    const n = pick.getNormal(true, true) ?? pick.getNormal(true, false)
    if (!n || !camera || !pick.pickedPoint) return n
    return Vector3.Dot(n, camera.position.subtract(pick.pickedPoint)) < 0 ? n.scale(-1) : n
  }

  /** Quaternion rotating the marker's local +Y onto a surface normal (falls back to straight up). */
  const orientToNormal = (nrm?: Vector3 | null): Quaternion => {
    if (!nrm) return Quaternion.Identity()
    const n = nrm.normalizeToNew()
    const up = Vector3.Up()
    const dot = Vector3.Dot(up, n)
    if (dot >= 0.99999) return Quaternion.Identity()
    if (dot <= -0.99999) return Quaternion.RotationAxis(Vector3.Right(), Math.PI)  // exactly downward
    return Quaternion.RotationAxis(Vector3.Cross(up, n).normalize(), Math.acos(dot))
  }

  /** Adds a picked surface point (marker oriented along its normal); the third click starts fresh. */
  const addMeasurePoint = (p: Vector3, nrm?: Vector3 | null, on?: AbstractMesh | null) => {
    if (!scene) return
    if (measurePoints.length >= 2) clearMeasure()
    measurePoints.push(p.clone())
    const n = measurePoints.length
    const pin = buildTeardrop(`measurePin${n}`, scene, pickedSurfaceColor(on))
    pin.position.copyFrom(p)
    pin.rotationQuaternion = orientToNormal(nrm)
    measureNodes.push(pin)
    if (measurePoints.length === 2) {
      const line = MeshBuilder.CreateLines('measureLine', { points: [measurePoints[0], measurePoints[1]] }, scene)
      line.color = new Color3(1, 0.82, 0.25); line.isPickable = false; line.renderingGroupId = 1
      measureNodes.push(line)
      setMeasureDist(Vector3.Distance(measurePoints[0], measurePoints[1]))
    }
    setMeasureCount(measurePoints.length)  // set last so the distance is available when the HUD re-renders
  }

  /** Glues the live hover teardrop to the surface point under the cursor, oriented along its normal. */
  const updateMeasureHover = () => {
    if (!scene) return
    if (!measureActive()) { measureHover?.setEnabled(false); return }
    const pick = scene.pick(scene.pointerX, scene.pointerY)
    if (!pick?.hit || !pick.pickedPoint) { measureHover?.setEnabled(false); return }
    const surface = pickedSurfaceColor(pick.pickedMesh)
    if (!measureHover) measureHover = buildTeardrop('measureHover', scene, surface)
    // The hover drop is built once and reused, so it has to re-tint as it travels: a model can be
    // several colour groups, and the drop must stay readable when it crosses from one into the next.
    else tintTeardrop(measureHover, surface)
    measureHover.setEnabled(true)
    measureHover.position.copyFrom(pick.pickedPoint)
    measureHover.rotationQuaternion = orientToNormal(outwardNormal(pick))
  }

  // ── Photo tool ───────────────────────────────────────────────────────────────
  /** Enters framing mode: turns off measuring, closes panels and locks the camera so drags crop. */
  /**
   * Collects the rendered triangles as one flat soup, in the coordinates the
   * source file used.
   *
   * Each mesh's own world matrix is applied so a 3MF that places its objects by
   * transform comes out arranged the way it is drawn. modelRoot is undone
   * again: that node only carries the viewer's Z-up correction, and baking it
   * in would hand back parts rotated 90 degrees away from the original.
   *
   * `onlyPlate` restricts the soup to one build plate of a multi-plate 3MF.
   */
  const collectTriangleSoup = (onlyPlate: number | 'all'): Float32Array => {
    const wanted = onlyPlate === 'all' || builtPlates.length === 0
      ? null
      : new Set<Mesh>(builtPlates[onlyPlate]?.meshes ?? [])
    const chunks: number[] = []
    const undoRoot = modelRoot ? Matrix.Invert(modelRoot.getWorldMatrix()) : null
    for (const group of builtGroups) {
      for (const mesh of group.meshes) {
        if (wanted && !wanted.has(mesh)) continue
        const positions = mesh.getVerticesData(VertexBuffer.PositionKind)
        if (!positions) continue
        mesh.computeWorldMatrix(true)
        const toSource = undoRoot ? mesh.getWorldMatrix().multiply(undoRoot) : mesh.getWorldMatrix()
        // A mesh without an index buffer is already a triangle soup.
        const indices = mesh.getIndices() ?? Array.from({ length: positions.length / 3 }, (_, i) => i)
        const point = new Vector3()
        for (const index of indices) {
          point.set(positions[index * 3], positions[index * 3 + 1], positions[index * 3 + 2])
          const world = Vector3.TransformCoordinates(point, toSource)
          chunks.push(world.x, world.y, world.z)
        }
      }
    }
    return new Float32Array(chunks)
  }

  /** Opens the explanation step; nothing is computed until the user starts it. */
  const openSplitIntro = () => {
    setToolsOpen(false)
    setSplitPlate('all')
    setSplitError(false)
    setSplitAddError('')
    setSplitPos(null)
    setSplitPhase('intro')
  }

  /** Separates the model into its unconnected objects. */
  const runSplit = async () => {
    if (splitPhase() === 'busy') return
    setSplitPhase('busy')
    setSplitProgress(0)
    setSplitError(false)
    try {
      const soup = collectTriangleSoup(splitPlate())
      splitParts = await splitConnectedComponents(soup, fraction => setSplitProgress(fraction))
      setSplitNames(splitParts.map((_, index) => splitFileName(index)))
      // Everything is chosen to begin with: the common case is wanting all of it.
      setSplitChosen(splitParts.map(() => true))
      setSplitAdded(splitParts.map(() => false))
      placeSplitPanelRight()
      setSplitPhase('done')
    } catch {
      splitParts = []
      setSplitNames([])
      setSplitChosen([])
      setSplitError(true)
      setSplitPhase('done')
    }
  }

  /** Parks the result panel at the right edge, vertically centred. */
  const placeSplitPanelRight = () => {
    const width = 420
    const height = Math.min(520, window.innerHeight - 80)
    setSplitPos({ x: Math.max(16, window.innerWidth - width - 32), y: Math.max(16, (window.innerHeight - height) / 2) })
  }

  const clearSplitHighlight = () => {
    if (splitFlashTick && scene) scene.onBeforeRenderObservable.removeCallback(splitFlashTick)
    splitFlashTick = undefined
    splitHighlight?.dispose()
    splitHighlight = undefined
    setSplitFlash(null)
  }

  /** How long a part stays pointed out after the magnifier is clicked. */
  const SPLIT_FLASH_MS = 4000

  /**
   * Points out one part by pulsing a copy of it over the model for a few
   * seconds, then taking it away again.
   *
   * A copy rather than a recolour of the original: the parts are only data, and
   * the model on screen is one mesh per colour group with no notion of them.
   * It pulses rather than sitting there in a flat colour because a static
   * overlay on a similarly-shaped object is easy to miss - movement is what the
   * eye picks out. It also removes itself, so the list never leaves the model
   * in a state the viewer has to be told to undo.
   */
  const flashSplitPart = (index: number) => {
    clearSplitHighlight()
    const part = splitParts[index]
    if (!scene || !part) return

    const mesh = new Mesh('splitHighlight', scene)
    const data = new VertexData()
    data.positions = Array.from(part.positions)
    data.indices = Array.from({ length: part.positions.length / 3 }, (_, i) => i)
    const normals: number[] = []
    VertexData.ComputeNormals(data.positions, data.indices, normals)
    data.normals = normals
    data.applyToMesh(mesh)

    const material = new StandardMaterial('splitHighlightMat', scene)
    material.emissiveColor = Color3.FromHexString('#ffd23f')
    material.diffuseColor = Color3.FromHexString('#ffd23f')
    material.specularColor = Color3.Black()
    // Drawn just in front of the surface it covers, or it would z-fight with it.
    material.zOffset = -2
    material.backFaceCulling = false
    mesh.material = material
    mesh.isPickable = false
    if (modelRoot) mesh.parent = modelRoot
    splitHighlight = mesh
    setSplitFlash(index)

    const startedAt = performance.now()
    splitFlashTick = () => {
      const elapsed = performance.now() - startedAt
      if (elapsed >= SPLIT_FLASH_MS) { clearSplitHighlight(); return }
      // Shimmer, then fade out over the last half second so it does not just vanish.
      const pulse = 0.55 + 0.45 * Math.sin(elapsed / 110)
      const fade = Math.min(1, (SPLIT_FLASH_MS - elapsed) / 500)
      material.alpha = 0.9 * pulse * fade
    }
    scene.onBeforeRenderObservable.add(splitFlashTick)
  }

  const toggleSplitChoice = (index: number) => {
    setSplitChosen(prev => prev.map((on, i) => (i === index ? !on : on)))
    setSplitAddError('')
  }

  const setAllSplitChoices = (on: boolean) => {
    setSplitChosen(prev => prev.map(() => on))
    setSplitAddError('')
  }


  const chosenIndices = () => splitChosen().reduce<number[]>((list, on, index) => (on ? [...list, index] : list), [])

  const closeSplit = () => {
    clearSplitHighlight()
    splitParts = []
    setSplitNames([])
    setSplitChosen([])
    setSplitPhase('idle')
    setSplitAdded([])
    setSplitError(false)
    setSplitAddError('')
    setSplitPos(null)
  }

  /** Base name for the produced files, derived from the model being viewed. */
  const splitBaseName = () =>
    (props.designName || props.filename || 'model').replace(/\.[^.]+$/, '').replace(/[^\w.-]+/g, '_') || 'model'

  const splitFileName = (index: number) => `${splitBaseName()}-part-${String(index + 1).padStart(2, '0')}.stl`

  /** Hands the chosen parts over as one ZIP. */
  const downloadSplit = () => {
    const indices = chosenIndices()
    if (indices.length === 0) return
    const zip = buildZip(indices.map(index => ({ name: splitFileName(index), data: writeBinaryStl(splitParts[index]) })))
    const url = URL.createObjectURL(zip)
    const anchor = document.createElement('a')
    anchor.href = url
    anchor.download = `${splitBaseName()}-parts.zip`
    anchor.click()
    // Revoked on a later tick: revoking immediately cancels the download in
    // some browsers before it has read the blob.
    setTimeout(() => URL.revokeObjectURL(url), 10_000)
  }

  /** Adds the chosen parts to the design's current version via the parent callback. */
  /** Chosen parts that are not in the design yet - what an "add" would send. */
  const pendingIndices = () => chosenIndices().filter(index => !splitAdded()[index])

  const addSplitToDesign = async () => {
    const indices = pendingIndices()
    if (!props.onSaveFiles || indices.length === 0 || splitSaving()) return
    setSplitSaving(true)
    setSplitAddError('')
    try {
      const files = indices.map(index =>
        new File([writeBinaryStl(splitParts[index])], splitFileName(index), { type: 'model/stl' }))
      await props.onSaveFiles(files)
      setSplitAdded(prev => prev.map((added, i) => added || indices.includes(i)))
    } catch (failure) {
      // The reason is shown as it came back: a rejected upload is usually
      // specific (duplicate name, size, permissions), and hiding that behind a
      // generic sentence is what made this hard to place in the first place.
      setSplitAddError(failure instanceof Error && failure.message ? failure.message : 'unknown')
    } finally {
      setSplitSaving(false)
    }
  }

  /** Drag the panel by its header. */
  const startSplitDrag = (event: PointerEvent) => {
    const start = splitPos() ?? { x: 0, y: 0 }
    const originX = event.clientX
    const originY = event.clientY
    const move = (moveEvent: PointerEvent) => {
      setSplitPos({
        x: Math.max(0, Math.min(window.innerWidth - 120, start.x + moveEvent.clientX - originX)),
        y: Math.max(0, Math.min(window.innerHeight - 60, start.y + moveEvent.clientY - originY)),
      })
    }
    const stop = () => {
      window.removeEventListener('pointermove', move)
      window.removeEventListener('pointerup', stop)
    }
    window.addEventListener('pointermove', move)
    window.addEventListener('pointerup', stop)
  }

  // Closes the menu whenever a load begins: switching files while it is open
  // would otherwise leave tools pointing at geometry that is being replaced.
  createEffect(() => { if (isLoading()) setToolsOpen(false) })

  const startPhoto = () => {
    setMeasure(false)
    setToolsOpen(false); setColorsOpen(false); setSettingsOpen(false)
    setPhotoRegion(null); setPhotoUrl(null); setPhotoSaved(false); setPhotoSaving(false); setPhotoError(false)
    camera?.detachControl()
    setPhotoMode('aim')
  }

  /** Leaves the photo tool entirely and restores normal camera controls. */
  const exitPhoto = () => {
    setPhotoMode('off')
    setPhotoUrl(null); setPhotoRegion(null); setPhotoSaved(false); setPhotoSaving(false); setPhotoError(false)
    photoDragStart = undefined
    if (camera && canvasRef) camera.attachControl(canvasRef, true)
  }

  /** Captures the current view (whole framebuffer, or the selected crop) into a PNG data URL. */
  const takePhoto = () => {
    if (!engine || !scene || !canvasRef) return
    scene.render()   // refresh the preserved drawing buffer so toDataURL/drawImage see this exact view
    const region = photoRegion()
    let url: string
    if (region && region.w > 4 && region.h > 4) {
      const scale = canvasRef.width / canvasRef.clientWidth   // render-buffer px per CSS px
      const out = document.createElement('canvas')
      out.width = Math.round(region.w * scale)
      out.height = Math.round(region.h * scale)
      const ctx = out.getContext('2d')!
      ctx.drawImage(canvasRef, region.x * scale, region.y * scale, region.w * scale, region.h * scale,
        0, 0, out.width, out.height)
      url = out.toDataURL('image/png')
    } else {
      url = canvasRef.toDataURL('image/png')
    }
    setPhotoUrl(url)
    setPhotoSaved(false)
    setPhotoError(false)
    setPhotoMode('review')
  }

  /** Downloads the captured photo as a PNG file. */
  const downloadPhoto = () => {
    const url = photoUrl(); if (!url) return
    const safe = (props.designName || 'modell').replace(/[^\w.-]+/g, '_')
    const a = document.createElement('a')
    a.href = url; a.download = `${safe}-foto.png`
    a.click()
  }

  /**
   * Decodes a `data:` URL into a Blob - deliberately WITHOUT `fetch(url)`. The Content-Security-
   * Policy only allows `connect-src 'self'`, and a fetch() of a data URL counts as a connection:
   * the browser blocks it and the upload failed silently.
   */
  const dataUrlToBlob = (url: string): Blob => {
    const [head, b64] = url.split(',')
    const mime = /:(.*?);/.exec(head)?.[1] || 'image/png'
    const bin = atob(b64)
    const bytes = new Uint8Array(bin.length)
    for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i)
    return new Blob([bytes], { type: mime })
  }

  /** Uploads the captured photo to the design gallery via the parent-provided callback. */
  const addPhotoToDesign = async () => {
    const url = photoUrl()
    if (!url || !props.onSaveImage || photoSaving() || photoSaved()) return
    setPhotoSaving(true)
    setPhotoError(false)
    try {
      await props.onSaveImage(dataUrlToBlob(url))
      setPhotoSaved(true)
    } catch {
      // The caller does show a toast, but it is easy to miss behind the viewer overlay - report in
      // the dialog as well rather than letting the click look like it did nothing.
      setPhotoError(true)
    }
    setPhotoSaving(false)
  }

  /** Pointer handlers on the 'aim' overlay that draw the crop marquee (camera is detached here). */
  const photoDragDown = (e: PointerEvent) => {
    const t = e.currentTarget as HTMLElement
    t.setPointerCapture(e.pointerId)
    photoDragStart = { x: e.offsetX, y: e.offsetY }
    setPhotoRegion({ x: e.offsetX, y: e.offsetY, w: 0, h: 0 })
  }
  const photoDragMove = (e: PointerEvent) => {
    if (!photoDragStart) return
    const s = photoDragStart
    setPhotoRegion({ x: Math.min(s.x, e.offsetX), y: Math.min(s.y, e.offsetY),
      w: Math.abs(e.offsetX - s.x), h: Math.abs(e.offsetY - s.y) })
  }
  const photoDragUp = () => {
    photoDragStart = undefined
    const r = photoRegion()
    if (r && (r.w <= 4 || r.h <= 4)) setPhotoRegion(null)   // treat a click/tiny drag as "no crop"
  }

  /** Keeps the floating distance label pinned to the midpoint of the measured segment. */
  const updateMeasureLabel = () => {
    const el = measureLabelEl
    if (!el) return
    if (measurePoints.length !== 2 || !scene || !camera || !canvasRef) { el.style.display = 'none'; return }
    const mid = Vector3.Center(measurePoints[0], measurePoints[1])
    const proj = Vector3.Project(mid, Matrix.Identity(), scene.getTransformMatrix(),
      camera.viewport.toGlobal(canvasRef.clientWidth, canvasRef.clientHeight))
    el.style.display = 'block'
    el.style.left = `${proj.x}px`
    el.style.top = `${proj.y}px`
  }

  /** Shows a single build plate (hiding the others), re-frames the camera on it, and highlights it. */
  /**
   * Partitions each mesh's triangles into an octree so scene.pick raycasts
   * (measure tool, double-tap focus) stay fast on dense meshes - the
   * reconstructed resin STL alone has ~1.8M triangles, where a brute-force
   * per-triangle pick on every hover would stutter.
   *
   * Deferred, and one mesh per idle slot: building it is seconds of synchronous
   * work on such a mesh, and doing it before the model appeared left the viewer
   * frozen right when it looked ready to use. Picking simply falls back to the
   * brute-force path until this has run.
   *
   * MUST run after every transform: the octree stores WORLD coordinates
   * (bbox.minimumWorld) and is never refreshed automatically. Built before the
   * Z-up rotation, its blocks end up rotated 90° away from the model.
   */
  const scheduleOctreeBuild = (groups: BuiltGroup[]) => {
    const pending = groups.flatMap(group => group.meshes)
    const idle: (callback: () => void) => void =
      typeof requestIdleCallback === 'function'
        ? callback => requestIdleCallback(() => callback(), { timeout: 2000 })
        : callback => setTimeout(callback, 0)
    const step = () => {
      const mesh = pending.shift()
      if (!mesh) return
      if (!mesh.isDisposed()) {
        mesh.createOrUpdateSubmeshesOctree?.(64, 2)
        // Picking only. Otherwise Babylon also uses the octree for render selection
        // (useOctreeForRenderingSelection defaults to on) and drops submeshes whose octree
        // blocks miss the frustum - zooming in narrows the frustum, and a small inaccuracy
        // is enough to make whole objects vanish. Visibility now hangs solely on the mesh's
        // (always current) bounding box.
        mesh.useOctreeForRenderingSelection = false
      }
      if (pending.length > 0) idle(step)
    }
    idle(step)
  }

  const selectPlate = (idx: number) => {
    if (idx < 0 || idx >= builtPlates.length) return
    setActivePlate(idx)
    builtPlates.forEach((plate, i) => {
      const on = i === idx
      for (const mesh of plate.meshes) mesh.setEnabled(on)
    })
    clearMeasure()    // a measurement on the previous plate no longer makes sense
    refreshHelpers()  // rebuild grid / bed for the now-visible plate
    resetView()       // frame it (incl. bed) from the default 45° angle
  }

  onMount(() => {
    document.body.style.overflow = 'hidden'
    initViewer()
  })

  onCleanup(() => {
    document.body.style.overflow = ''
    window.removeEventListener('resize', resizeHandler)
    window.removeEventListener('keydown', keydownHandler)
    canvasRef?.removeEventListener('pointerleave', hoverLeaveHandler)
    engine?.dispose()
  })

  /**
   * Bootstraps the Babylon.js engine and scene, fetches the model file,
   * dispatches to the correct parser, applies a material, and fits the
   * camera to the model's bounding box.
   */
  const initViewer = async () => {
    if (!canvasRef) return

    // adaptToDeviceRatio (4th arg) keeps the render buffer at native resolution on
    // HiDPI/Retina screens - without it the canvas renders at half resolution and looks blurry.
    // preserveDrawingBuffer lets the "Foto erstellen" tool read the framebuffer via
    // canvasRef.toDataURL()/drawImage() after a render (WYSIWYG capture of the current view).
    engine = new Engine(canvasRef, true, { preserveDrawingBuffer: true, stencil: true }, true)
    scene = new Scene(engine)
    // Don't let Babylon raycast the scene on every pointer move to track the mesh-under-cursor;
    // the measure tool does its own pick only when needed. On the ~1.8M-triangle resin mesh this
    // hidden per-move pick was a big part of the rotate stutter.
    scene.skipPointerMovePicking = true
    // Restored user background (default: neutral mid-grey so black and white models both read well).
    scene.clearColor = hexToClearColor(bgColor())

    // ArcRotateCamera handles rotate / zoom / pan out of the box
    camera = new ArcRotateCamera('cam', VIEW_ALPHA, VIEW_BETA, 10, Vector3.Zero(), scene)
    camera.attachControl(canvasRef, true)
    camera.lowerRadiusLimit = 0.1
    camera.wheelPrecision = 50 / 4  // niedriger = schneller; 4× der ursprünglichen Zoom-Geschwindigkeit (Basis 50)
    camera.panningSensibility = 500

    // Lighting: hemispheric base with a bright ground colour (no dark underside) plus a
    // "headlight" that follows the camera, so whatever side the user orbits to is lit.
    const hemi = new HemisphericLight('hemi', new Vector3(0, 1, 0), scene)
    hemi.intensity = 0.65
    hemi.diffuse = new Color3(1, 1, 1)
    hemi.groundColor = new Color3(0.55, 0.55, 0.55)
    const headlight = new DirectionalLight('dir', new Vector3(1, -2, -1).normalize(), scene)
    headlight.intensity = 0.75
    scene.onBeforeRenderObservable.add(() => {
      if (camera) headlight.direction = camera.getForwardRay().direction
      // Adaptive depth-clip: bracket the z-buffer tightly around the model so the interpenetrating
      // G-code beads (and stacked STL facets) stop z-fighting/flickering. Perspective depth precision
      // is dominated by the near plane, so push minZ as far forward as the framed content allows;
      // maxZ stays generous enough to still contain the far-reaching reference grid.
      if (camera && sceneRadius > 0) {
        // While the camera sits outside the bounding sphere no visible triangle can be closer than
        // (radius − sceneRadius), so minZ may travel that far (minus headroom for panning). Zooming
        // in moves the camera INTO the sphere, where that bound no longer holds: a percentage-based
        // minZ then slices into the very object being inspected up close (parts "disappear"). Hence
        // the tiny fraction there - depth resolution stays far finer than any wall thickness or
        // extrusion width even so.
        const pad = sceneRadius * 2.5   // headroom so panning / the bed overlay never clip the front
        camera.minZ = Math.max(camera.radius - pad, camera.radius * 0.001, 0.01)
        camera.maxZ = camera.radius * (GRID_REACH_FACTOR + 2) + sceneRadius
      }
      updateMeasureLabel()
      // Regrow the reference grid when the user zooms far enough that its extent no longer fills the view.
      if (camera && gridOn() && gridRadiusAtBuild > 0) {
        const ratio = camera.radius / gridRadiusAtBuild
        if (ratio > 1.6 || ratio < 0.625) buildGrid()
      }
    })

    engine.runRenderLoop(() => scene?.render())
    window.addEventListener('resize', resizeHandler)
    window.addEventListener('keydown', keydownHandler)
    canvasRef.addEventListener('pointerleave', hoverLeaveHandler)

    scene.onPointerObservable.add((info) => {
      if (!scene) return
      // Track drag state so the measure hover never raycasts mid-orbit (the source of the stutter).
      if (info.type === PointerEventTypes.POINTERDOWN) { pointerDown = true; measureHover?.setEnabled(false) }
      else if (info.type === PointerEventTypes.POINTERUP) { pointerDown = false }
      // Measure tool: a live teardrop follows the cursor over the surface (oriented along the normal).
      if (info.type === PointerEventTypes.POINTERMOVE && measureActive()) {
        // Only pick when NOT dragging: picking the full-resolution resin mesh on every orbit move
        // made rotating stutter. Hover resumes as soon as the button is released.
        if (!pointerDown) updateMeasureHover()
        return
      }
      // Measure tool: each single tap on the surface drops a point (third tap starts over).
      if (info.type === PointerEventTypes.POINTERTAP && measureActive()) {
        const pick = scene.pick(scene.pointerX, scene.pointerY)
        if (pick?.hit && pick.pickedPoint) addMeasurePoint(pick.pickedPoint, outwardNormal(pick), pick.pickedMesh)
        return
      }
      // Double-tap a part to frame & orbit it; double-tap empty space to reset the view.
      if (info.type === PointerEventTypes.POINTERDOUBLETAP && !measureActive()) {
        const pick = scene.pick(scene.pointerX, scene.pointerY)
        if (pick?.hit && pick.pickedMesh) focusOnMesh(pick.pickedMesh)
        else resetView()
      }
    })

    try {
      const response = await fetch(props.url, { credentials: 'include' })
      if (!response.ok) throw new Error(`HTTP ${response.status}`)
      // Read the body in chunks so the loading screen can show how far along the
      // download is. Without a Content-Length (chunked responses) there is
      // nothing to measure, and it falls back to the plain read.
      const declaredLength = Number(response.headers.get('content-length') || 0)
      let buffer: ArrayBuffer
      if (response.body && declaredLength > 0) {
        const reader = response.body.getReader()
        const chunks: Uint8Array[] = []
        let received = 0
        for (;;) {
          const { done, value } = await reader.read()
          if (done) break
          chunks.push(value)
          received += value.length
          reportLoad('download', received / declaredLength)
        }
        const joined = new Uint8Array(received)
        let at = 0
        for (const chunk of chunks) { joined.set(chunk, at); at += chunk.length }
        buffer = joined.buffer
      } else {
        buffer = await response.arrayBuffer()
      }
      // Parsing is one synchronous stretch with no measurable steps, so the bar
      // gives way to an indeterminate state rather than pretending to advance.
      // The bar carries on from where the download left it. Formats whose parser
      // reports nothing simply hold here rather than dropping the bar entirely -
      // it still shows how much of the whole load is behind us.
      reportLoad('parse', 0)

      // Detect format: prefer filename hint, fall back to magic bytes
      const extHint = fileExtension()
      const magic = new Uint8Array(buffer, 0, 4)
      const isZip = magic[0] === 0x50 && magic[1] === 0x4B && magic[2] === 0x03 && magic[3] === 0x04

      let fmt: string
      if (extHint === 'gcode' || extHint === 'gco' || extHint === 'g') {
        fmt = 'gcode'
      } else if (extHint === 'obj') {
        fmt = 'obj'
      } else if (extHint === '3mf' || isZip) {
        fmt = '3mf'
      } else {
        fmt = 'stl'
      }
      setFormatLabel(fmt.toUpperCase())

      setIsGcode(fmt === 'gcode')
      if (fmt === 'gcode') {
        // G-code is generated tool-path data, not a mesh: parse once, then render as coloured
        // lines or a swept-bead solid per the user's setting (switchable without re-parsing).
        gcodeData = parseGcode(decodeText(new Uint8Array(buffer)))
        builtGroups = []
        setColorGroups([]) // no per-part colour panel for toolpaths
        if (gcodeData) renderGcode(); else setErrorMessage(translate('viewer_load_failed'))
      } else {
        gcodeData = null
        let groups: BuiltGroup[]
        if (fmt === 'obj') {
          groups = [singleGroup(parseWavefrontMesh(new TextDecoder().decode(buffer), scene), scene)]
        } else if (fmt === '3mf') {
          const res = await parse3mf(buffer, scene)
          groups = res.groups
          // Only offer the plate switcher when the file actually splits across several plates.
          builtPlates = res.plates.length > 1 ? res.plates : []
        } else {
          groups = [singleGroup(await parseStl(
            buffer, scene,
            fraction => reportLoad('parse', fraction),
            () => reportLoad('build', 0),
          ), scene)]
          reportLoad('build', 1)
        }
        builtGroups = groups
        setColorGroups(groups.map(g => ({ label: g.label, hex: g.defaultHex })))

        // Slicer 3MF are reliably Z-up; re-orient them to the viewer's Y-up world so they lie flat
        // on the build plate (matches the G-code parser). STL/OBJ carry no reliable up-axis, so they
        // are shown as authored - forcing a rotation would tip some models through the plate.
        // Reconstructed resin meshes (props.zUp) are also Z-up → same re-orientation.
        if (fmt === '3mf' || props.zUp) {
          modelRoot = new TransformNode('modelRoot', scene)
          modelRoot.rotation.x = -Math.PI / 2
          for (const g of groups) for (const m of g.meshes) m.parent = modelRoot
        }

        scheduleOctreeBuild(groups)

        if (builtPlates.length > 1) {
          setPlatesUI(builtPlates.map(p => ({ name: p.name, thumbnail: p.thumbnail, colorIdx: p.colorIdx })))
          selectPlate(0) // show the first plate; rebuilds overlays and frames the view
          setIsLoading(false)
          return
        }
      }

      refreshHelpers() // draw the grid / build plate if they were left enabled
      resetView()      // frame the model (+ bed) from the default 45° angle
      setIsLoading(false)
    } catch (err: unknown) {
      setErrorMessage(translate('viewer_load_failed') + ': ' + (err instanceof Error ? err.message : String(err)))
      setIsLoading(false)
    }
  }

  // ── Parsers ────────────────────────────────────────────────────────────────

  /**
   * Parses a binary or ASCII STL buffer into a Babylon.js Mesh.
   * Detects ASCII vs binary by inspecting the first 5 bytes and surrounding text.
   */
  /**
   * Parses a binary or ASCII STL.
   *
   * Asynchronous and chunked on purpose. The binary path walks millions of
   * triangles, and doing that in one synchronous stretch is what made the
   * browser report the page as unresponsive - and left the loading bar frozen
   * mid-transition, because no frames were painted. Yielding every so often
   * costs a little wall-clock and buys back a responsive page and a progress
   * figure that means something: on a local server the download is over in
   * milliseconds, so this is the part the user actually waits for.
   *
   * Positions and normals go into preallocated Float32Arrays rather than
   * number[] with push: the triangle count is in the header, and growing a
   * plain array to 16 million boxed numbers was a large part of the cost.
   */
  const parseStl = async (
    buffer: ArrayBuffer,
    scene: Scene,
    onProgress?: (fraction: number) => void,
    onBuildStart?: () => void,
  ): Promise<Mesh> => {
    const dataView = new DataView(buffer)
    const uint8View = new Uint8Array(buffer)
    const first5 = new TextDecoder().decode(uint8View.slice(0, 5))

    let isAscii = false
    if (first5.toLowerCase() === 'solid') {
      const preview = new TextDecoder('utf-8', { fatal: false }).decode(uint8View.slice(0, 256))
      isAscii = preview.includes('facet') || preview.includes('endsolid')
    }

    const breathe = () => new Promise<void>(resolve => setTimeout(resolve, 0))

    let positions: Float32Array | number[]
    let normals: Float32Array | number[]

    if (!isAscii) {
      const triangleCount = dataView.getUint32(80, true)
      const expectedSize = 84 + triangleCount * 50
      if (triangleCount === 0 || triangleCount > 5_000_000 || expectedSize > buffer.byteLength + 100) {
        throw new Error(`Invalid STL: ${triangleCount} triangles, ${buffer.byteLength} bytes`)
      }
      const usable = Math.min(triangleCount, Math.floor((buffer.byteLength - 84) / 50))
      // Report in fiftieths rather than every fixed number of triangles: a fixed
      // chunk on a 1.6M mesh left the last update at 91%, and that was the value
      // the bar sat on while the build step ran.
      const step = Math.max(1, Math.floor(usable / 50))
      const positionData = new Float32Array(usable * 9)
      const normalData = new Float32Array(usable * 9)
      let offset = 84
      let write = 0
      for (let i = 0; i < usable; i++) {
        const nx = dataView.getFloat32(offset, true)
        const ny = dataView.getFloat32(offset + 4, true)
        const nz = dataView.getFloat32(offset + 8, true)
        offset += 12
        for (let vertexIndex = 0; vertexIndex < 3; vertexIndex++) {
          positionData[write] = dataView.getFloat32(offset, true)
          positionData[write + 1] = dataView.getFloat32(offset + 4, true)
          positionData[write + 2] = dataView.getFloat32(offset + 8, true)
          normalData[write] = nx
          normalData[write + 1] = ny
          normalData[write + 2] = nz
          write += 3
          offset += 12
        }
        offset += 2
        if (i % step === 0) {
          onProgress?.(i / usable)
          await breathe()
        }
      }
      positions = positionData
      normals = normalData
    } else {
      // ASCII STL carries no count, so the arrays have to grow. Rare and slow by
      // nature; it is chunked for responsiveness rather than for speed.
      const lines = new TextDecoder().decode(buffer).split('\n')
      const positionList: number[] = []
      const normalList: number[] = []
      for (let i = 0; i < lines.length; i++) {
        const parts = lines[i].trim().split(/\s+/)
        if (parts[0] === 'vertex') {
          positionList.push(parseFloat(parts[1]), parseFloat(parts[2]), parseFloat(parts[3]))
        } else if (parts[0] === 'facet' && parts[1] === 'normal') {
          // repeat the normal for each of the 3 upcoming vertices
          for (let vertexIndex = 0; vertexIndex < 3; vertexIndex++) normalList.push(parseFloat(parts[2]), parseFloat(parts[3]), parseFloat(parts[4]))
        }
        if (i % 50_000 === 0) {
          onProgress?.(i / lines.length)
          await breathe()
        }
      }
      positions = positionList
      normals = normalList
    }
    onProgress?.(1)
    // Two yields: one so the finished bar is actually painted, and one so the
    // phase change below reaches the screen before the build blocks the thread.
    await breathe()
    onBuildStart?.()
    await breathe()

    // buildMesh hands the data to Babylon, which uploads it to the GPU. That is
    // one opaque call with no steps to report, so the caller drops the bar and
    // says what is happening instead of showing a figure that cannot move.
    //
    // Smooth-shade only reconstructed resin meshes (props.zUp): their coarse marching-cubes
    // surface otherwise reads as flat facets. Regular STLs keep their crisp per-facet normals.
    return buildMesh('stl', positions, normals, scene, !!props.zUp)
  }

  /**
   * Parses a Wavefront OBJ string into a Babylon.js Mesh.
   * Handles polygonal faces by fan-triangulating any n-gon.
   * Normals are used when present; otherwise Babylon computes them.
   */
  const parseWavefrontMesh = (text: string, scene: Scene): Mesh => {
    const allV: number[] = [], allN: number[] = []
    const positions: number[] = [], normals: number[] = []

    for (const line of text.split('\n')) {
      const parts = line.trim().split(/\s+/)
      if (parts[0] === 'v')  { allV.push(parseFloat(parts[1]), parseFloat(parts[2]), parseFloat(parts[3])) }
      else if (parts[0] === 'vn') { allN.push(parseFloat(parts[1]), parseFloat(parts[2]), parseFloat(parts[3])) }
      else if (parts[0] === 'f') {
        const face = parts.slice(1)
        for (let i = 1; i < face.length - 1; i++) {
          for (const def of [face[0], face[i], face[i + 1]]) {
            const index = def.split('/').map(indexStr => parseInt(indexStr) - 1)
            positions.push(allV[index[0] * 3], allV[index[0] * 3 + 1], allV[index[0] * 3 + 2])
            if (index[2] >= 0 && allN.length > 0)
              normals.push(allN[index[2] * 3], allN[index[2] * 3 + 1], allN[index[2] * 3 + 2])
          }
        }
      }
    }

    return buildMesh('obj', positions, normals.length === positions.length ? normals : [], scene)
  }

  /**
   * Extracts and decompresses a file entry from a ZIP archive.
   * Supports stored (method 0) and deflated (method 8) entries.
   * Scans all local file headers and returns the first .model file
   * that contains vertex data.
   */
  const decompress = async (data: Uint8Array<ArrayBuffer>): Promise<Uint8Array> => {
    const ds = new DecompressionStream('deflate-raw')
    const writer = ds.writable.getWriter()
    const reader = ds.readable.getReader()
    writer.write(data)
    writer.close()
    const chunks: Uint8Array[] = []
    while (true) {
      const { value, done } = await reader.read()
      if (done) break
      chunks.push(value)
    }
    const total = chunks.reduce((s, c) => s + c.length, 0)
    const out = new Uint8Array(total)
    let pos = 0
    for (const chunk of chunks) { out.set(chunk, pos); pos += chunk.length }
    return out
  }

  /**
   * Reads the real 64-bit values from the ZIP64 extra field (header id 0x0001).
   * Only the fields whose 32-bit base value is 0xFFFFFFFF are present, in a
   * fixed order: uncompressed size, compressed size, local header offset.
   */
  const parseZip64Extra = (extra: Uint8Array, hasUncomp: boolean, hasComp: boolean, hasOff: boolean): { comp?: number; off?: number } => {
    const dv = new DataView(extra.buffer, extra.byteOffset, extra.byteLength)
    let p = 0
    while (p + 4 <= extra.byteLength) {
      const id = dv.getUint16(p, true)
      const sz = dv.getUint16(p + 2, true)
      if (id === 0x0001) {
        let q = p + 4
        const end = p + 4 + sz
        const res: { comp?: number; off?: number } = {}
        if (hasUncomp && q + 8 <= end) q += 8 // unkomprimierte Größe überspringen
        if (hasComp && q + 8 <= end) { res.comp = Number(dv.getBigUint64(q, true)); q += 8 }
        if (hasOff && q + 8 <= end) { res.off = Number(dv.getBigUint64(q, true)); q += 8 }
        return res
      }
      p += 4 + sz
    }
    return {}
  }

  /**
   * Pulls the .model/.config entries out of a 3MF (a ZIP) through the central
   * directory. That is the only place the compressed size is reliable: in the
   * local header a streaming slicer writes 0 (data descriptor, general-purpose
   * bit 3), and ZIP64 writes the 0xFFFFFFFF sentinel.
   */
  const extractModelFiles = async (buffer: ArrayBuffer): Promise<Map<string, Uint8Array>> => {
    const view = new DataView(buffer)
    const bytes = new Uint8Array(buffer)
    const files = new Map<string, Uint8Array>()

    // Search backwards for the end of central directory (its comment is normally empty).
    let eocd = -1
    for (let i = buffer.byteLength - 22; i >= 0; i--) {
      if (view.getUint32(i, true) === 0x06054b50) { eocd = i; break }
    }
    if (eocd < 0) return files

    let cdOffset = view.getUint32(eocd + 16, true)
    let cdCount = view.getUint16(eocd + 10, true)

    // The ZIP64 EOCD, for when the 32-bit fields are saturated.
    if ((cdOffset === 0xFFFFFFFF || cdCount === 0xFFFF) && eocd - 20 >= 0 && view.getUint32(eocd - 20, true) === 0x07064b50) {
      const z64eocd = Number(view.getBigUint64(eocd - 20 + 8, true))
      if (z64eocd >= 0 && z64eocd + 56 <= buffer.byteLength && view.getUint32(z64eocd, true) === 0x06064b50) {
        cdCount = Number(view.getBigUint64(z64eocd + 32, true))
        cdOffset = Number(view.getBigUint64(z64eocd + 48, true))
      }
    }

    let cd = cdOffset
    for (let n = 0; n < cdCount && cd + 46 <= buffer.byteLength; n++) {
      if (view.getUint32(cd, true) !== 0x02014b50) break
      const method = view.getUint16(cd + 10, true)
      let compSize = view.getUint32(cd + 20, true)
      const uncompSize = view.getUint32(cd + 24, true)
      const nameLen = view.getUint16(cd + 28, true)
      const extraLen = view.getUint16(cd + 30, true)
      const commentLen = view.getUint16(cd + 32, true)
      let localOff = view.getUint32(cd + 42, true)
      const name = new TextDecoder().decode(bytes.subarray(cd + 46, cd + 46 + nameLen))

      if (compSize === 0xFFFFFFFF || uncompSize === 0xFFFFFFFF || localOff === 0xFFFFFFFF) {
        const extra = bytes.subarray(cd + 46 + nameLen, cd + 46 + nameLen + extraLen)
        const z = parseZip64Extra(extra, uncompSize === 0xFFFFFFFF, compSize === 0xFFFFFFFF, localOff === 0xFFFFFFFF)
        if (compSize === 0xFFFFFFFF && z.comp != null) compSize = z.comp
        if (localOff === 0xFFFFFFFF && z.off != null) localOff = z.off
      }

      // .model/.config carry geometry + settings; plate_N.png are the slicer-rendered plate previews
      // (the no_light/top/pick variants are deliberately excluded via the `plate_<digits>` anchor).
      if (name.endsWith('.model') || name.endsWith('.config') || /(^|\/)plate_\d+\.png$/i.test(name)) {
        // The local header carries its own name/extra lengths, which may differ.
        const lNameLen = view.getUint16(localOff + 26, true)
        const lExtraLen = view.getUint16(localOff + 28, true)
        const dataStart = localOff + 30 + lNameLen + lExtraLen
        const chunk = bytes.subarray(dataStart, dataStart + compSize)
        const raw = method === 8 ? await decompress(chunk)
                  : method === 0 ? chunk
                  : null
        if (raw) files.set(name.replace(/^\/+/, ''), raw)
      }
      cd += 46 + nameLen + extraLen + commentLen
    }
    return files
  }

  /** A resolved 3MF object: raw mesh data, a list of sub-component references, or both. */
  interface M3mfObject {
    verts?: number[]
    tris?: number[]
    components?: { path: string; objectid: string; matrix: Matrix }[]
  }

  /** One render group sharing a single filament colour, plus its Babylon material for live edits. */
  interface BuiltGroup {
    label: string
    /** All instance meshes of this colour; they share {@link material}. */
    meshes: Mesh[]
    material: StandardMaterial
    /** Hex colour as read from the file, used by the reset button. */
    defaultHex: string
  }

  /** One build plate of a multi-plate 3MF: its meshes, a rendered thumbnail, and the colours it uses. */
  interface PlateInfo {
    /** plater_id from model_settings.config. */
    id: string
    /** plater_name (may be empty → a generic "Plate N" label is shown). */
    name: string
    /** data: URL of the slicer-rendered plate preview (Metadata/plate_N.png), if present. */
    thumbnail?: string
    /** Every instance mesh placed on this plate; shown only while the plate is active. */
    meshes: Mesh[]
    /** Indices into {@link builtGroups}/colorGroups for the filament colours used on this plate. */
    colorIdx: number[]
  }

  /** Builds a flat-coloured StandardMaterial (double-sided, low specular) for a render group. */
  const makeMaterial = (name: string, hex: string, scene: Scene): StandardMaterial => {
    const material = new StandardMaterial(name, scene)
    material.diffuseColor = Color3.FromHexString(normalizeHex(hex))
    material.specularColor = new Color3(0.1, 0.1, 0.1)
    material.backFaceCulling = false
    return material
  }

  /** Wraps a single parsed mesh (STL/OBJ/plain 3MF) as the lone default-coloured render group. */
  const singleGroup = (mesh: Mesh, scene: Scene): BuiltGroup => {
    const material = makeMaterial('mat', DEFAULT_HEX, scene)
    mesh.material = material
    return { label: props.designName || 'Model', meshes: [mesh], material, defaultHex: DEFAULT_HEX }
  }

  /** Finds the first archive entry whose path ends with `suffix`. */
  const findEntry = (files: Map<string, Uint8Array>, suffix: string): Uint8Array | undefined => {
    for (const [path, bytes] of files) if (path.endsWith(suffix)) return bytes
    return undefined
  }

  /**
   * Parses Metadata/model_settings.config: which extruder each object uses and which plate
   * (named colour group) each extruder belongs to. Object ids here match the <build> objectids
   * in the main model.
   */
  /** One build plate as declared in model_settings.config, before its geometry is resolved. */
  interface PlateSpec { id: string; name: string; thumbFile: string; objectIds: string[] }

  const parseModelSettings = (bytes?: Uint8Array): {
    objExtruder: Map<string, string>
    plateNameForExtruder: Map<string, string>
    plates: PlateSpec[]
  } => {
    const objExtruder = new Map<string, string>()
    const plateNameForExtruder = new Map<string, string>()
    const plates: PlateSpec[] = []
    if (!bytes) return { objExtruder, plateNameForExtruder, plates }

    const doc = new DOMParser().parseFromString(decodeText(bytes), 'text/xml')
    for (const obj of Array.from(doc.getElementsByTagName('object'))) {
      const id = obj.getAttribute('id')
      if (!id) continue
      // The extruder metadata is a direct child of <object> (parts carry their own metadata).
      for (const child of Array.from(obj.children)) {
        if (child.tagName === 'metadata' && child.getAttribute('key') === 'extruder') {
          const v = child.getAttribute('value')
          if (v) objExtruder.set(id, v)
        }
      }
    }
    for (const plate of Array.from(doc.getElementsByTagName('plate'))) {
      let name = '', id = '', thumbFile = ''
      const objectIds: string[] = []
      // Plate-level fields are direct <metadata> children; object ids live inside <model_instance>.
      for (const child of Array.from(plate.children)) {
        if (child.tagName === 'metadata') {
          const k = child.getAttribute('key'), v = child.getAttribute('value') || ''
          if (k === 'plater_name') name = v
          else if (k === 'plater_id') id = v
          else if (k === 'thumbnail_file') thumbFile = v
        } else if (child.tagName === 'model_instance') {
          for (const md of Array.from(child.getElementsByTagName('metadata'))) {
            if (md.getAttribute('key') === 'object_id') {
              const v = md.getAttribute('value')
              if (v) objectIds.push(v)
            }
          }
        }
      }
      plates.push({ id: id || String(plates.length + 1), name, thumbFile, objectIds })
      if (name) for (const oid of objectIds) {
        const ex = objExtruder.get(oid)
        if (ex && !plateNameForExtruder.has(ex)) plateNameForExtruder.set(ex, name)
      }
    }
    return { objExtruder, plateNameForExtruder, plates }
  }

  /** Reads the filament/extruder colour arrays from Metadata/project_settings.config (JSON). */
  const parseProjectColors = (bytes?: Uint8Array): (extruder: string) => string | undefined => {
    let filament: string[] = []
    let extruder: string[] = []
    if (bytes) {
      try {
        const json = JSON.parse(decodeText(bytes))
        if (Array.isArray(json.filament_colour)) filament = json.filament_colour
        if (Array.isArray(json.extruder_colour)) extruder = json.extruder_colour
      } catch { /* malformed settings → fall back to default colour */ }
    }
    return (ex: string) => {
      const idx = parseInt(ex) - 1 // extruder numbers are 1-based
      if (idx < 0) return undefined
      return filament[idx] || extruder[idx] || undefined
    }
  }

  /** Builds a Babylon Matrix from a 3MF transform string (12 values, row-vector convention). */
  /**
   * Reads one .model entry into its objects (keyed by id) and its <build> items.
   *
   * The scan runs in a worker: it is the longest stretch of opening a 3MF, and
   * on the main thread nothing could be painted while it ran - the loading bar
   * sat frozen and the browser flagged the page as unresponsive. The worker
   * also cannot use DOMParser, which is why the geometry is read from the text
   * instead of from a DOM built with one element per vertex.
   *
   * A worker that fails to start (or throws) is not fatal: the caller falls
   * back to reading the file on this thread, which is slow but correct.
   */
  const parseModelFile = (bytes: Uint8Array, onProgress?: (fraction: number) => void): Promise<{
    objects: Map<string, M3mfObject>
    build: { objectid: string; matrix: Matrix }[]
  }> => new Promise((resolve, reject) => {
    let worker: Worker
    try {
      worker = new Worker(new URL('../workers/model3mf.ts', import.meta.url), { type: 'module' })
    } catch (failure) {
      reject(failure)
      return
    }
    const finish = () => worker.terminate()

    worker.onmessage = (event: MessageEvent<Model3mfResponse>) => {
      const message = event.data
      if (message.type === 'progress') { onProgress?.(message.fraction); return }
      if (message.type === 'error') { finish(); reject(new Error(message.message)); return }

      const objects = new Map<string, M3mfObject>()
      for (const scanned of message.objects) {
        const object: M3mfObject = {}
        if (scanned.vertices.length) {
          object.verts = Array.from(scanned.vertices)
          object.tris = Array.from(scanned.triangles)
        }
        if (scanned.components.length) {
          object.components = scanned.components.map(component => ({
            path: component.path,
            objectid: component.objectid,
            matrix: matrixFromValues(component.transform),
          }))
        }
        objects.set(scanned.id, object)
      }
      const build = message.build.map(item => ({ objectid: item.objectid, matrix: matrixFromValues(item.transform) }))
      finish()
      resolve({ objects, build })
    }
    worker.onerror = failure => { finish(); reject(new Error(failure.message || 'worker failed')) }

    // The buffer is transferred, so this copy is the worker's from here on.
    const copy = bytes.slice()
    worker.postMessage({ bytes: copy.buffer, path: '' } satisfies Model3mfRequest, [copy.buffer])
  })

  /** Same layout as parseMatrix, but from the twelve numbers the scan returns. */
  const matrixFromValues = (values: number[] | null): Matrix => {
    if (!values || values.length < 12) return Matrix.Identity()
    return Matrix.FromValues(
      values[0], values[1], values[2], 0,
      values[3], values[4], values[5], 0,
      values[6], values[7], values[8], 0,
      values[9], values[10], values[11], 1,
    )
  }

  /**
   * Parses a 3MF archive into one Babylon.js render group per filament colour.
   *
   * Handles the 3MF Production Extension that Bambu/MakerWorld use: the main model holds
   * assemblies (<object> built from <component p:path=… objectid=… transform=…>) whose
   * geometry lives in separate /3D/Objects/*.model files, and a <build> section places
   * each top-level object via a transform. We resolve that tree, multiply the transforms
   * down each branch, and place every mesh instance at its world position - without it,
   * all parts collapse onto the origin and repeated instances are lost.
   *
   * Each build item's objectid is looked up in model_settings.config for its extruder, which
   * indexes the filament_colour array in project_settings.config. Geometry is accumulated into
   * one buffer per extruder so each colour becomes its own mesh + material (individually
   * recolourable and pickable). Falls back to a single default-coloured group for plain
   * (non-production / no colour data) 3MF files.
   *
   * When model_settings.config declares several build plates, each build instance is also
   * assigned to its plate (via the plate's model_instance object_ids) and the plate's rendered
   * preview (Metadata/plate_N.png) is decoded, so the UI can offer a plate switcher.
   */
  const parse3mf = async (buffer: ArrayBuffer, scene: Scene): Promise<{ groups: BuiltGroup[]; plates: PlateInfo[] }> => {
    const files = await extractModelFiles(buffer)
    if (files.size === 0) throw new Error('Invalid 3MF: no model files in archive')

    const settings = parseModelSettings(findEntry(files, 'model_settings.config'))
    const colorForExtruder = parseProjectColors(findEntry(files, 'project_settings.config'))

    // Plate previews: index the decoded plate_N.png entries by their plate number.
    const thumbById = new Map<string, string>()
    for (const [path, bytes] of files) {
      const m = /(?:^|\/)plate_(\d+)\.png$/i.exec(path)
      if (m) thumbById.set(m[1], 'data:image/png;base64,' + bytesToBase64(bytes))
    }

    // Plate membership: object-id → plate index, and per-plate mesh/colour accumulators.
    const havePlates = settings.plates.length > 0
    const plateForObject = new Map<string, number>()
    settings.plates.forEach((p, i) => { for (const oid of p.objectIds) if (!plateForObject.has(oid)) plateForObject.set(oid, i) })
    const plateMeshes: Mesh[][] = settings.plates.map(() => [])
    const plateKeys: Set<string>[] = settings.plates.map(() => new Set<string>())

    const parsed = new Map<string, Map<string, M3mfObject>>()
    let mainPath = ''
    let buildItems: { objectid: string; matrix: Matrix }[] = []
    for (const [path, bytes] of files) {
      if (!path.endsWith('.model')) continue
      const { objects, build } = await parseModelFile(bytes, fraction => reportLoad('parse', fraction))
      parsed.set(path, objects)
      if (build.length && !mainPath) { mainPath = path; buildItems = build }
    }
    if (!mainPath) {
      mainPath = [...parsed.keys()].find(p => p.endsWith('3dmodel.model')) || [...parsed.keys()][0]
    }

    // One colour group (shared material) per extruder key; one mesh per build instance, so a
    // double-click can pick a single part while every instance of a colour shares one material.
    const groupMap = new Map<string, BuiltGroup>()
    const groupFor = (key: string): BuiltGroup => {
      let g = groupMap.get(key)
      if (!g) {
        // Lifted when the file asks for something too dark to show its own shape.
        const hex = key === 'default' ? DEFAULT_HEX : legibleModelHex(colorForExtruder(key) || DEFAULT_HEX)
        const material = makeMaterial('mat_' + key, hex, scene)
        const label = settings.plateNameForExtruder.get(key) || (key === 'default' ? 'Model' : 'Extruder ' + key)
        g = { label, material, meshes: [], defaultHex: normalizeHex(hex) }
        groupMap.set(key, g)
      }
      return g
    }

    // Append one resolved object's transformed geometry into a single instance's buffers.
    const emit = (positions: number[], indices: number[], verts: number[], tris: number[], matrix: Matrix) => {
      const mm = matrix.m
      const base = positions.length / 3
      for (let i = 0; i < verts.length; i += 3) {
        const x = verts[i], y = verts[i + 1], z = verts[i + 2]
        positions.push(
          x * mm[0] + y * mm[4] + z * mm[8] + mm[12],
          x * mm[1] + y * mm[5] + z * mm[9] + mm[13],
          x * mm[2] + y * mm[6] + z * mm[10] + mm[14],
        )
      }
      for (const t of tris) indices.push(base + t)
    }

    // Resolve an object into the given instance buffers, multiplying transforms down each branch.
    const resolve = (positions: number[], indices: number[], path: string, objectid: string, matrix: Matrix, depth: number) => {
      if (depth > 50) return
      const obj = parsed.get(path)?.get(objectid)
      if (!obj) return
      if (obj.verts && obj.tris) emit(positions, indices, obj.verts, obj.tris, matrix)
      if (obj.components) {
        for (const c of obj.components) {
          resolve(positions, indices, c.path || path, c.objectid, c.matrix.multiply(matrix), depth + 1)
        }
      }
    }

    // Builds one flat-shaded, pickable mesh for an instance and attaches it to its colour group.
    const buildInstance = (positions: number[], indices: number[], key: string, name: string): Mesh | null => {
      if (positions.length === 0) return null
      const vd = new VertexData()
      vd.positions = positions
      vd.indices = indices
      const mesh = new Mesh(name, scene)
      vd.applyToMesh(mesh)
      // Flat per-face normals → crisp edges like the STL parser (vs. smoothed ComputeNormals).
      mesh.convertToFlatShadedMesh()
      const g = groupFor(key)
      mesh.material = g.material
      g.meshes.push(mesh)
      return mesh
    }

    let instanceN = 0
    if (buildItems.length) {
      for (const item of buildItems) {
        const key = settings.objExtruder.get(item.objectid) || 'default'
        const positions: number[] = []
        const indices: number[] = []
        resolve(positions, indices, mainPath, item.objectid, item.matrix, 0)
        const mesh = buildInstance(positions, indices, key, `part_${item.objectid}_${instanceN++}`)
        if (mesh && havePlates) {
          // Unassigned instances (objectid not referenced by any plate) fall back to the first plate.
          const pi = plateForObject.get(item.objectid) ?? 0
          plateMeshes[pi].push(mesh)
          plateKeys[pi].add(key)
        }
      }
    } else {
      // Plain 3MF (no production <build>): one mesh per object at identity, all default-coloured.
      for (const objects of parsed.values()) {
        for (const obj of objects.values()) {
          if (!obj.verts || !obj.tris) continue
          const positions: number[] = []
          const indices: number[] = []
          emit(positions, indices, obj.verts, obj.tris, Matrix.Identity())
          buildInstance(positions, indices, 'default', `part_${instanceN++}`)
        }
      }
    }

    if (groupMap.size === 0) throw new Error('Invalid 3MF: no mesh data found')

    // Order groups by extruder number for a stable colour panel.
    const sortedKeys = [...groupMap.keys()].sort((a, b) => (parseInt(a) || 0) - (parseInt(b) || 0))
    const groups = sortedKeys.map(k => groupMap.get(k)!)
    const keyToIndex = new Map(sortedKeys.map((k, i) => [k, i] as const))

    // Materialise plates (dropping any that ended up empty), mapping their colour keys to group indices.
    const plates: PlateInfo[] = havePlates
      ? settings.plates
          .map((p, i): PlateInfo => ({
            id: p.id,
            name: p.name,
            thumbnail: thumbById.get(p.id),
            meshes: plateMeshes[i],
            colorIdx: [...plateKeys[i]]
              .map(k => keyToIndex.get(k))
              .filter((x): x is number => x != null)
              .sort((a, b) => a - b),
          }))
          .filter(p => p.meshes.length > 0)
      : []

    return { groups, plates }
  }

  /**
   * Assembles a Babylon.js Mesh from flat position and normal arrays.
   * Generates a sequential index buffer and recomputes normals when the
   * provided normal array does not match the position count.
   *
   * `smooth` welds coincident vertices and averages their normals. Used for
   * reconstructed resin meshes so the coarse marching-cubes surface reads as a
   * smooth object instead of a field of flat facets. Not for regular STL/OBJ,
   * whose hard edges must stay crisp.
   */
  const buildMesh = (name: string, positions: number[] | Float32Array, normals: number[] | Float32Array, scene: Scene, smooth = false): Mesh => {
    if (smooth) {
      const w = weldSmooth(positions)
      const vd = new VertexData()
      vd.positions = w.positions
      vd.indices = w.indices
      vd.normals = w.normals
      const mesh = new Mesh(name, scene)
      vd.applyToMesh(mesh)
      return mesh
    }

    // A preallocated index buffer rather than push: for a multi-million-triangle
    // mesh, growing a plain array here cost about as much as parsing the file.
    const vertexCount = Math.floor(positions.length / 3)
    const indices = new Uint32Array(vertexCount)
    for (let i = 0; i < vertexCount; i++) indices[i] = i

    // Only trust supplied normals when they carry real direction. Reconstructed resin
    // meshes (marching cubes) ship zero-length facet normals; used as-is they leave the
    // mesh unlit (N·L = 0) and it renders solid black. Fall back to computed flat normals
    // whenever the supplied set is missing or degenerate (all zero-length).
    let computedNormals = normals
    if (normals.length !== positions.length || !hasUsableNormals(normals)) {
      computedNormals = []
      VertexData.ComputeNormals(positions, indices, computedNormals)
    }

    const vd = new VertexData()
    vd.positions = positions
    vd.indices = indices
    vd.normals = computedNormals
    const mesh = new Mesh(name, scene)
    vd.applyToMesh(mesh)
    return mesh
  }

  /** Height gradient for the toolpath: blue at the bottom, red at the top. */
  const heightColor = (t: number): Color4 => {
    const clamped = Math.min(Math.max(t, 0), 1)
    const c = Color3.FromHSV((1 - clamped) * 240, 0.85, 1)
    return new Color4(c.r, c.g, c.b, 1)
  }

  /**
   * Parses g-code moves (G0/G1) into extrusion polylines; a travel move breaks
   * the line. The axes are converted from Z-up (g-code) to Y-up (Babylon), so
   * Babylon's Y is the height. Very large files are capped at MAX_POINTS.
   * Returns the paths plus the height range and the estimated layer height,
   * which the solid mode uses as the bead height.
   */
  const parseGcode = (text: string): { polylines: Vector3[][]; minH: number; maxH: number; layerH: number } | null => {
    const MAX_POINTS = 1_200_000
    const polylines: Vector3[][] = []
    let poly: Vector3[] = []
    let x = 0, y = 0, z = 0, e = 0
    let absXYZ = true, absE = true
    let minH = Infinity, maxH = -Infinity
    let count = 0
    const heights = new Set<number>()   // distinct extrusion heights → layer-height estimate
    const flush = () => { if (poly.length >= 2) polylines.push(poly); poly = [] }

    const rows = text.split('\n')
    for (const raw of rows) {
      if (count > MAX_POINTS) break
      let line = raw
      const semi = line.indexOf(';')
      if (semi >= 0) line = line.slice(0, semi)
      line = line.trim()
      if (!line) continue
      const parts = line.split(/\s+/)
      const cmd = parts[0].toUpperCase()

      if (cmd === 'G90') { absXYZ = true; continue }
      if (cmd === 'G91') { absXYZ = false; continue }
      if (cmd === 'M82') { absE = true; continue }
      if (cmd === 'M83') { absE = false; continue }
      if (cmd === 'G92') {
        for (let i = 1; i < parts.length; i++) {
          if (parts[i][0] === 'E' || parts[i][0] === 'e') { const v = parseFloat(parts[i].slice(1)); if (!isNaN(v)) e = v }
        }
        continue
      }
      if (cmd !== 'G0' && cmd !== 'G1' && cmd !== 'G00' && cmd !== 'G01') continue

      let nx = x, ny = y, nz = z, de = 0, hasE = false
      for (let i = 1; i < parts.length; i++) {
        const axis = parts[i][0].toUpperCase()
        const v = parseFloat(parts[i].slice(1))
        if (isNaN(v)) continue
        if (axis === 'X') nx = absXYZ ? v : x + v
        else if (axis === 'Y') ny = absXYZ ? v : y + v
        else if (axis === 'Z') nz = absXYZ ? v : z + v
        else if (axis === 'E') { hasE = true; de = absE ? v - e : v; e = absE ? v : e + v }
      }

      if (hasE && de > 1e-6) {
        // An extrusion move continues the polyline, opening one if needed.
        if (poly.length === 0) { poly.push(new Vector3(x, z, y)); count++ }
        poly.push(new Vector3(nx, nz, ny)); count++
        if (nz < minH) minH = nz
        if (nz > maxH) maxH = nz
        heights.add(Math.round(nz * 100) / 100)
      } else {
        flush() // Reisebewegung unterbricht den Weg
      }
      x = nx; y = ny; z = nz
    }
    flush()
    if (polylines.length === 0) return null
    return { polylines, minH, maxH, layerH: estimateLayerHeight(heights) }
  }

  /** The median height difference approximates the layer height; 0.2 mm when it cannot be told. */
  const estimateLayerHeight = (heights: Set<number>): number => {
    const sorted = [...heights].sort((a, b) => a - b)
    const diffs: number[] = []
    for (let i = 1; i < sorted.length; i++) { const d = sorted[i] - sorted[i - 1]; if (d > 1e-3) diffs.push(d) }
    if (diffs.length === 0) return 0.2
    diffs.sort((a, b) => a - b)
    return diffs[Math.floor(diffs.length / 2)] || 0.2
  }

  /** Builds the toolpath lines: either the height gradient ("heat") or one flat colour. */
  const buildGcodeLines = (data: { polylines: Vector3[][]; minH: number; maxH: number }, scene: Scene): Mesh => {
    const { polylines, minH, maxH } = data
    if (gcodeLineColor() === 'custom') {
      const mesh = MeshBuilder.CreateLineSystem('gcode', { lines: polylines }, scene)
      mesh.color = Color3.FromHexString(gcodeColor())
      mesh.isPickable = false
      return mesh
    }
    const range = maxH - minH || 1
    const colors: Color4[][] = polylines.map(pl => pl.map(pt => heightColor((pt.y - minH) / range)))
    const mesh = MeshBuilder.CreateLineSystem('gcode', { lines: polylines, colors, useVertexAlpha: false }, scene)
    mesh.isPickable = false
    return mesh
  }

  /**
   * Turns the extrusion paths into an opaque, lit "solid" body - for looking at,
   * not for export: every segment becomes an oriented box (roughly extrusion
   * width × layer height) written into one shared mesh buffer. Past
   * {@link MAX_SOLID_SEGMENTS} the segments are thinned out to keep the
   * geometry manageable.
   */
  const buildGcodeSolid = (data: { polylines: Vector3[][]; layerH: number }, scene: Scene): Mesh => {
    const MAX_SOLID_SEGMENTS = 140_000
    const halfH = Math.max(data.layerH, 0.05) / 2
    const halfW = Math.max(data.layerH * 1.8, 0.1) / 2   // Raupenbreite ≈ 1,8 × Schichthöhe
    let segCount = 0
    for (const pl of data.polylines) segCount += pl.length - 1
    const stride = segCount > MAX_SOLID_SEGMENTS ? Math.ceil(segCount / MAX_SOLID_SEGMENTS) : 1

    const positions: number[] = []
    const indices: number[] = []
    const worldUp = new Vector3(0, 1, 0), altUp = new Vector3(0, 0, 1)
    // Box indices (12 triangles) wound outwards; the base is the box's first vertex.
    const BOX = [0,1,5, 0,5,4,  3,6,2, 3,7,6,  0,7,3, 0,4,7,  1,6,5, 1,2,6,  0,3,2, 0,2,1,  4,5,6, 4,6,7]

    for (const pl of data.polylines) {
      for (let i = 0; i + 1 < pl.length; i += stride) {
        const a = pl[i], b = pl[Math.min(i + stride, pl.length - 1)]
        const dir = b.subtract(a)
        const len = dir.length()
        if (len < 1e-5) continue
        dir.scaleInPlace(1 / len)
        const ref = Math.abs(dir.y) > 0.99 ? altUp : worldUp
        const right = Vector3.Cross(dir, ref); right.normalize(); right.scaleInPlace(halfW)
        const up = Vector3.Cross(right, dir); up.normalize(); up.scaleInPlace(halfH)
        const base = positions.length / 3
        // 8 Ecken: a∓right∓up (0..3), b∓right∓up (4..7)
        for (const p of [a, b]) {
          positions.push(p.x - right.x - up.x, p.y - right.y - up.y, p.z - right.z - up.z)
          positions.push(p.x + right.x - up.x, p.y + right.y - up.y, p.z + right.z - up.z)
          positions.push(p.x + right.x + up.x, p.y + right.y + up.y, p.z + right.z + up.z)
          positions.push(p.x - right.x + up.x, p.y - right.y + up.y, p.z - right.z + up.z)
        }
        for (const idx of BOX) indices.push(base + idx)
      }
    }

    const vd = new VertexData()
    vd.positions = positions; vd.indices = indices
    const mesh = new Mesh('gcodeSolid', scene)
    vd.applyToMesh(mesh)
    // Flat shading (per-facet normals) instead of the smoothed per-corner normals ComputeNormals
    // gives. On the visible top layer, differently-oriented beads (perimeter loop vs. straight
    // infill) overlap coplanar at the exact same height - an unbreakable depth tie that no clip-plane
    // precision can resolve. Smoothed, their shading differs so the tie shimmers; flat-shaded, every
    // top face's normal is exactly (0,1,0), the overlapping faces render identically, and the tie
    // becomes invisible. convertToFlatShadedMesh also computes the normals, so we skip ComputeNormals.
    mesh.convertToFlatShadedMesh()
    const mat = new StandardMaterial('gcodeSolidMat', scene)
    mat.diffuseColor = Color3.FromHexString(gcodeColor())
    mat.specularColor = new Color3(0.15, 0.15, 0.15)
    mesh.material = mat
    gcodeMat = mat
    mesh.isPickable = false
    return mesh
  }

  /** (Re)builds the g-code mesh for the current display and colour mode, without re-parsing. */
  const renderGcode = () => {
    if (!scene || !gcodeData) return
    if (gcodePath) { gcodePath.dispose(); gcodePath = undefined }
    gcodeMat = undefined
    gcodePath = gcodeMode() === 'solid'
      ? buildGcodeSolid(gcodeData, scene)
      : buildGcodeLines(gcodeData, scene)
    // The grid and the plate need no rebuild: the bounds are practically the same
    // for lines and solid, and the overlays do not depend on the mesh identity.
    // The camera stays where it is.
  }

  const applyGcodeMode = (mode: 'lines' | 'solid') => {
    setGcodeMode(mode)
    localStorage.setItem(VIEWER_GCODE_MODE_KEY, mode)
    renderGcode()
  }
  const applyGcodeLineColor = (mode: 'heat' | 'custom') => {
    setGcodeLineColor(mode)
    localStorage.setItem(VIEWER_GCODE_LINECOLOR_KEY, mode)
    if (gcodeMode() === 'lines') renderGcode()   // heat↔custom wechselt Vertex-Farben vs. flache Farbe
  }
  /** Live colour change: updates the solid material or the flat line colour without a rebuild. */
  const applyGcodeColor = (raw: string) => {
    const hex = normalizeHex(raw)
    setGcodeColor(hex)
    localStorage.setItem(VIEWER_GCODE_COLOR_KEY, hex)
    if (gcodeMat) gcodeMat.diffuseColor = Color3.FromHexString(hex)
    if (gcodePath && gcodeMode() === 'lines' && gcodeLineColor() === 'custom') (gcodePath as LinesMesh).color = Color3.FromHexString(hex)
  }

  // ── Controls ───────────────────────────────────────────────────────────────

  /** Resets the camera to the initial radius and target recorded after model load. */
  const resetView = () => {
    if (!camera) return
    fitView()  // recompute framing for the current model + bed state
    camera.alpha = VIEW_ALPHA
    camera.beta = VIEW_BETA
    camera.radius = initialRadius
    camera.target = initialTarget.clone()
  }

  /**
   * Frames the camera on a single picked mesh and re-centres the orbit pivot on it,
   * so the user can rotate around the chosen part. Keeps the current view angle.
   */
  const focusOnMesh = (mesh: AbstractMesh) => {
    if (!camera) return
    mesh.computeWorldMatrix(true)
    const bb = mesh.getBoundingInfo().boundingBox
    const min = bb.minimumWorld, max = bb.maximumWorld
    const size = Math.max(max.x - min.x, max.y - min.y, max.z - min.z)
    // setTarget moves the orbit pivot (recomputing alpha/beta/radius to hold position);
    // then radius zooms in to frame the part.
    camera.setTarget(bb.centerWorld.clone())
    camera.radius = Math.max(size * 1.8, camera.lowerRadiusLimit ?? 0.1)
  }

  return (
    <div style={{ position: 'fixed', inset: '0', background: 'rgba(0,0,0,0.92)', 'z-index': '1000', display: 'flex', 'flex-direction': 'column' }}>
      {/* Header */}
      <div style={{ display: 'flex', 'align-items': 'center', gap: '14px', padding: '12px 20px', background: 'rgba(0,0,0,0.6)', 'border-bottom': '1px solid rgba(255,255,255,0.08)', 'flex-shrink': '0' }}>
        <span style={{ 'font-family': "'DM Sans',sans-serif", 'font-size': '15px', 'font-weight': '600', color: '#fff', flex: '1', overflow: 'hidden', 'text-overflow': 'ellipsis', 'white-space': 'nowrap' }}>
          {props.designName}
        </span>
        <Show when={formatLabel()}>
          <span style={{ 'font-family': "'DM Mono',monospace", 'font-size': '11px', background: 'rgba(74,144,217,0.2)', color: '#4a90d9', 'border-radius': '5px', padding: '2px 8px', 'flex-shrink': '0' }}>
            {formatLabel()}
          </span>
        </Show>
        <span style={{ 'font-family': "'DM Mono',monospace", 'font-size': '11px', color: 'rgba(255,255,255,0.4)', 'flex-shrink': '0' }}>
          {translate('viewer_controls_hint')}
        </span>
        {/* Werkzeuge: für alle Typen sichtbar (Foto funktioniert überall). "Messen" ist im Menü
            weiterhin nur für nicht-generierte Meshes (STL/OBJ/3MF) - bei G-code fehlt der Punkt. */}
        {/* Unavailable until the model is there. Every tool behind it works on the
            loaded geometry - measuring needs a surface to pick, splitting needs
            triangles to walk - so offering them mid-load can only disappoint. */}
        <button onClick={() => { if (isLoading()) return; setToolsOpen(o => !o); setColorsOpen(false); setSettingsOpen(false) }}
          disabled={isLoading()}
          title={isLoading() ? translate('viewer_tools_wait') : translate('viewer_btn_tools')}
          style={{ background: toolsOpen() || measureActive() ? 'rgba(74,144,217,0.25)' : 'rgba(255,255,255,0.08)', border: `1px solid ${toolsOpen() || measureActive() ? 'rgba(74,144,217,0.6)' : 'rgba(255,255,255,0.15)'}`, 'border-radius': '8px', padding: '6px 14px', color: '#fff', 'font-size': '12px', cursor: isLoading() ? 'not-allowed' : 'pointer', opacity: isLoading() ? '0.4' : '1', 'font-family': "'DM Sans',sans-serif", 'flex-shrink': '0' }}>
          {translate('viewer_btn_tools')}
        </button>
        <Show when={colorGroups().length > 0}>
          <button onClick={() => { setColorsOpen(o => !o); setSettingsOpen(false); setToolsOpen(false) }}
            style={{ background: colorsOpen() ? 'rgba(74,144,217,0.25)' : 'rgba(255,255,255,0.08)', border: `1px solid ${colorsOpen() ? 'rgba(74,144,217,0.6)' : 'rgba(255,255,255,0.15)'}`, 'border-radius': '8px', padding: '6px 14px', color: '#fff', 'font-size': '12px', cursor: 'pointer', 'font-family': "'DM Sans',sans-serif", 'flex-shrink': '0' }}>
            {translate('viewer_btn_colors')}
          </button>
        </Show>
        <button onClick={() => { setSettingsOpen(o => !o); setColorsOpen(false); setToolsOpen(false) }}
          style={{ background: settingsOpen() ? 'rgba(74,144,217,0.25)' : 'rgba(255,255,255,0.08)', border: `1px solid ${settingsOpen() ? 'rgba(74,144,217,0.6)' : 'rgba(255,255,255,0.15)'}`, 'border-radius': '8px', padding: '6px 14px', color: '#fff', 'font-size': '12px', cursor: 'pointer', 'font-family': "'DM Sans',sans-serif", 'flex-shrink': '0' }}>
          {translate('viewer_btn_settings')}
        </button>
        <button onClick={resetView} title={translate('viewer_btn_reset')}
          style={{ background: 'rgba(255,255,255,0.08)', border: '1px solid rgba(255,255,255,0.15)', 'border-radius': '8px', padding: '6px 14px', color: '#fff', 'font-size': '12px', cursor: 'pointer', 'font-family': "'DM Sans',sans-serif", 'flex-shrink': '0' }}>
          {translate('viewer_btn_reset')}
        </button>
        <button onClick={props.onClose}
          style={{ background: 'none', border: 'none', color: '#fff', cursor: 'pointer', 'font-size': '24px', 'line-height': '1', 'flex-shrink': '0', opacity: '0.7', padding: '0 4px' }}>
          ×
        </button>
      </div>

      {/* Canvas */}
      <div style={{ flex: '1', position: 'relative', overflow: 'hidden' }}>
        <canvas ref={canvasRef} style={{ display: 'block', width: '100%', height: '100%' }} />

        {/* Left plate switcher - one card per build plate (thumbnail + name + filament colours) */}
        <Show when={platesUI().length > 1}>
          <div style={{ position: 'absolute', top: '14px', left: '14px', bottom: '14px', width: '154px', display: 'flex', 'flex-direction': 'column', gap: '8px', 'z-index': '6' }}>
            <span style={{ 'font-family': "'DM Sans',sans-serif", 'font-size': '11px', 'font-weight': '600', 'letter-spacing': '0.04em', 'text-transform': 'uppercase', color: 'rgba(255,255,255,0.5)', padding: '0 2px', 'flex-shrink': '0' }}>
              {translate('viewer_plates_title')}
            </span>
            <div style={{ display: 'flex', 'flex-direction': 'column', gap: '8px', 'overflow-y': 'auto', 'padding-right': '2px', 'min-height': '0' }}>
              <For each={platesUI()}>{(plate, i) => (
                <button onClick={() => selectPlate(i())}
                  title={plate.name || `${translate('viewer_plate')} ${i() + 1}`}
                  style={{
                    background: activePlate() === i() ? 'rgba(74,144,217,0.16)' : 'rgba(13,17,23,0.85)',
                    border: `1px solid ${activePlate() === i() ? 'rgba(74,144,217,0.7)' : 'rgba(255,255,255,0.12)'}`,
                    'border-radius': '10px', padding: '8px', display: 'flex', 'flex-direction': 'column', gap: '6px',
                    cursor: 'pointer', 'backdrop-filter': 'blur(6px)', 'flex-shrink': '0', 'text-align': 'left',
                    'box-shadow': activePlate() === i() ? '0 4px 16px rgba(0,0,0,0.35)' : 'none',
                  }}>
                  <div style={{ position: 'relative', width: '100%', 'aspect-ratio': '1 / 1', 'border-radius': '6px', background: 'rgba(255,255,255,0.05)', overflow: 'hidden', display: 'flex', 'align-items': 'center', 'justify-content': 'center' }}>
                    <Show when={plate.thumbnail}
                      fallback={<span style={{ 'font-family': "'DM Sans',sans-serif", 'font-size': '22px', 'font-weight': '700', color: 'rgba(255,255,255,0.35)' }}>{i() + 1}</span>}>
                      <img src={plate.thumbnail} alt="" style={{ width: '100%', height: '100%', 'object-fit': 'contain' }} />
                    </Show>
                    <span style={{ position: 'absolute', top: '4px', left: '5px', 'font-family': "'DM Mono',monospace", 'font-size': '10px', 'font-weight': '600', color: '#fff', background: 'rgba(0,0,0,0.55)', 'border-radius': '4px', padding: '0 5px', 'line-height': '15px' }}>
                      {i() + 1}
                    </span>
                  </div>
                  <div style={{ display: 'flex', 'align-items': 'center', 'justify-content': 'space-between', gap: '6px' }}>
                    <span style={{ 'font-family': "'DM Sans',sans-serif", 'font-size': '11px', color: activePlate() === i() ? '#fff' : 'rgba(255,255,255,0.7)', overflow: 'hidden', 'text-overflow': 'ellipsis', 'white-space': 'nowrap' }}>
                      {plate.name || `${translate('viewer_plate')} ${i() + 1}`}
                    </span>
                    <Show when={plate.colorIdx.length > 0}>
                      <span style={{ display: 'flex', gap: '3px', 'flex-shrink': '0' }}>
                        <For each={plate.colorIdx}>{(idx) => (
                          <span title={colorGroups()[idx]?.hex}
                            style={{ width: '11px', height: '11px', 'border-radius': '50%', background: colorGroups()[idx]?.hex || '#888', border: '1px solid rgba(255,255,255,0.35)', 'box-shadow': '0 0 0 1px rgba(0,0,0,0.3)' }} />
                        )}</For>
                      </span>
                    </Show>
                  </div>
                </button>
              )}</For>
            </div>
          </div>
        </Show>

        {/* Live colour panel - filament groups for multi-colour 3MF, a single swatch otherwise */}
        <Show when={colorsOpen() && colorGroups().length > 0}>
          <div style={{ position: 'absolute', top: '14px', right: '14px', background: 'rgba(13,17,23,0.88)', border: '1px solid rgba(255,255,255,0.12)', 'border-radius': '10px', padding: '12px 14px', display: 'flex', 'flex-direction': 'column', gap: '8px', 'min-width': '180px', 'max-width': '260px', 'backdrop-filter': 'blur(6px)', 'box-shadow': '0 8px 24px rgba(0,0,0,0.4)', 'z-index': '6' }}>
            <div style={{ display: 'flex', 'align-items': 'center', 'justify-content': 'space-between', gap: '10px', 'margin-bottom': '2px' }}>
              <span style={{ 'font-family': "'DM Sans',sans-serif", 'font-size': '12px', 'font-weight': '600', color: 'rgba(255,255,255,0.85)' }}>
                {translate('viewer_colors_title')}
              </span>
              <button onClick={resetColors}
                style={{ background: 'rgba(255,255,255,0.08)', border: '1px solid rgba(255,255,255,0.15)', 'border-radius': '6px', padding: '3px 9px', color: 'rgba(255,255,255,0.8)', 'font-size': '11px', cursor: 'pointer', 'font-family': "'DM Sans',sans-serif", 'flex-shrink': '0' }}>
                {translate('viewer_colors_reset')}
              </button>
            </div>
            <For each={colorGroups()}>{(group, i) => (
              <label style={{ display: 'flex', 'align-items': 'center', gap: '10px', cursor: 'pointer' }}>
                <input type="color" value={group.hex} onInput={e => setGroupColor(i(), e.currentTarget.value)}
                  style={{ width: '26px', height: '26px', 'min-width': '26px', padding: '0', border: '1px solid rgba(255,255,255,0.2)', 'border-radius': '6px', background: 'none', cursor: 'pointer' }} />
                <span style={{ 'font-family': "'DM Sans',sans-serif", 'font-size': '12px', color: 'rgba(255,255,255,0.75)', overflow: 'hidden', 'text-overflow': 'ellipsis', 'white-space': 'nowrap' }}>
                  {group.label}
                </span>
              </label>
            )}</For>
          </div>
        </Show>

        {/* Tools panel - selectable viewer tools */}
        <Show when={toolsOpen()}>
          <div style={{ position: 'absolute', top: '14px', right: '14px', background: 'rgba(13,17,23,0.88)', border: '1px solid rgba(255,255,255,0.12)', 'border-radius': '10px', padding: '12px 14px', display: 'flex', 'flex-direction': 'column', gap: '8px', 'min-width': '200px', 'max-width': '260px', 'backdrop-filter': 'blur(6px)', 'box-shadow': '0 8px 24px rgba(0,0,0,0.4)', 'z-index': '6' }}>
            <span style={{ 'font-family': "'DM Sans',sans-serif", 'font-size': '12px', 'font-weight': '600', color: 'rgba(255,255,255,0.85)' }}>
              {translate('viewer_tools_title')}
            </span>
            {/* Messen: nur für nicht-generierte Meshes (STL/OBJ/3MF) – braucht pickbare Oberflächen. */}
            <Show when={!isGcode()}>
              <button onClick={() => setMeasure(!measureActive())}
                style={{ display: 'flex', 'align-items': 'center', 'justify-content': 'space-between', gap: '10px', background: measureActive() ? 'rgba(74,144,217,0.22)' : 'rgba(255,255,255,0.05)', border: `1px solid ${measureActive() ? 'rgba(74,144,217,0.6)' : 'rgba(255,255,255,0.12)'}`, 'border-radius': '8px', padding: '8px 10px', color: '#fff', cursor: 'pointer', 'font-family': "'DM Sans',sans-serif", 'font-size': '12px' }}>
                <span>📏 {translate('viewer_tool_measure')}</span>
                <span style={{ 'font-size': '10px', color: measureActive() ? '#9ec5ff' : 'rgba(255,255,255,0.4)', 'font-family': "'DM Mono',monospace" }}>
                  {measureActive() ? translate('viewer_tool_on') : translate('viewer_tool_off')}
                </span>
              </button>
            </Show>
            {/* Zerlegen: nur für Meshes. Ein G-code-Pfad hat keine Objekte, die man trennen könnte. */}
            <Show when={!isGcode()}>
              <button onClick={openSplitIntro}
                style={{ display: 'flex', 'align-items': 'center', gap: '10px', background: 'rgba(255,255,255,0.05)', border: '1px solid rgba(255,255,255,0.12)', 'border-radius': '8px', padding: '8px 10px', color: '#fff', cursor: 'pointer', 'font-family': "'DM Sans',sans-serif", 'font-size': '12px' }}>
                <span>✂️ {translate('viewer_tool_split')}</span>
              </button>
            </Show>
            {/* Foto erstellen: für alle Typen (STL/OBJ/3MF + G-code) – arbeitet auf dem Framebuffer. */}
            <button onClick={startPhoto}
              style={{ display: 'flex', 'align-items': 'center', gap: '10px', background: 'rgba(255,255,255,0.05)', border: '1px solid rgba(255,255,255,0.12)', 'border-radius': '8px', padding: '8px 10px', color: '#fff', cursor: 'pointer', 'font-family': "'DM Sans',sans-serif", 'font-size': '12px' }}>
              <span>📷 {translate('viewer_tool_photo')}</span>
            </button>
          </div>
        </Show>

        {/* Measure tool: floating distance label + status HUD */}
        <Show when={measureDist() !== null}>
          <div ref={el => (measureLabelEl = el)}
            style={{ position: 'absolute', display: 'none', transform: 'translate(-50%,-140%)', background: 'rgba(20,16,4,0.9)', border: '1px solid rgba(255,209,63,0.7)', color: '#ffd23f', padding: '3px 8px', 'border-radius': '6px', 'font-family': "'DM Mono',monospace", 'font-size': '12px', 'font-weight': '600', 'white-space': 'nowrap', 'pointer-events': 'none', 'z-index': '7' }}>
            {measureDist()!.toFixed(1)} mm
          </div>
        </Show>
        <Show when={measureActive()}>
          <div style={{ position: 'absolute', bottom: '18px', left: '50%', transform: 'translateX(-50%)', background: 'rgba(13,17,23,0.9)', border: '1px solid rgba(255,255,255,0.14)', 'border-radius': '10px', padding: '8px 14px', display: 'flex', 'align-items': 'center', gap: '14px', 'backdrop-filter': 'blur(6px)', 'box-shadow': '0 8px 24px rgba(0,0,0,0.4)', 'z-index': '7' }}>
            <span style={{ 'font-family': "'DM Sans',sans-serif", 'font-size': '13px', color: '#fff' }}>
              {measureDist() !== null
                ? `${translate('viewer_measure_distance')}: ${measureDist()!.toFixed(1)} mm`
                : measureCount() === 1
                  ? translate('viewer_measure_hint2')
                  : translate('viewer_measure_hint1')}
            </span>
            <Show when={measureCount() > 0}>
              <button onClick={clearMeasure}
                style={{ background: 'rgba(255,255,255,0.08)', border: '1px solid rgba(255,255,255,0.15)', 'border-radius': '6px', padding: '3px 10px', color: 'rgba(255,255,255,0.85)', 'font-size': '11px', cursor: 'pointer', 'font-family': "'DM Sans',sans-serif" }}>
                {translate('viewer_measure_reset')}
              </button>
            </Show>
          </div>
        </Show>

        {/* Photo tool - framing overlay: drag to draw an optional crop rectangle (camera is locked). */}
        <Show when={photoMode() === 'aim'}>
          <div onPointerDown={photoDragDown} onPointerMove={photoDragMove} onPointerUp={photoDragUp}
            style={{ position: 'absolute', inset: '0', cursor: 'crosshair', 'z-index': '8', overflow: 'hidden' }}>
            <Show when={photoRegion()}>
              {(r) => (
                <div style={{ position: 'absolute', left: `${r().x}px`, top: `${r().y}px`, width: `${r().w}px`, height: `${r().h}px`,
                  border: '1.5px dashed #4a90d9', 'box-shadow': '0 0 0 9999px rgba(0,0,0,0.28)', 'pointer-events': 'none' }} />
              )}
            </Show>
          </div>
          {/* Top-centre confirm / cancel bar */}
          <div style={{ position: 'absolute', top: '16px', left: '50%', transform: 'translateX(-50%)', background: 'rgba(13,17,23,0.92)', border: '1px solid rgba(255,255,255,0.14)', 'border-radius': '12px', padding: '8px 10px 8px 16px', display: 'flex', 'align-items': 'center', gap: '12px', 'backdrop-filter': 'blur(6px)', 'box-shadow': '0 8px 24px rgba(0,0,0,0.45)', 'z-index': '9' }}>
            <span style={{ 'font-family': "'DM Sans',sans-serif", 'font-size': '12px', color: 'rgba(255,255,255,0.85)', 'white-space': 'nowrap' }}>
              {translate(photoRegion() ? 'viewer_photo_region_hint' : 'viewer_photo_aim_hint')}
            </span>
            <Show when={photoRegion()}>
              <button onClick={() => setPhotoRegion(null)}
                style={{ background: 'rgba(255,255,255,0.08)', border: '1px solid rgba(255,255,255,0.15)', 'border-radius': '7px', padding: '4px 10px', color: 'rgba(255,255,255,0.85)', 'font-size': '11px', cursor: 'pointer', 'font-family': "'DM Sans',sans-serif", 'white-space': 'nowrap' }}>
                {translate('viewer_photo_region_clear')}
              </button>
            </Show>
            <button onClick={takePhoto} title={translate('viewer_photo_take')}
              style={{ display: 'flex', 'align-items': 'center', 'justify-content': 'center', width: '34px', height: '34px', background: 'rgba(46,160,67,0.9)', border: '1px solid rgba(46,160,67,1)', 'border-radius': '9px', color: '#fff', 'font-size': '17px', cursor: 'pointer' }}>
              ✓
            </button>
            <button onClick={exitPhoto} title={translate('viewer_photo_cancel')}
              style={{ display: 'flex', 'align-items': 'center', 'justify-content': 'center', width: '34px', height: '34px', background: 'rgba(255,255,255,0.08)', border: '1px solid rgba(255,255,255,0.2)', 'border-radius': '9px', color: '#fff', 'font-size': '16px', cursor: 'pointer' }}>
              ✕
            </button>
          </div>
        </Show>

        {/* Split tool. One panel across all steps: explanation, progress, result.
            Placed centrally until the result arrives, then parked at the right edge
            and draggable by its header so it need not cover the model. */}
        <Show when={splitPhase() !== 'idle'}>
          <div style={{
            position: 'absolute',
            ...(splitPos()
              ? { left: `${splitPos()!.x}px`, top: `${splitPos()!.y}px` }
              : { top: '50%', left: '50%', transform: 'translate(-50%,-50%)' }),
            width: '420px', 'max-width': 'calc(100vw - 32px)',
            background: 'rgba(13,17,23,0.96)', border: '1px solid rgba(255,255,255,0.16)',
            'border-radius': '14px', display: 'flex', 'flex-direction': 'column',
            'backdrop-filter': 'blur(8px)', 'box-shadow': '0 12px 40px rgba(0,0,0,0.55)', 'z-index': '9',
          }}>
            {/* Header: drag handle and the only close control. */}
            <div onPointerDown={splitPhase() === 'done' ? startSplitDrag : undefined}
              style={{ display: 'flex', 'align-items': 'center', 'justify-content': 'space-between', gap: '12px', padding: '14px 16px', 'border-bottom': '1px solid rgba(255,255,255,0.1)', cursor: splitPhase() === 'done' ? 'move' : 'default' }}>
              <span style={{ 'font-family': "'DM Sans',sans-serif", 'font-size': '14px', 'font-weight': '600', color: '#fff' }}>
                {translate('viewer_split_title')}
              </span>
              <button onClick={closeSplit} title={translate('viewer_split_close')}
                style={{ background: 'none', border: 'none', color: 'rgba(255,255,255,0.55)', cursor: 'pointer', padding: '2px', display: 'flex', 'align-items': 'center' }}>
                <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4">
                  <line x1="18" y1="6" x2="6" y2="18" /><line x1="6" y1="6" x2="18" y2="18" />
                </svg>
              </button>
            </div>

            <div style={{ padding: '16px', display: 'flex', 'flex-direction': 'column', gap: '14px' }}>

              {/* ── Step 1: what this does, and what to run it on ───────────── */}
              <Show when={splitPhase() === 'intro'}>
                <span style={{ 'font-family': "'DM Sans',sans-serif", 'font-size': '13px', 'line-height': '1.55', color: 'rgba(255,255,255,0.78)' }}>
                  {translate('viewer_split_explain')}
                </span>
                <Show when={builtPlates.length > 1}>
                  <label style={{ display: 'flex', 'flex-direction': 'column', gap: '6px' }}>
                    <span style={{ 'font-family': "'DM Sans',sans-serif", 'font-size': '12px', color: 'rgba(255,255,255,0.6)' }}>
                      {translate('viewer_split_plate_label')}
                    </span>
                    <select value={String(splitPlate())} onChange={e => setSplitPlate(e.currentTarget.value === 'all' ? 'all' : Number(e.currentTarget.value))}
                      style={{ background: 'rgba(255,255,255,0.07)', border: '1px solid rgba(255,255,255,0.16)', 'border-radius': '8px', padding: '7px 9px', color: '#fff', 'font-size': '13px', 'font-family': "'DM Sans',sans-serif", 'color-scheme': 'dark' }}>
                      <option value="all">{translate('viewer_split_plate_all')}</option>
                      <For each={platesUI()}>{(plate, index) => (
                        <option value={String(index())}>{plate.name || translate('viewer_split_plate_n', { n: String(index() + 1) })}</option>
                      )}</For>
                    </select>
                  </label>
                </Show>
                <div style={{ display: 'flex', gap: '8px', 'justify-content': 'flex-end' }}>
                  <button onClick={closeSplit}
                    style={{ background: 'rgba(255,255,255,0.06)', border: '1px solid rgba(255,255,255,0.15)', 'border-radius': '8px', padding: '8px 14px', color: 'rgba(255,255,255,0.8)', 'font-size': '13px', cursor: 'pointer', 'font-family': "'DM Sans',sans-serif" }}>
                    {translate('viewer_split_cancel')}
                  </button>
                  <button onClick={runSplit}
                    style={{ background: 'rgba(74,144,217,0.3)', border: '1px solid rgba(74,144,217,0.7)', 'border-radius': '8px', padding: '8px 14px', color: '#fff', 'font-size': '13px', 'font-weight': '600', cursor: 'pointer', 'font-family': "'DM Sans',sans-serif" }}>
                    {translate('viewer_split_start')}
                  </button>
                </div>
              </Show>

              {/* ── Step 2: separating ───────────────────────────────────────── */}
              <Show when={splitPhase() === 'busy'}>
                <span style={{ 'font-family': "'DM Sans',sans-serif", 'font-size': '13px', color: 'rgba(255,255,255,0.78)' }}>
                  {translate('viewer_split_working')}
                </span>
                <div style={{ display: 'flex', 'align-items': 'center', gap: '10px' }}>
                  <div style={{ flex: '1', height: '6px', background: 'rgba(255,255,255,0.1)', 'border-radius': '3px', overflow: 'hidden' }}>
                    <div style={{ height: '100%', width: `${Math.round(splitProgress() * 100)}%`, background: '#4a90d9', transition: 'width 120ms linear' }} />
                  </div>
                  <span style={{ 'font-family': "'DM Mono',monospace", 'font-size': '13px', color: '#9ec5ff', 'min-width': '42px', 'text-align': 'right' }}>
                    {Math.round(splitProgress() * 100)}%
                  </span>
                </div>
              </Show>

              {/* ── Step 3: the parts ────────────────────────────────────────── */}
              <Show when={splitPhase() === 'done'}>
                <Show when={!splitError() && splitNames().length > 1} fallback={
                  <span style={{ 'font-family': "'DM Sans',sans-serif", 'font-size': '13px', color: 'rgba(255,255,255,0.78)' }}>
                    {splitError() ? translate('viewer_split_failed') : translate('viewer_split_single')}
                  </span>
                }>
                  <div style={{ display: 'flex', 'align-items': 'center', 'justify-content': 'space-between', gap: '10px' }}>
                    <span style={{ 'font-family': "'DM Sans',sans-serif", 'font-size': '13px', color: 'rgba(255,255,255,0.78)' }}>
                      {translate('viewer_split_found', { count: String(splitNames().length) })}
                    </span>
                    <div style={{ display: 'flex', gap: '6px' }}>
                      <button onClick={() => setAllSplitChoices(true)}
                        style={{ background: 'rgba(255,255,255,0.06)', border: '1px solid rgba(255,255,255,0.14)', 'border-radius': '6px', padding: '4px 9px', color: 'rgba(255,255,255,0.8)', 'font-size': '11px', cursor: 'pointer', 'font-family': "'DM Sans',sans-serif" }}>
                        {translate('viewer_split_select_all')}
                      </button>
                      <button onClick={() => setAllSplitChoices(false)}
                        style={{ background: 'rgba(255,255,255,0.06)', border: '1px solid rgba(255,255,255,0.14)', 'border-radius': '6px', padding: '4px 9px', color: 'rgba(255,255,255,0.8)', 'font-size': '11px', cursor: 'pointer', 'font-family': "'DM Sans',sans-serif" }}>
                        {translate('viewer_split_select_none')}
                      </button>
                    </div>
                  </div>

                  <div style={{ 'max-height': '260px', 'overflow-y': 'auto', display: 'flex', 'flex-direction': 'column', gap: '3px', border: '1px solid rgba(255,255,255,0.08)', 'border-radius': '8px', padding: '5px' }}>
                    {/* Magnifier points the part out in the model, the rest of the row is a
                        plain selection toggle. Two separate targets, so neither click has to
                        guess which of the two the user meant. */}
                    <For each={splitNames()}>{(name, index) => (
                      <div onClick={() => toggleSplitChoice(index())}
                        title={translate('viewer_split_box_hint')}
                        style={{ display: 'flex', 'align-items': 'center', gap: '8px', background: splitFlash() === index() ? 'rgba(255,210,63,0.16)' : 'transparent', 'border-radius': '6px', padding: '5px 7px', color: 'rgba(255,255,255,0.8)', 'font-size': '12px', cursor: 'pointer', 'font-family': "'DM Mono',monospace", width: '100%', 'box-sizing': 'border-box' }}>
                        <button onClick={event => { event.stopPropagation(); flashSplitPart(index()) }}
                          title={translate('viewer_split_row_hint')}
                          style={{ background: 'none', border: 'none', padding: '2px', cursor: 'pointer', display: 'flex', 'align-items': 'center', color: splitFlash() === index() ? '#ffd23f' : 'rgba(255,255,255,0.45)', 'flex-shrink': '0' }}>
                          <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4">
                            <circle cx="11" cy="11" r="7" /><line x1="21" y1="21" x2="16.2" y2="16.2" />
                          </svg>
                        </button>
                        <span style={{ width: '14px', height: '14px', 'border-radius': '3px', border: `1.5px solid ${splitChosen()[index()] ? '#4a90d9' : 'rgba(255,255,255,0.35)'}`, background: splitChosen()[index()] ? '#4a90d9' : 'transparent', 'flex-shrink': '0', display: 'flex', 'align-items': 'center', 'justify-content': 'center' }}>
                          <Show when={splitChosen()[index()]}>
                            <svg width="9" height="9" viewBox="0 0 24 24" fill="none" stroke="#fff" stroke-width="4"><polyline points="20 6 9 17 4 12" /></svg>
                          </Show>
                        </span>
                        <span style={{ overflow: 'hidden', 'text-overflow': 'ellipsis', 'white-space': 'nowrap', flex: '1' }}>{name}</span>
                        <Show when={splitAdded()[index()]}>
                          <span title={translate('viewer_split_added')} style={{ color: '#7fd18b', 'flex-shrink': '0', display: 'flex', 'align-items': 'center' }}>
                            <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3"><polyline points="20 6 9 17 4 12" /></svg>
                          </span>
                        </Show>
                      </div>
                    )}</For>
                  </div>

                  <div style={{ display: 'flex', gap: '8px', 'flex-wrap': 'wrap' }}>
                    <button onClick={downloadSplit} disabled={chosenIndices().length === 0}
                      style={{ background: 'rgba(74,144,217,0.28)', border: '1px solid rgba(74,144,217,0.65)', 'border-radius': '8px', padding: '8px 13px', color: '#fff', 'font-size': '12px', cursor: chosenIndices().length === 0 ? 'default' : 'pointer', opacity: chosenIndices().length === 0 ? '0.45' : '1', 'font-family': "'DM Sans',sans-serif" }}>
                      ⭳ {translate('viewer_split_download')}
                    </button>
                    <Show when={props.onSaveFiles}>
                      {/* Offers only what is not in the design yet, so a second click
                          cannot resend a part the version already has by that name. */}
                      <button onClick={addSplitToDesign} disabled={splitSaving() || pendingIndices().length === 0}
                        style={{ background: 'rgba(255,255,255,0.06)', border: '1px solid rgba(255,255,255,0.15)', 'border-radius': '8px', padding: '8px 13px', color: pendingIndices().length === 0 && chosenIndices().length > 0 ? '#7fd18b' : '#fff', 'font-size': '12px', cursor: splitSaving() || pendingIndices().length === 0 ? 'default' : 'pointer', opacity: pendingIndices().length === 0 ? '0.55' : '1', 'font-family': "'DM Sans',sans-serif" }}>
                        {splitSaving() ? translate('viewer_split_adding')
                          : pendingIndices().length === 0 && chosenIndices().length > 0 ? translate('viewer_split_added')
                          : translate('viewer_split_add')}
                      </button>
                    </Show>
                  </div>
                  <Show when={splitAddError()}>
                    <span style={{ 'font-family': "'DM Sans',sans-serif", 'font-size': '11px', color: '#ff8a8a' }}>
                      {translate('viewer_split_add_failed')} ({splitAddError()})
                    </span>
                  </Show>
                </Show>
              </Show>
            </div>
          </div>
        </Show>

        {/* Photo tool - review: preview the capture, then download and/or add to the design gallery. */}
        <Show when={photoMode() === 'review' && photoUrl()}>
          <div style={{ position: 'absolute', inset: '0', background: 'rgba(0,0,0,0.6)', 'backdrop-filter': 'blur(4px)', display: 'flex', 'align-items': 'center', 'justify-content': 'center', 'z-index': '10', padding: '24px' }}>
            <div style={{ background: 'rgba(13,17,23,0.96)', border: '1px solid rgba(255,255,255,0.14)', 'border-radius': '14px', padding: '16px', display: 'flex', 'flex-direction': 'column', gap: '14px', 'max-width': 'min(88vw, 720px)', 'max-height': '88vh', 'box-shadow': '0 12px 40px rgba(0,0,0,0.55)' }}>
              <span style={{ 'font-family': "'DM Sans',sans-serif", 'font-size': '13px', 'font-weight': '600', color: 'rgba(255,255,255,0.9)' }}>
                {translate('viewer_photo_title')}
              </span>
              <img src={photoUrl()!} alt="" style={{ 'max-width': '100%', 'max-height': '60vh', 'object-fit': 'contain', 'border-radius': '8px', background: 'rgba(255,255,255,0.04)', border: '1px solid rgba(255,255,255,0.08)' }} />
              <Show when={photoError()}>
                <span style={{ 'font-family': "'DM Sans',sans-serif", 'font-size': '12px', color: 'var(--danger)' }}>
                  {translate('viewer_photo_failed')}
                </span>
              </Show>
              <div style={{ display: 'flex', 'flex-wrap': 'wrap', gap: '8px', 'justify-content': 'flex-end' }}>
                <button onClick={() => setPhotoMode('aim')}
                  style={{ background: 'rgba(255,255,255,0.06)', border: '1px solid rgba(255,255,255,0.15)', 'border-radius': '8px', padding: '8px 14px', color: 'rgba(255,255,255,0.85)', 'font-size': '12px', cursor: 'pointer', 'font-family': "'DM Sans',sans-serif", 'margin-right': 'auto' }}>
                  ↺ {translate('viewer_photo_retake')}
                </button>
                <button onClick={downloadPhoto}
                  style={{ background: 'rgba(255,255,255,0.08)', border: '1px solid rgba(255,255,255,0.18)', 'border-radius': '8px', padding: '8px 14px', color: '#fff', 'font-size': '12px', cursor: 'pointer', 'font-family': "'DM Sans',sans-serif" }}>
                  ⭳ {translate('viewer_photo_download')}
                </button>
                <Show when={props.onSaveImage}>
                  <button onClick={addPhotoToDesign} disabled={photoSaving() || photoSaved()}
                    style={{ background: photoSaved() ? 'rgba(46,160,67,0.9)' : 'var(--accent)', border: `1px solid ${photoSaved() ? 'rgba(46,160,67,1)' : 'var(--accent)'}`, 'border-radius': '8px', padding: '8px 14px', color: '#fff', 'font-size': '12px', cursor: photoSaving() || photoSaved() ? 'default' : 'pointer', 'font-family': "'DM Sans',sans-serif", opacity: photoSaving() ? '0.7' : '1' }}>
                    {photoSaved() ? `✓ ${translate('viewer_photo_added')}` : photoSaving() ? translate('viewer_photo_adding') : `＋ ${translate('viewer_photo_add')}`}
                  </button>
                </Show>
                <button onClick={exitPhoto}
                  style={{ background: 'rgba(255,255,255,0.06)', border: '1px solid rgba(255,255,255,0.15)', 'border-radius': '8px', padding: '8px 14px', color: 'rgba(255,255,255,0.85)', 'font-size': '12px', cursor: 'pointer', 'font-family': "'DM Sans',sans-serif" }}>
                  {translate('viewer_photo_done')}
                </button>
              </div>
            </div>
          </div>
        </Show>

        {/* Settings panel - background colour + optional grid / build-plate overlays */}
        <Show when={settingsOpen()}>
          <div style={{ position: 'absolute', top: '14px', right: '14px', background: 'rgba(13,17,23,0.88)', border: '1px solid rgba(255,255,255,0.12)', 'border-radius': '10px', padding: '12px 14px', display: 'flex', 'flex-direction': 'column', gap: '12px', 'min-width': '210px', 'max-width': '260px', 'backdrop-filter': 'blur(6px)', 'box-shadow': '0 8px 24px rgba(0,0,0,0.4)', 'z-index': '6' }}>
            <span style={{ 'font-family': "'DM Sans',sans-serif", 'font-size': '12px', 'font-weight': '600', color: 'rgba(255,255,255,0.85)' }}>
              {translate('viewer_settings_title')}
            </span>
            {/* Darstellung - nur für generierte Strukturen (G-code): Linien vs. Solid + Farbe. */}
            <Show when={isGcode()}>
              <div style={{ display: 'flex', 'flex-direction': 'column', gap: '8px', 'padding-bottom': '10px', 'border-bottom': '1px solid rgba(255,255,255,0.1)' }}>
                <span style={{ 'font-family': "'DM Sans',sans-serif", 'font-size': '12px', color: 'rgba(255,255,255,0.75)' }}>{translate('viewer_gcode_display')}</span>
                <div style={{ display: 'flex', gap: '6px' }}>
                  <button onClick={() => applyGcodeMode('lines')} style={segBtnStyle(gcodeMode() === 'lines')}>{translate('viewer_gcode_lines')}</button>
                  <button onClick={() => applyGcodeMode('solid')} style={segBtnStyle(gcodeMode() === 'solid')}>{translate('viewer_gcode_solid')}</button>
                </div>
                {/* Linien: Höhen-Gradient („Heat") oder eigene Farbe. */}
                <Show when={gcodeMode() === 'lines'}>
                  <div style={{ display: 'flex', 'align-items': 'center', 'justify-content': 'space-between', gap: '10px' }}>
                    <div style={{ display: 'flex', gap: '6px', flex: '1' }}>
                      <button onClick={() => applyGcodeLineColor('heat')} style={segBtnStyle(gcodeLineColor() === 'heat')}>{translate('viewer_gcode_heat')}</button>
                      <button onClick={() => applyGcodeLineColor('custom')} style={segBtnStyle(gcodeLineColor() === 'custom')}>{translate('viewer_gcode_custom')}</button>
                    </div>
                    <Show when={gcodeLineColor() === 'custom'}>
                      <input type="color" value={gcodeColor()} onInput={e => applyGcodeColor(e.currentTarget.value)} style={colorInputStyle} />
                    </Show>
                  </div>
                </Show>
                {/* Solid: feste Farbe. */}
                <Show when={gcodeMode() === 'solid'}>
                  <label style={{ display: 'flex', 'align-items': 'center', 'justify-content': 'space-between', gap: '10px', cursor: 'pointer' }}>
                    <span style={{ 'font-family': "'DM Sans',sans-serif", 'font-size': '12px', color: 'rgba(255,255,255,0.75)' }}>{translate('viewer_gcode_color')}</span>
                    <input type="color" value={gcodeColor()} onInput={e => applyGcodeColor(e.currentTarget.value)} style={colorInputStyle} />
                  </label>
                </Show>
              </div>
            </Show>
            {/* Background colour */}
            <label style={{ display: 'flex', 'align-items': 'center', 'justify-content': 'space-between', gap: '10px', cursor: 'pointer' }}>
              <span style={{ 'font-family': "'DM Sans',sans-serif", 'font-size': '12px', color: 'rgba(255,255,255,0.75)' }}>{translate('viewer_bg_title')}</span>
              <input type="color" value={bgColor()} onInput={e => applyBgColor(e.currentTarget.value)}
                style={{ width: '26px', height: '26px', 'min-width': '26px', padding: '0', border: '1px solid rgba(255,255,255,0.2)', 'border-radius': '6px', background: 'none', cursor: 'pointer' }} />
            </label>
            {/* Grid (XYZ) overlay */}
            <label style={{ display: 'flex', 'align-items': 'center', 'justify-content': 'space-between', gap: '10px', cursor: 'pointer' }}>
              <span style={{ 'font-family': "'DM Sans',sans-serif", 'font-size': '12px', color: 'rgba(255,255,255,0.75)' }}>{translate('viewer_grid_toggle')}</span>
              <input type="checkbox" checked={gridOn()} onChange={e => setGrid(e.currentTarget.checked)}
                style={{ width: '16px', height: '16px', 'accent-color': '#4a90d9', cursor: 'pointer' }} />
            </label>
            {/* Build-plate overlay */}
            <label style={{ display: 'flex', 'align-items': 'center', 'justify-content': 'space-between', gap: '10px', cursor: 'pointer' }}>
              <span style={{ 'font-family': "'DM Sans',sans-serif", 'font-size': '12px', color: 'rgba(255,255,255,0.75)' }}>{translate('viewer_bed_toggle')}</span>
              <input type="checkbox" checked={bedOn()} onChange={e => setBed(e.currentTarget.checked)}
                style={{ width: '16px', height: '16px', 'accent-color': '#4a90d9', cursor: 'pointer' }} />
            </label>
            {/* Build-plate footprint (width × length in mm) */}
            <div style={{ display: 'flex', 'align-items': 'center', gap: '6px', 'padding-left': '4px', opacity: bedOn() ? '1' : '0.5' }}>
              <span style={{ 'font-family': "'DM Sans',sans-serif", 'font-size': '11px', color: 'rgba(255,255,255,0.55)', 'flex-shrink': '0' }}>{translate('viewer_bed_size')}</span>
              <input type="number" min="10" max="2000" value={bedW()} onChange={e => applyBedSize('w', parseFloat(e.currentTarget.value))}
                title={translate('viewer_bed_width')} class="stlv-num"
                style={{ width: '52px', padding: '3px 6px', background: 'rgba(255,255,255,0.06)', border: '1px solid rgba(255,255,255,0.15)', 'border-radius': '6px', color: '#fff', 'font-size': '12px', 'font-family': "'DM Mono',monospace", 'text-align': 'right', 'color-scheme': 'dark' }} />
              <span style={{ 'font-family': "'DM Sans',sans-serif", 'font-size': '11px', color: 'rgba(255,255,255,0.55)' }}>×</span>
              <input type="number" min="10" max="2000" value={bedL()} onChange={e => applyBedSize('l', parseFloat(e.currentTarget.value))}
                title={translate('viewer_bed_length')} class="stlv-num"
                style={{ width: '52px', padding: '3px 6px', background: 'rgba(255,255,255,0.06)', border: '1px solid rgba(255,255,255,0.15)', 'border-radius': '6px', color: '#fff', 'font-size': '12px', 'font-family': "'DM Mono',monospace", 'text-align': 'right', 'color-scheme': 'dark' }} />
              <span style={{ 'font-family': "'DM Sans',sans-serif", 'font-size': '11px', color: 'rgba(255,255,255,0.55)' }}>mm</span>
            </div>
            {/* Build-plate colour - visually subordinated under the plate toggle, like the size row. */}
            <label style={{ display: 'flex', 'align-items': 'center', gap: '6px', 'padding-left': '4px', cursor: 'pointer', opacity: bedOn() ? '1' : '0.5' }}>
              <span style={{ 'font-family': "'DM Sans',sans-serif", 'font-size': '11px', color: 'rgba(255,255,255,0.55)', 'flex-shrink': '0' }}>{translate('viewer_bed_color')}</span>
              <input type="color" value={bedColor()} onInput={e => applyBedColor(e.currentTarget.value)} style={colorInputStyle} />
            </label>
          </div>
        </Show>

        <Show when={isLoading()}>
          {/* Opaque loading screen in the app theme colour (dark/light) so the text stays
              readable regardless of the viewer background; when the model is ready this overlay
              is removed and the actual viewer background is revealed. Self-contained @keyframes
              so the spinner rotates independently of the parent's styles. */}
          <div style={{ position: 'absolute', inset: '0', display: 'flex', 'flex-direction': 'column', 'align-items': 'center', 'justify-content': 'center', gap: '16px', background: 'var(--bg2)' }}>
            <style>{`@keyframes spin{to{transform:rotate(360deg)}}`}</style>
            <div style={{ width: '48px', height: '48px', border: '3px solid var(--border)', 'border-top': '3px solid var(--accent)', 'border-radius': '50%', animation: 'spin 0.8s linear infinite' }} />
            <span style={{ 'font-family': "'DM Sans',sans-serif", color: 'var(--muted)', 'font-size': '14px' }}>
              {loadPhase() === 'build' ? translate('viewer_loading_building')
                : loadPhase() === 'parse' ? translate('viewer_loading_parsing')
                : translate('label_loading_model')}
            </span>
            {/* A bar only while the download has a known size. The parse that follows is one
                synchronous stretch with nothing to report, so claiming progress there would
                just be a bar that sits still. */}
            <Show when={loadProgress() !== null}>
              <div style={{ display: 'flex', 'align-items': 'center', gap: '10px', width: '220px' }}>
                {/* No CSS transition here on purpose. An animated width needs painted
                    frames to advance, and the parse that follows the download blocks the
                    main thread - the bar would freeze part-way while the percentage next
                    to it already showed the final value. Both read the same signal, so
                    applying the width in the same paint keeps them honest. */}
                <div style={{ flex: '1', height: '5px', background: 'var(--border)', 'border-radius': '3px', overflow: 'hidden' }}>
                  <div style={{ height: '100%', width: `${Math.round((loadProgress() ?? 0) * 100)}%`, background: 'var(--accent)' }} />
                </div>
                <span style={{ 'font-family': "'DM Mono',monospace", 'font-size': '12px', color: 'var(--muted)', 'min-width': '38px', 'text-align': 'right' }}>
                  {Math.round((loadProgress() ?? 0) * 100)}%
                </span>
              </div>
            </Show>
          </div>
        </Show>

        <Show when={errorMessage()}>
          <div style={{ position: 'absolute', inset: '0', display: 'flex', 'align-items': 'center', 'justify-content': 'center' }}>
            <div style={{ background: 'var(--danger-bg)', border: '1px solid var(--danger-border)', 'border-radius': '12px', padding: '20px 28px', color: 'var(--danger)', 'font-family': "'DM Sans',sans-serif", 'font-size': '14px', 'max-width': '400px', 'text-align': 'center' }}>
              <div style={{ 'font-size': '32px', 'margin-bottom': '12px' }}>⚠️</div>
              {errorMessage()}
            </div>
          </div>
        </Show>
      </div>
    </div>
  )
}
