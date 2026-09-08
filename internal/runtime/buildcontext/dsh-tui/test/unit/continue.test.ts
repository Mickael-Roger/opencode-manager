import { expect, test } from "bun:test"
import { newestWorkspaceSession } from "../../src/features/session/continue"

const session = (sessionId: string, cwd: string, extra = {}) => ({ sessionId, cwd, updatedAt: 0, running: false, blank: false, ...extra })

test("continue preserves DSH activity order while excluding unrelated and child sessions", () => {
  const sessions = [session("other", "/elsewhere"), session("child", "/work", { parentSessionId: "parent" }), session("newest", "/work"), session("older", "/work")]
  expect(newestWorkspaceSession(sessions, "/work")?.sessionId).toBe("newest")
})

test("continue returns no session for another workspace", () => {
  expect(newestWorkspaceSession([session("other", "/elsewhere")], "/work")).toBeUndefined()
})
