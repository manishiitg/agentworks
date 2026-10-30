import { useCallback, useEffect, useMemo, useState } from 'react'
import { useLLMStore } from '../../stores/useLLMStore'
import { buildAgentProfileEngineGroups, loadAgentProfileProviderOptions, modelReasoningLevels, type AgentProfileProviderOption } from '../../utils/agentProfileCapabilities'
import { llmConfigService, type ByokModel, type DynamicModelEntry, type ModelMetadata, type ProviderConnection } from '../../services/llm-config-api'
import { byokServiceOf } from '../../utils/byok'
import { readyCodingProviders } from '../../utils/providerCatalogFilter'
import { allowedModelOrFirst, filterAllowedModels } from '../../utils/allowedModels'

// The server data both the Models page and the composer chips choose from:
// the profile's provider options, the provider catalog and this person's accounts.
export function useWorkModelSources({ profileId, profileVersion, accountProduct, workspacePath, enabled = true }: {
  profileId: string
  profileVersion?: number
  accountProduct: string
  workspacePath: string
  enabled?: boolean
}) {
  const providerManifest = useLLMStore(state => state.providerManifest)
  const providerManifestLoaded = useLLMStore(state => state.providerManifestLoaded)
  const loadProviderManifest = useLLMStore(state => state.loadProviderManifest)
  const [options, setOptions] = useState<AgentProfileProviderOption[]>([])
  const [accounts, setAccounts] = useState<ProviderConnection[] | null>(null)

  useEffect(() => {
    if (!enabled) return
    let cancelled = false
    void loadAgentProfileProviderOptions(profileId, profileVersion).then(loaded => {
      if (!cancelled) setOptions(loaded)
    })
    return () => { cancelled = true }
  }, [profileId, profileVersion, enabled])

  useEffect(() => {
    if (enabled && !providerManifestLoaded) void loadProviderManifest()
  }, [loadProviderManifest, providerManifestLoaded, enabled])

  useEffect(() => {
    if (!enabled) return
    let cancelled = false
    setAccounts(null)
    const refreshAccounts = () => {
      void llmConfigService.getProviderConnections({ workspacePath, product: accountProduct }).then(records => {
        if (!cancelled) setAccounts(records)
      }).catch(() => { if (!cancelled) setAccounts([]) })
    }
    refreshAccounts()
    window.addEventListener('provider-connections-changed', refreshAccounts)
    return () => { cancelled = true; window.removeEventListener('provider-connections-changed', refreshAccounts) }
  }, [workspacePath, accountProduct, enabled])

  const refresh = useCallback(async () => {
    const [loaded, records] = await Promise.all([
      loadAgentProfileProviderOptions(profileId, profileVersion),
      llmConfigService.getProviderConnections({ workspacePath, product: accountProduct }),
      loadProviderManifest(),
    ])
    setOptions(loaded)
    setAccounts(records)
  }, [loadProviderManifest, profileId, profileVersion, accountProduct, workspacePath])

  return { options, accounts, providerManifest, refresh }
}

/** The person's newest usable own account for a provider. */
export function ownDefaultConnectionId(accounts: ProviderConnection[] | null, provider: string | undefined) {
  return accounts
    ?.filter(account => account.provider === provider && (account.relation ?? (account.scope === 'global' ? 'server' : 'own')) === 'own'
      && account.usable !== false && account.configured !== false)
    .sort((a, b) => String(b.updated_at || '').localeCompare(String(a.updated_at || '')))[0]?.id
}

// The models and reasoning levels offered for one provider option on one account.
export function useWorkModelChoices({ options, accounts, providerManifest }: ReturnType<typeof useWorkModelSources>, { selectedOption, selectedConnectionId, inUseConnectionId, workspacePath, accountProduct }: {
  selectedOption: AgentProfileProviderOption | undefined
  /** The account the saved choice names (allowed-model limits follow it). */
  selectedConnectionId: string | undefined
  /** The account the chat actually runs on. */
  inUseConnectionId: string | undefined
  workspacePath: string
  accountProduct: string
}) {
  const [cursorModels, setCursorModels] = useState<DynamicModelEntry[]>([])
  const readyProviders = useMemo(() => accounts === null ? [] : readyCodingProviders(providerManifest, accounts), [providerManifest, accounts])
  const modelCatalog = useMemo(() => {
    const models = readyProviders.flatMap(provider => provider.id === 'cursor-cli' ? [] : provider.models || [])
    // Cursor's selected-account inventory is authoritative. Static metadata
    // must not reintroduce unavailable models (PLAT-872 / PLAT-387).
    for (const model of cursorModels) {
      models.push({
        provider: 'cursor-cli', model_id: model.model_id, model_name: model.model_name,
        context_window: model.context_window || 0, input_cost_per_1m: model.cost_input || 0, output_cost_per_1m: model.cost_output || 0,
      })
    }
    return models
  }, [readyProviders, cursorModels])
  const engineGroups = useMemo(
    // Use the platform catalog; Cursor's catalog comes from this account.
    // Do not reinsert a profile default that Cursor did not offer.
    () => buildAgentProfileEngineGroups(options
      .filter(option => readyProviders.some(provider => provider.id === option.provider))
      .map(option => ({ ...option, models: undefined, model_id: option.provider === 'cursor-cli' ? '' : option.model_id })), modelCatalog),
    [readyProviders, modelCatalog, options],
  )
  // Only providers this person may run here: one with a usable account (the server's, if it is available to them,
  // or one of their own or shared with them). Offering Codex to someone it is closed to saved a model every turn then
  // refused ("the codex-cli server account is not available to you here", server B 2026-10-07).
  const workProviderIds = useMemo(() => {
    const all = options.map(option => option.provider || option.id)
    if (accounts === null) return all
    return all.filter(provider => accounts.some(account => account.provider === provider && account.usable !== false))
  }, [options, accounts])

  // A model key account (PLAT-717): its own models (the person's picks), with the live catalog's free and price data.
  const inUseAccount = accounts?.find(account => account.id === inUseConnectionId)
  const cursorAccountId = selectedOption?.provider === 'cursor-cli' ? inUseConnectionId || 'global:cursor-cli' : undefined
  const [cursorModelsError, setCursorModelsError] = useState('')
  const [cursorSelectionDisabled, setCursorSelectionDisabled] = useState(false)
  useEffect(() => {
    let cancelled = false
    setCursorModels([]); setCursorModelsError(''); setCursorSelectionDisabled(false)
    if (cursorAccountId) {
      void llmConfigService.getProviderModels('cursor-cli', false, false, {
        connection_id: cursorAccountId, workspace_path: workspacePath, product: accountProduct,
      }).then(result => {
        if (!cancelled) {
          setCursorModels(result.models || [])
          setCursorModelsError(result.error || '')
          setCursorSelectionDisabled(result.selection_disabled === true)
        }
      }).catch(() => { if (!cancelled) setCursorModelsError('Cursor models could not be loaded. Check this account in Providers, then refresh.') })
    }
    return () => { cancelled = true }
  }, [cursorAccountId, accounts, workspacePath, accountProduct])

  const keyService = selectedOption?.provider === 'pi-cli' ? byokServiceOf(inUseAccount) : undefined
  const [keyModels, setKeyModels] = useState<ByokModel[]>([])
  const keyAccountId = keyService ? inUseAccount?.id : undefined
  useEffect(() => {
    let cancelled = false
    setKeyModels([])
    if (keyAccountId) {
      void llmConfigService.byokModels({ connection_id: keyAccountId, workspace_path: workspacePath }).then(result => {
        if (!cancelled) setKeyModels(result.models || [])
      }).catch(() => undefined)
    }
    return () => { cancelled = true }
  }, [keyAccountId, workspacePath])

  // The models an account allows (absent = every model): the account the project names, else the server's.
  const allowedModelsFor = useCallback((provider: string | undefined, connectionId: string | undefined) => (
    provider ? accounts?.find(account => account.id === (connectionId || `global:${provider}`))?.allowed_models : undefined
  ), [accounts])
  const defaultForOption = useCallback((option: AgentProfileProviderOption | undefined, connectionId?: string) => {
    if (!option) return { modelId: '', reasoningEffort: undefined as string | undefined }
    const defaults = providerManifest.find(provider => provider.id === option.provider)?.default_tier_models?.builder
    const profileEffort = typeof option.options?.reasoning_effort === 'string' ? option.options.reasoning_effort : undefined
    const manifestEffort = typeof defaults?.options?.reasoning_effort === 'string' ? defaults.options.reasoning_effort : undefined
    return {
      modelId: allowedModelOrFirst(defaults?.model_id || option.model_id || '', allowedModelsFor(option.provider, connectionId)) || '',
      reasoningEffort: [profileEffort, manifestEffort, option.reasoning_efforts?.[0]]
        .find(effort => effort && option.reasoning_efforts?.includes(effort)),
    }
  }, [providerManifest, allowedModelsFor])

  const currentGroup = engineGroups.find(group => group.option.id === selectedOption?.id)
  const selectedAllowedModels = keyService ? inUseAccount?.allowed_models : allowedModelsFor(selectedOption?.provider, selectedConnectionId)
  const keyModelChoices = useMemo<ModelMetadata[] | null>(() => {
    if (!keyService) return null
    const byId = new Map(keyModels.map(model => [model.model_id, model]))
    const picks = inUseAccount?.allowed_models?.length ? inUseAccount.allowed_models : keyModels.filter(model => model.recommended).slice(0, 12).map(model => model.model_id)
    return picks.map(id => {
      const meta = byId.get(id)
      return {
        model_id: id, model_name: meta?.model_name || id.slice(id.indexOf('/') + 1), provider: 'pi-cli',
        context_window: meta?.context_window || 0, input_cost_per_1m: meta?.cost_input || 0, output_cost_per_1m: meta?.cost_output || 0, is_free: meta?.is_free,
      } satisfies ModelMetadata
    }).sort((a, b) => Number(Boolean(b.is_free)) - Number(Boolean(a.is_free)))
  }, [keyService, keyModels, inUseAccount?.allowed_models])
  const selectableModels = useMemo(() => {
    if (keyModelChoices) return keyModelChoices
    const metadataById = new Map(modelCatalog.map(model => [model.model_id, model]))
    return filterAllowedModels((currentGroup?.models || []).map(({ id, label }) => metadataById.get(id) || {
      model_id: id,
      model_name: label,
      provider: selectedOption?.provider || '',
      context_window: 0,
      input_cost_per_1m: 0,
      output_cost_per_1m: 0,
    } satisfies ModelMetadata), selectedAllowedModels)
  }, [keyModelChoices, currentGroup?.models, modelCatalog, selectedOption?.provider, selectedAllowedModels])
  // Another provider's models for the composer's switcher, as allowed on the account the server would pick for it.
  // Cursor and key accounts list their models per account; switching to them starts on their default model.
  const modelsForOption = (option: AgentProfileProviderOption) => {
    if (option.id === selectedOption?.id) return selectableModels
    if (option.provider === 'cursor-cli' || option.provider === 'pi-cli') return []
    const group = engineGroups.find(candidate => candidate.option.id === option.id)
    return filterAllowedModels((group?.models || []).map(({ id, label }) => ({
      model_id: id, model_name: label, provider: option.provider || '', context_window: 0, input_cost_per_1m: 0, output_cost_per_1m: 0,
    } satisfies ModelMetadata)), allowedModelsFor(option.provider, ownDefaultConnectionId(accounts, option.provider)))
  }
  const levelsForModel = (modelId: string, option = selectedOption) => modelReasoningLevels(option,
    modelCatalog.find(model => model.provider === option?.provider && model.model_id === modelId))
  const effortForModel = (modelId: string, requested: string | undefined, option = selectedOption) => {
    const levels = levelsForModel(modelId, option)
    return [requested, defaultForOption(option).reasoningEffort, levels[0]?.id]
      .find(effort => effort && levels.some(level => level.id === effort))
  }
  // The reasoning level saved with a newly picked model; Antigravity encodes it in the model id.
  const effortForPick = (modelId: string, requested: string | undefined, option = selectedOption) => option?.provider === 'agy-cli'
    ? modelId.match(/-(low|medium|high)$/)?.[1]
    : effortForModel(modelId, requested, option)

  return {
    workProviderIds, inUseAccount, keyService, cursorModels, cursorModelsError, cursorSelectionDisabled,
    defaultForOption, selectedAllowedModels, selectableModels, modelsForOption, levelsForModel, effortForModel, effortForPick,
  }
}
