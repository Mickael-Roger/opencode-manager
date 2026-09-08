import { mkdir, readFile, rename, writeFile } from "node:fs/promises"
import { dirname, join } from "node:path"

const limit = 100

export function normalizePromptHistory(value: unknown): string[] {
  if (!Array.isArray(value)) return []
  return value.filter((entry): entry is string => typeof entry === "string" && entry.length > 0).slice(-limit)
}

export function appendPromptHistory(history: readonly string[], prompt: string): string[] {
  return history.at(-1) === prompt ? [...history] : [...history, prompt].slice(-limit)
}

// DSH owns session persistence. This OCM-owned, workspace-local file retains
// only the TUI composer history across dsh-tui client restarts.
export class PromptHistoryStore {
  private readonly path: string
  private writes = Promise.resolve()

  constructor(path = join(process.env.HOME ?? ".", ".local", "state", "opencode-manager", "dsh-prompt-history.json")) {
    this.path = path
  }

  async load(): Promise<string[]> {
    try {
      return normalizePromptHistory(JSON.parse(await readFile(this.path, "utf8")))
    } catch {
      return []
    }
  }

  save(history: readonly string[]): void {
    const body = JSON.stringify(normalizePromptHistory(history))
    this.writes = this.writes.then(async () => {
      await mkdir(dirname(this.path), { recursive: true })
      const temporary = `${this.path}.tmp`
      await writeFile(temporary, body, { mode: 0o600 })
      await rename(temporary, this.path)
    }).catch(() => {})
  }

  async flush(): Promise<void> {
    await this.writes
  }
}
