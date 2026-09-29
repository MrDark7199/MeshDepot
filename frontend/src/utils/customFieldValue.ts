/**
 * A multiple choice keeps its values as a JSON array, so a filter can match one
 * entry exactly rather than searching the text - "A1" must not match "A1 mini".
 * Every other type stores its value as it stands.
 */
export function parseChoices(value: string | undefined): string[] {
  if (!value) return []
  try {
    const parsed = JSON.parse(value)
    return Array.isArray(parsed) ? parsed.filter(entry => typeof entry === 'string') : []
  } catch {
    return []
  }
}

export function serialiseChoices(choices: string[]): string {
  return choices.length ? JSON.stringify(choices) : ''
}
