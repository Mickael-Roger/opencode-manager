import { expect, test } from "bun:test"
import type { ModelCatalog } from "../../src/dsh/types"
import { modelOptions, modelSelection } from "../../src/features/model/options"

const catalog: ModelCatalog = {
  default: { provider: "litellm", model: "plain" },
  routableProviders: ["litellm"],
  groups: [{
    id: "litellm",
    name: "LiteLLM",
    models: [
      { id: "plain", name: "Plain" },
      { id: "thinking", name: "Thinking", reasoning: { efforts: [{ id: "low", name: "Low" }, { id: "xhigh", name: "Extra high", description: "Maximum reasoning" }], defaultEffort: "low" } },
    ],
  }],
  failures: [],
}

test("only exposes reasoning choices advertised by the model catalog", () => {
  const [plain, thinking] = modelOptions(catalog)
  expect(plain?.reasoningEfforts).toEqual([])
  expect(thinking?.reasoningEfforts).toEqual([
    { id: "low", name: "Low" },
    { id: "xhigh", name: "Extra high", description: "Maximum reasoning" },
  ])
})

test("adds reasoning effort only when one was selected", () => {
  const [plain, thinking] = modelOptions(catalog)
  expect(modelSelection(plain!)).toEqual({ provider: "litellm", model: "plain" })
  expect(modelSelection(thinking!, thinking!.reasoningEfforts[1])).toEqual({ provider: "litellm", model: "thinking", reasoningEffort: "xhigh" })
})
