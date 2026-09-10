export const DEFAULT_VIEWER_BG_DARK = '#202423'

export const DEFAULT_VIEWER_BG_LIGHT = '#8b8383'

export const VIEWER_BG_KEY = 'meshdepot_viewer_bg'

export const VIEWER_GRID_KEY = 'meshdepot_viewer_grid'

export const VIEWER_BED_KEY = 'meshdepot_viewer_bed'

export const VIEWER_BED_W_KEY = 'meshdepot_viewer_bed_w'

export const VIEWER_BED_L_KEY = 'meshdepot_viewer_bed_l'

export const VIEWER_BED_COLOR_KEY = 'meshdepot_viewer_bed_color'

export const VIEWER_GCODE_MODE_KEY = 'meshdepot_viewer_gcode_mode'          // 'lines' | 'solid'

export const VIEWER_GCODE_LINECOLOR_KEY = 'meshdepot_viewer_gcode_linecolor' // 'heat' | 'custom'

export const VIEWER_GCODE_COLOR_KEY = 'meshdepot_viewer_gcode_color'         // #rrggbb, shared by solid + custom lines

export const DEFAULT_BED_W = 256

export const DEFAULT_BED_L = 256

export const BED_GRAIN_PX = 256

export const BED_GRAIN_MM = 40

export const DEFAULT_BED_COLOR = '#2b2f37'

export const DEFAULT_GCODE_COLOR = '#4a90d9'

export const clampBedDim = (v: number) => Math.min(2000, Math.max(10, Math.round(v)))

export const VIEW_ALPHA = -Math.PI / 2

export const VIEW_BETA = Math.PI / 4          // 45° above the horizontal plate

export const VIEW_RADIUS_FACTOR = 2.0         // distance = model size × this (a bit further away)

export const GRID_REACH_FACTOR = 8
