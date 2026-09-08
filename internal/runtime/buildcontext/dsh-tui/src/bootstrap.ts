import type { CliOptions } from "./cli"
import type { DshGateway } from "./dsh/gateway"
import type { CommandDescriptor, ModelCatalog, ModelSelection, SessionSummary } from "./dsh/types"
import { mcpStatuses, type McpStatus } from "./features/mcp/status"
import { agentTeamsInstalled } from "./features/teams/dag"
import { newestWorkspaceSession } from "./features/session/continue"

export interface InitialState {
  sessions: readonly SessionSummary[]
  catalog: ModelCatalog
  sessionId: string
  model: ModelSelection
  commands: readonly CommandDescriptor[]
  mcp: readonly McpStatus[]
  agentTeams: boolean
}

export async function bootstrap(gateway: DshGateway, cwd: string, options: CliOptions): Promise<InitialState> {
  const [sessions, catalog, inventory] = await Promise.all([gateway.listSessions(), gateway.getModelCatalog(), gateway.getPluginInventory()])
  let sessionId: string
  if (options.session) {
    if (!sessions.some(session => session.sessionId === options.session)) throw new Error(`Session ${options.session} does not exist`)
    sessionId = options.session
  } else {
    const previous = options.continueSession ? newestWorkspaceSession(sessions, cwd) : undefined
    sessionId = previous?.sessionId ?? (await gateway.createSession(cwd)).sessionId
  }
  let model = sessions.find(session => session.sessionId === sessionId)?.model ?? catalog.default
  if (options.model) {
    const available = catalog.groups.flatMap(group => group.models.map(item => ({ provider: group.id, model: item.id })))
    const selection = available.find(item => `${item.provider}/${item.model}` === options.model)
    if (!selection) throw new Error(`Model route ${options.model} is not available from DSH`)
    model = await gateway.selectModel(sessionId, selection)
  }
  const commands = await gateway.listCommands(sessionId)
  return { sessions, catalog, sessionId, model, commands, mcp: mcpStatuses(inventory.entries), agentTeams: agentTeamsInstalled(inventory.entries) }
}
