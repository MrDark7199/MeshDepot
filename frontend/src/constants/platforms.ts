// Central platform constants - the single source of truth for colours, display
// names, base URLs and detection. Previously duplicated across App.tsx,
// DesignPage, DesignModals, ServerSettings and AccountSettings.
//
// The backend keeps the same table in internal/platforms/platforms.go and cannot
// import this file; TestPlatformTableMatchesExternalLists fails when the two
// drift apart.

/** Accent colour per platform (badges, bars etc.). */
export const PLATFORM_COLORS: Record<string, string> = {
  thingiverse: '#248bfb',
  printables: '#fa6831',
  makerworld: '#16a34a',
  thangs: '#a855f7',
  cults3d: '#e63946',
  myminifactory: '#00b96b',
  manual: '#64748b',
}

/** Display name per platform. */
export const PLATFORM_LABELS: Record<string, string> = {
  thingiverse: 'Thingiverse',
  printables: 'Printables',
  makerworld: 'MakerWorld',
  thangs: 'Thangs',
  cults3d: 'Cults3D',
  myminifactory: 'MyMiniFactory',
  manual: 'Custom',
}

/**
 * Display name per platform, with an i18n translation for 'manual' (designs the
 * user added themselves). translate comes from the useI18n() context and can
 * therefore not live in PLATFORM_LABELS; the value there is only a fallback.
 */
export const platformLabel = (key: string, translate: (k: string) => string): string =>
  key === 'manual' ? translate('platform_manual') : (PLATFORM_LABELS[key] || key)

/** Base URL per platform (e.g. to resolve relative links in descriptions). */
export const PLATFORM_BASES: Record<string, string> = {
  printables: 'https://www.printables.com',
  makerworld: 'https://makerworld.com',
  thingiverse: 'https://www.thingiverse.com',
  cults3d: 'https://cults3d.com',
  thangs: 'https://thangs.com',
  myminifactory: 'https://www.myminifactory.com',
}

/** Selectable source platforms (without 'manual'). */
export const PLATFORMS = [
  'printables',
  'thingiverse',
  'makerworld',
  'thangs',
  'cults3d',
  'myminifactory',
]

/**
 * Platforms whose downloads need stored credentials or a token.
 *
 * Printables and Thangs are on the list too: both hand the token from their
 * login to the file request, so without an account the download comes back
 * empty-handed - minutes later, as a queue entry that failed for no visible
 * reason. Kept in step with NeedsCredentials in the Go platform table.
 */
export const PLATFORMS_REQUIRING_CREDENTIALS = [
  'thingiverse',
  'printables',
  'makerworld',
  'thangs',
  'myminifactory',
  'cults3d',
]

/** Detects a known 3D printing platform from a URL ('manual' as the fallback). */
export function detectPlatformFromUrl(url: string): string {
  if (url.includes('thingiverse.com')) return 'thingiverse'
  if (url.includes('printables.com')) return 'printables'
  if (url.includes('makerworld.com')) return 'makerworld'
  if (url.includes('thangs.com')) return 'thangs'
  if (url.includes('cults3d.com')) return 'cults3d'
  if (url.includes('myminifactory.com')) return 'myminifactory'
  return 'manual'
}
