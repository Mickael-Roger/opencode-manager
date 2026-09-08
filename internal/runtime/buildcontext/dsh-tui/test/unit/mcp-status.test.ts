import { expect, test } from "bun:test"
import { mcpStatuses } from "../../src/features/mcp/status"

test("extracts and labels MCP plugin rows", () => {
  expect(mcpStatuses([
    { entryId: "include:mcp-context7", moduleName: "@deepseek-ai/dsh-mcp-client", enabled: true, fiberPhase: "active" },
    { entryId: "include:commands", moduleName: "@deepseek-ai/dsh-commands", enabled: true, fiberPhase: "active" },
    { entryId: "custom-memory", moduleName: "@deepseek-ai/dsh-mcp-client", enabled: false, fiberPhase: null },
  ])).toEqual([
    { name: "context7", enabled: true, phase: "active" },
    { name: "custom-memory", enabled: false, phase: null },
  ])
})

test("returns no rows when the MCP module is absent", () => {
  expect(mcpStatuses([])).toEqual([])
})
