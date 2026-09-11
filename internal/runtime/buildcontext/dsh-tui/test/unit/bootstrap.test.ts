import { expect, test } from "bun:test"
import { bootstrap } from "../../src/bootstrap"
import type { DshGateway } from "../../src/dsh/gateway"

const catalog = { default: { provider: "p", model: "default" }, routableProviders: ["p"], groups: [{ id: "p", name: "Provider", models: [{ id: "default", name: "Default" }, { id: "other", name: "Other" }] }], failures: [] }
function gateway(sessions: any[] = []): DshGateway {
  return {
    listSessions: async () => sessions, listSubagents: async () => [], createSession: async () => ({ sessionId: "created" }), getModelCatalog: async () => catalog,
    selectModel: async (_id, model) => model, sendPrompt: async () => {}, cancel: async () => {}, listCommands: async id => [{ name: `command-${id}`, description: "Command" }], executeCommand: async () => undefined, completeFileReferences: async () => [],
    getPluginInventory: async () => ({ entries: [
      { entryId: "include:mcp-context7", moduleName: "@deepseek-ai/dsh-mcp-client", enabled: true, fiberPhase: "active" },
      { entryId: "include:agent-teams", moduleName: "@nanmicoder/dsh-agent-teams", enabled: true, fiberPhase: "active" },
    ] }),
    getAgentTeams: async () => [],
    async *followSession() {}, async *followContextPressure() {}, async *followApprovals() {}, answerApproval: async () => {},
  }
}

test("bootstrap resolves a continued workspace session before rendering", async () => {
  const result = await bootstrap(gateway([{ sessionId: "existing", cwd: "/work", updatedAt: 1, running: false, blank: false }]), "/work", { continueSession: true, cwd: "/work", debug: false })
  expect(result.sessionId).toBe("existing")
  expect(result.commands[0]?.name).toBe("command-existing")
  expect(result.mcp).toEqual([{ name: "context7", enabled: true, phase: "active" }])
  expect(result.agentTeams).toBe(true)
})

test("bootstrap creates a session and validates explicit model routes", async () => {
  const result = await bootstrap(gateway(), "/work", { continueSession: false, cwd: "/work", debug: false, model: "p/other" })
  expect(result.sessionId).toBe("created")
  expect(result.model).toEqual({ provider: "p", model: "other" })
  expect(result.commands[0]?.name).toBe("command-created")
})
