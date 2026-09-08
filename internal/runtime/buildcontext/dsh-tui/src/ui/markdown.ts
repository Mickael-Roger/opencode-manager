import { BoxRenderable, CodeRenderable, createMarkdownCodeBlockRenderer, infoStringToFiletype, type MarkdownCodeBlockRenderer } from "@opentui/core"
import { shellFallback } from "./shell"

const SHELL_LANGUAGES = new Set(["bash", "sh", "shell", "zsh", "console"])

const codeBlock: MarkdownCodeBlockRenderer = (token, context) => {
  const fallback = context.defaultRender()
  if (!fallback) return null
  const lang = token.lang ?? ""
  const filetype = infoStringToFiletype(lang) ?? lang
  const panel = new BoxRenderable(fallback.ctx, {
    width: "100%", flexDirection: "column", paddingLeft: 1, paddingRight: 1,
    paddingTop: 1, paddingBottom: 1, marginTop: 1, marginBottom: 1, backgroundColor: "#1d1d1d",
  })
  panel.add(new CodeRenderable(fallback.ctx, {
    content: token.text, filetype, syntaxStyle: context.syntaxStyle,
    treeSitterClient: context.treeSitterClient, conceal: false, width: "100%", wrapMode: "word", drawUnstyledText: true,
    onHighlight: SHELL_LANGUAGES.has(lang) ? shellFallback : undefined,
  }))
  return panel
}

export const renderMarkdownNode = createMarkdownCodeBlockRenderer(Object.fromEntries([
  "bash", "c", "cpp", "css", "go", "html", "javascript", "json", "jsx", "markdown",
  "python", "rust", "sh", "shell", "sql", "tsx", "typescript", "yaml", "zsh",
].map(language => [language, codeBlock])))
