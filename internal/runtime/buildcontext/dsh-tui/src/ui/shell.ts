import type { OnHighlightCallback, SimpleHighlight } from "@opentui/core"

// @opentui/core ships tree-sitter grammars for javascript, typescript,
// markdown and zig only. Shell commands have no parser, so CodeRenderable
// receives zero highlights for them; this scanner produces approximate
// tree-sitter-style ranges instead. Strings and comments are honoured,
// command-position words become function calls, and grammar keywords,
// variables, flags, numbers and operators get their standard captures.
const GRAMMAR_KEYWORDS = new Set(["if", "then", "elif", "else", "fi", "for", "while", "until", "do", "done", "case", "esac", "in", "function", "select", "coproc", "time", "return", "break", "continue"])
const BUILTIN_KEYWORDS = new Set(["local", "export", "readonly", "declare", "typeset", "unset", "shift", "source", "alias", "exec", "eval", "exit", "trap", "set", "getopts", "read", "printf", "echo", "cd", "pushd", "popd", "builtin", "command", "type", "let", "hash", "wait", "test"])
const KEYWORDS = new Set([...GRAMMAR_KEYWORDS, ...BUILTIN_KEYWORDS])
const WORD = /[A-Za-z0-9_]/
const WORD_CHAR = /[A-Za-z0-9_.\-/*?]/

export function shellHighlights(content: string): SimpleHighlight[] {
  const highlights: SimpleHighlight[] = []
  const push = (start: number, end: number, group: string) => { if (end > start) highlights.push([start, end, group]) }
  const length = content.length
  let index = 0
  let commandPosition = true
  let assignmentPending = false
  let skipWord = false
  while (index < length) {
    const char = content[index]
    if (char === "\n") { commandPosition = true; assignmentPending = false; index++; continue }
    if (char === " " || char === "\t" || char === "\r") { index++; continue }
    if (char === "#") {
      let end = content.indexOf("\n", index)
      if (end === -1) end = length
      push(index, end, "comment")
      index = end
      continue
    }
    if (char === '"' || char === "'" || char === "`") {
      let end = index + 1
      while (end < length && content[end] !== char) {
        if (char !== "'" && content[end] === "\\") end++
        end++
      }
      push(index, Math.min(end + 1, length), "string")
      index = end + 1
      continue
    }
    if (char === "$") {
      const next = content[index + 1]
      if (next === "{") {
        let end = content.indexOf("}", index + 2)
        end = end === -1 ? length : end + 1
        push(index, end, "variable.bash")
        index = end
        continue
      }
      if (next === "(") { index += 2; commandPosition = true; assignmentPending = false; continue }
      if (next && WORD.test(next)) {
        let end = index + 1
        while (end < length && WORD.test(content[end])) end++
        push(index, end, "variable.bash")
        index = end
        continue
      }
      index++
      continue
    }
    if (char === "\\") { index += 2; continue }
    if (char === "-") {
      let dashEnd = index
      while (content[dashEnd] === "-") dashEnd++
      if (WORD.test(content[dashEnd] ?? "")) {
        let end = dashEnd
        while (end < length && WORD_CHAR.test(content[end])) end++
        if (!commandPosition) push(index, end, "attribute")
        index = end
        continue
      }
      index++
      continue
    }
    if (/[0-9]/.test(char)) {
      let end = index
      while (end < length && /[0-9]/.test(content[end])) end++
      if (content[end] === ">") {
        while (content[end] === ">") end++
        if (content[end] === "&") { end++; while (/[0-9]/.test(content[end] ?? "")) end++ }
        push(index, end, "operator")
        index = end
        continue
      }
      push(index, end, "number")
      index = end
      continue
    }
    if (WORD.test(char) || char === "." || char === "/") {
      let end = index
      while (end < length && WORD_CHAR.test(content[end])) end++
      const word = content.slice(index, end)
      if (content[end] === "=" && content[end + 1] !== "=" && /^[A-Za-z_][A-Za-z0-9_]*$/.test(word)) {
        push(index, end, "property")
        index = end + 1
        if (commandPosition) assignmentPending = true
        continue
      }
      if (commandPosition) {
        if (assignmentPending) assignmentPending = false
        else if (skipWord) skipWord = false
        else if (GRAMMAR_KEYWORDS.has(word)) {
          push(index, end, "keyword")
          if (word === "for" || word === "select" || word === "case") skipWord = true
          if (word === "in") commandPosition = false
        }
        else {
          push(index, end, "function.call")
          commandPosition = false
        }
      } else if (KEYWORDS.has(word)) push(index, end, "keyword")
      index = end
      continue
    }
    if (char === ">" || char === "<") {
      let end = index
      while (content[end] === char) end++
      if (content[end] === "&") { end++; while (/[0-9]/.test(content[end] ?? "")) end++ }
      push(index, end, "operator")
      index = end
      continue
    }
    if (char === "|" || char === "&" || char === ";") {
      let end = index
      while (end < length && "&|;".includes(content[end])) end++
      push(index, end, "operator")
      index = end
      commandPosition = true
      assignmentPending = false
      continue
    }
    if (char === "(") { commandPosition = true; assignmentPending = false; index++; continue }
    index++
  }
  return highlights
}

// Wire into CodeRenderable's onHighlight: keeps real tree-sitter highlights
// when a grammar produced them, otherwise falls back to the shell scanner.
export const shellFallback: OnHighlightCallback = (highlights, context) => {
  if (highlights.length > 0) return undefined
  return shellHighlights(context.content)
}
