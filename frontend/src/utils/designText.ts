import type { Design } from '../types'

/**
 * Language-aware display texts for a design.
 *
 * The backend stores canonical English in name/description and per-language
 * display translations (currently German) in name_de/description_de. These
 * helpers pick the variant for the active UI language, falling back to the
 * canonical English text when no translation exists. When the user has turned
 * off design translation (`translate=false`), the canonical text is always
 * returned. Filenames are never translated.
 */
export function displayName(design: Pick<Design, 'name' | 'name_de'>, lang: string, translate = true): string {
  if (translate && lang === 'de' && design.name_de) return design.name_de
  return design.name
}

/** See {@link displayName}; returns the canonical description when no translation applies. */
export function displayDescription(design: Pick<Design, 'description' | 'description_de'>, lang: string, translate = true): string {
  if (translate && lang === 'de' && design.description_de) return design.description_de
  return design.description || ''
}

/**
 * Author name to show for a design.
 *
 * Self-authored ("manual") designs have no real foreign author field, so for
 * the viewer's own manual designs we show their own username instead of the
 * stored raw `author` value. Shared designs keep the stored author (so the
 * owner's name shows, not the viewer's). Everything else uses `author` as-is.
 */
export function displayAuthor(
  design: Pick<Design, 'source_platform' | 'author' | 'is_shared'>,
  userName?: string,
): string {
  if (design.source_platform === 'manual' && !design.is_shared) return userName || ''
  return design.author || ''
}
