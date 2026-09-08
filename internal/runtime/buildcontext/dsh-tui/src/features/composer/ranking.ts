export interface ReferenceCandidate { kind: "subagent" | "file" | "directory"; label: string; insertText: string; detail?: string }

function score(candidate: ReferenceCandidate, query: string): number {
  const label = candidate.label.toLowerCase()
  const needle = query.toLowerCase()
  if (!needle || label === needle) return 0
  if (label.startsWith(needle)) return 1
  if (label.split("/").at(-1)?.startsWith(needle)) return 2
  let offset = 0
  for (const char of needle) {
    offset = label.indexOf(char, offset)
    if (offset < 0) return Number.POSITIVE_INFINITY
    offset++
  }
  return 3
}

export function rankReferences(candidates: readonly ReferenceCandidate[], query: string): ReferenceCandidate[] {
  return candidates.map((candidate, index) => ({ candidate, index, score: score(candidate, query) }))
    .filter(entry => Number.isFinite(entry.score))
    .sort((a, b) => a.score - b.score || a.index - b.index)
    .map(entry => entry.candidate)
}
