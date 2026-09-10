/** Formats bytes as a human-readable string. */
export function formatBytes(bytes: number): string {
  if (!bytes) return '0 B'
  if (bytes >= 1073741824) return (bytes / 1073741824).toFixed(1) + ' GB'
  if (bytes >= 1048576)    return (bytes / 1048576).toFixed(1) + ' MB'
  if (bytes >= 1024)       return (bytes / 1024).toFixed(0) + ' KB'
  return bytes + ' B'
}

/** Formats minutes as "Xh Ym". */
export function formatPrintTime(minutes: number): string {
  if (!minutes) return '-'
  const hours = Math.floor(minutes / 60)
  const mins = minutes % 60
  return hours > 0 ? `${hours}h ${mins}m` : `${mins}m`
}
