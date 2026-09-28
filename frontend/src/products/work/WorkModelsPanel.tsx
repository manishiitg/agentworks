import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { BrainCircuit, ChevronDown, Gauge, Loader2 } from 'lucide-react'
import { TierModelSelector } from '../../components/ui/TierModelSelector'
import { WorkspaceViewHeader } from '../../components/workflow/WorkspaceViewHeader'
import { WorkspaceViewActions } from '../../components/workflow/WorkspaceViewActions'
import GuidedProviderTerminal from '../../components/providers/GuidedProviderTerminal'
import WorkflowLLMConfigurationPanel from '../../components/workflow/WorkflowLLMConfigurationPanel'
import type { LLMProvider, PresetLLMConfig } from '../../services/api-types'
import { llmConfigService, type ModelMetadata, type ProviderSetupSession } from '../../services/llm-config-api'
import { useAuthStore } from '../../stores/useAuthStore'
import { useChatStore } from '../../stores/useChatStore'
import { useLLMStore } from '../../stores/useLLMStore'
import { buildAgentProfileEngineGroups, loadAgentProfileProviderOptions, type AgentProfileProviderOption } from '../../utils/agentProfileCapabilities'
import { WORK_PROFILE_ID, WORK_PROFILE_VERSION } from './workData'
import { workLLMSelectionFromConfig } from './workSessions'
import type { WorkRuntimeSelection } from './workTabs'

const PROVIDERS_WITH_USAGE = new Set(['claude-code', 'codex-cli', 'muse-cli'])

export function WorkModelsPanel({
  tabId,
  workspacePath,
  onAsk,
  projectLLMConfig,
  onRuntimeChange,
  hideHeader,
}: {
  tabId: string
  workspacePath: string
  onAsk?: (message: string) => void
  projectLLMConfig?: PresetLLMConfig
  onRuntimeChange: (selection: WorkRuntimeSelection) => void | Promise<void>
  hideHeader?: boolean
}) {
  const tab = useChatStore(state => state.chatTabs[tabId])
  const events = useChatStore(state => tab?.sessionId ? state.tabEvents[tab.sessionId] : undefined)
  const activeRuntime = useChatStore(state => tab?.sessionId
    ? state.activeSessionsCache.find(session => session.session_id === tab.sessionId)?.runtime
    : undefined)
  const providerManifest = useLLMStore(state => state.providerManifest)
  const providerManifestLoaded = useLLMStore(state => state.providerManifestLoaded)
  const loadProviderManifest = useLLMStore(state => state.loadProviderManifest)
  const [options, setOptions] = useState<AgentProfileProviderOption[]>([])
  const [refreshing, setRefreshing] = useState(false)
  const [modelPickerOpen, setModelPickerOpen] = useState(false)
  const [usageSession, setUsageSession] = useState<ProviderSetupSession | null>(null)
  const usageSessionRef = useRef<ProviderSetupSession | null>(null)
  const [usageStarting, setUsageStarting] = useState(false)
  const [usageError, setUsageError] = useState<string | null>(null)
  const [usageConflict, setUsageConflict] = useState(false)
  const isMultiUserMode = useAuthStore(state => state.isMultiUserMode)
  const isAdmin = useAuthStore(state => state.user?.is_admin === true)
  const canCheckUsage = !isMultiUserMode || isAdmin

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
    void loadAgentProfileProviderOptions(WORK_PROFILE_ID, WORK_PROFILE_VERSION).then(loaded => {
      if (!cancelled) setOptions(loaded)
    })
    return () => { cancelled = true }
  }, [])

  useEffect(() => {
    if (!providerManifestLoaded) void loadProviderManifest()
  }, [loadProviderManifest, providerManifestLoaded])

  const refresh = useCallback(async () => {
    setRefreshing(true)
    try {
      const [loaded] = await Promise.all([
        loadAgentProfileProviderOptions(WORK_PROFILE_ID, WORK_PROFILE_VERSION),
        loadProviderManifest(),
      ])
      setOptions(loaded)
    } finally {
      setRefreshing(false)
    }
  }, [loadProviderManifest])

  const modelCatalog = useMemo(
    () => providerManifest.flatMap(provider => provider.models || []),
    [providerManifest],
  )
  const engineGroups = useMemo(
    // Work intentionally offers the full platform catalog for each CLI. The
    // profile's model list may be present in an older running server until it
    // restarts, so do not let that stale curation hide the new project picker.
    () => buildAgentProfileEngineGroups(options.map(option => ({ ...option, models: undefined })), modelCatalog),
    [modelCatalog, options],
  )
  const workProviderIds = useMemo(
    () => options.map(option => option.provider || option.id),
    [options],
  )

  const hasStarted = events?.some(event => event.type === 'user_message') ?? false
  const savedSelection = workLLMSelectionFromConfig(projectLLMConfig)
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
    connection_id: savedSelection?.connectionId,
  } : undefined, [selectedOption, savedSelection?.connectionId])
  const defaultForOption = useCallback((option: AgentProfileProviderOption | undefined) => {
    if (!option) return { modelId: '', reasoningEffort: undefined as string | undefined }
    const defaults = providerManifest.find(provider => provider.id === option.provider)?.default_tier_models?.builder
    return {
      modelId: defaults?.model_id || option.model_id || '',
      reasoningEffort: typeof defaults?.options?.reasoning_effort === 'string'
        ? defaults.options.reasoning_effort
        : typeof option.options?.reasoning_effort === 'string'
          ? option.options.reasoning_effort
          : option.reasoning_efforts?.[0],
    }
  }, [providerManifest])

  const currentGroup = engineGroups.find(group => group.option.id === selectedOption?.id)
  const selectedDefaults = defaultForOption(selectedOption)
  const metadataMatchesSelectedProvider = tab?.metadata?.agentProfileEngine === selectedOption?.id
  const currentModelId = savedSelection?.modelId
    || (metadataMatchesSelectedProvider ? tab?.metadata?.agentProfileModelID : undefined)
    || activeRuntime?.model_id
    || selectedDefaults.modelId
  const selectableModels = useMemo(() => {
    const metadataById = new Map(modelCatalog.map(model => [model.model_id, model]))
    return (currentGroup?.models || []).map(({ id, label }) => metadataById.get(id) || {
      model_id: id,
      model_name: label,
      provider: selectedOption?.provider || '',
      context_window: 0,
      input_cost_per_1m: 0,
      output_cost_per_1m: 0,
    } satisfies ModelMetadata)
  }, [currentGroup?.models, modelCatalog, selectedOption?.provider])
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
      const session = await llmConfigService.startProviderSetup(
        selectedOption.provider,
        'usage',
        100,
        24,
        undefined,
        replaceRunning,
      )
      setUsageSession(session)
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
    const defaults = defaultForOption(option)
    void onRuntimeChange({
      engine: option.id,
      connectionId: config.connection_id,
      provider: option.provider,
      modelId: defaults.modelId,
      reasoningEffort: defaults.reasoningEffort,
    })
  }

  const selectModel = (modelId: string) => {
    if (!selectedOption) return
    const metadataMatchesProvider = tab?.metadata?.agentProfileEngine === selectedOption.id
    void onRuntimeChange({
      engine: selectedOption.id,
      connectionId: savedSelection?.connectionId,
      provider: selectedOption.provider,
      modelId,
      reasoningEffort: (metadataMatchesProvider ? tab?.metadata?.agentProfileReasoningEffort : undefined) || selectedDefaults.reasoningEffort,
    })
  }

  // Embedded in the Identity view's Models tab: the shared header owns the
  // title, tabs, and Ask AI, and the tab owns the scroll.
  const body = (
    <>
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
              <p className="mt-0.5 truncate text-xs text-muted-foreground">{currentModelLabel}</p>
            </div>
            <ChevronDown className={`h-4 w-4 shrink-0 text-muted-foreground transition-transform ${modelPickerOpen ? 'rotate-180' : ''}`} />
          </button>
          {modelPickerOpen && (
            <div className="border-t border-border px-4 pb-4 pt-3">
              <p className="text-xs text-muted-foreground">The provider's Builder model is selected by default. You can change the model at any time.</p>
              <TierModelSelector
                models={selectableModels}
                selectedModelId={currentModelId}
                onSelect={selectModel}
                className="mt-3"
              />
            </div>
          )}
        </section>
        {hasStarted && <p className="mt-3 text-xs text-muted-foreground">Changing the coding agent or model relaunches this project's retained session on the next message while keeping the project chat history.</p>}
        {canCheckUsage && usageSupported && (
          <section className="mt-5 border-t border-border pt-4">
            <div className="flex items-center justify-between gap-3">
              <div className="min-w-0">
                <h3 className="text-sm font-medium text-foreground">Provider usage</h3>
                <p className="mt-0.5 text-xs text-muted-foreground">View limits for the connected {selectedOption?.label || 'provider'} account.</p>
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
        subtitle="Choose the coding agent and model this project uses. You can change either at any time."
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
