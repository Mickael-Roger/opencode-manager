import type { SessionSummary, SubagentSummary } from "../../dsh/types"

export interface AgentStatus {
  label: string
  activity: "running" | "inactive"
  main?: boolean
}

export function agentStatuses(main: SessionSummary | undefined, mainRunning: boolean, subagents: readonly SubagentSummary[]): AgentStatus[] {
  return [
    { label: main?.title ? `Main: ${main.title}` : "Main agent", activity: mainRunning || main?.running ? "running" : "inactive", main: true },
    ...subagents.map(agent => ({ label: agent.label ?? agent.id, activity: agent.activity })),
  ]
}
