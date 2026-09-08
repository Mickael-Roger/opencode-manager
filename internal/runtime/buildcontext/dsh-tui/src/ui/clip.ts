export const COLLAPSED_LINE_LIMIT = 10

export interface ClippedText { visible: string; hidden: number }

// Long tool output (bash results, written files, ...) is collapsed to its
// first lines; the transcript renders a toggle row for the remainder.
export function clipText(text: string, limit = COLLAPSED_LINE_LIMIT): ClippedText {
  const lines = text.replace(/\r\n/g, "\n").replace(/\r/g, "\n").replace(/\n$/, "").split("\n")
  if (lines.length <= limit) return { visible: lines.join("\n"), hidden: 0 }
  return { visible: lines.slice(0, limit).join("\n"), hidden: lines.length - limit }
}
