import { Suspense, lazy, useCallback, useEffect, useMemo, useState } from 'react'
import { BrainCircuit, ChevronDown, Cpu, KeyRound } from 'lucide-react'
import { openWorkspaceSettings } from '../../utils/workspaceSettingsNavigation'
import { TierModelSelector } from '../../components/ui/TierModelSelector'
import { CursorModelPicker } from '../../components/ui/CursorModelPicker'
import { WorkspaceViewHeader } from '../../components/workflow/WorkspaceViewHeader'
import { WorkspaceViewActions } from '../../components/workflow/WorkspaceViewActions'
import WorkflowLLMConfigurationPanel from '../../components/workflow/WorkflowLLMConfigurationPanel'
import { agentApi } from '../../services/api'
import { useQueryClient } from '@tanstack/react-query'
import { workChatModelsQueryKey } from './useWorkChatModels'
import { isProjectProductId } from './projectProduct'
import type { AgentProfileChatModel, AgentProfileSideChat, LLMProvider, PresetLLMConfig } from '../../services/api-types'
import { llmConfigService, type ProviderConnection } from '../../services/llm-config-api'
import ModalPortal from '../../components/ui/ModalPortal'
// Loaded on first use: the key setup is rare and kept the eager bundle over budget (PLAT-717).
const ByokSetup = lazy(() => import('../../components/providers/ByokSetup'))
const ByokModelBrowser = lazy(() => import('../../components/providers/ByokModelBrowser'))
import { useChatStore } from '../../stores/useChatStore'
import { useProjectProduct } from './projectProduct'
import { workLLMSelectionFromConfig } from './workSessions'
import type { WorkRuntimeSelection } from './workTabs'
import { ProviderChangeNotice } from '../../components/chat/ProviderChangeNotice'
import { allowedModelOrFirst } from '../../utils/allowedModels'
import { ownDefaultConnectionId, useWorkModelChoices, useWorkModelSources } from './useWorkModelChoices'
import { SharedTokenUsageNotice } from '../../components/providers/SharedTokenUsageNotice'
import { sharedAccountProvider } from '../../utils/tokenLimits'

export function WorkModelsPanel({
  tabId,
  workspacePath,
  projectId: projectIdProp,
  projectLLMConfig,
  onRuntimeChange,
  hideHeader,
  profileId,
  profileVersion,
  accountProduct,
}: {
  tabId: string
  workspacePath: string
  projectId?: string
  projectLLMConfig?: PresetLLMConfig
  onRuntimeChange: (selection: WorkRuntimeSelection) => void | Promise<void>
  hideHeader?: boolean
  profileId?: string
  profileVersion?: number
  accountProduct?: string
}) {
  // The provider options of this project's own product: a Code asks the Code
  // profile. Asking Crew's left a Code-only member (server B) with no
  // providers at all (issue #252).
  const queryClient = useQueryClient()
  const product = useProjectProduct()
  const effectiveProfileId = profileId ?? product.profileId
  const effectiveProfileVersion = profileVersion ?? product.profileVersion
  const effectiveAccountProduct = accountProduct ?? effectiveProfileId
  const tab = useChatStore(state => state.chatTabs[tabId])
  const projectId = projectIdProp || tab?.metadata?.agentProfileProjectId
  const initialModelTarget = tab?.metadata?.agentProfileConversationKey
  const mainChatKey = useChatStore(state => Object.values(state.chatTabs).find(chat =>
    chat.metadata?.agentProfileId === effectiveProfileId && chat.metadata?.agentProfileProjectId === projectId && chat.metadata?.agentProfileIsMainChat === true)?.metadata?.agentProfileConversationKey) || projectId
  const supportsChatModels = isProjectProductId(effectiveProfileId) && Boolean(projectId)
  const [chatModels, setChatModels] = useState<AgentProfileChatModel[]>([])
  const [modelChats, setModelChats] = useState<AgentProfileSideChat[]>([])
  const [modelTarget, setModelTarget] = useState('') // Empty = Crew default.
  const [chatModelsLoading, setChatModelsLoading] = useState(false)
  const [chatModelsReady, setChatModelsReady] = useState(false)
  const [chatModelSaving, setChatModelSaving] = useState(false)
  const [chatModelError, setChatModelError] = useState('')
  useEffect(() => {
    if (!supportsChatModels || !projectId) return
    let cancelled = false
    setChatModelsLoading(true); setChatModelsReady(false); setChatModelError(''); setModelTarget(initialModelTarget === projectId || initialModelTarget?.startsWith(`${projectId}:chat:`) ? initialModelTarget : ''); setChatModels([]); setModelChats([])
    void Promise.resolve().then(() => Promise.all([agentApi.listAgentProfileChatModels(effectiveProfileId, projectId), agentApi.listAgentProfileSideChats(effectiveProfileId, projectId)])).then(([models, chats]) => {
      if (cancelled) return
      setChatModels(models); setModelChats(chats.filter(chat => !chat.agent_id)); setChatModelsReady(true)
      // The main chat's tab may carry the bare project id; the server keys the chat as <project>:chat:root-…,
      // so open on that chat, not the Crew's model.
      if (initialModelTarget === projectId) {
        const mainKey = [...models.map(model => model.conversation_key), ...chats.map(chat => chat.conversation_key)].find(key => key?.startsWith(`${projectId}:chat:root-`))
        if (mainKey) setModelTarget(mainKey)
      }
    }).catch(() => { if (!cancelled) setChatModelError('Chat models could not be loaded. Reopen Models to retry.') })
      .finally(() => { if (!cancelled) setChatModelsLoading(false) })
    return () => { cancelled = true }
  }, [effectiveProfileId, projectId, supportsChatModels, initialModelTarget])
  const observingModel = Boolean(modelTarget) && modelChats.some(chat => chat.conversation_key === modelTarget && chat.can_write === false)
  const ownChatModel = modelTarget ? chatModels.find(model => model.conversation_key === modelTarget) : undefined
  const saveChatModel = async (selection?: WorkRuntimeSelection) => {
    if (observingModel || !projectId || !modelTarget || !chatModelsReady || chatModelSaving || chatModelsLoading) return
    setChatModelSaving(true); setChatModelError('')
    try {
      const models = await agentApi.setAgentProfileChatModel(effectiveProfileId, projectId, {
        conversation_key: modelTarget, engine: selection?.engine || '', model_id: selection?.modelId,
        reasoning_effort: selection?.reasoningEffort, connection_id: selection?.connectionId,
      })
      setChatModels(models)
      await queryClient.invalidateQueries({ queryKey: workChatModelsQueryKey(effectiveProfileId, projectId) })
    } catch (cause) {
      setChatModelError(cause instanceof Error ? cause.message : 'Could not save this chat model.')
    } finally { setChatModelSaving(false) }
  }
  const changeSelection = (selection: WorkRuntimeSelection) => modelTarget ? saveChatModel(selection) : onRuntimeChange(selection)
  const events = useChatStore(state => tab?.sessionId ? state.tabEvents[tab.sessionId] : undefined)
  const activeRuntime = useChatStore(state => tab?.sessionId
    ? state.activeSessionsCache.find(session => session.session_id === tab.sessionId)?.runtime
    : undefined)
  const sources = useWorkModelSources({ profileId: effectiveProfileId, profileVersion: effectiveProfileVersion, accountProduct: effectiveAccountProduct, workspacePath })
  const { options, accounts, providerManifest, refresh: refreshSources } = sources
  const [refreshing, setRefreshing] = useState(false)
  const [modelPickerOpen, setModelPickerOpen] = useState(false)

  const refresh = useCallback(async () => {
    setRefreshing(true)
    try {
      await refreshSources()
    } finally {
      setRefreshing(false)
    }
  }, [refreshSources])

  const hasStarted = events?.some(event => event.type === 'user_message') ?? false
  const projectSelection = workLLMSelectionFromConfig(projectLLMConfig)
  const ownOption = options.find(option => option.id === ownChatModel?.engine)
  const savedSelection = ownChatModel ? {
    provider: ownOption?.provider, modelId: ownChatModel.model_id || '', reasoningEffort: ownChatModel.reasoning_effort,
    connectionId: ownChatModel.connection_id || (ownOption?.provider === projectSelection?.provider ? projectSelection?.connectionId : undefined),
  } : projectSelection
  const selectedConnectionId = savedSelection?.connectionId ?? (supportsChatModels ? undefined : tab?.metadata?.agentProfileConnectionID)
  const savedOption = options.find(option => option.provider === savedSelection?.provider)
  // The account this chat runs on: the project's choice, else the one the chat recorded for this provider,
  // else the default the server resolves (the person's own signed-in account, newest first; else the server's).
  const runtimeOption = options.find(option => option.provider === activeRuntime?.provider)
  const metadataOption = options.find(option => option.id === tab?.metadata?.agentProfileEngine)
  const selectedOption = savedOption
    || ownOption
    || (!supportsChatModels ? metadataOption : undefined)
    || (!supportsChatModels ? runtimeOption : undefined)
    || options.find(option => option.default)
    || options[0]
  const recordedConnectionId = !supportsChatModels && tab?.metadata?.agentProfileChatProvider === selectedOption?.provider
    ? tab?.metadata?.agentProfileChatConnectionID || undefined
    : undefined
  const inUseConnectionId = selectedConnectionId || recordedConnectionId || ownDefaultConnectionId(accounts, selectedOption?.provider)
  // A company account from a connected server counts on that server: its limits are shown from there.
  const companyServer = accounts?.find(account => account.id === inUseConnectionId)?.via_connection
  const {
    workProviderIds, inUseAccount, keyService, cursorModels, cursorModelsError, cursorSelectionDisabled,
    defaultForOption, selectedAllowedModels, selectableModels, levelsForModel, effortForModel, effortForPick,
  } = useWorkModelChoices(sources, { selectedOption, selectedConnectionId, inUseConnectionId, workspacePath, accountProduct: effectiveAccountProduct })
  const [keyBrowserOpen, setKeyBrowserOpen] = useState(false)
  const [keyPicksDraft, setKeyPicksDraft] = useState<string[] | null>(null)
  const [keySetupOpen, setKeySetupOpen] = useState(false)
  const llmConfig = useMemo<PresetLLMConfig | undefined>(() => selectedOption?.provider ? {
    schema_version: 2,
    mode: 'provider_profile',
    provider: selectedOption.provider as LLMProvider,
    connection_id: selectedConnectionId || recordedConnectionId,
  } : undefined, [selectedOption, selectedConnectionId, recordedConnectionId])

  const selectedDefaults = defaultForOption(selectedOption, selectedConnectionId)
  const metadataMatchesSelectedProvider = !supportsChatModels && tab?.metadata?.agentProfileEngine === selectedOption?.id
  // Only an allowed model is shown selected; a saved one the account no longer allows reads as its first allowed model
  // (the server runs it that way too).
  const currentModelId = allowedModelOrFirst(savedSelection?.modelId
    || (metadataMatchesSelectedProvider ? tab?.metadata?.agentProfileModelID : undefined)
    || (!supportsChatModels ? activeRuntime?.model_id : undefined)
    || selectedDefaults.modelId, selectedAllowedModels)
  const requestedReasoningEffort = savedSelection?.reasoningEffort
    || (metadataMatchesSelectedProvider ? tab?.metadata?.agentProfileReasoningEffort : undefined)
    || selectedDefaults.reasoningEffort
  const reasoningLevels = levelsForModel(currentModelId)
  const currentReasoningEffort = effortForModel(currentModelId, requestedReasoningEffort)
  const currentModelLabel = selectableModels.find(model => model.model_id === currentModelId)?.model_name
    || currentModelId
    || 'Provider default'
  const saveKeyPicks = async (picks: string[]) => {
    if (!inUseAccount) return
    try {
      await llmConfigService.setAccountAllowedModels(inUseAccount.id, picks)
      window.dispatchEvent(new Event('provider-connections-changed'))
      setKeyBrowserOpen(false); setKeyPicksDraft(null)
    } catch { /* the browser stays open; the person can retry */ }
  }
  const piOption = options.find(option => option.provider === 'pi-cli')
  const piRuntimeAvailable = providerManifest.find(provider => provider.id === 'pi-cli')?.runtime_available !== false
  const keyEntryAvailable = Boolean(piOption && piRuntimeAvailable)
  const switchToNewKey = (record: ProviderConnection, picks: string[]) => {
    setKeySetupOpen(false)
    if (!piOption) return
    void changeSelection({ engine: piOption.id, connectionId: record.id, provider: 'pi-cli', modelId: picks[0] || '', reasoningEffort: undefined })
  }

  const selectProvider = (config: PresetLLMConfig) => {
    const option = options.find(candidate => candidate.provider === config.provider)
    if (!option) return
    const defaults = defaultForOption(option, config.connection_id)
    void changeSelection({
      engine: option.id,
      connectionId: config.connection_id,
      provider: option.provider,
      modelId: defaults.modelId,
      reasoningEffort: effortForModel(defaults.modelId, defaults.reasoningEffort, option),
    })
  }

  const selectModel = (modelId: string) => {
    if (!selectedOption) return
    void changeSelection({
      engine: selectedOption.id,
      connectionId: selectedConnectionId,
      provider: selectedOption.provider,
      modelId,
      reasoningEffort: effortForPick(modelId, currentReasoningEffort),
    })
  }

  const selectReasoningEffort = (reasoningEffort: string) => {
    if (!selectedOption || !reasoningLevels.some(level => level.id === reasoningEffort)) return
    void changeSelection({
      engine: selectedOption.id,
      connectionId: selectedConnectionId,
      provider: selectedOption.provider,
      modelId: currentModelId,
      reasoningEffort,
    })
  }

  // Embedded in the Identity view's Models tab: the shared header owns the
  // title and tabs, and the tab owns the scroll.
  const savedProviderUnusable = Boolean(accounts !== null && selectedOption?.provider && !workProviderIds.includes(selectedOption.provider))
  const body = (
    <>
        {supportsChatModels && <section className="mb-4 overflow-hidden rounded-xl border border-border" aria-label="Models by chat">
          <button type="button" disabled={chatModelSaving} aria-pressed={!modelTarget} onClick={() => setModelTarget('')} className={`flex w-full items-center justify-between gap-3 px-3 py-2.5 text-left text-sm ${!modelTarget ? 'bg-primary/10' : 'hover:bg-muted'}`}>
            <span className="font-medium">Crew’s model</span><span className="text-xs text-muted-foreground">For chats without their own model</span>
          </button>
          {chatModelsLoading ? <p role="status" className="px-3 py-2 text-xs text-muted-foreground">Loading chats…</p> : [
            { conversation_key: mainChatKey!, name: 'Main chat', can_write: true },
            ...modelChats,
          ].map(chat => {
            const own = chatModels.find(model => model.conversation_key === chat.conversation_key)
            return <button key={chat.conversation_key} type="button" disabled={chat.can_write === false || chatModelSaving || !chatModelsReady} aria-pressed={modelTarget === chat.conversation_key} onClick={() => setModelTarget(chat.conversation_key)} className={`flex w-full items-center justify-between gap-3 border-t border-border px-3 py-2 text-left text-sm disabled:opacity-50 ${modelTarget === chat.conversation_key ? 'bg-primary/10' : 'hover:bg-muted'}`}>
              <span className="min-w-0 truncate">{chat.name}</span>
              <span className="max-w-[60%] truncate text-xs text-muted-foreground">{own ? own.display_label || 'Selected model' : "Crew's model"}</span>
            </button>
          })}
        </section>}
        {chatModelError && <p role="alert" className="mb-3 text-sm text-destructive">{chatModelError}</p>}
        {modelTarget && <div className="mb-3 flex items-center justify-between gap-2">
          <p className="text-xs text-muted-foreground">{ownChatModel ? 'Used for messages and automated work in this agent.' : "Uses the Crew's model."}</p>
          {ownChatModel && <button type="button" disabled={observingModel || chatModelSaving} onClick={() => void saveChatModel()} className="shrink-0 text-xs font-medium text-primary hover:underline disabled:opacity-50">Use Crew's model</button>}
        </div>}
        <fieldset disabled={observingModel || chatModelSaving || (Boolean(modelTarget) && chatModelsLoading)} className="min-w-0 border-0 p-0">
        {savedProviderUnusable && (
          <p role="alert" className="mb-3 rounded-lg border border-amber-500/40 bg-amber-500/10 px-3 py-2 text-xs text-amber-700 dark:text-amber-300">
            This {effectiveProfileId === 'knowledgebase' || effectiveProfileId === 'caplayer' ? 'assistant' : 'project'} is set to {selectedOption?.label || selectedOption?.provider}, which is not available to you here. Choose another coding agent below; your next message uses it.
          </p>
        )}
        <WorkflowLLMConfigurationPanel
          workspacePath={workspacePath}
          llmConfig={llmConfig}
          onChange={selectProvider}
          scopeNoun={modelTarget ? 'chat' : effectiveProfileId === 'knowledgebase' || effectiveProfileId === 'caplayer' ? 'assistant' : 'project'}
          canWriteOverride
          allowedProviderIds={workProviderIds}
          splitPiProviders={false}
          showModelsPerRole={false}
          configurationSource="agent_profile"
          product={effectiveAccountProduct}
          accountUsage={companyServer
            ? <SharedTokenUsageNotice inline accountProvider={selectedOption?.provider} serverConnection={companyServer} />
            : <SharedTokenUsageNotice inline accountProvider={inUseConnectionId || accounts !== null ? sharedAccountProvider(inUseConnectionId, selectedOption?.provider) : undefined} />}
        />
        <section
          className="mt-4 overflow-hidden rounded-xl border border-border bg-card"
          onKeyDown={event => {
            if (event.key !== 'Escape' || !modelPickerOpen) return
            event.stopPropagation()
            setModelPickerOpen(false)
            event.currentTarget.querySelector<HTMLButtonElement>('button[aria-expanded]')?.focus()
          }}
        >
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
              {keyService && inUseAccount && (
                <div className="flex flex-wrap items-center justify-between gap-2 text-xs text-muted-foreground">
                  <span className="inline-flex items-center gap-1.5"><KeyRound className="h-3.5 w-3.5" />Your key: {inUseAccount.display_name} ({keyService.label}). Free models first.</span>
                  {(inUseAccount.relation ?? 'own') === 'own' && <button type="button" className="font-medium text-primary hover:underline" onClick={() => { setKeyBrowserOpen(open => !open); setKeyPicksDraft(inUseAccount.allowed_models ?? []) }}>{keyBrowserOpen ? 'Close' : `Browse ${keyService.label} models`}</button>}
                </div>
              )}
              {keyService && inUseAccount && keyBrowserOpen && keyPicksDraft && (
                <div className="mt-3 space-y-2 rounded-lg border border-border p-3">
                  <Suspense fallback={null}><ByokModelBrowser request={{ connection_id: inUseAccount.id, workspace_path: workspacePath }} service={inUseAccount.underlying_provider} value={keyPicksDraft} onChange={setKeyPicksDraft} manualIds={inUseAccount.underlying_provider === 'openai-compatible'} /></Suspense>
                  <button type="button" disabled={keyPicksDraft.length === 0} onClick={() => void saveKeyPicks(keyPicksDraft)} className="rounded-md bg-primary px-3 py-1.5 text-xs font-medium text-primary-foreground disabled:opacity-50">Save picks</button>
                </div>
              )}
              {cursorModelsError && <p role="alert" className="text-sm text-destructive">{cursorModelsError}</p>}
              {selectedOption?.provider === 'cursor-cli' && !keyService
                ? <CursorModelPicker
                    models={cursorSelectionDisabled ? cursorModels : cursorModels.filter(model => selectableModels.some(choice => choice.model_id === model.model_id))}
                    selectedModelId={currentModelId}
                    onSelect={selectModel}
                    selectionDisabled={cursorSelectionDisabled}
                    className="mt-3"
                  />
                : <TierModelSelector
                    models={selectableModels}
                    selectedModelId={currentModelId}
                    onSelect={selectModel}
                    className="mt-3"
                    showPrices={Boolean(keyService)}
                  />}
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
        {!modelTarget && keyEntryAvailable && (workProviderIds.length === 0 || savedProviderUnusable
          ? <div className="mt-4 rounded-xl border border-primary/30 bg-primary/5 p-3">
              <p className="text-sm font-medium text-foreground">No coding agent is available to you here.</p>
              <p className="mt-1 text-xs text-muted-foreground">Add your own model key (OpenRouter, NVIDIA NIM, Groq, Google AI Studio…) and chat on it. Many models are free.</p>
              <button type="button" onClick={() => setKeySetupOpen(true)} className="mt-2 inline-flex items-center gap-1.5 rounded-md bg-primary px-3 py-1.5 text-xs font-medium text-primary-foreground"><KeyRound className="h-3.5 w-3.5" />Use your own model key</button>
            </div>
          : <button type="button" onClick={() => setKeySetupOpen(true)} className="mt-3 inline-flex items-center gap-1.5 text-xs font-medium text-primary hover:underline"><KeyRound className="h-3.5 w-3.5" />Use your own model key (free models available)</button>)}
        {keySetupOpen && (
          <ModalPortal>
            <div className="fixed inset-0 z-[1000] flex items-center justify-center bg-gray-950/55 p-3" onMouseDown={event => { if (event.target === event.currentTarget) setKeySetupOpen(false) }}>
              <div role="dialog" aria-modal="true" aria-label="Use your own model key" className="max-h-[calc(100vh-2rem)] w-full max-w-2xl overflow-y-auto rounded-xl border border-border bg-white p-4 shadow-2xl dark:bg-gray-900">
                <Suspense fallback={null}><ByokSetup onCancel={() => setKeySetupOpen(false)} onSaved={(record, picks) => { window.dispatchEvent(new Event('provider-connections-changed')); switchToNewKey(record, picks) }} /></Suspense>
              </div>
            </div>
          </ModalPortal>
        )}
        <ProviderChangeNotice turnRunning={Boolean(tab?.isStreaming)} runningProvider={activeRuntime?.provider} selectedProvider={selectedOption?.provider} />
        </fieldset>
        {/* Accounts and keys for every program live in Settings; the chat's model menu stays about this chat. */}
        <button type="button" onClick={() => openWorkspaceSettings('providers')} className="mt-4 inline-flex items-center gap-1.5 text-xs font-medium text-primary hover:underline"><Cpu className="h-3.5 w-3.5" aria-hidden="true" />AI models &amp; accounts</button>
    </>
  )

  if (hideHeader) return body

  return (
    <section className="flex h-full min-h-0 w-full flex-col bg-background">
      <WorkspaceViewHeader
        icon={BrainCircuit}
        title="Project agent configuration"
        actions={<WorkspaceViewActions
          onRefresh={refresh}
          refreshing={refreshing}
          refreshLabel="Refresh models"
        />}
      />
      <div className="min-h-0 flex-1 overflow-y-auto p-4">{body}</div>
    </section>
  )
}
