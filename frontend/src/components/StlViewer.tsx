import { createSignal, createEffect, onMount, onCleanup, Show, type JSX } from 'solid-js'
import { useI18n } from '../i18n/index'
import { useTheme } from '../ThemeContext'
import { splitConnectedComponents, writeBinaryStl, buildZip, type MeshPart } from '../utils/meshSplit'
import {
  Engine, Scene, ArcRotateCamera, Vector3, Matrix, Plane,
  HemisphericLight, DirectionalLight, Color3, Color4,
  StandardMaterial, Mesh, type LinesMesh, VertexData, PointerEventTypes, AbstractMesh,
  MeshBuilder, TransformNode, Quaternion, DynamicTexture, Texture,
  MultiMaterial, SubMesh, VertexBuffer, type PickingInfo,
} from '@babylonjs/core'
// Side-effect import: registers the octree-accelerated triangle picking, which
// the barrel import can otherwise tree-shake away.
import '@babylonjs/core/Culling/Octrees/octreeSceneComponent'
import { DEFAULT_HEX, normalizeHex, relativeLuminance, legibleModelHex, hexToClearColor, heightColor } from '../utils/viewerColors'
import { decodeText, bytesToBase64, hasUsableNormals, weldSmooth } from '../utils/meshTools'
import { extractModelFiles, findEntry, parseModelSettings, parseProjectColors, parseModelFile, type M3mfObject } from '../utils/threemf'
import { parseGcode } from '../utils/gcodeParse'
import type { DeviationResponse } from '../workers/deviation'
import { splitPanel, type SplitPanelDeps } from '../app/viewerSplitPanel'
import { settingsPanel, type SettingsPanelDeps } from '../app/viewerSettingsPanel'
import { viewerHeader, type ViewerHeaderDeps } from '../app/viewerHeader'
import { viewerOverlays, type ViewerOverlaysDeps } from '../app/viewerOverlays'
import { photoReview, type PhotoReviewDeps } from '../app/viewerPhotoReview'
import {
  DEFAULT_VIEWER_BG_DARK, DEFAULT_VIEWER_BG_LIGHT, VIEWER_BG_KEY, VIEWER_GRID_KEY, VIEWER_BED_KEY,
  VIEWER_BED_W_KEY, VIEWER_BED_L_KEY, VIEWER_BED_COLOR_KEY, VIEWER_GCODE_MODE_KEY,
  VIEWER_GCODE_LINECOLOR_KEY, VIEWER_GCODE_COLOR_KEY, DEFAULT_BED_W, DEFAULT_BED_L, BED_GRAIN_PX,
  BED_GRAIN_MM, DEFAULT_BED_COLOR, DEFAULT_GCODE_COLOR, clampBedDim, VIEW_ALPHA, VIEW_BETA,
  VIEW_RADIUS_FACTOR, GRID_REACH_FACTOR,
} from '../constants/viewer'

export interface StlViewerModalProps {
  url: string
  filename: string
  designName: string
  onClose: () => void
  onSaveImage?: (blob: Blob) => Promise<void>
  onSaveFiles?: (files: File[]) => Promise<void>
  zUp?: boolean
  /**
   * An older fassung of the same file, laid over the one being viewed. Given,
   * the viewer turns into a comparison: both models are tinted and the tools
   * that act on a single model step aside.
   */
  compareUrl?: string
  /** What the two sides are called - version numbers, as a rule. */
  compareLabel?: string
  baseLabel?: string
}

/**
 * Full-screen 3D viewer for STL, OBJ and 3MF, powered by Babylon.js.
 * Left-drag rotates, scroll zooms, right-drag pans.
 */
export function StlViewerModal(props: StlViewerModalProps) {
  const { translate } = useI18n()
  let canvasRef: HTMLCanvasElement | undefined
  let engine: Engine | undefined
  let scene: Scene | undefined
  let camera: ArcRotateCamera | undefined
  let initialRadius = 10
  let initialTarget = Vector3.Zero()
  let builtGroups: BuiltGroup[] = []
  let builtPlates: PlateInfo[] = []
  let modelRoot: TransformNode | undefined
  let splitParts: MeshPart[] = []
  let splitHighlight: Mesh | undefined
  let splitFlashTick: (() => void) | undefined

  // Green for what is in the design now, red for what it replaced.
  const COMPARE_NEW_HEX = '#4ade80'
  const COMPARE_OLD_HEX = '#e63946'

  /** The older model of a comparison, and the two tinted materials. */
  let compareMeshes: Mesh[] = []
  let baseMaterial: StandardMaterial | undefined
  let compareMaterial: StandardMaterial | undefined
  const isComparing = () => !!props.compareUrl
  const [compareReady, setCompareReady] = createSignal(false)
  const [showBase, setShowBase] = createSignal(true)
  const [showCompare, setShowCompare] = createSignal(true)
  /**
   * Where the wipe stands, 0..1 across the model, or null when it is off. Left
   * of it the older fassung shows, right of it the newer one.
   */
  const [wipeAt, setWipeAt] = createSignal<number | null>(null)
  /** The deviation colouring: off, being computed, or on with its scale. */
  const [deviationState, setDeviationState] = createSignal<'off' | 'running' | 'on'>('off')
  const [deviationProgress, setDeviationProgress] = createSignal(0)
  const [deviationMax, setDeviationMax] = createSignal(0)
  let deviationWorker: Worker | undefined

  let gcodePath: Mesh | undefined
  let gcodeData: { polylines: Vector3[][]; minH: number; maxH: number; layerH: number } | null = null
  /**
   * The extrusion paths of each layer, and where each layer's indices end in the
   * built mesh. Together they let the slider narrow what is drawn to a range of
   * the index buffer: building the geometry again at every step is what made the
   * model flicker.
   */
  let gcodeLayers: Vector3[][][] = []
  let gcodeLayerEnds: number[] = []
  /**
   * Whether the built mesh draws by vertex range rather than by index range.
   * The solid body does: the flat-shading pass unfolds it into one vertex per
   * index, in the same order, and Babylon then draws it unindexed.
   */
  let gcodeDrawsUnindexed = false
  /** How many layers the loaded g-code has; 0 while none is loaded. */
  const [gcodeLayerCount, setGcodeLayerCount] = createSignal(0)
  /** The topmost layer still drawn. -1 means the whole print. */
  const [gcodeTopLayer, setGcodeTopLayer] = createSignal(-1)

  let gcodeMat: StandardMaterial | undefined
  let gridNodes: { dispose: () => void }[] = []
  let bedNodes: { dispose: () => void }[] = []
  let bedGrainTextures: { normal: DynamicTexture; diffuse: DynamicTexture } | null = null
  let gridRadiusAtBuild = 0
  let sceneRadius = 0

  const [isLoading, setIsLoading] = createSignal(true)
  /**
   * One figure across the whole load, 0..1, or null when nothing can be measured.
   * Downloading is only the first stretch of the wait, so each phase advances its
   * own slice of a single bar rather than restarting it.
   */
  const [loadProgress, setLoadProgress] = createSignal<number | null>(null)
  const [loadPhase, setLoadPhase] = createSignal<'download' | 'parse' | 'build'>('download')

  /**
   * Share of the bar each phase owns, weighted by what takes the time: from a local
   * server the download is over in milliseconds, while reading a
   * multi-million-triangle mesh is the bulk of the wait.
   */
  const LOAD_PHASE_RANGE: Record<'download' | 'parse' | 'build', [number, number]> = {
    download: [0, 0.15],
    parse: [0.15, 0.85],
    build: [0.85, 1],
  }

  const reportLoad = (phase: 'download' | 'parse' | 'build', fraction: number) => {
    const [from, to] = LOAD_PHASE_RANGE[phase]
    setLoadPhase(phase)
    setLoadProgress(from + (to - from) * Math.max(0, Math.min(1, fraction)))
  }
  const [errorMessage, setErrorMessage] = createSignal('')
  const [formatLabel, setFormatLabel] = createSignal('')
  const [colorsOpen, setColorsOpen] = createSignal(false)
  const [settingsOpen, setSettingsOpen] = createSignal(false)
  const [gridOn, setGridOn] = createSignal(localStorage.getItem(VIEWER_GRID_KEY) === '1')
  const [bedOn, setBedOn] = createSignal(localStorage.getItem(VIEWER_BED_KEY) === '1')
  const [bedW, setBedW] = createSignal(clampBedDim(parseFloat(localStorage.getItem(VIEWER_BED_W_KEY) || '') || DEFAULT_BED_W))
  const [bedL, setBedL] = createSignal(clampBedDim(parseFloat(localStorage.getItem(VIEWER_BED_L_KEY) || '') || DEFAULT_BED_L))
  const [bedColor, setBedColor] = createSignal(normalizeHex(localStorage.getItem(VIEWER_BED_COLOR_KEY) || DEFAULT_BED_COLOR))

  const [isGcode, setIsGcode] = createSignal(false)
  const [gcodeMode, setGcodeMode] = createSignal<'lines' | 'solid'>(localStorage.getItem(VIEWER_GCODE_MODE_KEY) === 'solid' ? 'solid' : 'lines')
  const [gcodeLineColor, setGcodeLineColor] = createSignal<'heat' | 'custom'>(localStorage.getItem(VIEWER_GCODE_LINECOLOR_KEY) === 'custom' ? 'custom' : 'heat')
  const [gcodeColor, setGcodeColor] = createSignal(normalizeHex(localStorage.getItem(VIEWER_GCODE_COLOR_KEY) || DEFAULT_GCODE_COLOR))

  /**
   * 'intro' explains the tool and waits for a deliberate start: separating a dense
   * mesh takes seconds and must not begin on the click that opens the menu.
   */
  const [splitPhase, setSplitPhase] = createSignal<'idle' | 'intro' | 'busy' | 'done'>('idle')
  const [splitProgress, setSplitProgress] = createSignal(0)
  const [splitNames, setSplitNames] = createSignal<string[]>([])
  const [splitChosen, setSplitChosen] = createSignal<boolean[]>([])
  const [splitFlash, setSplitFlash] = createSignal<number | null>(null)
  const [splitPlate, setSplitPlate] = createSignal<number | 'all'>('all')
  const [splitHidden, setSplitHidden] = createSignal<boolean[]>([])
  /** Which of the separated parts are inside out, and would download as such. */
  const [splitInverted, setSplitInverted] = createSignal<boolean[]>([])
  /** Trust the file's own objects rather than the geometry; see canSplitByObjects. */
  const [splitByObjects, setSplitByObjects] = createSignal(true)
  /** The meshes whose faces look inward, and the tool for turning them round. */
  const [invertedMeshes, setInvertedMeshes] = createSignal<Mesh[]>([])
  const [flippedCount, setFlippedCount] = createSignal(0)
  const [flipSaving, setFlipSaving] = createSignal(false)
  const [flipSaved, setFlipSaved] = createSignal(false)
  const [splitSaving, setSplitSaving] = createSignal(false)
  /**
   * Which parts are already in the design. The selection is cumulative, so a second
   * "add" would resend the first ones and the server rejects them by name. Tracked
   * per part rather than as one flag, so adding more later stays possible.
   */
  const [splitAdded, setSplitAdded] = createSignal<boolean[]>([])
  const [splitError, setSplitError] = createSignal(false)
  /**
   * Deliberately separate from splitError: one signal for both reported a failed
   * upload as "the model could not be separated". Carries the server's message.
   */
  const [splitAddError, setSplitAddError] = createSignal('')
  const [splitPos, setSplitPos] = createSignal<{ x: number; y: number } | null>(null)

  const [toolsOpen, setToolsOpen] = createSignal(false)
  const [measureActive, setMeasureActive] = createSignal(false)
  const [measureCount, setMeasureCount] = createSignal(0)
  const [measureDist, setMeasureDist] = createSignal<number | null>(null)
  let measurePoints: Vector3[] = []
  let measureNodes: TransformNode[] = []   // placed teardrop markers (each a root node) + connecting line
  let measureLabelEl: HTMLDivElement | undefined  // floating "xx mm" label on the line
  let measureHover: TransformNode | undefined
  /** True while a button is held, which suppresses the measure hover raycast.
   *  Tracked explicitly because event.buttons is unreliable for synthetic moves. */
  let pointerDown = false

  const [photoMode, setPhotoMode] = createSignal<'off' | 'aim' | 'review'>('off')
  const [photoUrl, setPhotoUrl] = createSignal<string | null>(null)
  const [photoSaving, setPhotoSaving] = createSignal(false)
  const [photoSaved, setPhotoSaved] = createSignal(false)
  const [photoError, setPhotoError] = createSignal(false)
  const [photoRegion, setPhotoRegion] = createSignal<{ x: number; y: number; w: number; h: number } | null>(null)
  let photoDragStart: { x: number; y: number } | undefined  // drag origin while marqueeing a crop
  const [colorGroups, setColorGroups] = createSignal<{ label: string; hex: string }[]>([])
  const [platesUI, setPlatesUI] = createSignal<{ name: string; thumbnail?: string; colorIdx: number[] }[]>([])
  const [activePlate, setActivePlate] = createSignal(0)
  const theme = useTheme()
  const themeDefaultBg = () => (theme.resolvedTheme() === 'light' ? DEFAULT_VIEWER_BG_LIGHT : DEFAULT_VIEWER_BG_DARK)
  const storedBg = localStorage.getItem(VIEWER_BG_KEY)
  const [customBg, setCustomBg] = createSignal<string | null>(storedBg ? normalizeHex(storedBg) : null)
  const bgColor = () => customBg() ?? themeDefaultBg()

  const applyBgColor = (hex: string) => {
    const norm = normalizeHex(hex)
    setCustomBg(norm)
    localStorage.setItem(VIEWER_BG_KEY, norm)
    if (scene) scene.clearColor = hexToClearColor(norm)
    if (gridOn()) buildGrid()   // grid colour is derived from the background - re-derive it
  }

  // Follow theme changes while open when the user has not overridden the background.
  createEffect(() => {
    const def = themeDefaultBg()
    if (!customBg() && scene) {
      scene.clearColor = hexToClearColor(def)
      if (gridOn()) buildGrid()
    }
  })

  const setGroupColor = (index: number, hex: string) => {
    const g = builtGroups[index]
    if (g) g.material.diffuseColor = Color3.FromHexString(normalizeHex(hex))
    setColorGroups(prev => prev.map((cg, i) => (i === index ? { ...cg, hex } : cg)))
  }

  const resetColors = () => {
    for (const g of builtGroups) g.material.diffuseColor = Color3.FromHexString(normalizeHex(g.defaultHex))
    setColorGroups(builtGroups.map(g => ({ label: g.label, hex: g.defaultHex })))
  }

  const fileExtension = () => (props.filename || props.url.split('?')[0]).split('.').pop()?.toLowerCase() || ''
  const resizeHandler = () => engine?.resize()
  const hoverLeaveHandler = () => measureHover?.setEnabled(false)
  const keydownHandler = (e: KeyboardEvent) => {
    if (e.key !== 'Escape') return
    if (measureActive()) { setMeasure(false); e.stopPropagation() }
    else if (photoMode() !== 'off') { exitPhoto(); e.stopPropagation() }
  }

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

  const visibleModelMeshes = (): AbstractMesh[] => {
    if (gcodePath) return [gcodePath]
    if (builtPlates.length > 1) return builtPlates[activePlate()].meshes
    // The older fassung counts as well while comparing: framed on the newer one
    // alone, whatever grew beyond it would sit outside the picture.
    return [...builtGroups.flatMap(g => g.meshes), ...compareMeshes]
  }

  /**
   * The visible model, expanded to the full print-bed footprint when the bed
   * overlay is on, so the whole plate is in view.
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

  const fitView = () => {
    if (!camera || visibleModelMeshes().length === 0) return
    const { min, max } = boundsForView()
    const center = Vector3.Center(min, max)
    const size = Math.max(max.x - min.x, max.y - min.y, max.z - min.z) || 1
    // Bounding radius around the target, used to size the depth-clip range per frame.
    sceneRadius = Math.max(max.subtract(min).length() / 2, 1)
    camera.target = center
    camera.radius = size * VIEW_RADIUS_FACTOR
    camera.lowerRadiusLimit = size * 0.05
    initialRadius = camera.radius
    initialTarget = center.clone()
  }

  const niceStep = (span: number) => {
    const raw = (span || 1) / 10
    const pow = Math.pow(10, Math.floor(Math.log10(raw)))
    const norm = raw / pow
    return (norm >= 5 ? 5 : norm >= 2 ? 2 : 1) * pow
  }

  const disposeGrid = () => { for (const n of gridNodes) n.dispose(); gridNodes = [] }
  const disposeBed = () => { for (const n of bedNodes) n.dispose(); bedNodes = [] }

  /**
   * A floor reference grid with coloured XYZ axes under the visible model. Its
   * extent scales with the camera distance so it always runs past the screen edges,
   * while an adaptive step keeps the cell count bounded however far it stretches.
   */
  const buildGrid = () => {
    disposeGrid()
    if (!scene || !camera) return
    const meshes = visibleModelMeshes()
    if (meshes.length === 0) return
    const { min, max } = worldBoundsOf(meshes)
    const cx = (min.x + max.x) / 2, cz = (min.z + max.z) / 2, y = min.y
    const span = Math.max(max.x - min.x, max.z - min.z, 20)
    // Never smaller than the model itself. GRID_REACH_FACTOR is generous so a tilted
    // view still sees grid out to the horizon rather than an abrupt edge.
    gridRadiusAtBuild = camera.radius
    const reach = Math.max(camera.radius * GRID_REACH_FACTOR, span)
    const step = niceStep(reach / 3)          // ~60 cells across the whole grid, snapped to 1/2/5×10ⁿ
    const half = Math.ceil(reach / step) * step
    const lines: Vector3[][] = []
    for (let x = -half; x <= half + 1e-6; x += step) lines.push([new Vector3(cx + x, y, cz - half), new Vector3(cx + x, y, cz + half)])
    for (let z = -half; z <= half + 1e-6; z += step) lines.push([new Vector3(cx - half, y, cz + z), new Vector3(cx + half, y, cz + z)])
    const grid = MeshBuilder.CreateLineSystem('vgrid', { lines }, scene)
    // Derived from the background rather than a fixed grey, so the grid stays
    // readable on any background. Opaque on purpose: alpha would blend the line back
    // towards the background and undo the contrast just computed.
    grid.color = contrastLineColor(Color3.FromHexString(normalizeHex(bgColor())))
    grid.alpha = 1; grid.isPickable = false
    gridNodes.push(grid)
    // Coloured XYZ axes at the origin (Babylon Y is up), kept at model scale so they
    // stay a readable orientation gizmo.
    const axisLen = Math.max(span * 0.75, step * 2)
    // The X and Z axes run along the grid lines through the origin, so they z-fight
    // while orbiting. A hundredth of a cell separates them - relative, because a
    // fixed epsilon falls below the depth resolution once zoomed out.
    const ay = y + step * 0.01
    const axis = (to: Vector3, col: Color3, name: string) => {
      const l = MeshBuilder.CreateLines(name, { points: [new Vector3(cx, ay, cz), to] }, scene)
      l.color = col; l.isPickable = false; gridNodes.push(l)
    }
    axis(new Vector3(cx + axisLen, ay, cz), new Color3(0.9, 0.28, 0.28), 'axisX')
    axis(new Vector3(cx, y + axisLen, cz), new Color3(0.35, 0.8, 0.35), 'axisY')
    axis(new Vector3(cx, ay, cz + axisLen), new Color3(0.38, 0.52, 1), 'axisZ')
  }

  const relLuminance = (c: Color3): number => relativeLuminance(c.r, c.g, c.b)

  /**
   * Picks a line colour that stays visible on a freely configurable background.
   *
   * The WCAG contrast ratio (1.4.11 asks for 3:1 on graphical objects) decides.
   * Rather than snapping to black or white, the background is mixed towards one of
   * them step by step and the first step that meets the target wins, so the line
   * keeps a family resemblance and gets only as strong as it has to be. Luminance
   * picks the direction: below 0.179 there is more headroom upwards.
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
   * Seamlessly tileable height field for the plate's grain. Two layers, like a real
   * textured PEI sheet: broad blotches from value noise on a wrapping lattice - the
   * wrap is what makes the tile seam-free - plus a fine per-pixel speckle, which
   * needs no wrapping at one pixel per sample.
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
    // Gap between the plate's top face and the model on it. A fixed 0.02 mm fell
    // below the depth-buffer resolution once zoomed out and z-fought; 0.3 % of the
    // shorter edge is invisible and stays above the resolution at every zoom.
    const lift = Math.max(Math.min(width, depth) * 0.003, 0.05)
    const topY = y - lift          // top face sits just below the model
    const bed = MeshBuilder.CreateBox('bed', { width, depth, height: thickness }, scene)
    bed.position.set(cx, topY - thickness / 2, cz)

    const plateMaterial = (name: string) => {
      const m = new StandardMaterial(name, scene!)
      m.diffuseColor = base
      // Emissive keeps the plate readable at any light angle, but also flattens the
      // grain - 0.22 still lets the texture show without the plate going black.
      m.emissiveColor = base.scale(0.22)
      // A tightly focused highlight: the rough surface should glint as it turns, which
      // is most of what makes it read as textured rather than painted.
      m.specularColor = new Color3(0.2, 0.2, 0.22)
      m.specularPower = 24
      m.alpha = 0.9                // slightly translucent, but still reads as a solid plate
      // Polygon offset pushes only the plate surface back, independently of zoom, so
      // what rests on it stays on top even where the geometric gap gets tight.
      m.zOffset = 4
      m.zOffsetUnits = 4
      return m
    }
    const matTop = plateMaterial('bedMatTop'), matBody = plateMaterial('bedMatBody')

    // Tiled to a fixed physical size, so the roughness keeps its scale whether the
    // plate is 120 mm or 350 mm.
    const grain = bedGrain()
    if (grain) {
      const uS = Math.max(width / BED_GRAIN_MM, 1), vS = Math.max(depth / BED_GRAIN_MM, 1)
      grain.diffuse.uScale = uS; grain.diffuse.vScale = vS
      grain.normal.uScale = uS; grain.normal.vScale = vS
      matTop.diffuseTexture = grain.diffuse // multiplies diffuseColor → tint survives, brightness varies
      matTop.bumpTexture = grain.normal
      matTop.bumpTexture.level = 0.55       // subtle: a fine tooth, not a hammered finish
    }

    // Only the printing surface is textured, like the milled carrier of a real plate,
    // which needs one submesh per face pointing at a MultiMaterial. Which face is the
    // top one is read from the vertex normals rather than assumed from Babylon's face
    // order, where a silently wrong result would be easy to miss.
    const normals = bed.getVerticesData(VertexBuffer.NormalKind)
    const multi = new MultiMaterial('bedMulti', scene)
    multi.subMaterials = [matTop, matBody]
    bed.subMeshes = []
    for (let f = 0; f < 6; f++) {
      const isTop = !!normals && normals[f * 4 * 3 + 1] > 0.9   // normal.y ≈ 1
      new SubMesh(isTop ? 0 : 1, f * 4, 4, f * 6, 6, bed)
    }
    bed.material = multi; bed.isPickable = false
    // Materials are disposed with the plate - buildBed() runs on every colour-picker
    // event and mesh.dispose() leaves them behind. The grain textures are shared and
    // must not go with them, hence not dispose(false, true).
    bedNodes.push(multi, matTop, matBody, bed)

    // No overlays on the plate: the grid toggle owns the raster, and two rasters of
    // different spacing help nobody. Orientation comes from the grid's XYZ axes.
  }

  const applyBedColor = (raw: string) => {
    const hex = normalizeHex(raw)
    setBedColor(hex)
    localStorage.setItem(VIEWER_BED_COLOR_KEY, hex)
    if (bedOn()) buildBed()
  }

  const segBtnStyle = (active: boolean): JSX.CSSProperties => ({
    flex: '1', background: active ? 'rgba(74,144,217,0.25)' : 'rgba(255,255,255,0.05)',
    border: `1px solid ${active ? 'rgba(74,144,217,0.6)' : 'rgba(255,255,255,0.12)'}`,
    'border-radius': '7px', padding: '6px 8px', color: '#fff', 'font-size': '11px',
    cursor: 'pointer', 'font-family': "'DM Sans',sans-serif",
  })
  const colorInputStyle: JSX.CSSProperties = {
    width: '26px', height: '26px', 'min-width': '26px', padding: '0',
    border: '1px solid rgba(255,255,255,0.2)', 'border-radius': '6px', background: 'none', cursor: 'pointer',
  }

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
    // Deliberately no re-framing: position and zoom stay put, as with the grid.
  }
  const applyBedSize = (which: 'w' | 'l', raw: number) => {
    if (!isFinite(raw) || raw <= 0) return
    const v = clampBedDim(raw)
    if (which === 'w') { setBedW(v); localStorage.setItem(VIEWER_BED_W_KEY, String(v)) }
    else { setBedL(v); localStorage.setItem(VIEWER_BED_L_KEY, String(v)) }
    if (bedOn()) { buildBed(); fitView() }
  }

  const clearMeasure = () => {
    for (const n of measureNodes) n.dispose()
    measureNodes = []
    measurePoints = []
    setMeasureCount(0)
    setMeasureDist(null)
    if (measureLabelEl) measureLabelEl.style.display = 'none'
  }

  const setMeasure = (on: boolean) => {
    setMeasureActive(on)
    if (canvasRef) canvasRef.style.cursor = on ? 'crosshair' : ''
    if (on) updateMeasureHover()                    // show the hover drop immediately if the cursor is on the object
    else { clearMeasure(); measureHover?.setEnabled(false) }
  }

  /**
   * Colour of the surface a pick landed on, as the marker sees it. Anything without
   * a plain diffuse colour falls back to the viewer background, which is what the
   * marker is read against there anyway.
   */
  const pickedSurfaceColor = (mesh?: AbstractMesh | null): Color3 => {
    const mat = mesh?.material
    if (mat instanceof StandardMaterial) return mat.diffuseColor
    return Color3.FromHexString(normalizeHex(bgColor()))
  }

  /**
   * Recolours a teardrop to contrast with whatever it sits on - the same WCAG
   * approach as the grid, since the model colour is just as configurable. Part of
   * it goes in as emissive, or the contrast would hold only on the lit sides.
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
   * Surface normal under the cursor, always pointing out of the solid towards the
   * viewer. The raw pick normal is not enough: vertex normals are missing on some
   * meshes, and STL files routinely ship inconsistent winding, so the normal can
   * point into the body and the marker stands on its head. The face we are looking
   * at is by definition the outward one.
   */
  const outwardNormal = (pick: PickingInfo): Vector3 | null => {
    const n = pick.getNormal(true, true) ?? pick.getNormal(true, false)
    if (!n || !camera || !pick.pickedPoint) return n
    return Vector3.Dot(n, camera.position.subtract(pick.pickedPoint)) < 0 ? n.scale(-1) : n
  }

  const orientToNormal = (nrm?: Vector3 | null): Quaternion => {
    if (!nrm) return Quaternion.Identity()
    const n = nrm.normalizeToNew()
    const up = Vector3.Up()
    const dot = Vector3.Dot(up, n)
    if (dot >= 0.99999) return Quaternion.Identity()
    if (dot <= -0.99999) return Quaternion.RotationAxis(Vector3.Right(), Math.PI)  // exactly downward
    return Quaternion.RotationAxis(Vector3.Cross(up, n).normalize(), Math.acos(dot))
  }

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

  const updateMeasureHover = () => {
    if (!scene) return
    if (!measureActive()) { measureHover?.setEnabled(false); return }
    const pick = scene.pick(scene.pointerX, scene.pointerY)
    if (!pick?.hit || !pick.pickedPoint) { measureHover?.setEnabled(false); return }
    const surface = pickedSurfaceColor(pick.pickedMesh)
    if (!measureHover) measureHover = buildTeardrop('measureHover', scene, surface)
    // Built once and reused, so it re-tints as it travels: a model can be several
    // colour groups, and the drop must stay readable across them.
    else tintTeardrop(measureHover, surface)
    measureHover.setEnabled(true)
    measureHover.position.copyFrom(pick.pickedPoint)
    measureHover.rotationQuaternion = orientToNormal(outwardNormal(pick))
  }

  // - Photo tool --------------------------------
  /**
   * Collects the rendered triangles as one flat soup, in the coordinates of the
   * source file. Each mesh's world matrix is applied so a 3MF comes out arranged
   * the way it is drawn, while modelRoot is undone again: that node only carries
   * the viewer's Z-up correction, and baking it in would rotate the parts.
   */
  const sourceMeshesFor = (onlyPlate: number | 'all'): Mesh[] => {
    const wanted = onlyPlate === 'all' || builtPlates.length === 0
      ? null
      : new Set<Mesh>(builtPlates[onlyPlate]?.meshes ?? [])
    const meshes: Mesh[] = []
    for (const group of builtGroups) {
      for (const mesh of group.meshes) {
        if (wanted && !wanted.has(mesh)) continue
        meshes.push(mesh)
      }
    }
    return meshes
  }

  /** One mesh's triangles, in the coordinates the file was written in. */
  /**
   * Six times the volume the faces enclose, by the divergence theorem. Positive
   * when they look outward, negative when the object is inside out - which is
   * the one thing a slicer cannot make sense of: it reads the inside as the
   * outside and prints a shell around a hole, or nothing at all.
   *
   * Taken over the triangles as they are, so an open mesh gives a figure that
   * means little - hence the margin before anything is called inverted.
   */
  const facingVolumeOf = (mesh: Mesh): number => {
    const positions = mesh.getVerticesData(VertexBuffer.PositionKind)
    if (!positions) return 0
    const indices = mesh.getIndices() ?? Array.from({ length: positions.length / 3 }, (_, index) => index)
    let total = 0
    for (let at = 0; at + 2 < indices.length; at += 3) {
      const a = indices[at] * 3, b = indices[at + 1] * 3, c = indices[at + 2] * 3
      const ax = positions[a], ay = positions[a + 1], az = positions[a + 2]
      const bx = positions[b], by = positions[b + 1], bz = positions[b + 2]
      const cx = positions[c], cy = positions[c + 1], cz = positions[c + 2]
      total += ax * (by * cz - bz * cy) - ay * (bx * cz - bz * cx) + az * (bx * cy - by * cx)
    }
    return total / 6
  }

  /**
   * The same test for a loose triangle soup - nine floats a triangle, as the
   * split hands them about - with its own bounding box, since a part has no
   * mesh to ask for one yet.
   */
  const soupLooksInverted = (positions: Float32Array): boolean => {
    let volume = 0
    let minX = Infinity, minY = Infinity, minZ = Infinity
    let maxX = -Infinity, maxY = -Infinity, maxZ = -Infinity
    for (let at = 0; at + 8 < positions.length; at += 9) {
      const ax = positions[at], ay = positions[at + 1], az = positions[at + 2]
      const bx = positions[at + 3], by = positions[at + 4], bz = positions[at + 5]
      const cx = positions[at + 6], cy = positions[at + 7], cz = positions[at + 8]
      volume += ax * (by * cz - bz * cy) - ay * (bx * cz - bz * cx) + az * (bx * cy - by * cx)
      for (const [x, y, z] of [[ax, ay, az], [bx, by, bz], [cx, cy, cz]]) {
        if (x < minX) minX = x
        if (y < minY) minY = y
        if (z < minZ) minZ = z
        if (x > maxX) maxX = x
        if (y > maxY) maxY = y
        if (z > maxZ) maxZ = z
      }
    }
    const box = Math.max((maxX - minX) * (maxY - minY) * (maxZ - minZ), 1e-6)
    return volume / 6 < -0.05 * box
  }

  /** Turns a soup's triangles round, corner two and three swapped. */
  const flipSoup = (positions: Float32Array): Float32Array => {
    const flipped = new Float32Array(positions.length)
    for (let at = 0; at + 8 < positions.length; at += 9) {
      flipped[at] = positions[at]; flipped[at + 1] = positions[at + 1]; flipped[at + 2] = positions[at + 2]
      flipped[at + 3] = positions[at + 6]; flipped[at + 4] = positions[at + 7]; flipped[at + 5] = positions[at + 8]
      flipped[at + 6] = positions[at + 3]; flipped[at + 7] = positions[at + 4]; flipped[at + 8] = positions[at + 5]
    }
    return flipped
  }

  /** Looks over every object and remembers the ones that are inside out. */
  const findInvertedMeshes = () => {
    const inverted: Mesh[] = []
    for (const group of builtGroups) {
      for (const mesh of group.meshes) {
        const bounds = mesh.getBoundingInfo().boundingBox.extendSize
        // A margin against the arithmetic, not against the question: a flat or
        // open piece can come out slightly negative without being inverted.
        const scale = Math.max(bounds.x * bounds.y * bounds.z, 1e-6)
        if (facingVolumeOf(mesh) < -0.05 * scale) inverted.push(mesh)
      }
    }
    setInvertedMeshes(inverted)
  }

  /**
   * Turns one object's faces round: every triangle is read through its indices,
   * its second and third corner swapped, and written back as its own vertices
   * with fresh normals. Doing it through the indices covers both shapes a mesh
   * can be in here - indexed, or unfolded by the flat-shading pass.
   */
  const flipMesh = (mesh: Mesh) => {
    const positions = mesh.getVerticesData(VertexBuffer.PositionKind)
    if (!positions) return
    const indices = mesh.getIndices() ?? Array.from({ length: positions.length / 3 }, (_, index) => index)
    const flipped: number[] = []
    for (let at = 0; at + 2 < indices.length; at += 3) {
      for (const corner of [indices[at], indices[at + 2], indices[at + 1]]) {
        flipped.push(positions[corner * 3], positions[corner * 3 + 1], positions[corner * 3 + 2])
      }
    }
    const data = new VertexData()
    data.positions = flipped
    data.indices = Array.from({ length: flipped.length / 3 }, (_, index) => index)
    const normals: number[] = []
    VertexData.ComputeNormals(data.positions, data.indices, normals)
    data.normals = normals
    data.applyToMesh(mesh)
    setFlippedCount(count => count + 1)
    setFlipSaved(false)
    findInvertedMeshes()
  }

  const flipAllInverted = () => {
    for (const mesh of [...invertedMeshes()]) flipMesh(mesh)
  }

  /**
   * Turns the inside-out parts round, previews and all. What leaves the viewer
   * afterwards - the ZIP, a single STL, the files added to the design - is built
   * from these same soups, so repairing here repairs what is downloaded.
   */
  const repairSplitParts = () => {
    const inverted = splitInverted()
    splitParts = splitParts.map((part, index) =>
      inverted[index] ? { positions: flipSoup(part.positions), triangleCount: part.triangleCount } : part)
    setSplitInverted(splitParts.map(() => false))
    buildSplitPartMeshes()
  }

  /**
   * The repaired model as one STL, added to the design beside the file it came
   * from. STL rather than the format it arrived in: this is a repair, and what
   * is being repaired is geometry - a 3MF's colours would have to be rebuilt to
   * carry them, which is a different job.
   */
  const addFixedToDesign = async () => {
    if (!props.onSaveFiles) return
    setFlipSaving(true)
    try {
      const soup = collectTriangleSoup('all')
      const base = (props.filename || 'model').replace(/\.[^.]+$/, '')
      const file = new File([writeBinaryStl({ positions: soup, triangleCount: soup.length / 9 })],
        `${base}-korrigiert.stl`, { type: 'model/stl' })
      await props.onSaveFiles([file])
      setFlipSaved(true)
    } catch {
      setFlipSaved(false)
    } finally {
      setFlipSaving(false)
    }
  }

  const soupOfMesh = (mesh: Mesh, undoRoot: Matrix | null): number[] => {
    const chunks: number[] = []
    const positions = mesh.getVerticesData(VertexBuffer.PositionKind)
    if (!positions) return chunks
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
    return chunks
  }

  const collectTriangleSoup = (onlyPlate: number | 'all'): Float32Array => {
    const chunks: number[] = []
    const undoRoot = modelRoot ? Matrix.Invert(modelRoot.getWorldMatrix()) : null
    for (const mesh of sourceMeshesFor(onlyPlate)) chunks.push(...soupOfMesh(mesh, undoRoot))
    return new Float32Array(chunks)
  }

  /**
   * The objects as the file itself describes them: a 3MF is built one mesh per
   * build item, so each mesh is one object with everything it is made of.
   */
  const collectObjectSoups = (onlyPlate: number | 'all'): MeshPart[] => {
    const undoRoot = modelRoot ? Matrix.Invert(modelRoot.getWorldMatrix()) : null
    const parts: MeshPart[] = []
    for (const mesh of sourceMeshesFor(onlyPlate)) {
      const chunks = soupOfMesh(mesh, undoRoot)
      if (chunks.length === 0) continue
      parts.push({ positions: new Float32Array(chunks), triangleCount: chunks.length / 9 })
    }
    // Largest first, as the connected-component split returns them.
    return parts.sort((left, right) => right.triangleCount - left.triangleCount)
  }

  /**
   * Whether the file says what belongs together. Only a 3MF does, and only when
   * it holds more than one object - with one, there is nothing its own grouping
   * could tell us that the geometry does not.
   */
  const canSplitByObjects = () =>
    formatLabel() === '3MF' && sourceMeshesFor(splitPlate()).length > 1

  // The file as it is stored, straight from the viewer - otherwise the way to it
  // is back out to the design and down into the file list.
  const downloadFile = () => {
    setToolsOpen(false)
    const anchor = document.createElement('a')
    anchor.href = props.url
    anchor.download = props.filename || 'model'
    anchor.click()
  }

  const openSplitIntro = () => {
    setToolsOpen(false)
    // The plate on screen is the one being looked at, so it is the one offered.
    // With a single plate the chooser stays hidden and "all" means the same thing.
    setSplitPlate(builtPlates.length > 1 ? activePlate() : 'all')
    setSplitError(false)
    setSplitAddError('')
    setSplitPos(null)
    setSplitPhase('intro')
  }

  const runSplit = async () => {
    if (splitPhase() === 'busy') return
    disposeSplitPartMeshes()
    setSplitPhase('busy')
    setSplitProgress(0)
    setSplitError(false)
    try {
      // What the file calls one object stays one part. Connectivity cannot know
      // that: a two-colour print, a lid sitting inside its box, an inlay - all
      // of them are several shells that touch at most, and splitting them apart
      // is what made a 3MF fall into pieces that belong together.
      if (splitByObjects() && canSplitByObjects()) {
        splitParts = collectObjectSoups(splitPlate())
        setSplitProgress(1)
      } else {
        const soup = collectTriangleSoup(splitPlate())
        splitParts = await splitConnectedComponents(soup, fraction => setSplitProgress(fraction))
      }
      setSplitNames(splitParts.map((_, index) => splitFileName(index)))
      // Checked here rather than on the way out: what is downloaded is these
      // soups, so this is where a part can still be put right.
      setSplitInverted(splitParts.map(part => soupLooksInverted(part.positions)))
      // Everything is chosen to begin with: the common case is wanting all of it.
      setSplitChosen(splitParts.map(() => true))
      setSplitAdded(splitParts.map(() => false))
      buildSplitPartMeshes()
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

  const placeSplitPanelRight = () => {
    const width = 420
    const height = Math.min(520, window.innerHeight - 80)
    setSplitPos({ x: Math.max(16, window.innerWidth - width - 32), y: Math.max(16, (window.innerHeight - height) / 2) })
  }

  let splitPartMeshes: Mesh[] = []
  let splitHiddenSources: Mesh[] = []

  /**
   * One mesh per part, standing in for the meshes they came from. The model is
   * built per colour group and knows nothing of parts, so hiding one is only
   * possible once every part is its own mesh. The colour groups do not survive
   * this: while the panel is open the plate wears one material.
   */
  const buildSplitPartMeshes = () => {
    disposeSplitPartMeshes()
    if (!scene || splitParts.length === 0) return
    const sources = sourceMeshesFor(splitPlate())
    const material = sources[0]?.material ?? null

    splitPartMeshes = splitParts.map((part, index) => {
      const mesh = new Mesh(`splitPart${index}`, scene!)
      const data = new VertexData()
      data.positions = Array.from(part.positions)
      data.indices = Array.from({ length: part.positions.length / 3 }, (_, i) => i)
      const normals: number[] = []
      VertexData.ComputeNormals(data.positions, data.indices, normals)
      data.normals = normals
      data.applyToMesh(mesh)
      if (material) mesh.material = material
      mesh.metadata = { splitIndex: index }
      if (modelRoot) mesh.parent = modelRoot
      return mesh
    })

    for (const mesh of sources) {
      if (!mesh.isVisible) continue
      mesh.isVisible = false
      splitHiddenSources.push(mesh)
    }
    setSplitHidden(splitParts.map(() => false))
  }

  const disposeSplitPartMeshes = () => {
    for (const mesh of splitPartMeshes) mesh.dispose()
    splitPartMeshes = []
    for (const mesh of splitHiddenSources) mesh.isVisible = true
    splitHiddenSources = []
    setSplitHidden([])
  }

  const toggleSplitVisible = (index: number) => {
    setSplitHidden(prev => prev.map((hidden, position) => (position === index ? !hidden : hidden)))
    const mesh = splitPartMeshes[index]
    if (mesh) mesh.isVisible = !splitHidden()[index]
  }

  const clearSplitHighlight = () => {
    if (splitFlashTick && scene) scene.onBeforeRenderObservable.removeCallback(splitFlashTick)
    splitFlashTick = undefined
    splitHighlight?.dispose()
    splitHighlight = undefined
    setSplitFlash(null)
  }

  const SPLIT_FLASH_MS = 4000

  /**
   * Points out one part by pulsing a copy of it over the model for a few seconds.
   * A copy, because the model on screen is one mesh per colour group with no notion
   * of parts; pulsing, because a static overlay on a similar shape is easy to miss.
   * It removes itself, so the list never leaves the model in a state to undo.
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
    // Drawn just in front of the surface it covers, or it would z-fight.
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
      // Shimmer, then fade over the last half second so it does not just vanish.
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
    disposeSplitPartMeshes()
    splitParts = []
    setSplitNames([])
    setSplitChosen([])
    setSplitPhase('idle')
    setSplitAdded([])
    setSplitError(false)
    setSplitAddError('')
    setSplitPos(null)
  }

  const splitBaseName = () =>
    (props.designName || props.filename || 'model').replace(/\.[^.]+$/, '').replace(/[^\w.-]+/g, '_') || 'model'

  const splitFileName = (index: number) => `${splitBaseName()}-part-${String(index + 1).padStart(2, '0')}.stl`

  // A single part is handed over as the STL itself: an archive holding one file
  // only asks the person to unpack it again.
  const downloadSplit = () => {
    const indices = chosenIndices()
    if (indices.length === 0) return
    const single = indices.length === 1
    const blob = single
      ? new Blob([writeBinaryStl(splitParts[indices[0]])], { type: 'model/stl' })
      : buildZip(indices.map(index => ({ name: splitFileName(index), data: writeBinaryStl(splitParts[index]) })))
    const url = URL.createObjectURL(blob)
    const anchor = document.createElement('a')
    anchor.href = url
    anchor.download = single ? splitFileName(indices[0]) : `${splitBaseName()}-parts.zip`
    anchor.click()
    // Revoked on a later tick: revoking at once cancels the download in some browsers
    // before it has read the blob.
    setTimeout(() => URL.revokeObjectURL(url), 10_000)
  }

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
      // Shown as it came back: a rejected upload is usually specific, and hiding that
      // behind a generic sentence is what made this hard to place.
      setSplitAddError(failure instanceof Error && failure.message ? failure.message : 'unknown')
    } finally {
      setSplitSaving(false)
    }
  }

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

  // Switching files with the menu open would leave tools pointing at geometry that
  // is being replaced.
  createEffect(() => { if (isLoading()) setToolsOpen(false) })

  const startPhoto = () => {
    setMeasure(false)
    setToolsOpen(false); setColorsOpen(false); setSettingsOpen(false)
    setPhotoRegion(null); setPhotoUrl(null); setPhotoSaved(false); setPhotoSaving(false); setPhotoError(false)
    camera?.detachControl()
    setPhotoMode('aim')
  }

  const exitPhoto = () => {
    setPhotoMode('off')
    setPhotoUrl(null); setPhotoRegion(null); setPhotoSaved(false); setPhotoSaving(false); setPhotoError(false)
    photoDragStart = undefined
    if (camera && canvasRef) camera.attachControl(canvasRef, true)
  }

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

  const downloadPhoto = () => {
    const url = photoUrl(); if (!url) return
    const safe = (props.designName || 'modell').replace(/[^\w.-]+/g, '_')
    const a = document.createElement('a')
    a.href = url; a.download = `${safe}-foto.png`
    a.click()
  }

  /**
   * Decodes a `data:` URL without fetch(): connect-src is 'self' only, and the
   * browser counts a fetch of a data URL as a connection - the upload failed
   * silently that way.
   */
  const dataUrlToBlob = (url: string): Blob => {
    const [head, b64] = url.split(',')
    const mime = /:(.*?);/.exec(head)?.[1] || 'image/png'
    const bin = atob(b64)
    const bytes = new Uint8Array(bin.length)
    for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i)
    return new Blob([bytes], { type: mime })
  }

  const addPhotoToDesign = async () => {
    const url = photoUrl()
    if (!url || !props.onSaveImage || photoSaving() || photoSaved()) return
    setPhotoSaving(true)
    setPhotoError(false)
    try {
      await props.onSaveImage(dataUrlToBlob(url))
      setPhotoSaved(true)
    } catch {
      // The caller shows a toast, but it is easy to miss behind the viewer overlay.
      setPhotoError(true)
    }
    setPhotoSaving(false)
  }

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

  /**
   * Partitions each mesh's triangles into an octree, so picking stays fast on dense
   * meshes - the reconstructed resin STL alone has ~1.8M triangles.
   *
   * Deferred and one mesh per idle slot: building it is seconds of synchronous work
   * and froze the viewer just as it looked ready. Picking falls back to brute force
   * until it has run. It must run after every transform, since the octree stores
   * world coordinates and is never refreshed.
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
        // Picking only: Babylon otherwise uses the octree for render selection too and
        // drops submeshes whose blocks miss the frustum, which made whole objects vanish
        // when zooming in. Visibility now hangs on the mesh's own bounding box.
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
    // A measurement nobody is waiting for any more: the viewer is closing.
    deviationWorker?.terminate()
    engine?.dispose()
  })

  /**
   * Bootstraps engine and scene, fetches the file, dispatches to the parser and
   * fits the camera to the model.
   */
  const initViewer = async () => {
    if (!canvasRef) return

    // adaptToDeviceRatio keeps the buffer at native resolution on HiDPI screens.
    // preserveDrawingBuffer lets the photo tool read the framebuffer after a render.
    engine = new Engine(canvasRef, true, { preserveDrawingBuffer: true, stencil: true }, true)
    scene = new Scene(engine)
    // Babylon would otherwise raycast on every pointer move to track the mesh under
    // the cursor; on the 1.8M-triangle resin mesh that was much of the rotate stutter.
    scene.skipPointerMovePicking = true
    scene.clearColor = hexToClearColor(bgColor())

    camera = new ArcRotateCamera('cam', VIEW_ALPHA, VIEW_BETA, 10, Vector3.Zero(), scene)
    camera.attachControl(canvasRef, true)
    camera.lowerRadiusLimit = 0.1
    camera.wheelPrecision = 50 / 4  // niedriger = schneller; 4× der ursprünglichen Zoom-Geschwindigkeit (Basis 50)
    camera.panningSensibility = 500

    // Hemispheric base with a bright ground colour plus a headlight that follows the
    // camera, so whatever side the user orbits to is lit.
    const hemi = new HemisphericLight('hemi', new Vector3(0, 1, 0), scene)
    hemi.intensity = 0.65
    hemi.diffuse = new Color3(1, 1, 1)
    hemi.groundColor = new Color3(0.55, 0.55, 0.55)
    const headlight = new DirectionalLight('dir', new Vector3(1, -2, -1).normalize(), scene)
    headlight.intensity = 0.75
    scene.onBeforeRenderObservable.add(() => {
      if (camera) headlight.direction = camera.getForwardRay().direction
      // Adaptive depth-clip: bracket the z-buffer tightly around the model so
      // interpenetrating G-code beads stop flickering. Perspective depth precision is
      // dominated by the near plane, so minZ goes as far forward as the content allows.
      if (camera && sceneRadius > 0) {
        // Outside the bounding sphere no visible triangle can be closer than
        // (radius - sceneRadius), so minZ may travel that far. Zooming in moves the
        // camera into the sphere, where a percentage-based minZ would slice into the very
        // object being inspected - hence the tiny fraction there.
        const pad = sceneRadius * 2.5   // headroom so panning / the bed overlay never clip the front
        camera.minZ = Math.max(camera.radius - pad, camera.radius * 0.001, 0.01)
        camera.maxZ = camera.radius * (GRID_REACH_FACTOR + 2) + sceneRadius
      }
      updateMeasureLabel()
      // Regrow the grid when the user zooms out past its extent.
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
      // A tap on a part while the list is open points that row out, so the model
      // answers the same question the magnifier in the list does.
      if (info.type === PointerEventTypes.POINTERTAP && !measureActive() && splitPhase() === 'done') {
        const pick = scene.pick(scene.pointerX, scene.pointerY)
        const index = pick?.pickedMesh?.metadata?.splitIndex
        if (typeof index === 'number') { flashSplitPart(index); return }
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
        gcodeLayers = splitByLayer()
        gcodeLayerEnds = []
        setGcodeLayerCount(gcodeLayers.length)
        setGcodeTopLayer(-1)
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

      if (isComparing()) await loadComparison(scene)

      findInvertedMeshes()

      refreshHelpers() // draw the grid / build plate if they were left enabled
      resetView()      // frame the model (+ bed) from the default 45° angle
      setIsLoading(false)
    } catch (err: unknown) {
      setErrorMessage(translate('viewer_load_failed') + ': ' + (err instanceof Error ? err.message : String(err)))
      setIsLoading(false)
    }
  }

  // - Parsers --------------------------------

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
   * Loads the older fassung into the same scene and tints both sides: the file
   * on screen in green, the one it replaced in red, each half transparent so
   * the one inside the other still shows.
   *
   * A deliberately short path - no plates, no colour groups, no g-code: what is
   * being compared are two meshes of one file, and everything else the viewer
   * can do is switched off while it does this.
   */
  const loadComparison = async (scene: Scene) => {
    try {
      const response = await fetch(props.compareUrl!, { credentials: 'include' })
      if (!response.ok) throw new Error(`HTTP ${response.status}`)
      const buffer = await response.arrayBuffer()
      const magic = new Uint8Array(buffer, 0, 4)
      const isZip = magic[0] === 0x50 && magic[1] === 0x4B && magic[2] === 0x03 && magic[3] === 0x04
      const extension = fileExtension()

      let meshes: Mesh[]
      if (extension === 'obj') {
        meshes = [parseWavefrontMesh(new TextDecoder().decode(buffer), scene)]
      } else if (extension === '3mf' || isZip) {
        meshes = (await parse3mf(buffer, scene)).groups.flatMap(group => group.meshes)
      } else {
        meshes = [await parseStl(buffer, scene)]
      }
      compareMeshes = meshes

      // The same re-orientation the main model gets, or the two would lie at
      // right angles to each other.
      if (extension === '3mf' || isZip || props.zUp) {
        const root = new TransformNode('compareRoot', scene)
        root.rotation.x = -Math.PI / 2
        for (const mesh of meshes) mesh.parent = root
      }

      baseMaterial = makeMaterial('baseMat', COMPARE_NEW_HEX, scene)
      baseMaterial.alpha = 0.55
      compareMaterial = makeMaterial('compareMat', COMPARE_OLD_HEX, scene)
      compareMaterial.alpha = 0.55
      // Both sides are see-through, so the back faces have to be drawn too or
      // the inside of the shell reads as a hole.
      for (const material of [baseMaterial, compareMaterial]) material.backFaceCulling = false
      for (const group of builtGroups) for (const mesh of group.meshes) mesh.material = baseMaterial
      for (const mesh of meshes) { mesh.material = compareMaterial; mesh.isPickable = false }
      setCompareReady(true)
    } catch (failure: unknown) {
      setErrorMessage(translate('compare_load_failed') + ': ' + (failure instanceof Error ? failure.message : String(failure)))
    }
  }

  /** Which side of the wipe each model is drawn on, or neither when it is off. */
  const applyCompareView = () => {
    const base = builtGroups.flatMap(group => group.meshes)
    for (const mesh of base) mesh.setEnabled(showBase())
    for (const mesh of compareMeshes) mesh.setEnabled(showCompare())
    const at = wipeAt()
    if (!baseMaterial || !compareMaterial) return
    if (at === null) {
      baseMaterial.clipPlane = null
      compareMaterial.clipPlane = null
      return
    }
    // A fragment is dropped where normal·position + d is positive, so the newer
    // model keeps the right-hand side and the older one the left.
    const x = wipeWorldX(at)
    baseMaterial.clipPlane = new Plane(-1, 0, 0, x)
    compareMaterial.clipPlane = new Plane(1, 0, 0, -x)
  }

  /** The wipe position in world units - 0 is the left edge of the two models. */
  const wipeWorldX = (fraction: number) => {
    const { min, max } = worldBoundsOf(visibleModelMeshes())
    return min.x + (max.x - min.x) * fraction
  }

  /**
   * The geometry of a set of meshes as one triangle soup, with the vertex offset
   * of each mesh so the answer can be handed back per mesh. Positions are taken
   * as they are authored: both sides of a comparison get the same treatment, so
   * comparing in their own space is comparing like with like.
   */
  const meshGeometry = (meshes: Mesh[]) => {
    const positions: number[] = []
    const indices: number[] = []
    const offsets: number[] = []
    for (const mesh of meshes) {
      const own = mesh.getVerticesData(VertexBuffer.PositionKind)
      if (!own) { offsets.push(positions.length / 3); continue }
      const base = positions.length / 3
      offsets.push(base)
      for (let at = 0; at < own.length; at++) positions.push(own[at])
      const ownIndices = mesh.getIndices()
      if (ownIndices) {
        for (const index of ownIndices) indices.push(base + index)
      } else {
        // No index buffer: the positions are already one triangle after another.
        for (let vertex = 0; vertex < own.length / 3; vertex++) indices.push(base + vertex)
      }
    }
    offsets.push(positions.length / 3)
    return { positions: new Float32Array(positions), indices: new Uint32Array(indices), offsets }
  }

  /**
   * Colours the newer model by how far each of its vertices sits from the older
   * one's surface. The work happens in a worker - a few hundred thousand
   * vertices against as many triangles would otherwise stop the page.
   */
  const runDeviation = () => {
    const base = builtGroups.flatMap(group => group.meshes)
    if (base.length === 0 || compareMeshes.length === 0) return
    const reference = meshGeometry(compareMeshes)
    const samples = meshGeometry(base)
    if (reference.indices.length === 0 || samples.positions.length === 0) return

    setDeviationState('running')
    setDeviationProgress(0)
    deviationWorker?.terminate()
    deviationWorker = new Worker(new URL('../workers/deviation.ts', import.meta.url), { type: 'module' })
    deviationWorker.onmessage = (event: MessageEvent<DeviationResponse>) => {
      const message = event.data
      if (message.type === 'progress') { setDeviationProgress(message.fraction); return }
      if (message.type === 'error') { setDeviationState('off'); setErrorMessage(message.message); return }
      paintDeviation(base, samples.offsets, message.distances, message.maximum)
      deviationWorker?.terminate()
      deviationWorker = undefined
    }
    deviationWorker.postMessage({
      referencePositions: reference.positions.buffer,
      referenceTriangles: reference.indices.buffer,
      samplePositions: samples.positions.buffer,
    }, [reference.positions.buffer, reference.indices.buffer, samples.positions.buffer])
  }

  /** Writes the measured distances onto the meshes as vertex colours. */
  const paintDeviation = (meshes: Mesh[], offsets: number[], distances: Float32Array, maximum: number) => {
    setDeviationMax(maximum)
    const scale = maximum > 0 ? maximum : 1
    for (let at = 0; at < meshes.length; at++) {
      const from = offsets[at], to = offsets[at + 1]
      if (to <= from) continue
      const colours = new Float32Array((to - from) * 4)
      for (let vertex = from; vertex < to; vertex++) {
        const colour = heightColor(1 - distances[vertex] / scale)
        const target = (vertex - from) * 4
        colours[target] = colour.r
        colours[target + 1] = colour.g
        colours[target + 2] = colour.b
        colours[target + 3] = 1
      }
      meshes[at].setVerticesData(VertexBuffer.ColorKind, colours)
      meshes[at].useVertexColors = true
    }
    if (baseMaterial) {
      // White, so what shows is the vertex colour and not a tint over it, and
      // opaque, because a heat map read through a second model says nothing.
      baseMaterial.diffuseColor = new Color3(1, 1, 1)
      baseMaterial.alpha = 1
    }
    setShowCompare(false)
    setDeviationState('on')
    applyCompareView()
  }

  /** Back to the two tinted models. */
  const clearDeviation = () => {
    deviationWorker?.terminate()
    deviationWorker = undefined
    for (const group of builtGroups) for (const mesh of group.meshes) mesh.useVertexColors = false
    if (baseMaterial) {
      baseMaterial.diffuseColor = Color3.FromHexString(COMPARE_NEW_HEX)
      baseMaterial.alpha = 0.55
    }
    setDeviationState('off')
    setShowCompare(true)
    applyCompareView()
  }

  const toggleDeviation = () => { if (deviationState() === 'off') runDeviation(); else clearDeviation() }

  const applyShowBase = (on: boolean) => { setShowBase(on); applyCompareView() }
  const applyShowCompare = (on: boolean) => { setShowCompare(on); applyCompareView() }
  const applyWipe = (at: number | null) => { setWipeAt(at); applyCompareView() }

  interface BuiltGroup {
    label: string
    meshes: Mesh[]
    material: StandardMaterial
    defaultHex: string
  }

  interface PlateInfo {
    id: string
    name: string
    thumbnail?: string
    meshes: Mesh[]
    /** Indices into {@link builtGroups}/colorGroups for the filament colours used on this plate. */
    colorIdx: number[]
  }

  const makeMaterial = (name: string, hex: string, scene: Scene): StandardMaterial => {
    const material = new StandardMaterial(name, scene)
    material.diffuseColor = Color3.FromHexString(normalizeHex(hex))
    material.specularColor = new Color3(0.1, 0.1, 0.1)
    material.backFaceCulling = false
    // Both sides are drawn, so both sides are lit: without this a face seen from
    // behind takes its light from the wrong direction and reads as a different
    // material rather than as the back of the one it is.
    material.twoSidedLighting = true
    return material
  }

  const singleGroup = (mesh: Mesh, scene: Scene): BuiltGroup => {
    const material = makeMaterial('mat', DEFAULT_HEX, scene)
    mesh.material = material
    return { label: props.designName || 'Model', meshes: [mesh], material, defaultHex: DEFAULT_HEX }
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
      // A mirrored instance - a left part placed as the right one, say - turns
      // every triangle inside out. Without turning them back, that copy is lit
      // from within and would print as a hole rather than a part.
      if (matrix.determinant() < 0) {
        for (let at = 0; at + 2 < tris.length; at += 3) {
          indices.push(base + tris[at], base + tris[at + 2], base + tris[at + 1])
        }
      } else {
        for (const t of tris) indices.push(base + t)
      }
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

  /** The layer a height falls on, counted from the first extrusion. */
  const layerOf = (height: number) => {
    if (!gcodeData) return 0
    return Math.max(0, Math.round((height - gcodeData.minH) / Math.max(gcodeData.layerH, 1e-3)))
  }

  /** The height of the topmost drawn layer, for the readout beside the slider. */
  const gcodeTopHeight = () => {
    if (!gcodeData) return 0
    const top = gcodeTopLayer()
    return top < 0 ? gcodeData.maxH : gcodeData.minH + top * gcodeData.layerH
  }

  /** The whole print's height - what that readout will be at its widest. */
  const gcodeMaxHeight = () => gcodeData?.maxH ?? 0

  /**
   * The paths sorted into one bucket per layer, cut where they cross from one to
   * the next rather than filed whole: a vase-mode print is a single path running
   * through every layer, and filing it under one would leave the slider doing
   * nothing there.
   */
  const splitByLayer = (): Vector3[][][] => {
    if (!gcodeData) return []
    const layers: Vector3[][][] = []
    const bucket = (index: number) => {
      while (layers.length <= index) layers.push([])
      return layers[index]
    }
    for (const line of gcodeData.polylines) {
      let run: Vector3[] = []
      let runLayer = -1
      for (let index = 0; index + 1 < line.length; index++) {
        const layer = layerOf(Math.max(line[index].y, line[index + 1].y))
        if (layer !== runLayer) {
          if (run.length >= 2) bucket(runLayer).push(run)
          run = [line[index]]
          runLayer = layer
        }
        run.push(line[index + 1])
      }
      if (run.length >= 2 && runLayer >= 0) bucket(runLayer).push(run)
    }
    return layers
  }

  const buildGcodeLines = (data: { minH: number; maxH: number }, scene: Scene): Mesh => {
    const { minH, maxH } = data
    // In layer order, and with the end of each layer written down as it goes:
    // the line system emits two indices per segment, in the order of the array.
    const polylines: Vector3[][] = []
    gcodeLayerEnds = []
    let indexCount = 0
    for (const layer of gcodeLayers) {
      for (const line of layer) {
        polylines.push(line)
        indexCount += 2 * (line.length - 1)
      }
      gcodeLayerEnds.push(indexCount)
    }
    gcodeDrawsUnindexed = false
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
  const buildGcodeSolid = (data: { layerH: number }, scene: Scene): Mesh => {
    const MAX_SOLID_SEGMENTS = 140_000
    const halfH = Math.max(data.layerH, 0.05) / 2
    const halfW = Math.max(data.layerH * 1.8, 0.1) / 2   // Raupenbreite ≈ 1,8 × Schichthöhe
    let segCount = 0
    for (const layer of gcodeLayers) for (const pl of layer) segCount += pl.length - 1
    const stride = segCount > MAX_SOLID_SEGMENTS ? Math.ceil(segCount / MAX_SOLID_SEGMENTS) : 1

    const positions: number[] = []
    const indices: number[] = []
    const worldUp = new Vector3(0, 1, 0), altUp = new Vector3(0, 0, 1)
    // Box indices (12 triangles) wound outwards; the base is the box's first vertex.
    const BOX = [0,1,5, 0,5,4,  3,6,2, 3,7,6,  0,7,3, 0,4,7,  1,6,5, 1,2,6,  0,3,2, 0,2,1,  4,5,6, 4,6,7]

    gcodeLayerEnds = []
    for (const layer of gcodeLayers) {
    for (const pl of layer) {
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
    gcodeLayerEnds.push(indices.length)
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
    // It also unfolds the mesh: from here it is drawn by vertex range.
    gcodeDrawsUnindexed = true
    const mat = new StandardMaterial('gcodeSolidMat', scene)
    mat.diffuseColor = Color3.FromHexString(gcodeColor())
    mat.specularColor = new Color3(0.15, 0.15, 0.15)
    mesh.material = mat
    gcodeMat = mat
    mesh.isPickable = false
    return mesh
  }

  const renderGcode = () => {
    if (!scene || !gcodeData) return
    if (gcodePath) { gcodePath.dispose(); gcodePath = undefined }
    gcodeMat = undefined
    if (gcodeLayers.length === 0) return
    gcodePath = gcodeMode() === 'solid'
      ? buildGcodeSolid(gcodeData, scene)
      : buildGcodeLines(gcodeData, scene)
    applyLayerWindow()
    // The grid and the plate need no rebuild: the bounds are practically the same
    // for lines and solid, and the overlays do not depend on the mesh identity.
    // The camera stays where it is.
  }

  /**
   * Narrows what is drawn to the chosen layers. The geometry is built once and
   * sits in layer order, so this only moves the start and the length of the one
   * sub-mesh - no rebuild, and nothing to flicker.
   */
  const applyLayerWindow = () => {
    if (!gcodePath || gcodeLayerEnds.length === 0) return
    const last = gcodeLayerEnds.length - 1
    const top = gcodeTopLayer() < 0 ? last : Math.min(gcodeTopLayer(), last)
    const count = Math.max(0, gcodeLayerEnds[top])
    gcodePath.subMeshes = []
    // No bounding box: it would be recomputed on every step of the slider, and
    // the camera is framed on the whole print either way.
    if (gcodeDrawsUnindexed) new SubMesh(0, 0, count, 0, count, gcodePath, undefined, false)
    else new SubMesh(0, 0, gcodePath.getTotalVertices(), 0, count, gcodePath, undefined, false)
  }

  const applyGcodeTopLayer = (layer: number) => {
    setGcodeTopLayer(layer)
    applyLayerWindow()
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
  const applyGcodeColor = (raw: string) => {
    const hex = normalizeHex(raw)
    setGcodeColor(hex)
    localStorage.setItem(VIEWER_GCODE_COLOR_KEY, hex)
    if (gcodeMat) gcodeMat.diffuseColor = Color3.FromHexString(hex)
    if (gcodePath && gcodeMode() === 'lines' && gcodeLineColor() === 'custom') (gcodePath as LinesMesh).color = Color3.FromHexString(hex)
  }

  // - Controls --------------------------------

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

  const splitPanelDeps: SplitPanelDeps = {
    props, translate, builtPlates: () => builtPlates, platesUI, splitPhase, splitProgress, splitNames,
    splitChosen, splitAdded, splitFlash, splitHidden, toggleSplitVisible, splitPlate, setSplitPlate,
    canSplitByObjects, splitByObjects, setSplitByObjects, splitInverted, repairSplitParts,
    splitSaving, splitError, splitAddError,
    splitPos, chosenIndices, pendingIndices, toggleSplitChoice, setAllSplitChoices, flashSplitPart,
    runSplit, closeSplit, downloadSplit, addSplitToDesign, startSplitDrag,
  }

  const settingsPanelDeps: SettingsPanelDeps = {
    translate, settingsOpen, isGcode, bgColor, applyBgColor, gridOn, setGrid, bedOn, setBed, bedW, bedL,
    applyBedSize, bedColor, applyBedColor, gcodeMode, applyGcodeMode, gcodeLineColor, applyGcodeLineColor,
    gcodeColor, applyGcodeColor, segBtnStyle, colorInputStyle,
  }


  const setMeasureLabelEl = (element: HTMLDivElement) => { measureLabelEl = element }

  const viewerHeaderDeps: ViewerHeaderDeps = {
    props, translate, isLoading, formatLabel, colorGroups, colorsOpen, setColorsOpen,
    settingsOpen, setSettingsOpen, toolsOpen, setToolsOpen, isComparing, measureActive, resetView, downloadFile,
  }

  const viewerOverlaysDeps: ViewerOverlaysDeps = {
    translate, platesUI, activePlate, selectPlate, colorGroups, colorsOpen, setGroupColor, resetColors,
    toolsOpen, isGcode,
    isComparing, compareReady, baseLabel: () => props.baseLabel ?? '', compareLabel: () => props.compareLabel ?? '',
    showBase, setShowBase: applyShowBase, showCompare, setShowCompare: applyShowCompare,
    wipeAt, setWipeAt: applyWipe,
    deviationState, deviationProgress, deviationMax, toggleDeviation,
    gcodeLayerCount, gcodeTopLayer, setGcodeTopLayer: applyGcodeTopLayer,
    gcodeTopHeight, gcodeMaxHeight,
    invertedCount: () => invertedMeshes().length, flipMeshAll: flipAllInverted,
    flippedCount, canSaveFixed: () => !!props.onSaveFiles, addFixedToDesign, flipSaving, flipSaved,
    measureActive, setMeasure, clearMeasure, measureCount, measureDist,
    setMeasureLabelEl, openSplitIntro, startPhoto, takePhoto, exitPhoto, photoMode, photoRegion,
    setPhotoRegion, photoDragDown, photoDragMove, photoDragUp,
  }

  const photoReviewDeps: PhotoReviewDeps = {
    props, translate, photoMode, setPhotoMode, photoUrl, photoSaving, photoSaved, photoError,
    downloadPhoto, addPhotoToDesign, exitPhoto,
  }


  return (
    <div style={{ position: 'fixed', inset: '0', background: 'rgba(0,0,0,0.92)', 'z-index': '1000', display: 'flex', 'flex-direction': 'column' }}>
      {viewerHeader(viewerHeaderDeps)}
      {/* Canvas */}
      <div style={{ flex: '1', position: 'relative', overflow: 'hidden' }}>
        <canvas ref={canvasRef} style={{ display: 'block', width: '100%', height: '100%' }} />

        {viewerOverlays(viewerOverlaysDeps)}
        {splitPanel(splitPanelDeps)}

        {photoReview(photoReviewDeps)}
        {settingsPanel(settingsPanelDeps)}

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
