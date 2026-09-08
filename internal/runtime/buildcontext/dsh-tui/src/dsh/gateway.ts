import type { AgentTeamSnapshot, ApprovalEvent, CommandDescriptor, CommandExecution, ContextPressureUpdate, FileReference, ModelCatalog, ModelSelection, PluginInventorySnapshot, SessionFrame, SessionHandle, SessionSummary } from "./types"

// The application only depends on this interface. Transport details, including
// DSH's launch-token cookie exchange, remain inside remote-gateway.ts.
export interface DshGateway {
  listSessions(): Promise<readonly SessionSummary[]>
  createSession(cwd: string, sessionId?: string): Promise<SessionHandle>
  getModelCatalog(): Promise<ModelCatalog>
  selectModel(sessionId: string, selection: ModelSelection): Promise<ModelSelection>
  sendPrompt(sessionId: string, text: string): Promise<void>
  cancel(sessionId: string): Promise<void>
  listCommands(sessionId: string): Promise<readonly CommandDescriptor[]>
  executeCommand(sessionId: string, line: string): Promise<CommandExecution | undefined>
  getPluginInventory(): Promise<PluginInventorySnapshot>
  getAgentTeams(): Promise<readonly AgentTeamSnapshot[]>
  completeFileReferences(sessionId: string, query: string): Promise<readonly FileReference[]>
  followSession(sessionId: string, signal: AbortSignal): AsyncIterable<SessionFrame>
  followContextPressure(signal: AbortSignal): AsyncIterable<ContextPressureUpdate>
  followApprovals(signal: AbortSignal): AsyncIterable<ApprovalEvent>
  answerApproval(clientId: string, eventId: string, decision: "allowed-once" | "rejected"): Promise<void>
}
