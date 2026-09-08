import type { AgentTeamMember, AgentTeamSnapshot, AgentTeamTask } from "../../dsh/types"

export const AGENT_TEAMS_MODULE = "@nanmicoder/dsh-agent-teams"

export function agentTeamsInstalled(entries: readonly { moduleName: string }[]): boolean {
  return entries.some(entry => entry.moduleName === AGENT_TEAMS_MODULE)
}

const MARKERS: Record<string, string> = { completed: "✓", running: "●", open: "○", blocked: "◐", failed: "✗", cancelled: "⊘" }

function taskState(task: AgentTeamTask, states: Map<string, string>): string {
  if (task.state) return task.state
  const status = task.status ?? "pending"
  if (status === "completed" || status === "failed" || status === "cancelled") return status
  if (status === "in_progress" || status === "claimed") return "running"
  const blocked = (task.dependencies ?? []).some(dep => states.get(dep) !== "completed")
  return blocked ? "blocked" : "open"
}

// Longest-path depth from dependencies; cycles fall back to depth 0 instead of
// hanging, and unknown dependency ids are ignored.
function taskDepths(tasks: readonly AgentTeamTask[]): Map<string, number> {
  const byId = new Map(tasks.map(task => [task.id, task]))
  const memo = new Map<string, number>()
  const depthOf = (task: AgentTeamTask, path: Set<string>): number => {
    if (memo.has(task.id)) return memo.get(task.id)!
    let depth = 0
    if (!path.has(task.id)) {
      path.add(task.id)
      for (const dep of task.dependencies ?? []) {
        const parent = byId.get(dep)
        if (parent) depth = Math.max(depth, depthOf(parent, path) + 1)
      }
      path.delete(task.id)
    }
    memo.set(task.id, depth)
    return depth
  }
  for (const task of tasks) depthOf(task, new Set())
  return memo
}

function memberSummary(members: readonly AgentTeamMember[]): string {
  if (!members.length) return "members: none"
  return `members: ${members.map(member => `${member.name} ${member.activity ?? "?"} ${member.done ?? 0}/${member.total ?? 0}`).join(" · ")}`
}

function teamLines(team: AgentTeamSnapshot): string[] {
  const tasks = team.tasks ?? []
  const states = new Map(tasks.map(task => [task.id, task.state ?? task.status ?? "pending"]))
  const done = tasks.filter(task => (task.state ?? task.status) === "completed").length
  const header = `${team.name} (${team.phase ?? "running"}${team.halted ? ", halted" : ""}) — ${done}/${tasks.length} tasks`
  if (!tasks.length) return [header, memberSummary(team.members ?? []), "no tasks"]
  const depths = taskDepths(tasks)
  const layers = new Map<number, AgentTeamTask[]>()
  for (const task of tasks) {
    const depth = depths.get(task.id) ?? 0
    layers.set(depth, [...(layers.get(depth) ?? []), task])
  }
  const lines = [header, memberSummary(team.members ?? [])]
  for (const depth of [...layers.keys()].sort((left, right) => left - right)) {
    for (const task of (layers.get(depth) ?? []).slice().sort((left, right) => left.id.localeCompare(right.id))) {
      const state = taskState(task, states)
      const marker = MARKERS[state] ?? "•"
      const assignee = task.assignee ? ` — ${task.assignee}` : ""
      const deps = task.dependencies?.length ? ` ◀ ${task.dependencies.join(", ")}` : ""
      lines.push(`L${depth} ${marker} ${task.subject}${assignee} (${state})${deps}`)
    }
  }
  return lines
}

export function teamPanelLines(teams: readonly AgentTeamSnapshot[]): readonly string[] {
  if (!teams.length) return ["No AgentTeams teams found.", "Create one with /agent-teams <goal>"]
  return teams.flatMap((team, index) => index === 0 ? teamLines(team) : ["", ...teamLines(team)])
}
