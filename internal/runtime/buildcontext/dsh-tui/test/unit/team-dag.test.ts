import { expect, test } from "bun:test"
import { agentTeamsInstalled, teamPanelLines } from "../../src/features/teams/dag"
import type { AgentTeamSnapshot } from "../../src/dsh/types"

const team = (tasks: AgentTeamSnapshot["tasks"], members: AgentTeamSnapshot["members"] = []): AgentTeamSnapshot => ({
  teamId: "t1", name: "review-squad", captainSessionId: "s1", phase: "running", members, tasks,
})

test("layers tasks by longest dependency path with markers and dependency trails", () => {
  const lines = teamPanelLines([team([
    { id: "requirements", subject: "Requirements", state: "completed", assignee: "analyst", dependencies: [] },
    { id: "implementation", subject: "Implementation", state: "running", assignee: "impl", dependencies: ["requirements"] },
    { id: "docs", subject: "Docs", state: "open", assignee: "writer", dependencies: ["requirements"] },
    { id: "review", subject: "Review", state: "blocked", assignee: "qa", dependencies: ["implementation", "docs"] },
  ], [
    { name: "analyst", activity: "idle", done: 1, total: 1 },
    { name: "impl", activity: "working", done: 0, total: 1 },
  ])])
  expect(lines).toEqual([
    "review-squad (running) — 1/4 tasks",
    "members: analyst idle 1/1 · impl working 0/1",
    "L0 ✓ Requirements — analyst (completed)",
    "L1 ○ Docs — writer (open) ◀ requirements",
    "L1 ● Implementation — impl (running) ◀ requirements",
    "L2 ◐ Review — qa (blocked) ◀ implementation, docs",
  ])
})

test("dependency cycles terminate and unknown dependencies are ignored", () => {
  const lines = teamPanelLines([team([
    { id: "a", subject: "A", state: "open", dependencies: ["b"] },
    { id: "b", subject: "B", state: "open", dependencies: ["a"] },
    { id: "c", subject: "C", state: "open", dependencies: ["ghost"] },
  ])])
  const taskLines = lines.filter(line => line.startsWith("L"))
  expect(taskLines).toHaveLength(3)
  expect(taskLines.find(line => line.includes("C"))?.startsWith("L0 ")).toBe(true)
})

test("derives visual state from status when the snapshot omits it", () => {
  const lines = teamPanelLines([team([
    { id: "a", subject: "A", status: "completed", dependencies: [] },
    { id: "b", subject: "B", status: "pending", dependencies: ["a"] },
    { id: "c", subject: "C", status: "in_progress", dependencies: [] },
  ])])
  expect(lines[2]).toBe("L0 ✓ A (completed)")
  expect(lines[3]).toBe("L0 ● C (running)")
  expect(lines[4]).toBe("L1 ○ B (open) ◀ a")
})

test("renders placeholders for no team and empty task lists", () => {
  expect(teamPanelLines([])).toEqual(["No AgentTeams teams found.", "Create one with /agent-teams <goal>"])
  const empty = teamPanelLines([team([])])
  expect(empty).toEqual(["review-squad (running) — 0/0 tasks", "members: none", "no tasks"])
})

test("detects the plugin through the inventory module name", () => {
  expect(agentTeamsInstalled([{ moduleName: "@nanmicoder/dsh-agent-teams" }])).toBe(true)
  expect(agentTeamsInstalled([{ moduleName: "@deepseek-ai/dsh-mcp-client" }])).toBe(false)
})
