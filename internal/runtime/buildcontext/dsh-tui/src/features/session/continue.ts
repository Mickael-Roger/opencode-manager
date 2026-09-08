import { realpath } from "node:fs/promises"
import type { SessionSummary } from "../../dsh/types"

export async function canonicalPath(path: string): Promise<string> {
  return realpath(path).catch(() => path)
}

export function newestWorkspaceSession(sessions: readonly SessionSummary[], cwd: string): SessionSummary | undefined {
  // session.list is ordered by activity. Filtering preserves DSH's ordering.
  return sessions.find(session => session.cwd === cwd && !session.parentSessionId && session.origin !== "subagent")
}
