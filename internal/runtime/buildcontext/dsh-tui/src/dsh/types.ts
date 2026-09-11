export interface SessionSummary {
  sessionId: string
  updatedAt: number
  running: boolean
  blank: boolean
  parentSessionId?: string
  origin?: "subagent"
  cwd?: string
  title?: string
  model?: ModelSelection
}

export interface SubagentSummary {
  id: string
  label?: string
  activity: "running" | "inactive"
  mode: "one-shot" | "continuable"
}

export interface ModelSelection {
  provider: string
  model: string
  reasoningEffort?: string
}

export interface ModelCatalog {
  default: ModelSelection
  routableProviders: readonly string[]
  groups: readonly {
    id: string
    name: string
    models: readonly {
      id: string
      name: string
      description?: string
      reasoning?: {
        efforts: readonly { id: string; name: string; description?: string }[]
        defaultEffort?: string
      }
    }[]
  }[]
  failures: readonly { id: string; name: string; message: string }[]
}

export interface SessionHandle {
  sessionId: string
}

export interface FileReference {
  path: string
  kind: "file" | "directory"
}

export interface CommandDescriptor {
  name: string
  description: string
  input?: { hint: string; images?: boolean }
}

export interface CommandExecution {
  commandId: string
  result:
    | { kind: "success"; text?: string; sourceEventSeq?: number }
    | { kind: "error"; text: string }
}

export interface ContextPressure {
  pressureTokens?: number
  projectedTokens?: number
  contextWindow?: number
}

export interface ContextPressureUpdate {
  sessionId: string
  pressure: ContextPressure
  seq: number
}

export type PluginFiberPhase = "pending" | "loading" | "active" | "failed" | "unloading" | null

export interface PluginInventoryEntry {
  entryId: string
  moduleName: string
  enabled: boolean
  fiberPhase: PluginFiberPhase
}

export interface PluginInventorySnapshot {
  entries: readonly PluginInventoryEntry[]
}

export interface AgentTeamTask {
  id: string
  subject: string
  status?: string
  state?: string
  assignee?: string
  dependencies?: readonly string[]
}

export interface AgentTeamMember {
  name: string
  role?: string
  activity?: string
  done?: number
  total?: number
}

export interface AgentTeamSnapshot {
  teamId: string
  name: string
  captainSessionId?: string
  phase?: string
  halted?: boolean
  members?: readonly AgentTeamMember[]
  tasks?: readonly AgentTeamTask[]
}

export interface SessionFrame {
  type: string
  seq?: number
  [key: string]: unknown
}

export type ConversationNode = {
  id: string
  kind: "user" | "assistant" | "tool" | "status" | "unknown"
  text: string
  complete?: boolean
  toolName?: string
  toolArgs?: Record<string, unknown>
  toolOutput?: string
  isError?: boolean
}

export interface ApprovalRequest {
  clientId: string
  eventId: string
  sessionId: string
  toolName: string
  callId?: string
  reason?: string
}

export type ApprovalEvent =
  | { type: "request"; request: ApprovalRequest }
  | { type: "cancel"; eventId: string }
