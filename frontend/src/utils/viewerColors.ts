import { Color3, Color4 } from '@babylonjs/core'

export const DEFAULT_HEX = '#4a90d9'

/** Coerces a colour into strict `#rrggbb`, stripping alpha and stray characters. */
export const normalizeHex = (hex: string): string => {
  let h = (hex || '').trim().toLowerCase()
  if (h.startsWith('#')) h = h.slice(1)
  h = h.replace(/[^0-9a-f]/g, '')
  return ('#' + (h + '000000').slice(0, 6))
}

/**
 * WCAG 2.1 relative luminance of linear-light RGB. Gamma-linearized and weighted
 * by cone sensitivity - not the naive 0.299/0.587/0.114 average, which works on
 * sRGB values and is well off for saturated colours.
 */
export const relativeLuminance = (r: number, g: number, b: number): number => {
  const lin = (v: number) => (v <= 0.04045 ? v / 12.92 : Math.pow((v + 0.055) / 1.055, 2.4))
  return 0.2126 * lin(r) + 0.7152 * lin(g) + 0.0722 * lin(b)
}

/**
 * The darkest a model may be drawn before its shape stops being readable, about
 * #3a3a3a. Babylon multiplies the diffuse colour by the light, so below this a
 * black part arrives as a flat silhouette with no edges or curvature.
 */
export const MIN_MODEL_LUMINANCE = 0.04

/**
 * Lifts a colour read from a file until the model is legible - never one somebody
 * picked, so a part deliberately set to black stays black.
 *
 * A colour with hue left is scaled up, which keeps it recognisable; one that is
 * essentially black is mixed towards white instead, since scaling zero by
 * anything is still zero.
 */
export const legibleModelHex = (hex: string): string => {
  const normalized = normalizeHex(hex)
  const r = parseInt(normalized.slice(1, 3), 16) / 255
  const g = parseInt(normalized.slice(3, 5), 16) / 255
  const b = parseInt(normalized.slice(5, 7), 16) / 255
  if (relativeLuminance(r, g, b) >= MIN_MODEL_LUMINANCE) return normalized

  const toHex = (value: number) =>
    Math.round(Math.min(1, Math.max(0, value)) * 255).toString(16).padStart(2, '0')

  const brightest = Math.max(r, g, b)
  if (brightest > 0.06) {
    // Stepping rather than solving for the factor: the luminance curve is not
    // linear, and a few dozen steps are exact enough and obvious to read.
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

export const hexToClearColor = (hex: string): Color4 => {
  const c = Color3.FromHexString(normalizeHex(hex))
  return new Color4(c.r, c.g, c.b, 1)
}

export const heightColor = (t: number): Color4 => {
  const clamped = Math.min(Math.max(t, 0), 1)
  const c = Color3.FromHSV((1 - clamped) * 240, 0.85, 1)
  return new Color4(c.r, c.g, c.b, 1)
}
