export interface TrackedPaste { start: number; end: number; text: string }

export function pasteSummary(text: string): { label: string; text: string } | undefined {
  const normalized = text.replace(/\r\n/g, "\n").replace(/\r/g, "\n")
  const content = normalized.trim()
  const lines = (content.match(/\n/g)?.length ?? 0) + 1
  if (lines < 3 && content.length <= 150) return undefined
  return { label: `[Pasted ~${lines} lines]`, text: content }
}

export function expandTrackedPastes(value: string, pastes: readonly TrackedPaste[]): string {
  return [...pastes].sort((a, b) => b.start - a.start).reduce(
    (result, paste) => result.slice(0, paste.start) + paste.text + result.slice(paste.end),
    value,
  )
}
