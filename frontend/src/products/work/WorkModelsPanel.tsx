import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { BrainCircuit, ChevronDown, Gauge, Loader2 } from 'lucide-react'
import { TierModelSelector } from '../../components/ui/TierModelSelector'
import { WorkspaceViewHeader } from '../../components/workflow/WorkspaceViewHeader'
import { WorkspaceViewActions } from '../../components/workflow/WorkspaceViewActions'
import GuidedProviderTerminal from '../../components/providers/GuidedProviderTerminal'
import WorkflowLLMConfigurationPanel from '../../components/workflow/WorkflowLLMConfigurationPanel'
import type { LLMProvider, PresetLLMConfig } from '../../services/api-types'
import { llmConfigService, type DynamicModelEntry, type ModelMetadata, type ProviderConnection, type ProviderSetupSession } from '../../services/llm-config-api'
import { useChatStore } from '../../stores/useChatStore'
import { useLLMStore } from '../../stores/useLLMStore'
import { buildAgentProfileEngineGroups, loadAgentProfileProviderOptions, modelReasoningLevels, type AgentProfileProviderOption } from '../../utils/agentProfileCapabilities'
import { useProjectProduct } from './projectProduct'
import { workLLMSelectionFromConfig } from './workSessions'
import type { WorkRuntimeSelection } from './workTabs'
import { readyCodingProviders } from '../../utils/providerCatalogFilter'
import { ProviderChangeNotice } from '../../components/chat/ProviderChangeNotice'
import { allowedModelOrFirst, filterAllowedModels } from '../../utils/allowedModels'
import { SharedTokenUsageNotice } from '../../components/providers/SharedTokenUsageNotice'

const PROVIDERS_WITH_USAGE = new Set(['claude-code', 'codex-cli', 'muse-cli'])

export function WorkModelsPanel({
  tabId,
  workspacePath,
  onAsk,
  projectLLMConfig,
  onRuntimeChange,
  hideHeader,
  profileId,
  profileVersion,
  accountProduct,
}: {
  tabId: string
  workspacePath: string
  onAsk?: (message: string) => void
  projectLLMConfig?: PresetLLMConfig
  onRuntimeChange: (selection: WorkRuntimeSelection) => void | Promise<void>
  hideHeader?: boolean
  profileId?: string
  profileVersion?: number
  accountProduct?: string
}) {
  // The provider options of this project's own product: a Code asks the Code
  // profile. Asking Crew's left a Code-only member (Excellence) with no
  // providers at all (issue #252).
  const product = useProjectProduct()
  const effectiveProfileId = profileId ?? product.profileId
  const effectiveProfileVersion = profileVersion ?? product.profileVersion
  const effectiveAccountProduct = accountProduct ?? effectiveProfileId
  const tab = useChatStore(state => state.chatTabs[tabId])
  const events = useChatStore(state => tab?.sessionId ? state.tabEvents[tab.sessionId] : undefined)
  const activeRuntime = useChatStore(state => tab?.sessionId
    ? state.activeSessionsCache.find(session => session.session_id === tab.sessionId)?.runtime
    : undefined)
  const providerManifest = useLLMStore(state => state.providerManifest)
  const providerManifestLoaded = useLLMStore(state => state.providerManifestLoaded)
  const loadProviderManifest = useLLMStore(state => state.loadProviderManifest)
  const [options, setOptions] = useState<AgentProfileProviderOption[]>([])
  const [cursorModels, setCursorModels] = useState<DynamicModelEntry[]>([])
  const [accounts, setAccounts] = useState<ProviderConnection[] | null>(null)
  const [refreshing, setRefreshing] = useState(false)
  const [modelPickerOpen, setModelPickerOpen] = useState(false)
  const [usageSession, setUsageSession] = useState<ProviderSetupSession | null>(null)
  // What the server collected for someone who may see usage but not open a terminal on the account.
  const [usageText, setUsageText] = useState<string | null>(null)
  const usageSessionRef = useRef<ProviderSetupSession | null>(null)
  const [usageStarting, setUsageStarting] = useState(false)
  const [usageError, setUsageError] = useState<string | null>(null)
  const [usageConflict, setUsageConflict] = useState(false)

  useEffect(() => {
    usageSessionRef.current = usageSession
  }, [usageSession])

  useEffect(() => () => {
    const current = usageSessionRef.current
    if (current?.status === 'running') {
      void llmConfigService.cancelProviderSetup(current.id).catch(() => undefined)
    }
  }, [])

  useEffect(() => {
    let cancelled = false
    void loadAgentProfileProviderOptions(effectiveProfileId, effectiveProfileVersion).then(loaded => {
      if (!cancelled) setOptions(loaded)
    })
    return () => { cancelled = true }
  }, [effectiveProfileId, effectiveProfileVersion])

  useEffect(() => {
    if (!providerManifestLoaded) void loadProviderManifest()
  }, [loadProviderManifest, providerManifestLoaded])

  useEffect(() => {
    let cancelled = false
    setAccounts(null)
    const refreshAccounts = () => {
      void llmConfigService.getProviderConnections({ workspacePath, product: effectiveAccountProduct }).then(records => {
        if (!cancelled) setAccounts(records)
      }).catch(() => { if (!cancelled) setAccounts([]) })
    }
    refreshAccounts()
    window.addEventListener('provider-connections-changed', refreshAccounts)
    return () => { cancelled = true; window.removeEventListener('provider-connections-changed', refreshAccounts) }
  }, [workspacePath, effectiveAccountProduct])

  const refresh = useCallback(async () => {
    setRefreshing(true)
    try {
      const [loaded, records] = await Promise.all([
        loadAgentProfileProviderOptions(effectiveProfileId, effectiveProfileVersion),
        llmConfigService.getProviderConnections({ workspacePath, product: effectiveAccountProduct }),
        loadProviderManifest(),
      ])
      setOptions(loaded)
      setAccounts(records)
    } finally {
      setRefreshing(false)
    }
  }, [loadProviderManifest, effectiveProfileId, effectiveProfileVersion, effectiveAccountProduct, workspacePath])

  const readyProviders = useMemo(() => accounts === null ? [] : readyCodingProviders(providerManifest, accounts), [providerManifest, accounts])
  const cursorReady = readyProviders.some(provider => provider.id === 'cursor-cli')
  useEffect(() => {
    let cancelled = false
    setCursorModels([])
    if (cursorReady) {
      void llmConfigService.getProviderModels('cursor-cli').then(result => {
        if (!cancelled) setCursorModels(result.models || [])
      }).catch(() => undefined)
    }
    return () => { cancelled = true }
  }, [cursorReady, accounts, workspacePath, effectiveProfileId])

  const modelCatalog = useMemo(() => {
    const models = readyProviders.flatMap(provider => provider.models || [])
    if (!cursorReady) return models
    const known = new Set(models.filter(model => model.provider === 'cursor-cli').map(model => model.model_id))
    // The installed CLI can offer additional models beyond the curated catalog.
    // Keep curated metadata/pricing for known IDs, and retain the exact CLI ID
    // for new choices rather than converting them into the Auto router.
    for (const model of cursorModels) {
      if (!model.model_id || known.has(model.model_id)) continue
      known.add(model.model_id)
      models.push({
        provider: 'cursor-cli', model_id: model.model_id, model_name: model.model_name,
        context_window: model.context_window || 0, input_cost_per_1m: 0, output_cost_per_1m: 0,
      })
    }
    return models
  }, [readyProviders, cursorReady, cursorModels])
  const engineGroups = useMemo(
    // Work intentionally offers the full platform catalog for each CLI. The
    // profile's model list may be present in an older running server until it
    // restarts, so do not let that stale curation hide the new project picker.
    () => buildAgentProfileEngineGroups(options
      .filter(option => readyProviders.some(provider => provider.id === option.provider))
      .map(option => ({ ...option, models: undefined })), modelCatalog),
    [readyProviders, modelCatalog, options],
  )
  // Only providers this person may run here: one with a usable account (the server's, if it is available to them,
  // or one of their own or shared with them). Offering Codex to someone it is closed to saved a model every turn then
  // refused ("the codex-cli server account is not available to you here", excellence 2026-10-07).
  const workProviderIds = useMemo(() => {
    const all = options.map(option => option.provider || option.id)
    if (accounts === null) return all
    return all.filter(provider => accounts.some(account => account.provider === provider && account.usable !== false))
  }, [options, accounts])

  const hasStarted = events?.some(event => event.type === 'user_message') ?? false
  const savedSelection = workLLMSelectionFromConfig(projectLLMConfig)
  const selectedConnectionId = savedSelection?.connectionId ?? tab?.metadata?.agentProfileConnectionID
  const savedOption = options.find(option => option.provider === savedSelection?.provider)
  const runtimeOption = options.find(option => option.provider === activeRuntime?.provider)
  const metadataOption = options.find(option => option.id === tab?.metadata?.agentProfileEngine)
  const selectedOption = savedOption
    || metadataOption
    || runtimeOption
    || options.find(option => option.default)
    || options[0]
  const llmConfig = useMemo<PresetLLMConfig | undefined>(() => selectedOption?.provider ? {
    schema_version: 2,
    mode: 'provider_profile',
    provider: selectedOption.provider as LLMProvider,
    connection_id: selectedConnectionId,
  } : undefined, [selectedOption, selectedConnectionId])
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
  const selectedAllowedModels = allowedModelsFor(selectedOption?.provider, selectedConnectionId)
  const selectedDefaults = defaultForOption(selectedOption, selectedConnectionId)
  const metadataMatchesSelectedProvider = tab?.metadata?.agentProfileEngine === selectedOption?.id
  // Only an allowed model is shown selected; a saved one the account no longer allows reads as its first allowed model
  // (the server runs it that way too).
  const currentModelId = allowedModelOrFirst(savedSelection?.modelId
    || (metadataMatchesSelectedProvider ? tab?.metadata?.agentProfileModelID : undefined)
    || activeRuntime?.model_id
    || selectedDefaults.modelId, selectedAllowedModels)
  const selectableModels = useMemo(() => {
    const metadataById = new Map(modelCatalog.map(model => [model.model_id, model]))
    return filterAllowedModels((currentGroup?.models || []).map(({ id, label }) => metadataById.get(id) || {
      model_id: id,
      model_name: label,
      provider: selectedOption?.provider || '',
      context_window: 0,
      input_cost_per_1m: 0,
      output_cost_per_1m: 0,
    } satisfies ModelMetadata), selectedAllowedModels)
  }, [currentGroup?.models, modelCatalog, selectedOption?.provider, selectedAllowedModels])
  const requestedReasoningEffort = savedSelection?.reasoningEffort
    || (metadataMatchesSelectedProvider ? tab?.metadata?.agentProfileReasoningEffort : undefined)
    || selectedDefaults.reasoningEffort
  const levelsForModel = (modelId: string, option = selectedOption) => modelReasoningLevels(option,
    modelCatalog.find(model => model.provider === option?.provider && model.model_id === modelId))
  const reasoningLevels = levelsForModel(currentModelId)
  const effortForModel = (modelId: string, requested: string | undefined, option = selectedOption) => {
    const levels = levelsForModel(modelId, option)
    return [requested, defaultForOption(option).reasoningEffort, levels[0]?.id]
      .find(effort => effort && levels.some(level => level.id === effort))
  }
  const currentReasoningEffort = effortForModel(currentModelId, requestedReasoningEffort)
  const currentModelLabel = selectableModels.find(model => model.model_id === currentModelId)?.model_name
    || currentModelId
    || 'Provider default'
  const selectedProviderManifest = providerManifest.find(provider => provider.id === selectedOption?.provider)
  const usageSupported = Boolean(
    selectedOption?.provider
    && PROVIDERS_WITH_USAGE.has(selectedOption.provider)
    && selectedProviderManifest?.usable,
  )

  const checkUsage = async (replaceRunning = false) => {
    if (!selectedOption?.provider || !usageSupported) return
    setUsageStarting(true)
    setUsageError(null)
    setUsageConflict(false)
    try {
      setUsageText(null)
      // The account this project uses: its own connection, else the server's. The server decides what the caller may
      // do with it: a terminal for the account's owner and admins, read-only text for anyone else it is available to.
      const result = await llmConfigService.checkProviderUsage(selectedOption.provider, selectedConnectionId, replaceRunning)
      if (result.session) setUsageSession(result.session)
      else setUsageText(result.usage_output || 'No usage output.')
    } catch (error) {
      const status = (error as { response?: { status?: number } })?.response?.status
      const responseMessage = (error as { response?: { data?: { error?: string } } })?.response?.data?.error
      setUsageError(responseMessage || (error instanceof Error ? error.message : 'Could not check provider usage'))
      setUsageConflict(status === 409)
    } finally {
      setUsageStarting(false)
    }
  }

  const selectProvider = (config: PresetLLMConfig) => {
    const option = options.find(candidate => candidate.provider === config.provider)
    if (!option) return
    const defaults = defaultForOption(option, config.connection_id)
    void onRuntimeChange({
      engine: option.id,
      connectionId: config.connection_id,
      provider: option.provider,
      modelId: defaults.modelId,
      reasoningEffort: effortForModel(defaults.modelId, defaults.reasoningEffort, option),
    })
  }

  const selectModel = (modelId: string) => {
    if (!selectedOption) return
    void onRuntimeChange({
      engine: selectedOption.id,
      connectionId: selectedConnectionId,
      provider: selectedOption.provider,
      modelId,
      reasoningEffort: selectedOption.provider === 'agy-cli'
        ? modelId.match(/-(low|medium|high)$/)?.[1]
        : effortForModel(modelId, currentReasoningEffort),
    })
  }

  const selectReasoningEffort = (reasoningEffort: string) => {
    if (!selectedOption || !reasoningLevels.some(level => level.id === reasoningEffort)) return
    void onRuntimeChange({
      engine: selectedOption.id,
      connectionId: selectedConnectionId,
      provider: selectedOption.provider,
      modelId: currentModelId,
      reasoningEffort,
    })
  }

  // Embedded in the Identity view's Models tab: the shared header owns the
  // title, tabs, and Ask AI, and the tab owns the scroll.
  const savedProviderUnusable = Boolean(accounts !== null && selectedOption?.provider && !workProviderIds.includes(selectedOption.provider))
  const body = (
    <>
        {savedProviderUnusable && (
          <p role="alert" className="mb-3 rounded-lg border border-amber-500/40 bg-amber-500/10 px-3 py-2 text-xs text-amber-700 dark:text-amber-300">
            This project is set to {selectedOption?.label || selectedOption?.provider}, which is not available to you here. Choose another coding agent below; your next message uses it.
          </p>
        )}
        <SharedTokenUsageNotice className="mb-3" />
        <WorkflowLLMConfigurationPanel
          workspacePath={workspacePath}
          llmConfig={llmConfig}
          onChange={selectProvider}
          scopeNoun="project"
          canWriteOverride
          allowedProviderIds={workProviderIds}
          splitPiProviders={false}
          showModelsPerRole={false}
          configurationSource="agent_profile"
          product={effectiveAccountProduct}
        />
        <section className="mt-4 overflow-hidden rounded-xl border border-border bg-card">
          <button
            type="button"
            aria-expanded={modelPickerOpen}
            onClick={() => setModelPickerOpen(open => !open)}
            className="flex w-full items-center gap-3 px-4 py-3 text-left transition-colors hover:bg-muted/40"
          >
            <div className="min-w-0 flex-1">
              <h3 className="text-sm font-semibold text-foreground">Model</h3>
              <p className="mt-0.5 truncate text-xs text-muted-foreground">{currentModelLabel}{reasoningLevels.length > 0 && currentReasoningEffort ? ` · ${currentReasoningEffort} reasoning` : ''}</p>
            </div>
            <ChevronDown className={`h-4 w-4 shrink-0 text-muted-foreground transition-transform ${modelPickerOpen ? 'rotate-180' : ''}`} />
          </button>
          {modelPickerOpen && (
            <div className="border-t border-border px-4 pb-4 pt-3">
                            <TierModelSelector
                models={selectableModels}
                selectedModelId={currentModelId}
                onSelect={selectModel}
                className="mt-3"
              />
            </div>
          )}
          {/* Reasoning effort belongs to the model: shown inside the Model card, not as a separate box. */}
          {reasoningLevels.length > 0 && (
          <div className="border-t border-border px-4 py-3">
            <p className="text-xs font-medium text-foreground">Reasoning effort</p>
            <div role="group" aria-label="Reasoning effort" className="mt-2 flex flex-wrap gap-2">
              {reasoningLevels.map(level => (
                <button
                  key={level.id}
                  type="button"
                  aria-pressed={currentReasoningEffort === level.id}
                  onClick={() => selectReasoningEffort(level.id)}
                  className={`rounded-md border px-3 py-1.5 text-xs transition-colors ${currentReasoningEffort === level.id ? 'border-primary bg-primary/10 text-primary' : 'border-border bg-background text-muted-foreground hover:bg-muted'}`}
                >
                  {level.label}
                </button>
              ))}
            </div>
          </div>
        )}
        </section>
        {hasStarted && <p className="mt-3 text-xs text-muted-foreground">Applies on the next message. Chat history is kept.</p>}
        <ProviderChangeNotice turnRunning={Boolean(tab?.isStreaming)} runningProvider={activeRuntime?.provider} selectedProvider={selectedOption?.provider} />
        {usageSupported && (
          <section className="mt-5 border-t border-border pt-4">
            <div className="flex items-center justify-between gap-3">
              <div className="min-w-0">
                <h3 className="text-sm font-medium text-foreground">Provider usage</h3>
                              </div>
              <button
                type="button"
                onClick={() => void checkUsage()}
                disabled={usageStarting || usageSession?.status === 'running'}
                className="flex shrink-0 items-center gap-1.5 rounded-md border border-border px-2.5 py-1.5 text-xs font-medium text-muted-foreground transition-colors hover:bg-muted hover:text-foreground disabled:cursor-not-allowed disabled:opacity-50"
              >
                {usageStarting ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Gauge className="h-3.5 w-3.5" />}
                Check usage
              </button>
            </div>
            {usageText && (
              <pre aria-label="Provider usage output" className="mt-3 max-h-64 overflow-auto whitespace-pre-wrap rounded-lg bg-muted p-3 text-xs text-foreground">{usageText}</pre>
            )}
            {usageSession && (
              <div className="mt-3">
                <GuidedProviderTerminal
                  session={usageSession}
                  onFinished={setUsageSession}
                  onClose={() => setUsageSession(null)}
                />
              </div>
            )}
            {usageError && (
              <div className="mt-3 rounded-lg border border-red-300 bg-red-50 px-3 py-2 text-xs text-red-700 dark:border-red-900 dark:bg-red-950/25 dark:text-red-300">
                <p>{usageError}</p>
                {usageConflict && (
                  <button
                    type="button"
                    onClick={() => void checkUsage(true)}
                    disabled={usageStarting}
                    className="mt-2 rounded-md border border-red-300 bg-background px-2.5 py-1.5 font-medium hover:bg-red-100 disabled:opacity-50 dark:border-red-800 dark:hover:bg-red-950/50"
                  >
                    End existing usage check and retry
                  </button>
                )}
              </div>
            )}
          </section>
        )}
    </>
  )

  if (hideHeader) return body

  return (
    <section className="flex h-full min-h-0 w-full flex-col bg-background">
      <WorkspaceViewHeader
        icon={BrainCircuit}
        title="Project agent configuration"
        actions={<WorkspaceViewActions
          workspacePath={workspacePath}
          message="Help me choose between the coding agents available for this project. Explain the practical differences before changing anything."
          onAsk={onAsk}
          onRefresh={refresh}
          refreshing={refreshing}
          refreshLabel="Refresh models"
        />}
      />
      <div className="min-h-0 flex-1 overflow-y-auto p-4">{body}</div>
    </section>
  )
}
