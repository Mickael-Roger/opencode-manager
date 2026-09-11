import { expect, test } from "bun:test"
import { agentStatuses } from "../../src/features/session/agents"

test("lists the main agent followed by its named subagents", () => {
  expect(agentStatuses(
    { sessionId: "main", updatedAt: 0, running: false, blank: false, title: "Implement feature" },
    true,
    [{ id: "child-1", label: "scout", activity: "running", mode: "one-shot" }, { id: "child-2", activity: "inactive", mode: "continuable" }],
  )).toEqual([
    { label: "Main: Implement feature", activity: "running", main: true },
    { label: "scout", activity: "running" },
    { label: "child-2", activity: "inactive" },
  ])
})
