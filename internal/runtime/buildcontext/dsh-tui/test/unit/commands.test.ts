import { expect, test } from "bun:test"
import { commandCandidates, commandLabel, commandName, completeCommand } from "../../src/features/commands/commands"

test("completes slash commands by prefix", () => {
  const candidates = commandCandidates([{ name: "compact", description: "Compact context" }, { name: "agent-teams", description: "Manage teammates", input: { hint: "<action>" } }])
  expect(completeCommand("/mod", candidates).map(command => command.name)).toEqual(["model"])
  expect(completeCommand("/co", candidates).map(command => command.name)).toEqual(["compact", "continue"])
  expect(completeCommand("hello /mod", candidates)).toEqual([])
  expect(commandLabel(candidates.find(command => command.name === "agent-teams")!)).toBe("/agent-teams <action>   Manage teammates")
})

test("local commands take precedence over remote commands", () => {
  const candidates = commandCandidates([{ name: "model", description: "Remote model" }])
  expect(candidates.filter(command => command.name === "model")).toEqual([{ name: "model", description: "Select the active model", source: "local" }])
})

test("adds the DAG command only when the agent-teams plugin is installed", () => {
  const without = commandCandidates([])
  expect(without.some(command => command.name === "agent-teams-dag")).toBe(false)
  const withPlugin = commandCandidates([{ name: "agent-teams", description: "Run a team", input: { hint: "<goal>" } }], { agentTeams: true })
  expect(withPlugin.find(command => command.name === "agent-teams-dag")).toEqual({ name: "agent-teams-dag", description: "Show the AgentTeams task DAG", source: "local" })
  expect(withPlugin.find(command => command.name === "agent-teams")?.source).toBe("remote")
})

test("parses complete command lines without discarding arguments", () => {
  expect(commandName("/agent-teams create reviewer")).toBe("agent-teams")
  expect(commandName("hello /agent-teams")).toBeUndefined()
  expect(commandName("/Bad")).toBeUndefined()
})
