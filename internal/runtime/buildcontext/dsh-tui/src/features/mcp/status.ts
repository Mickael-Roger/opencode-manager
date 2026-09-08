import type { PluginFiberPhase, PluginInventoryEntry } from "../../dsh/types"

const MCP_MODULE = "@deepseek-ai/dsh-mcp-client"

export interface McpStatus {
  name: string
  phase: PluginFiberPhase
  enabled: boolean
}

export function mcpStatuses(entries: readonly PluginInventoryEntry[]): readonly McpStatus[] {
  return entries
    .filter(entry => entry.moduleName === MCP_MODULE)
    .map(entry => ({ name: mcpName(entry.entryId), phase: entry.fiberPhase, enabled: entry.enabled }))
    .sort((left, right) => left.name.localeCompare(right.name))
}

function mcpName(entryId: string): string {
  const id = entryId.split(":").at(-1) ?? entryId
  return id.replace(/^mcp-/, "") || entryId
}
