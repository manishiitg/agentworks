/**
 * Pure helper functions extracted from ChatArea.tsx submitQueryWithQuery
 * and WorkflowLayout.tsx handleStartPhase to reduce complexity.
 */

import { workflowContextRefs } from './referenceTags'
import type { PollingEvent, ExtendedLLMConfiguration, AgentProfileChatRequest, AgentQueryRequest, ExecutionOptions } from '../services/api-types'
import type { ChatTab } from '../stores/useChatStore'
import type { ModeCategory } from '../stores/useModeStore'
import { useChatStore } from '../stores/useChatStore'
import { useWorkflowStore } from '../stores/useWorkflowStore'
import { logger } from './logger'
import type { AgentProfileProviderOption } from './agentProfileCapabilities'

// Workflow phases that support conversational chat mode instead of blocking human_feedback
const CHAT_COMPATIBLE_PHASES = new Set([
  'workflow-builder',
])


export function isChatCompatiblePhase(phaseId: string | undefined): boolean {
  return !!phaseId && CHAT_COMPATIBLE_PHASES.has(phaseId)
}

export function userInteractiveContinuationFlag(tab: Pick<ChatTab, 'metadata'>): true | undefined {
  return tab.metadata?.userInteractiveContinuation === true ? true : undefined
}

export type WorkflowReferenceContext = {
  presetId: string
  label: string
  workspacePath: string
}

// # references are message attachments, not conversation configuration. Remove
// only the references captured by an accepted submission so a reference added
// while that request is starting remains available for the next message.
export function remainingWorkflowContextAfterSubmission(
  current: WorkflowReferenceContext[],
  submitted: WorkflowReferenceContext[],
): WorkflowReferenceContext[] {
  if (submitted.length === 0) return current
  const consumed = new Set(submitted.map(reference => `${reference.presetId}\u0000${reference.workspacePath}`))
  return current.filter(reference => !consumed.has(`${reference.presetId}\u0000${reference.workspacePath}`))
}

// ---------------------------------------------------------------------------
// 1a. determineModeFlag — deduplicate useCodeExecutionMode
// ---------------------------------------------------------------------------

export function determineModeFlag(params: {
  correctAgentMode: string
  selectedModeCategory: string
  presetValue: boolean | undefined
  tabConfigValue: boolean | undefined
}): boolean | undefined {
  const { correctAgentMode, selectedModeCategory, presetValue, tabConfigValue } = params

  if (correctAgentMode === 'multi-agent') {
    // Multi-agent mode: preset wins, else tab config (default false)
    if (presetValue !== undefined) return presetValue
    if (selectedModeCategory === 'multi-agent') {
      return tabConfigValue ?? false
    }
    return false
  }

  if (correctAgentMode === 'workflow') {
    return presetValue
  }

  return undefined
}

// ---------------------------------------------------------------------------
// 1b. buildLLMConfigWithApiKeys
// ---------------------------------------------------------------------------

export function buildLLMConfigWithApiKeys(
  effectiveLLMConfig: ExtendedLLMConfiguration,
  _providerConfigs?: Record<string, ExtendedLLMConfiguration>
): ExtendedLLMConfiguration & { api_keys: Record<string, unknown> } {
  // API keys are now loaded server-side from workspace encrypted store.
  // No keys are sent from the frontend.
  const configWithoutApiKey = { ...effectiveLLMConfig }
  delete configWithoutApiKey.api_key
  return {
    ...configWithoutApiKey,
    api_keys: {},
  }
}

// ---------------------------------------------------------------------------
// 1c. buildQueryRequestPayload
// ---------------------------------------------------------------------------

export function buildQueryRequestPayload(params: {
  queryWithContext: string
  correctAgentMode: string
  selectedModeCategory: ModeCategory
  enabledTools: Array<{ name: string }>
  effectiveServers: string[]
  currentTab: ChatTab
  effectiveLLMConfig: ExtendedLLMConfiguration
  llmConfigWithApiKeys: ExtendedLLMConfiguration & { api_keys: Record<string, unknown> }
  useCodeExecutionMode: boolean | undefined
  executionOptions: unknown | undefined
  workflowPresetId: string | null
  chatPresetId: string | null
  filteredPresetTools: string[]
  hasActivePreset: boolean
  decryptedSecrets?: Array<{ name: string; value: string }>
  selectedGlobalSecrets?: string[] | null
  restoredConversationPath?: string
}): AgentQueryRequest {
  const {
    queryWithContext, correctAgentMode, selectedModeCategory,
    enabledTools, effectiveServers, currentTab, effectiveLLMConfig,
    llmConfigWithApiKeys, useCodeExecutionMode,
    executionOptions, workflowPresetId, chatPresetId,
    filteredPresetTools, hasActivePreset, decryptedSecrets,
    selectedGlobalSecrets,
    restoredConversationPath,
  } = params

  const isMultiAgentMode = selectedModeCategory === 'multi-agent'
  // Detect workflow phase chat mode: tab has a phaseId and the phase supports conversational editing
  const isWorkflowPhaseChat = selectedModeCategory === 'workflow'
    && currentTab?.metadata?.phaseId
    && CHAT_COMPATIBLE_PHASES.has(currentTab.metadata.phaseId)
  // isChatLikeMode: includes phase chat for basic settings (workspace access)
  const isChatLikeMode = isMultiAgentMode || isWorkflowPhaseChat
  // isChatWithExtras: only multi-agent mode gets optional extras (browser, skills, secrets, etc.)
  const isChatWithExtras = isMultiAgentMode

  // Browser mode can drift on resumed/migrated tabs when older fields exist.
  // Derive a robust effective mode so request payloads are consistent.
  const rawBrowserMode = currentTab?.config?.browserMode
  const legacyUseCdp = currentTab?.config?.useCdp === true
  const legacyEnableBrowser = currentTab?.config?.enableBrowserAccess === true
  let effectiveBrowserMode: 'none' | 'auto' | 'headless' | 'cdp' =
    rawBrowserMode
      ? rawBrowserMode
      : (legacyEnableBrowser
          ? (legacyUseCdp ? 'cdp' : 'headless')
          : 'none')

  // Guard against stale/migrated config where browserMode says headless
  // but useCdp is actually enabled in tab config.
  if (effectiveBrowserMode === 'headless' && legacyUseCdp) {
    effectiveBrowserMode = 'cdp'
  }
  const isBrowserAccessMode = effectiveBrowserMode !== 'none'

  return {
    query: queryWithContext,
    user_interactive_continuation: userInteractiveContinuationFlag(currentTab),
    agent_mode: (isWorkflowPhaseChat
        ? 'workflow_phase'
        : correctAgentMode) as AgentQueryRequest['agent_mode'],
    phase_id: isWorkflowPhaseChat ? currentTab.metadata!.phaseId : undefined,
    enabled_tools: enabledTools.map(tool => tool.name),
    enabled_servers: effectiveServers,
    selected_tools: hasActivePreset ? filteredPresetTools : undefined,
    provider: effectiveLLMConfig.provider as AgentQueryRequest['provider'],
    model_id: effectiveLLMConfig.model_id,
    llm_config: llmConfigWithApiKeys as AgentQueryRequest['llm_config'],
    preset_query_id: workflowPresetId || chatPresetId || undefined,
    use_code_execution_mode: correctAgentMode === 'multi-agent' ? (useCodeExecutionMode ?? false) : useCodeExecutionMode,
    execution_options: executionOptions as AgentQueryRequest['execution_options'],
    enable_browser_access: isChatWithExtras
      ? isBrowserAccessMode
      : undefined,
    browser_mode: isChatWithExtras
      ? effectiveBrowserMode
      : undefined,
    cdp_port: isChatWithExtras && (effectiveBrowserMode === 'auto' || effectiveBrowserMode === 'cdp')
      ? (currentTab?.config?.cdpPort || 9222)
      : undefined,
    delegation_tier_config: undefined,
    selected_skills: isChatWithExtras && currentTab?.config?.selectedSkills?.length
      ? currentTab.config.selectedSkills
      : undefined,
    decrypted_secrets: (isChatWithExtras || selectedModeCategory === 'workflow') && decryptedSecrets?.length ? decryptedSecrets : undefined,
    selected_global_secrets: (isChatWithExtras || selectedModeCategory === 'workflow')
      ? (selectedGlobalSecrets ?? undefined)
      : undefined,
    workflow_context_paths: (isChatWithExtras || selectedModeCategory === 'workflow') && currentTab?.config?.workflowContext?.length
      ? currentTab.config.workflowContext.map(w => w.workspacePath)
      : undefined,
    workflow_context_refs: (isChatWithExtras || selectedModeCategory === 'workflow') && currentTab?.config?.workflowContext?.length
      ? workflowContextRefs(currentTab.config.workflowContext)
      : undefined,
    restored_conversation_path: restoredConversationPath?.trim() || undefined,
  }
}

export function applyAgentProfileBinding(payload: AgentQueryRequest, tab: ChatTab): AgentQueryRequest {
  const metadata = tab.metadata
  if (!metadata?.agentProfileId) return payload
  return {
    ...payload,
    agent_profile_id: metadata.agentProfileId,
    connection_id: metadata.agentProfileConnectionID,
    agent_profile_version: metadata.agentProfileVersion,
    selected_folder: metadata.agentProfileWorkspace,
    agent_profile_context: {
      project_title: metadata.agentProfileProjectTitle || tab.name,
      workspace_description: metadata.agentProfileWorkspaceDescription,
    },
  }
}

export function buildAgentProfileChatRequest(payload: AgentQueryRequest, conversationKey?: string, engine?: string, modelId?: string, reasoningEffort?: string): AgentProfileChatRequest {
  return {
    message: payload.query,
    connection_id: payload.connection_id,
    ...(conversationKey ? { conversation_key: conversationKey } : {}),
    ...(engine ? { engine } : {}),
    ...(engine && modelId ? { model_id: modelId } : {}),
    ...(engine && reasoningEffort ? { reasoning_effort: reasoningEffort } : {}),
    ...(payload.enabled_servers?.length ? { enabled_servers: payload.enabled_servers } : {}),
    ...(payload.selected_skills?.length ? { selected_skills: payload.selected_skills } : {}),
    ...(payload.workflow_context_paths?.length ? { workflow_context_paths: payload.workflow_context_paths } : {}),
    ...(payload.workflow_context_refs?.length ? { workflow_context_refs: payload.workflow_context_refs } : {}),
  }
}

// A running server may still have an older embedded product profile after the
// frontend gains a CLI. Omit a saved effort it does not offer so chat can use
// the selected model's default instead of failing validation before launch.
export function offeredProfileReasoningEffort(
  options: AgentProfileProviderOption[],
  engine: string | undefined,
  effort: string | undefined,
  modelId?: string,
): string | undefined {
  if (!engine) return effort
  const option = options.find(candidate => candidate.id === engine)
  if (option?.provider === 'agy-cli') {
    const modelEffort = modelId?.match(/-(low|medium|high)$/)?.[1]
    if (modelEffort && option.reasoning_efforts?.includes(modelEffort)) return modelEffort
  }
  if (!effort) return effort
  return option && !option.reasoning_efforts?.includes(effort) ? undefined : effort
}

// ---------------------------------------------------------------------------
// 1d. resolveOrCreateTab — tab resolution + session ID guarantee
// ---------------------------------------------------------------------------

export async function resolveOrCreateTab(params: {
  freshActiveTab: ChatTab | undefined
  selectedModeCategory: ModeCategory
}): Promise<{ tab: ChatTab; sessionId: string } | null> {
  const { freshActiveTab, selectedModeCategory } = params
  let currentTab = freshActiveTab

  if (!currentTab) {
    logger.error('ChatArea', `No ${selectedModeCategory || 'agent'} tab — the owning surface must create one before submit`)
    return null
  }

  // Ensure session ID exists
  let sessionId = currentTab.sessionId
  if (!sessionId) {
    sessionId = globalThis.crypto.randomUUID()
    const chatStore = useChatStore.getState()
    chatStore.updateTabSessionId(currentTab.tabId, sessionId)
    currentTab = { ...currentTab, sessionId }
    logger.debug('ChatArea', `Generated session ID for tab ${currentTab.tabId}: ${sessionId}`)
  }

  return { tab: currentTab, sessionId }
}

// ---------------------------------------------------------------------------
// 1e. findOrCreateWorkflowTab — single-pass lookup for workflow phases
// ---------------------------------------------------------------------------

export async function findOrCreateWorkflowTab(params: {
  phaseId: string
  activePresetId: string
  phaseName: string
}): Promise<{ tabId: string; tab: ChatTab; isReusingTab: boolean } | null> {
  const { phaseId, activePresetId, phaseName } = params
  const chatStore = useChatStore.getState()
  const { getTabsByPhaseId, getTabStreamingStatus, switchTab, getActiveTab, createChatTab: createTab } = chatStore

  // Single pass: get all tabs for this phase scoped to the active preset
  const existingPhaseTabs = getTabsByPhaseId(phaseId, activePresetId)

  // Prefer streaming tab, then newest
  const runningTab = existingPhaseTabs.find(t => getTabStreamingStatus(t.tabId))
  const newestTab = existingPhaseTabs.length > 0
    ? existingPhaseTabs.sort((a, b) => b.createdAt - a.createdAt)[0]
    : null

  // Fallback: legacy tabs without presetQueryId that match the phase
  const legacyTab = !runningTab && !newestTab
    ? Object.values(chatStore.chatTabs).find(t =>
        t.metadata?.mode === 'workflow' &&
        t.metadata?.phaseId === phaseId &&
        !t.metadata?.presetQueryId
      )
    : null

  const existingTab = runningTab || newestTab || legacyTab

  if (existingTab) {
    logger.debug('WorkflowLayout', `Reusing tab ${existingTab.tabId} for phase ${phaseId}`)
    switchTab(existingTab.tabId)
    const tab = getActiveTab()
    if (!tab) return null
    return { tabId: existingTab.tabId, tab, isReusingTab: true }
  }

  // Create new tab
  try {
    logger.debug('WorkflowLayout', `Creating new tab for phase ${phaseId}, preset ${activePresetId}`)
    const tabId = await createTab(phaseName, {
      mode: 'workflow',
      phaseId,
      phaseName,
      presetQueryId: activePresetId || undefined
    })
    const tab = getActiveTab()
    if (!tab) return null
    return { tabId, tab, isReusingTab: false }
  } catch (error) {
    logger.error('WorkflowLayout', 'Failed to create workflow tab:', error)
    return null
  }
}

// ---------------------------------------------------------------------------
// 1f. createUserMessageEvent — typed factory replacing `as any` cast
// ---------------------------------------------------------------------------

export function createUserMessageEvent(
  content: string,
  eventIndex?: number,
  timestamp?: string,
  sessionId?: string,
): PollingEvent {
  const eventTimestamp = timestamp ?? new Date().toISOString()
  return {
    id: `user-message-${Date.now()}-${Math.random().toString(36).substr(2, 9)}`,
    type: 'user_message',
    timestamp: eventTimestamp,
    ...(sessionId ? { session_id: sessionId } : {}),
    ...(typeof eventIndex === 'number' ? { event_index: eventIndex } : {}),
    data: {
      type: 'user_message',
      timestamp: eventTimestamp,
      data: {
        content,
        timestamp: eventTimestamp
      }
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    } as any
  }
}

// Drop a submit's optimistic bubble after the send failed. The dead echo is
// a lie a retry would duplicate: the retry adds its own echo, and if the
// failed attempt actually reached the server, the authoritative server-side
// user_message renders instead — exactly one bubble either way. A stamped
// echo is already server-confirmed (the live filter suppressed its twin and
// transferred the delivery identity), so it stays.
export function withoutOptimisticUserMessage(events: PollingEvent[], eventId: string): PollingEvent[] {
  const target = events.find(event => event.id === eventId)
  if (!target) return events
  const inner = (target.data as unknown as Record<string, unknown> | undefined)?.data as Record<string, unknown> | undefined
  const metadata = inner?.metadata as Record<string, unknown> | undefined
  if (typeof metadata?.message_id === 'string' && metadata.message_id) return events
  return events.filter(event => event.id !== eventId)
}

// Synthetic separator injected when a follow-up is sent on a restored session.
// EventHierarchy uses this to collapse all events above it.
export function createConversationResumedEvent(previousEventCount: number): PollingEvent {
  return {
    id: `conversation-resumed-${Date.now()}`,
    type: 'conversation_resumed',
    timestamp: new Date().toISOString(),
    data: {
      type: 'conversation_resumed',
      data: {
        previous_event_count: previousEventCount,
        timestamp: new Date().toISOString(),
      }
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    } as any,
  }
}

// ---------------------------------------------------------------------------
// computeNewEventCount — pure function for WorkflowChatTabs badge count
// ---------------------------------------------------------------------------

export function computeNewEventCount(
  tab: ChatTab,
  isActive: boolean,
  tabEvents: Record<string, PollingEvent[]>,
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  shouldShowEventByMode: (type: string, mode?: any) => boolean
): number {
  if (isActive || !tab.sessionId) return 0

  const allEvents = tabEvents[tab.sessionId] || []
  const visibleEvents = allEvents.filter(e => e.type && shouldShowEventByMode(e.type))
  const lastViewedCount = tab.lastViewedEventCounts?.micro ?? tab.lastViewedEventCount ?? 0

  return Math.max(0, visibleEvents.length - lastViewedCount)
}

// ---------------------------------------------------------------------------
// validateExecutionGroups — check enabled_group_names for workflow mode
// ---------------------------------------------------------------------------

export function validateExecutionGroups(
  executionOptions: ExecutionOptions | undefined
): string | null {
  if (!executionOptions) return null

  const workflowStore = useWorkflowStore.getState()
  const variablesManifest = workflowStore.variablesManifest

  if (!variablesManifest?.groups || variablesManifest.groups.length === 0) {
    return 'Please create and enable at least one group before using automation builder or execution.'
  }

  const enabledGroupNames = executionOptions.enabled_group_names
  if (!enabledGroupNames || enabledGroupNames.length === 0) {
    return 'Please select at least one group before using automation builder or execution.'
  }

  return null
}
