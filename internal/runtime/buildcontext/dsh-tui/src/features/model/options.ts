import type { ModelCatalog, ModelSelection } from "../../dsh/types"

export type ReasoningEffortOption = { id: string; name: string; description?: string }
export type ModelOption = { provider: string; model: string; name: string; reasoningEfforts: readonly ReasoningEffortOption[] }

export function modelOptions(catalog: ModelCatalog): readonly ModelOption[] {
  return catalog.groups.flatMap(group => group.models.map(model => ({
    provider: group.id,
    model: model.id,
    name: model.name,
    reasoningEfforts: model.reasoning?.efforts ?? [],
  })))
}

export function modelSelection(model: ModelOption, effort?: ReasoningEffortOption): ModelSelection {
  return { provider: model.provider, model: model.model, ...(effort ? { reasoningEffort: effort.id } : {}) }
}
