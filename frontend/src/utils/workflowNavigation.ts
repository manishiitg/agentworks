import { useLLMStore } from '../stores/useLLMStore'
import { useAppStore } from '../stores/useAppStore'
import { normalizeEventViewMode, useChatStore, type EventViewMode } from '../stores/useChatStore'
import { useGlobalPresetStore } from '../stores/useGlobalPresetStore'
import { useModeStore } from '../stores/useModeStore'
import { useWorkflowStore } from '../stores/useWorkflowStore'
import { useProductSurfaceStore } from '../stores/useProductSurfaceStore'
import { isEnabledProductSurface } from '../products/productSurfaceConfig'
import type { CustomPreset, PredefinedPreset } from '../types/preset'

export type WorkflowNavigationContext = {
  workflowId: string | null
  tabId: string | null
  sessionId: string | null
  viewMode: EventViewMode
  generation: number
}

let context: WorkflowNavigationContext = {
  workflowId: null,
  tabId: null,
  sessionId: null,
  viewMode: 'formatted',
  generation: 0,
}

/** Begin an asynchronous workflow navigation and invalidate older lookups. */
export function beginWorkflowNavigation(workflowId: string): number {
  const viewMode = normalizeEventViewMode(useChatStore.getState().eventViewModePreference)
  context = {
    workflowId,
    tabId: null,
    sessionId: null,
    viewMode,
    generation: context.generation + 1,
  }
  return context.generation
}

export function isCurrentWorkflowNavigation(generation: number, workflowId: string): boolean {
  return generation === context.generation &&
    context.workflowId === workflowId &&
    useGlobalPresetStore.getState().activePresetIds.workflow === workflowId
}

export function getWorkflowNavigationContext(): Readonly<WorkflowNavigationContext> {
  return context
}

/** Leaving a workflow invalidates its delayed session/tab activation. */
export function cancelPendingWorkflowNavigation(): void {
  context = { ...context, workflowId: null, tabId: null, sessionId: null, generation: context.generation + 1 }
}

/**
 * The product surface a workflow belongs to: a Relay opens in Relays, everything else in Goals. Navigation that
 * lands on a workflow tab (activity pills, the global tab opener) must not force Goals, or opening a Relay
 * shows the Goals page.
 */
export function workflowSurfaceForPreset(presetId: string | undefined | null): 'relays' | 'agentworks' {
  if (!presetId) return 'agentworks'
  const preset = useGlobalPresetStore.getState().workflowPresets.find(item => item.id === presetId)
  return preset?.workflowKind === 'relay' && isEnabledProductSurface('relays') ? 'relays' : 'agentworks'
}

/** Project workflow selection into the existing report/workspace stores. */
export function selectWorkflowPreset(presetOrId: CustomPreset | PredefinedPreset | string): boolean {
  const presetStore = useGlobalPresetStore.getState()
  const workflowId = typeof presetOrId === 'string' ? presetOrId : presetOrId.id
  if (!workflowId) return false

  const preset = typeof presetOrId === 'string'
    ? presetStore.workflowPresets.find(item => item.id === workflowId)
    : presetOrId
  if (preset) {
    const surface = preset.workflowKind === 'relay' ? 'relays' : 'agentworks'
    if (isEnabledProductSurface(surface)) useProductSurfaceStore.getState().setProductSurface(surface)
  }

  // Re-activating a tab inside the current workflow must not re-run the preset
  // application lifecycle (which saves and reloads workflow settings).
  if (presetStore.activePresetIds.workflow !== workflowId) {
    const applied = presetStore.applyPreset(presetOrId, 'workflow')
    if (!applied.success) {
      // Old tabs can be restored before the manifest list finishes loading.
      // Preserve their ownership immediately; the normal preset hydration will
      // fill in query/tool/folder metadata once manifests are available.
      presetStore.setActivePreset('workflow', workflowId)
      useWorkflowStore.getState().switchToPreset(workflowId)
    }
  }

  useLLMStore.getState().setShowLLMModal(false)
  useAppStore.getState().setShowWorkflowsOverview(false)
  useAppStore.getState().setShowSchedulesOverview(false)
  if (useModeStore.getState().selectedModeCategory !== 'workflow') {
    useModeStore.getState().setModeCategory('workflow')
  }
  useWorkflowStore.getState().setShowChatArea(true)
  return true
}

/**
 * Atomically project one workflow navigation decision into the legacy stores.
 * Report/workspace selection, chat tab, session, and view mode must never be
 * written independently by a visible-navigation entry point.
 */
export function activateWorkflowTab(
  tabId: string,
  options: { expectedGeneration?: number; viewMode?: EventViewMode } = {},
): boolean {
  const chatStore = useChatStore.getState()
  const tab = chatStore.chatTabs[tabId]
  const workflowId = tab?.metadata?.presetQueryId
  if (!tab || tab.metadata?.mode !== 'workflow' || !workflowId) return false

  if (
    options.expectedGeneration !== undefined &&
    (options.expectedGeneration !== context.generation || context.workflowId !== workflowId)
  ) return false

  const crossingWorkflowBoundary = context.workflowId !== workflowId ||
    useGlobalPresetStore.getState().activePresetIds.workflow !== workflowId

  if (crossingWorkflowBoundary || options.expectedGeneration === undefined) {
    // Any direct tab click is a newer intent, including a schedule/main switch
    // inside the same workflow. Older asynchronous opens must not steal focus.
    context = {
      workflowId,
      tabId: null,
      sessionId: null,
      viewMode: normalizeEventViewMode(chatStore.eventViewModePreference),
      generation: context.generation + 1,
    }
  }

  selectWorkflowPreset(workflowId)

  const viewMode = normalizeEventViewMode(
    options.viewMode ||
    (options.expectedGeneration !== undefined
      ? context.viewMode
      : crossingWorkflowBoundary
        ? chatStore.eventViewModePreference
        : tab.viewMode)
  )
  chatStore.setTabViewMode(tabId, viewMode)
  chatStore.switchTab(tabId)

  context = {
    workflowId,
    tabId,
    sessionId: tab.sessionId,
    viewMode,
    generation: context.generation,
  }
  return true
}

export function resetWorkflowNavigationForTests(): void {
  context = {
    workflowId: null,
    tabId: null,
    sessionId: null,
    viewMode: 'formatted',
    generation: 0,
  }
}
