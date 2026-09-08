export interface ActiveMention { start: number; end: number; query: string }

export function findActiveMention(text: string, cursorOffset: number): ActiveMention | undefined {
  const cursor = Math.max(0, Math.min(cursorOffset, text.length))
  let start = cursor
  while (start > 0 && !/\s/.test(text[start - 1]!)) start--
  if (text[start] !== "@") return undefined
  let end = cursor
  while (end < text.length && !/\s/.test(text[end]!)) end++
  return { start, end, query: text.slice(start + 1, cursor) }
}

export function replaceMention(text: string, mention: ActiveMention, insertText: string): { text: string; cursorOffset: number } {
  const next = text.slice(0, mention.start) + insertText + text.slice(mention.end)
  return { text: next, cursorOffset: mention.start + insertText.length }
}
