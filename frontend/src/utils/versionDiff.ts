import type { DesignFile, DesignFileEntry } from '../types'

export type DiffStatus = 'same' | 'changed' | 'added' | 'removed' | 'renamed'

export interface DiffRow {
  status: DiffStatus
  /** The name in the newer version, or the old one for a file that is gone. */
  filename: string
  /** Where the file sat before, for a rename. */
  previousName?: string
  newer?: DesignFileEntry
  older?: DesignFileEntry
  /** How the size changed, for the rows where both sides exist. */
  sizeDelta?: number
}

const pathOf = (entry: DesignFileEntry) => (entry.relative_path || entry.filename).toLowerCase()
const hashOf = (entry: DesignFileEntry) => (entry.file_hash || '').trim()

/**
 * What changed between two versions of a design.
 *
 * The content hash decides, not the date: it is the same figure the sync
 * compares, so a file that was downloaded again but is byte for byte the old one
 * reads as unchanged. Names are matched first, because that is how a person
 * reads a version; what is left over is matched by hash, which is what turns a
 * rename from "one file gone, one new" into one line.
 */
export function diffVersions(older: DesignFile, newer: DesignFile): DiffRow[] {
  const oldEntries = [...(older.entries ?? [])]
  const newEntries = [...(newer.entries ?? [])]
  const rows: DiffRow[] = []

  const oldByPath = new Map<string, DesignFileEntry>()
  for (const entry of oldEntries) oldByPath.set(pathOf(entry), entry)

  const leftoverOld = new Set(oldEntries)
  const leftoverNew: DesignFileEntry[] = []

  for (const entry of newEntries) {
    const match = oldByPath.get(pathOf(entry))
    if (!match) { leftoverNew.push(entry); continue }
    leftoverOld.delete(match)
    const same = hashOf(entry) !== '' && hashOf(entry) === hashOf(match)
    rows.push({
      status: same ? 'same' : 'changed',
      filename: entry.relative_path || entry.filename,
      newer: entry, older: match,
      sizeDelta: entry.size_bytes - match.size_bytes,
    })
  }

  // Same content under another name: one line, not a death and a birth.
  for (const entry of [...leftoverNew]) {
    const hash = hashOf(entry)
    if (!hash) continue
    const twin = [...leftoverOld].find(candidate => hashOf(candidate) === hash)
    if (!twin) continue
    leftoverOld.delete(twin)
    leftoverNew.splice(leftoverNew.indexOf(entry), 1)
    rows.push({
      status: 'renamed',
      filename: entry.relative_path || entry.filename,
      previousName: twin.relative_path || twin.filename,
      newer: entry, older: twin,
    })
  }

  for (const entry of leftoverNew) {
    rows.push({ status: 'added', filename: entry.relative_path || entry.filename, newer: entry })
  }
  for (const entry of leftoverOld) {
    rows.push({ status: 'removed', filename: entry.relative_path || entry.filename, older: entry })
  }

  // Changed first, then what came and went, and the untouched files last: the
  // list is read from the top and the top should carry the news.
  const rank: Record<DiffStatus, number> = { changed: 0, added: 1, removed: 2, renamed: 3, same: 4 }
  return rows.sort((left, right) =>
    rank[left.status] - rank[right.status] || left.filename.localeCompare(right.filename))
}

/** The counts for the summary line above the list. */
export function diffSummary(rows: DiffRow[]): Record<DiffStatus, number> {
  const counts: Record<DiffStatus, number> = { same: 0, changed: 0, added: 0, removed: 0, renamed: 0 }
  for (const row of rows) counts[row.status]++
  return counts
}
