// An account may limit the models that can run on it (`allowed_models`). An
// empty or absent list means every model. The server enforces the list; this
// only keeps the pickers from offering what would be refused.

/** Whether the account limits its models. */
export function hasModelLimit(allowed?: string[] | null): allowed is string[] {
  return Array.isArray(allowed) && allowed.length > 0
}

/** The models of `models` the account allows, in their original order. */
export function filterAllowedModels<T extends { model_id: string }>(models: T[], allowed?: string[] | null): T[] {
  if (!hasModelLimit(allowed)) return models
  const wanted = new Set(allowed.map(id => id.trim().toLowerCase()))
  return models.filter(model => wanted.has(model.model_id.trim().toLowerCase()))
}

export function isModelAllowed(modelId: string | undefined, allowed?: string[] | null): boolean {
  if (!hasModelLimit(allowed)) return true
  return !!modelId && allowed.some(id => id.trim().toLowerCase() === modelId.trim().toLowerCase())
}

/** The model to show selected: the current one when allowed, else the first allowed. */
export function allowedModelOrFirst(modelId: string, allowed?: string[] | null): string
export function allowedModelOrFirst(modelId: string | undefined, allowed?: string[] | null): string | undefined
export function allowedModelOrFirst(modelId: string | undefined, allowed?: string[] | null): string | undefined {
  if (!hasModelLimit(allowed) || isModelAllowed(modelId, allowed)) return modelId
  return allowed[0]
}

/** Short card text: "All models" or "2 models". */
export function allowedModelsSummary(allowed?: string[] | null): string {
  if (!hasModelLimit(allowed)) return 'All models'
  return allowed.length === 1 ? '1 model' : `${allowed.length} models`
}
