import { useProductNavigationSidebar } from './workspace/ProductTopBar'
import { useLLMStore } from '../stores/useLLMStore'
import React, { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { Activity, AlertCircle, Bot, CalendarClock, ChevronDown, Clock, Loader2, MessageSquare, Pause, Play, Webhook } from 'lucide-react'
import type { ActiveSessionInfo, RunningWorkflowInfo } from '../services/api-types'
import { useChatStore, type ChatTab } from '../stores/useChatStore'
import { useModeStore } from '../stores/useModeStore'
import { useGlobalPresetStore } from '../stores/useGlobalPresetStore'
import {
  isScheduledWorkflowSession,
} from '../utils/workflowSessionRestore'
import { useAppStore } from '../stores/useAppStore'
import { isLocalActivityFallbackTab } from '../utils/activityFallback'
import { hasLiveBackgroundAgents, isProductProjectSession, isVisibleActivitySession, nonWorkflowActivityTitle } from '../utils/activitySessions'
import { runtimeNeedsUserInput } from '../utils/runtimeActivity'
import {
  currentSessionId as resolveCurrentSessionId,
  GLOBAL_ACTIVITY_REFRESH_MS,
  headerStatusLabel,
  statusTone,
  visibleActivitySessions,
} from '../utils/globalActivityMonitorStatus'
import { workflowTriggerLabel, isInternalChildSession } from '../utils/workflowSessionKinds'
import { isWorkProductSession, openGlobalActivitySession, openGlobalTab } from '../utils/globalProductNavigation'
import { WorkflowIcon } from './workflow/WorkflowIcon'
import type { CustomPreset } from '../types/preset'
import { EntityIdentityIcon } from './ui/EntityIdentityIcon'
import { activityTypeLabels, botPlatformLabel, crewActivityTitle, sessionOriginLabel, titleWithoutOrigin, type ActivityType } from '../utils/globalActivityPresentation'
import { useLiveRefetch } from '../hooks/useLiveRefetch'

type ActivityMonitorItem =
  | { type: 'session'; id: string; session: ActiveSessionInfo }
  | { type: 'builder-tab'; id: string; tab: ChatTab }

function activityType(session: ActiveSessionInfo): ActivityType {
  const triggerLabel = workflowTriggerLabel({ sessionId: session.session_id, triggeredBy: session.triggered_by })
  if (triggerLabel) return triggerLabel
  const trigger = (session.triggered_by || '').toLowerCase()
  const sessionID = session.session_id.toLowerCase()
  if (session.bot_platform || trigger.includes('bot') || trigger.includes('slack') || trigger.includes('whatsapp') || sessionID.startsWith('bot-')) {
    return 'Bot'
  }
  return 'Chat'
}

const activityTypeIcons: Record<ActivityType, typeof Webhook> = {
  Scheduled: CalendarClock,
  Webhook,
  Manual: Play,
  Bot,
  Chat: MessageSquare,
}

function ActivityTypeIcon({ type, platform }: { type: ActivityType; platform?: string }) {
  const Icon = activityTypeIcons[type]
  const label = type === 'Bot' && platform ? `${platform} bot conversation` : activityTypeLabels[type]
  return (
    <span className="inline-flex items-center gap-1 opacity-75" title={label} aria-label={label}>
      <Icon className="h-3 w-3" aria-hidden="true" />
      {type === 'Bot' && platform && <span className="text-[10px] leading-none">{platform}</span>}
    </span>
  )
}

function isWorkflowSession(session: ActiveSessionInfo): boolean {
  return session.agent_mode?.toLowerCase().includes('workflow') ?? false
}

function sessionTitle(session: ActiveSessionInfo, workflow?: RunningWorkflowInfo, fallbackWorkflowName?: string | null): string {
  if (isWorkflowSession(session)) {
    const workflowFolder = (workflow?.workspace_path || session.workspace_path)?.split('/').filter(Boolean).pop()
    const hasBackgroundWork = hasLiveBackgroundAgents(session)
    const scheduled = isScheduledWorkflowSession(session, workflow)

    if (scheduled) {
      return (
        workflow?.preset_name ||
        session.preset_name ||
        session.workflow_name ||
        session.workflow_label ||
        workflowFolder ||
        fallbackWorkflowName ||
        workflow?.title ||
        session.title ||
        session.query ||
        'Automation'
      )
    }

    return (
      workflow?.preset_name ||
      session.preset_name ||
      session.workflow_name ||
      session.workflow_label ||
      workflow?.title ||
      session.title ||
      workflowFolder ||
      fallbackWorkflowName ||
      (hasBackgroundWork ? 'Automation background task' : '') ||
      session.query ||
      'Automation'
    )
  }

  return nonWorkflowActivityTitle(session)
}

function displaySessionTitle(
  session: ActiveSessionInfo,
  tab?: ChatTab,
  workflow?: RunningWorkflowInfo,
  fallbackWorkflowName?: string | null,
): string {
  if (isWorkflowSession(session)) {
    // For view-only (schedule/bot) tabs, tab.name is a type label ("Schedule", "WhatsApp"),
    // not the actual workflow name — skip it and resolve the real workflow title instead.
    const genericTabName = (tab?.name || '').trim().toLowerCase()
    const tabNameIsTypeLabel = genericTabName === 'schedule' || genericTabName === 'webhook' ||
      genericTabName === 'scheduled run' ||
      genericTabName === 'bot' ||
      genericTabName === 'whatsapp' ||
      genericTabName === 'slack'
    if (tab?.name && tab.name !== 'Automation Builder' && !tab.metadata?.isViewOnly && !tabNameIsTypeLabel) {
      return tab.name
    }
    return sessionTitle(session, workflow, fallbackWorkflowName)
  }

  if (isWorkProductSession(session)) {
    return crewActivityTitle(tab, sessionTitle(session, workflow))
  }

  return tab?.name || sessionTitle(session, workflow)
}

function timeAgo(value?: string | number | null): string {
  if (value === undefined || value === null || value === '') return ''
  const ms = typeof value === 'number' ? value : new Date(value).getTime()
  if (Number.isNaN(ms)) return ''
  const minutes = Math.max(0, Math.floor((Date.now() - ms) / 60000))
  if (minutes < 1) return 'just now'
  if (minutes < 60) return `${minutes}m ago`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours}h ago`
  return `${Math.floor(hours / 24)}d ago`
}

function normalizedActivityIdentity(value?: string | null): string {
  return (value || '').trim().replace(/\/+$/, '').toLowerCase()
}

function pushActivityKey(keys: string[], prefix: string, value?: string | null): void {
  const normalized = normalizedActivityIdentity(value)
  if (normalized) keys.push(`${prefix}:${normalized}`)
}

function activityKeysForSession(session: ActiveSessionInfo, workflow?: RunningWorkflowInfo): string[] {
  const keys: string[] = []
  pushActivityKey(keys, 'session', session.session_id)
  pushActivityKey(keys, 'preset', session.preset_query_id || workflow?.preset_query_id)
  pushActivityKey(keys, 'workspace', workflow?.workspace_path || session.workspace_path)
  return keys
}

function activityKeysForTab(tab: ChatTab): string[] {
  const keys: string[] = []
  pushActivityKey(keys, 'session', tab.sessionId)
  pushActivityKey(keys, 'preset', tab.metadata?.presetQueryId)
  return keys
}

function workflowPresetForActivity(
  presets: CustomPreset[],
  session?: ActiveSessionInfo,
  tab?: ChatTab,
): CustomPreset | undefined {
  const presetID = normalizedActivityIdentity(session?.preset_query_id || tab?.metadata?.presetQueryId)
  if (presetID) {
    const byID = presets.find(preset => normalizedActivityIdentity(preset.id) === presetID)
    if (byID) return byID
  }

  const workspacePath = normalizedActivityIdentity(session?.workspace_path)
  if (workspacePath) {
    const byPath = presets.find(preset => {
      const presetPath = normalizedActivityIdentity(preset.selectedFolder?.filepath)
      return presetPath && (presetPath === workspacePath || workspacePath.endsWith(`/${presetPath}`))
    })
    if (byPath) return byPath
  }

  return undefined
}

export const GlobalActivityMonitor: React.FC = () => {
  const sidebar = useProductNavigationSidebar()
  const panelRef = useRef<HTMLDivElement>(null)
  const [panelPosition, setPanelPosition] = useState({ left: 0, top: 0 })
  const activeSessionsCache = useChatStore(state => state.activeSessionsCache)
  const getActiveSessions = useChatStore(state => state.getActiveSessions)
  const activeTabId = useChatStore(state => state.activeTabId)
  const chatTabs = useChatStore(state => state.chatTabs)
  const selectedModeCategory = useModeStore(state => state.selectedModeCategory)
  const showProviders = useLLMStore(state => state.showLLMModal)
  const showWorkflowsOverview = useAppStore(state => state.showWorkflowsOverview)
  const workflowPresets = useGlobalPresetStore(state => state.workflowPresets)
  const currentWorkflowPreset = useGlobalPresetStore(state => {
    const presetId = state.activePresetIds.workflow
    return state.workflowPresets.find(preset => preset.id === presetId) ?? null
  })
  const currentWorkflowPresetName = currentWorkflowPreset?.label ?? null

  useEffect(() => {
    // Initial load; after that the live feed drives refreshes. This surface
    // promises live state, so it bypasses the shared 30-second cache.
    if (!document.hidden) void getActiveSessions(true).catch(() => [])
  }, [getActiveSessions])
  // Session/schedule changes arrive as live notices; GLOBAL_ACTIVITY_REFRESH_MS
  // polling only while the feed is down. getActiveSessions coalesces overlaps.
  useLiveRefetch(() => { void getActiveSessions(true).catch(() => []) }, {
    kinds: ['sessions', 'schedules'],
    fallbackMs: GLOBAL_ACTIVITY_REFRESH_MS,
  })

  const activeSessions = useMemo(() => {
    return activeSessionsCache.filter(session =>
      !isInternalChildSession({
        parentSessionId: session.parent_session_id,
        sessionKind: session.session_kind,
      }) &&
      (!isProductProjectSession(session) || isWorkProductSession(session)) &&
      isVisibleActivitySession(session)
    )
  }, [activeSessionsCache])

  // Shared with ModePresetBar's current-workflow selector, so the two agree
  // on which session is "current" — see globalActivityMonitorStatus.ts.
  const currentSessionId = useMemo(
    () => resolveCurrentSessionId(activeTabId, chatTabs, selectedModeCategory, showWorkflowsOverview || showProviders),
    [activeTabId, chatTabs, selectedModeCategory, showWorkflowsOverview, showProviders],
  )
  const visibleSessions = useMemo(
    () => visibleActivitySessions(activeSessions, currentSessionId),
    [activeSessions, currentSessionId],
  )

  const visibleActivityKeys = useMemo(() => {
    const keys = new Set<string>()
    visibleSessions.forEach(session => {
      activityKeysForSession(session)
        .forEach(key => keys.add(key))
    })
    return keys
  }, [visibleSessions])

  // Local tab background-agent state is only a fallback. If the backend already
  // exposes the same session/preset/workflow, render the backend-backed item once.
  const fallbackBuilderTabs = useMemo(
    () => Object.values(chatTabs).filter(tab =>
      tab.tabId !== activeTabId &&
      (!tab.metadata?.agentProfileId || tab.metadata.agentProfileId === 'work') &&
      isLocalActivityFallbackTab(tab) &&
      !activityKeysForTab(tab).some(key => visibleActivityKeys.has(key))
    ),
    [chatTabs, activeTabId, visibleActivityKeys],
  )

  const sortedSessions = useMemo(() => {
    return [...visibleSessions].sort((a, b) => {
      if (runtimeNeedsUserInput(a) !== runtimeNeedsUserInput(b)) {
        return runtimeNeedsUserInput(a) ? -1 : 1
      }
      if (isWorkflowSession(a) !== isWorkflowSession(b)) {
        return isWorkflowSession(a) ? -1 : 1
      }
      return new Date(b.last_activity).getTime() - new Date(a.last_activity).getTime()
    })
  }, [visibleSessions])

  const activityItems = useMemo<ActivityMonitorItem[]>(() => [
    ...sortedSessions.map(session => ({
      type: 'session' as const,
      id: `session:${session.session_id}`,
      session,
    })),
    ...fallbackBuilderTabs.map(tab => ({
      type: 'builder-tab' as const,
      id: `builder-tab:${tab.tabId}`,
      tab,
    })),
  ], [sortedSessions, fallbackBuilderTabs])

  const dockRunningActivityCount = useMemo(() => {
    const busySessionIds = new Set<string>()
    for (const session of activeSessions) {
      const tone = statusTone(session)
      if (tone === 'running' || tone === 'background' || tone === 'needs-input') {
        busySessionIds.add(session.session_id)
      }
    }

    let localBusyTabCount = 0
    for (const tab of Object.values(chatTabs)) {
      if (!tab.isStreaming && !tab.isSyntheticTurn) continue
      if (tab.sessionId && busySessionIds.has(tab.sessionId)) continue
      localBusyTabCount += 1
    }

    return busySessionIds.size + localBusyTabCount
  }, [activeSessions, chatTabs])

  useEffect(() => {
    const api = (window as Window & { electronAPI?: { setRunningActivity?: (value: { count: number }) => void } }).electronAPI
    if (!api?.setRunningActivity) return
    api.setRunningActivity({ count: dockRunningActivityCount })
  }, [dockRunningActivityCount])

  useEffect(() => {
    return () => {
      const api = (window as Window & { electronAPI?: { setRunningActivity?: (value: { count: number }) => void } }).electronAPI
      api?.setRunningActivity?.({ count: 0 })
    }
  }, [])

  const openActiveWorkInQuickSwitcher = useCallback(() => {
    window.dispatchEvent(new CustomEvent('open-quick-switcher', {
      detail: { query: '@active ' },
    }))
  }, [])

  const handleOpenSession = useCallback(async (session: ActiveSessionInfo) => {
    // A workflow pill represents the workflow, not whichever reviewer/step
    // most recently emitted activity. Resolve the preset again and let the
    // canonical workflow navigation path choose its root main-agent session.
    const workflowPreset = workflowPresetForActivity(workflowPresets, session)
    await openGlobalActivitySession(session, {
      title: sessionTitle(session, undefined, workflowPreset?.label),
      source: 'global-activity-monitor',
    })
  }, [workflowPresets])

  const [open, setOpen] = useState(false)
  const rootRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!open) return
    const onPointerDown = (event: PointerEvent) => {
      const target = event.target as Node
      if (!rootRef.current?.contains(target) && !panelRef.current?.contains(target)) setOpen(false)
    }
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') setOpen(false)
    }
    document.addEventListener('pointerdown', onPointerDown)
    document.addEventListener('keydown', onKeyDown)
    return () => {
      document.removeEventListener('pointerdown', onPointerDown)
      document.removeEventListener('keydown', onKeyDown)
    }
  }, [open ])

  // Items can disappear (a run finishes) while the panel is open; never show
  // a stale list, and close instead of stranding an empty panel.
  useEffect(() => {
    if (open && activityItems.length === 0) setOpen(false)
  }, [open, activityItems.length])

  const aggregate = useMemo(() => {
    let needsInput = 0
    let working = 0
    for (const item of activityItems) {
      if (item.type === 'builder-tab') {
        if (item.tab.isStreaming || item.tab.isSyntheticTurn) working += 1
        continue
      }
      const tone = statusTone(item.session)
      if (tone === 'needs-input') needsInput += 1
      else if (tone === 'running' || tone === 'background') working += 1
    }
    return { needsInput, working }
  }, [activityItems])

  const selectSession = useCallback((session: ActiveSessionInfo) => {
    setOpen(false)
    void handleOpenSession(session)
  }, [handleOpenSession])

  const selectBuilderTab = useCallback((tabId: string) => {
    setOpen(false)
    openGlobalTab(tabId)
  }, [])

  const showQuickSwitcher = useCallback(() => {
    setOpen(false)
    openActiveWorkInQuickSwitcher()
  }, [openActiveWorkInQuickSwitcher])

  useLayoutEffect(() => {
    if (!open) return
    const reposition = () => {
      const trigger = rootRef.current?.getBoundingClientRect()
      const panel = panelRef.current?.getBoundingClientRect()
      if (!trigger || !panel) return
      setPanelPosition({
        left: Math.max(12, Math.min(sidebar ? trigger.right + 12 : trigger.right - panel.width, window.innerWidth - panel.width - 12)),
        top: Math.max(12, Math.min(sidebar ? trigger.bottom - panel.height : trigger.bottom + 8, window.innerHeight - panel.height - 12)),
      })
    }
    reposition()
    window.addEventListener('resize', reposition)
    document.addEventListener('scroll', reposition, true)
    return () => {
      window.removeEventListener('resize', reposition)
      document.removeEventListener('scroll', reposition, true)
    }
  }, [sidebar, open, activityItems.length])

  if (activityItems.length === 0) {
    return null
  }

  const count = activityItems.length
  // End user only cares about two states: is it working, or is it waiting for me?
  const buttonLabel = aggregate.needsInput > 0
    ? `${count} need${count === 1 ? 's' : ''} input`
    : aggregate.working > 0
      ? `${count} running`
      : `${count} active`
  const dotClasses = aggregate.needsInput > 0
    ? 'bg-amber-500 dark:bg-amber-400'
    : aggregate.working > 0
      ? 'bg-blue-500 dark:bg-blue-400'
      : 'bg-gray-400 dark:bg-gray-500'

  return (
    <div ref={rootRef} className="relative">
      <button
        type="button"
        data-tour="active-work-switcher"
        data-testid="tour-active-work-switcher"
        onClick={() => setOpen(current => !current)}
        aria-expanded={open}
        aria-label={`Active work: ${buttonLabel}. Activate to switch.`}
        title={`Active work · ${buttonLabel}`}
        className={`${sidebar ? 'relative h-9 w-full justify-center' : ''} flex items-center gap-1.5 px-2 py-1 rounded-md border text-xs font-medium transition-colors border-blue-200 bg-blue-50 text-blue-700 hover:bg-blue-100 dark:border-blue-800/60 dark:bg-blue-950/40 dark:text-blue-300 dark:hover:bg-blue-950/60`}
      >
        {sidebar && <Activity className="h-4 w-4" aria-hidden="true" />}
        <span className={`${sidebar ? 'absolute right-1.5 top-1.5' : ''} h-1.5 w-1.5 rounded-full motion-safe:animate-pulse ${dotClasses}`} />
        <span className={sidebar ? 'sr-only' : 'whitespace-nowrap'}>{buttonLabel}</span>
        <ChevronDown className={`${sidebar ? 'hidden' : ''} w-3.5 h-3.5 opacity-70 transition-transform ${open ? 'rotate-180' : ''}`} />
      </button>

      {/* Escape toolbar/sidebar stacking contexts so the split divider cannot cross the menu. */}
      {open && createPortal(
        <div
          ref={panelRef}
          role="menu"
          aria-label="Active work"
          style={panelPosition}
          className="fixed z-50 flex max-h-[75vh] w-80 max-w-[calc(100vw-5rem)] flex-col overflow-hidden rounded-lg border border-gray-200 bg-white shadow-xl dark:border-gray-700 dark:bg-gray-900"
        >
          <div className="px-3 py-2 text-xs font-semibold text-gray-700 dark:text-gray-300">
            Active work · {count}
          </div>
          <div className="min-h-0 max-h-96 overflow-y-auto pb-1">
            {activityItems.map(item => {
              if (item.type === 'builder-tab') {
                const builderBusy = item.tab.isStreaming || item.tab.isSyntheticTurn
                const isCrewBuilder = item.tab.metadata?.agentProfileId === 'work'
                const builderPreset = isCrewBuilder ? undefined : workflowPresetForActivity(workflowPresets, undefined, item.tab) ?? currentWorkflowPreset ?? undefined
                const builderName = isCrewBuilder
                  ? item.tab.metadata?.agentProfileIdentityName || item.tab.metadata?.agentProfileProjectTitle || 'Crew'
                  : builderPreset?.label || ((item.tab.name && item.tab.name !== 'Automation Builder') ? item.tab.name : currentWorkflowPresetName)
                const builderTitle = builderName || (isCrewBuilder ? 'Crew' : 'Automation')
                return (
                  <button
                    key={item.id}
                    type="button"
                    role="menuitem"
                    onClick={() => selectBuilderTab(item.tab.tabId)}
                    title={builderBusy ? 'Builder is processing — wait before sending a message' : 'Builder is idle — ready for your next message'}
                    className="flex w-full items-center gap-2 px-3 py-2 text-left text-sm transition-colors hover:bg-gray-100 dark:hover:bg-gray-800"
                  >
                    {builderBusy
                      ? <Loader2 className="h-3.5 w-3.5 shrink-0 animate-spin text-blue-500 dark:text-blue-400" />
                      : <span className="h-1.5 w-1.5 shrink-0 rounded-full bg-emerald-500 dark:bg-emerald-400" />}
                    {isCrewBuilder
                      ? <EntityIdentityIcon icon={item.tab.metadata?.agentProfileProjectIcon} label={builderTitle} />
                      : <WorkflowIcon icon={builderPreset?.icon} label={builderTitle} />}
                    <span className="min-w-0 flex-1 truncate text-gray-800 dark:text-gray-200">{builderTitle}</span>
                    <span className="whitespace-nowrap text-xs text-gray-500 dark:text-gray-400">
                      {timeAgo(item.tab.lastStreamingStartedAt ?? item.tab.lastAccessedAt)}
                    </span>
                  </button>
                )
              }

              const session = item.session
              const tab = Object.values(chatTabs).find(t => t.sessionId === session.session_id)
              const workflowPreset = isWorkflowSession(session)
                ? workflowPresetForActivity(workflowPresets, session, tab)
                : undefined
              const fallbackName = workflowPreset?.label || null
              const tone = statusTone(session)
              const origin = sessionOriginLabel(session)
              const title = titleWithoutOrigin(displaySessionTitle(session, tab, undefined, fallbackName), origin)
              const type = activityType(session)
              const crewSession = isWorkProductSession(session)
              const statusLabel = headerStatusLabel(session)
              const waitingTitle = session.waiting_message ? ` · ${session.waiting_message}` : ''
              return (
                <button
                  key={item.id}
                  type="button"
                  role="menuitem"
                  onClick={() => selectSession(session)}
                  title={`${title} · ${origin} · ${statusLabel}${waitingTitle}`}
                  className="flex w-full items-center gap-2 px-3 py-2 text-left text-sm transition-colors hover:bg-gray-100 dark:hover:bg-gray-800"
                >
                  {tone === 'needs-input'
                    ? <AlertCircle className="h-3.5 w-3.5 shrink-0 text-amber-500 dark:text-amber-400" />
                    : tone === 'running' || tone === 'background'
                      ? <Loader2 className="h-3.5 w-3.5 shrink-0 animate-spin text-blue-500 dark:text-blue-400 opacity-80" />
                      : tone === 'paused'
                        ? <Pause className="h-3.5 w-3.5 shrink-0 opacity-50" />
                        : <Clock className="h-3.5 w-3.5 shrink-0 opacity-50" />}
                  {isWorkflowSession(session)
                    ? <WorkflowIcon icon={workflowPreset?.icon} label={title} />
                    : crewSession
                      ? <EntityIdentityIcon icon={tab?.metadata?.agentProfileProjectIcon} label={tab?.metadata?.agentProfileIdentityName || title} />
                      : null}
                  <span className="flex min-w-0 flex-1 flex-col">
                    <span className="truncate text-gray-800 dark:text-gray-200">{title}</span>
                    <span className="truncate text-[11px] leading-tight text-gray-500 dark:text-gray-400">{origin}</span>
                  </span>
                  <ActivityTypeIcon type={type} platform={type === 'Bot' ? botPlatformLabel(session.bot_platform, session.session_id) : undefined} />
                  <span className="whitespace-nowrap text-xs text-gray-500 dark:text-gray-400">{timeAgo(session.last_activity)}</span>
                </button>
              )
            })}
          </div>
          <button
            type="button"
            onClick={showQuickSwitcher}
            className="w-full shrink-0 border-t border-gray-200 px-3 py-2 text-center text-xs font-medium text-gray-500 transition-colors hover:bg-gray-100 dark:border-gray-700 dark:text-gray-400 dark:hover:bg-gray-800"
          >
            Show all in Ctrl+K
          </button>
        </div>,
        document.body,
      )}
    </div>
  )
}
