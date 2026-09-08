import type { CommandDescriptor } from "../../dsh/types"

export interface CommandCandidate {
  name: string
  description: string
  source: "local" | "remote"
  input?: CommandDescriptor["input"]
}

export interface LocalCommandOptions {
  agentTeams?: boolean
}

export function localCommands(options: LocalCommandOptions = {}): readonly CommandCandidate[] {
  const base: readonly CommandCandidate[] = [
    { name: "model", description: "Select the active model", source: "local" },
    { name: "sessions", description: "Open a session", source: "local" },
    { name: "new", description: "Create a session", source: "local" },
    { name: "continue", description: "Continue the latest workspace session", source: "local" },
    { name: "help", description: "Show keyboard shortcuts", source: "local" },
    { name: "quit", description: "Quit the TUI", source: "local" },
  ]
  return options.agentTeams
    ? [...base, { name: "agent-teams-dag", description: "Show the AgentTeams task DAG", source: "local" as const }]
    : base
}

export function commandCandidates(remote: readonly CommandDescriptor[], options: LocalCommandOptions = {}): readonly CommandCandidate[] {
  const local = localCommands(options)
  const localNames = new Set(local.map(command => command.name))
  return [...local, ...remote.filter(command => !localNames.has(command.name)).map(command => ({ ...command, source: "remote" as const }))]
    .sort((left, right) => left.name.localeCompare(right.name))
}

export function completeCommand(input: string, candidates: readonly CommandCandidate[]): readonly CommandCandidate[] {
  const match = /^\/([^\s]*)$/.exec(input)
  if (!match) return []
  const prefix = match[1]!.toLowerCase()
  return candidates.filter(command => command.name.startsWith(prefix))
}

export function commandName(line: string): string | undefined {
  return /^\/([a-z][a-z0-9_-]*)(?:\s|$)/.exec(line)?.[1]
}

export function commandLabel(command: CommandCandidate): string {
  const hint = command.input?.hint ? ` ${command.input.hint}` : ""
  return `/${command.name}${hint}   ${command.description}`
}
