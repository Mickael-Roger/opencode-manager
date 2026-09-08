import { addDefaultParsers, type FiletypeParserOptions } from "@opentui/core"
import { existsSync, mkdirSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { dirname, join } from "node:path"
import { fileURLToPath } from "node:url"
import { queries } from "./queries"

interface GrammarSpec { filetype: string; aliases?: string[]; wasm: string; query: string }

// Languages whose grammars come from the tree-sitter-wasms package instead of
// the four grammars bundled with @opentui/core (js, ts, markdown, zig).
export const grammarSpecs: readonly GrammarSpec[] = [
  { filetype: "bash", aliases: ["sh", "shell", "zsh"], wasm: "tree-sitter-bash.wasm", query: "bash" },
  { filetype: "c", wasm: "tree-sitter-c.wasm", query: "c" },
  { filetype: "cpp", aliases: ["c++", "cxx"], wasm: "tree-sitter-cpp.wasm", query: "cpp" },
  { filetype: "css", wasm: "tree-sitter-css.wasm", query: "css" },
  { filetype: "go", wasm: "tree-sitter-go.wasm", query: "go" },
  { filetype: "html", wasm: "tree-sitter-html.wasm", query: "html" },
  { filetype: "json", wasm: "tree-sitter-json.wasm", query: "json" },
  { filetype: "python", aliases: ["py"], wasm: "tree-sitter-python.wasm", query: "python" },
  { filetype: "rust", aliases: ["rs"], wasm: "tree-sitter-rust.wasm", query: "rust" },
]

function wasmsDirectory(): string | undefined {
  const bases: string[] = []
  try { bases.push(dirname(fileURLToPath(import.meta.resolve("tree-sitter-wasms/package.json")))) } catch { /* not installed */ }
  bases.push(join(process.cwd(), "node_modules/tree-sitter-wasms"))
  for (const base of bases) {
    for (const subdir of ["out", "."]) {
      const directory = join(base, subdir)
      if (existsSync(join(directory, "tree-sitter-go.wasm"))) return directory
    }
  }
  return undefined
}

// Queries must live on disk for the tree-sitter worker; write the embedded
// ones to a stable scratch directory and hand the worker absolute paths.
function materializeQueries(): string | undefined {
  try {
    const directory = join(tmpdir(), "dsh-tui-queries")
    mkdirSync(directory, { recursive: true })
    return directory
  } catch { return undefined }
}

// Register extra grammars with the global tree-sitter client. Safe to call
// when tree-sitter-wasms is absent: nothing is registered and rendering
// falls back to plain text (plus the shell regex highlighter for bash).
export function registerGrammars(): number {
  try {
    const wasms = wasmsDirectory()
    const queryDirectory = materializeQueries()
    if (!wasms || !queryDirectory) return 0
    const parsers: FiletypeParserOptions[] = []
    for (const spec of grammarSpecs) {
      const wasm = join(wasms, spec.wasm)
      if (!existsSync(wasm)) continue
      const queryPath = join(queryDirectory, `${spec.filetype}-highlights.scm`)
      writeFileSync(queryPath, queries[spec.query] ?? "", "utf8")
      parsers.push({ filetype: spec.filetype, aliases: spec.aliases, queries: { highlights: [queryPath] }, wasm })
    }
    if (parsers.length) addDefaultParsers(parsers)
    return parsers.length
  } catch { return 0 }
}
