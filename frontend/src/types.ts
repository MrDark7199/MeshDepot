/**
 * How a design is named outside the server: an opaque public id, never the
 * database rowid. That one is sequential, so a link to a design told its reader
 * how many others exist and invited them to try the neighbours.
 */
export type DesignID = string

/** All possible source platforms for 3D print designs. */
export type SourcePlatform = 'thingiverse' | 'printables' | 'makerworld' | 'thangs' | 'cults3d' | 'myminifactory' | 'manual'

export interface User {
  /** Opaque public id - the account is addressed by this in every route. */
  id: string
  email: string | null
  name: string
  admin: boolean
  must_change_password: boolean
  language?: string
  avatar_url?: string | null
  /** The account's own stylesheet, applied to the document on login and reload. */
  custom_css?: string
}

export interface Tag {
  id: number
  name: string
  color: string
}

export interface DesignImage {
  id: number
  design_id: DesignID
  path: string
  sort_order: number
  is_cover: boolean
  created_at?: string
}

/** An individual file stored within a design version. */
export interface DesignFileEntry {
  id: number
  design_file_id: number
  filename: string
  path: string
  size_bytes: number
  created_at?: string
  /** Print parameters extracted from a G-code file (only set for G-code). */
  gcode_meta?: GcodeMeta
}

/**
 * Normalized print parameters of a sliced file (only the fields present).
 *
 * Both FDM and resin settings ride in the same field, which is why `kind`
 * exists: it selects which half is filled in. Rows written before resin support
 * carry no `kind` and are FDM.
 */
export interface GcodeMeta {
  kind?: 'resin'
  layer_height?: number
  print_time?: string
  slicer?: string
  // ── FDM (G-code) ──
  first_layer_height?: number
  nozzle_temp?: number
  bed_temp?: number
  infill?: number
  filament_used_g?: number
  filament_used_m?: number
  nozzle_diameter?: number
  filament_type?: string
  // ── Resin (MSLA) ──
  exposure_time?: number
  bottom_exposure_time?: number
  bottom_layers?: number
  light_off_time?: number
  lift_height?: number
  lift_speed?: number
  bottom_lift_height?: number
  bottom_lift_speed?: number
  retract_speed?: number
  resin_volume?: number
  resin_weight?: number
  resin_cost?: number
  layer_count?: number
  resolution_x?: number
  resolution_y?: number
  pixel_size?: number
  display_width?: number
  display_height?: number
  anti_aliasing?: number
  material?: string
  printer?: string
}

/** A version snapshot of a design's files. */
export interface DesignFile {
  id: number
  design_id: DesignID
  version: string
  filename: string
  path: string
  size_bytes?: number
  file_count?: number
  is_current: number
  notes?: string
  created_at: string
  /** Individual files within this version */
  entries?: DesignFileEntry[]
}

/**
 * A link that hands a design to someone without an account (GET
 * /designs/{id}/links). The token is the whole credential, so it is only ever
 * shown to the owner.
 */
export interface ShareLink {
  id: number
  token: string
  /** null: runs until it is deleted. */
  expires_at?: string | null
  last_used_at?: string | null
  view_count: number
  created_at: string
  /** 1 when the expiry has passed; the server refuses it either way. */
  expired?: number
}

/** A share link as the account settings list it: with the design it points at. */
export interface UserShareLink extends ShareLink {
  design_id: DesignID
  design_name: string
}

export interface DesignShare {
  id: number
  shared_with_name: string
  shared_with_email: string
  created_at?: string
}

export interface Collection {
  id: number
  name: string
  description?: string
  cover_path?: string
  design_count?: number
  /** Set when the collection mirrors one on a platform, empty for own ones. */
  source_platform?: string
  /** Hidden collections are kept out of the filter and design details; the
   *  collection tab still shows them so they can be unhidden. 0/1 from SQLite. */
  is_hidden?: boolean
  created_at?: string
  updated_at?: string
}

export interface Design {
  id: DesignID
  name: string
  description?: string
  /** German display translations (DeepL); name/description hold canonical English */
  name_de?: string | null
  description_de?: string | null
  source_url?: string
  source_platform?: SourcePlatform
  source_id?: string
  cover_path?: string
  category?: string
  license?: string
  author?: string
  rating?: number
  print_time_minutes?: number
  notes?: string
  created_at?: string
  updated_at?: string
  /** Attached by index query */
  tags?: Tag[]
  current_file_id?: number
  current_version?: string
  filename?: string
  size_bytes?: number
  file_count?: number
  /** Set when this design was shared with the current user */
  is_shared?: boolean
  shared_by_name?: string
  is_hidden?: boolean
  /** Full data from /designs/{id} */
  images?: DesignImage[]
  collections?: Collection[]
  shares?: DesignShare[]
}

export interface QueueItem {
  id: number
  design_id?: DesignID
  design_name?: string
  source_url?: string
  platform?: string
  status: 'pending' | 'downloading' | 'done' | 'failed'
  error_msg?: string
  retry_count: number
  current_step?: string | null
  step_current?: number | null
  step_total?: number | null
}

/**
 * A queue row as rendered in the grid. Optimistic placeholders (negative id,
 * created the moment a download is triggered, before the server row exists)
 * carry no `retry_count` yet.
 */
export type DownloadJob = Omit<QueueItem, 'retry_count'> & { retry_count?: number }

/**
 * Pause/block state of one platform's download queue (GET /queue/blocks and the
 * admin settings `queue_blocks`). `paused` is the manual admin switch; `blocked`
 * is the automatic anti-bot/rate-limit pause that lifts by itself at `until`.
 */
export interface QueueBlock {
  platform: string
  paused: boolean
  blocked: boolean
  until?: string
  reason?: string
  since?: string
}

/** A stored credential set for one source platform (GET /users/{id}/platform-accounts). */
export interface PlatformAccount {
  id: number
  platform: string
  username?: string | null
  token?: string | null
  created_at?: string
  updated_at?: string
}

/** Payload of GET /designs/check-url - whether the URL is already in the library. */
export interface UrlCheckResult {
  exists: boolean
  design?: Pick<Design, 'id' | 'name'>
}

/** One entry of GET /designs/sync-status - an update check in flight. */
export interface SyncJob {
  id: number
  design_id: DesignID
  design_name?: string
  status: 'pending' | 'running' | 'done' | 'failed'
  progress?: number
  current_step?: string | null
  step_current?: number | null
  step_total?: number | null
  error_msg?: string
}

/**
 * What the sync buttons of the account settings need to decide whether they may
 * be offered (GET /users/{id}/sync-state). The cooldowns live on the server, and
 * a queue that is still being filled looks empty for a moment - a button reading
 * only its own countdown would reopen in the middle of the run it just started.
 */
export interface SyncState {
  has_accounts: boolean
  library_cooldown_seconds: number
  update_all_cooldown_seconds: number
  downloads_pending: number
  syncs_pending: number
  queue_busy: boolean
}

/**
 * One entry in the notification bell. Server rows (GET /users/{id}/notifications)
 * have a positive id; locally generated feedback (sync done/failed, bulk download
 * summary) carries `local: true` and a negative id so it can be removed client-side.
 */
export interface AppNotification {
  id: number
  title: string
  body?: string
  created_at: string
  read_at?: string | null
  local?: boolean
}

/** Live sync phase of one design, shown as a category label on the card. */
export interface SyncStep {
  step: string
  cur: number
  tot: number
}

/** Per-design state of a running update check, as shown on the card/row. */
export type SyncStatus = 'queued' | 'syncing' | 'updated' | 'no_change' | 'error'

export interface Filters {
  source_platform: string
  tag_ids: number[]
  shared_only: boolean
  show_hidden: boolean
}
