import React, { useState, useEffect, useRef, useMemo, useCallback } from 'react'
import { isWorkSideChatTab } from '../products/work/workTabs'
import { Activity, CalendarClock, Code2, Cpu, Layers, LayoutGrid, MessageSquare, NotebookText, Plug, PlugZap, ScrollText, Search, Users, X } from 'lucide-react'
import { useGlobalPresetStore } from '../stores/useGlobalPresetStore'
import { useModeStore } from '../stores/useModeStore'
import { useChatStore } from '../stores'
import type { ChatTab } from '../stores/useChatStore'
import type { CustomPreset, PredefinedPreset } from '../types/preset'
import type { ActiveSessionInfo } from '../services/api-types'
import { sessionOriginLabel, titleWithoutOrigin } from '../utils/globalActivityPresentation'
import { workflowSurfaceForPreset } from '../utils/workflowNavigation'
import { scopeQuickSwitcherToAgentWorks } from '../utils/quickSwitcherScope'
import { openWorkflowPresetPage, pickWorkflowActiveSession, workflowSessionBotPlatform } from '../utils/workflowSessionRestore'
import { runtimeHasBackgroundAgents, runtimeNeedsUserInput, sessionRuntimeStatus } from '../utils/runtimeActivity'
import { hasIdleAliveCodingAgent, isVisibleActivitySession, nonWorkflowActivityTitle } from '../utils/activitySessions'
import { isLocalActivityFallbackTab } from '../utils/activityFallback'
import { isCodeProductSession, isWorkProductSession, openGlobalActivitySession, openGlobalTab, workProjectIdForTab } from '../utils/globalProductNavigation'
import type { WorkSession } from '../products/work/workSessions'
import { CODE_PRODUCT } from '../products/work/projectProduct'
import { useProductSurfaceStore } from '../stores/useProductSurfaceStore'
import { useAuthStore } from '../stores/useAuthStore'
import { intersectAllowedProductSurfaces, isEnabledProductSurface } from '../products/productSurfaceConfig'
import { EntityIdentityIcon } from './ui/EntityIdentityIcon'
import { openQuickNavigation, quickNavigationItems, type QuickNavigationItem, type QuickNavigationScope } from '../utils/quickNavigation'
import { openProductWorkspace } from '../utils/productWorkspaceNavigation'
import { ProductSurfaceIcon } from './ProductSurfaceIcon'
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from './ui/tooltip'

interface QuickSwitcherProps {
  isOpen: boolean
  onClose: () => void
  initialQuery?: string
}

interface WorkflowItem {
  type: 'workflow'
  id: string
  label: string
  subtitle: string
  isActive: boolean
  lastAccessedAt: number
  preset: CustomPreset | PredefinedPreset
  activeSession?: ActiveSessionInfo
  hasLocalActivity: boolean
}

interface ChatTabItem {
  type: 'chat'
  id: string
  label: string
  subtitle: string
  isActive: boolean
  lastAccessedAt: number
  tabId: string
  activeSession?: ActiveSessionInfo
  hasLocalActivity: boolean
}

interface ActiveWorkItem {
  type: 'active'
  id: string
  label: string
  subtitle: string
  isActive: boolean
  lastAccessedAt: number
  session: ActiveSessionInfo
  /** Already shown through its tab, Crew or automation row; listed on its
   * own only under @active, which shows every running session. */
  activeScopeOnly?: boolean
  tabId?: string
  mode: 'workflow' | 'multi-agent'
}

interface CrewChatItem {
  /** A Crew, or a Code workspace (same project surface, its own product). */
  type: 'crew' | 'code'
  id: string
  label: string
  subtitle: string
  isActive: boolean
  lastAccessedAt: number
  /** Open chat tab for this Crew; absent for a Crew listed from the directory. */
  tabId?: string
  /** Crew project to open when there is no tab yet. */
  projectId?: string
  activeSession?: ActiveSessionInfo
  hasLocalActivity: boolean
  icon?: string
}

type QuickSwitcherItem = WorkflowItem | ChatTabItem | CrewChatItem | ActiveWorkItem | QuickNavigationItem

const EMPTY_CHAT_TABS: Record<string, ChatTab> = {}
const EMPTY_ACTIVE_SESSIONS: ActiveSessionInfo[] = []
const EMPTY_WORKFLOW_PRESETS: Array<CustomPreset | PredefinedPreset> = []
const EMPTY_RECENT_PRESET_ORDER: string[] = []
const EMPTY_RECENT_PRESET_ACCESSED_AT: Record<string, number> = {}

const isWorkflowSession = (session: ActiveSessionInfo): boolean => {
  return session.agent_mode === 'workflow' ||
    session.agent_mode === 'workflow_phase' ||
    !!session.workflow_name ||
    !!session.workflow_label ||
    !!session.workspace_path ||
    !!session.preset_query_id
}

const activeSessionLabel = (session: ActiveSessionInfo): string => {
  if (isWorkProductSession(session) || isCodeProductSession(session)) {
    return session.current_execution_name ||
      session.title ||
      session.workspace_path?.split('/').filter(Boolean).pop() ||
      session.query ||
      'Crew work'
  }
  if (isWorkflowSession(session)) {
    return session.workflow_label ||
      session.workflow_name ||
      session.preset_name ||
      session.workspace_path?.split('/').filter(Boolean).pop() ||
      session.title ||
      'Automation'
  }
  return nonWorkflowActivityTitle(session)
}

const activeSessionStatusLabel = (session: ActiveSessionInfo): string => {
  if (runtimeNeedsUserInput(session)) return 'waiting for input'
  const bgCount = session.running_background_agent_count ?? 0
  if (bgCount > 0) return `${bgCount} bg agent${bgCount === 1 ? '' : 's'}`
  if (runtimeHasBackgroundAgents(session)) return 'bg agents running'
  if (hasIdleAliveCodingAgent(session) && sessionRuntimeStatus(session) === 'stopped') return 'idle'
  return sessionRuntimeStatus(session)
}

const sessionShortId = (sessionId: string): string => sessionId.slice(0, 8)

const normalizeWorkspacePath = (path?: string): string => (path || '').replace(/\/+$/, '')

const findTabForSession = (tabs: Record<string, ChatTab>, sessionId: string): ChatTab | undefined => {
  return Object.values(tabs).find(tab => tab.sessionId === sessionId)
}

const workflowSessionMatchesPreset = (
  session: ActiveSessionInfo,
  preset: CustomPreset | PredefinedPreset,
  tabs: Record<string, ChatTab>,
): boolean => {
  if (!isWorkflowSession(session)) return false
  if (session.preset_query_id === preset.id) return true
  if (
    normalizeWorkspacePath(session.workspace_path) &&
    normalizeWorkspacePath(session.workspace_path) === normalizeWorkspacePath(preset.selectedFolder?.filepath)
  ) return true
  const tab = findTabForSession(tabs, session.session_id)
  return tab?.metadata?.presetQueryId === preset.id
}

const itemTypeRank = (item: QuickSwitcherItem): number => {
  if (item.type === 'active') return 0
  if (item.type === 'crew' || item.type === 'code') return 1
  if (item.type === 'chat') return 1
  if (item.type === 'workflow') return 2
  return 3
}

const itemActiveSession = (item: QuickSwitcherItem): ActiveSessionInfo | undefined => {
  if (item.type === 'active') return item.session
  return item.activeSession
}

const tabHasRunningWork = (tab: ChatTab): boolean =>
  !tab.isCompleted && (tab.isStreaming || tab.isSyntheticTurn || isLocalActivityFallbackTab(tab))

const itemHasRunningWork = (item: QuickSwitcherItem): boolean => {
  const session = itemActiveSession(item)
  if (session) return sessionRuntimeStatus(session) === 'busy' || runtimeHasBackgroundAgents(session) || runtimeNeedsUserInput(session)
  return item.type !== 'active' && item.hasLocalActivity
}

const activeSessionSuffix = (session?: ActiveSessionInfo): string => {
  if (!session) return ''
  const source = workflowSessionBotPlatform(session)
  const sourcePart = source ? ` · ${source}` : ''
  const current = session.current_execution_name ? ` · ${session.current_execution_name}` : ''
  return ` · active: ${activeSessionStatusLabel(session)}${sourcePart}${current} · ${sessionShortId(session.session_id)}`
}

function QuickNavigationIcon({ item }: { item: QuickNavigationItem }) {
  const surface = item.surface ?? (item.scope === 'workflows' ? 'agentworks' : item.scope === 'relays' ? 'relays' : item.scope === 'crew' ? 'work' : item.scope === 'code' ? 'code' : undefined)
  if (surface) return <ProductSurfaceIcon surface={surface} />
  const Icon = item.scope === 'products' ? LayoutGrid : item.scope === 'chats' ? MessageSquare
    : item.action === 'providers' ? Cpu : item.action === 'users' ? Users : item.action === 'mcp' ? Plug
    : item.action === 'schedules' ? CalendarClock : item.action === 'activity' ? NotebookText
    : item.action === 'vault-audit' ? ScrollText : item.action === 'vault-connect' ? PlugZap : Activity
  return <Icon className="h-4 w-4 shrink-0" aria-hidden="true" />
}

export const QuickSwitcher: React.FC<QuickSwitcherProps> = ({
  isOpen,
  onClose,
  initialQuery = '',
}) => {
  const [query, setQuery] = useState('')
  const [scopeFilter, setScopeFilter] = useState<QuickNavigationScope | null>(null)
  const [selectedIndex, setSelectedIndex] = useState(0)
  const searchInputRef = useRef<HTMLInputElement>(null)
  const listRef = useRef<HTMLDivElement>(null)

  const selectedModeCategory = useModeStore(state => state.selectedModeCategory)
  const productSurface = useProductSurfaceStore(state => state.productSurface)
  const isWorkflowMode = selectedModeCategory === 'workflow'
  const isChatMode = selectedModeCategory === 'multi-agent'
  const activePresetId = useGlobalPresetStore(state => state.activePresetIds.workflow)
  // Subscribe only while open. chatTabs changes on streaming event updates, so
  // keeping this inactive when the switcher is closed avoids background churn.
  const activeTabId = useChatStore(state => (isOpen ? state.activeTabId : null))
  const chatTabs = useChatStore(state => (isOpen ? state.chatTabs : EMPTY_CHAT_TABS))
  const activeSessions = useChatStore(state => (isOpen ? state.activeSessionsCache : EMPTY_ACTIVE_SESSIONS))
  const workflowPresets = useGlobalPresetStore(state => (isOpen ? state.workflowPresets : EMPTY_WORKFLOW_PRESETS))
  const recentPresetOrder = useGlobalPresetStore(state => (isOpen ? state.recentPresetOrder : EMPTY_RECENT_PRESET_ORDER))
  const recentPresetAccessedAt = useGlobalPresetStore(state => (isOpen ? state.recentPresetAccessedAt : EMPTY_RECENT_PRESET_ACCESSED_AT))

  // Every Crew the user can use (owned and shared), so the switcher lists them
  // all, not only Crews with an open tab or running work. Loaded per opening.
  const [crewDirectory, setCrewDirectory] = useState<WorkSession[]>([])
  const [codeDirectory, setCodeDirectory] = useState<WorkSession[]>([])
  // Code workspaces are listed only where this deployment offers Code and
  // the account may open it.
  const allowedProducts = useAuthStore(state => state.user?.allowed_products)
  const user = useAuthStore(state => state.user)
  const navigationItems = useMemo(() => quickNavigationItems(user, productSurface), [user, productSurface])
  const footerItems = navigationItems.filter(item => item.type === 'menu')
  const codeAvailable = useMemo(
    () => isEnabledProductSurface('code') && intersectAllowedProductSurfaces(['code'], allowedProducts).includes('code'),
    [allowedProducts],
  )
  useEffect(() => {
    if (!isOpen) return
    let cancelled = false
    // Loaded lazily: a static import of the Crew product module from the
    // app shell forms an import cycle.
    const sessions = import('../products/work/workSessions')
    void sessions
      .then(module => module.loadWorkSessionsIncludingShared())
      .then(crews => { if (!cancelled) setCrewDirectory(crews) })
      .catch(() => { if (!cancelled) setCrewDirectory([]) })
    if (codeAvailable) {
      void sessions
        .then(module => module.loadWorkSessionsIncludingShared(CODE_PRODUCT))
        .then(codes => { if (!cancelled) setCodeDirectory(codes) })
        .catch(() => { if (!cancelled) setCodeDirectory([]) })
    } else {
      setCodeDirectory([])
    }
    // All accessible workflows, even if the workflow view was never opened.
    const presets = useGlobalPresetStore.getState()
    if (!presets.workflowPresetsLoaded) void presets.refreshPresets()
    return () => { cancelled = true }
  }, [isOpen, codeAvailable])

  // Reset state on open.
  useEffect(() => {
    if (isOpen) {
      setQuery(initialQuery)
      setScopeFilter(null)
      setSelectedIndex(0)
      // Paint from the subscribed cache immediately. The normal TTL still
      // refreshes stale data without forcing a request into the open path.
      void useChatStore.getState().getActiveSessions()
      setTimeout(() => searchInputRef.current?.focus(), 50)
    }
  }, [isOpen, initialQuery])

  // All products share navigation; stable product metadata owns project routing.
  const allItems = useMemo<QuickSwitcherItem[]>(() => {
    if (!isOpen) return []

    const { tabs: agentWorksTabs, sessions: agentWorksSessions } = scopeQuickSwitcherToAgentWorks(chatTabs, activeSessions)
    const crewTabs = Object.fromEntries(
      Object.entries(chatTabs).filter(([, tab]) => tab.metadata?.agentProfileId === 'work'),
    )
    const codeTabs = codeAvailable
      ? Object.fromEntries(Object.entries(chatTabs).filter(([, tab]) => tab.metadata?.agentProfileId === 'code'))
      : EMPTY_CHAT_TABS
    const crewSessionIds = new Set([...Object.values(crewTabs), ...Object.values(codeTabs)].map(tab => tab.sessionId).filter(Boolean))
    const sharedSessions = activeSessions.filter(session =>
      agentWorksSessions.some(candidate => candidate.session_id === session.session_id) ||
      crewSessionIds.has(session.session_id) ||
      isWorkProductSession(session) ||
      (codeAvailable && isCodeProductSession(session)),
    )
    const allTabs = { ...agentWorksTabs, ...crewTabs, ...codeTabs }
    const activeSessionsByID = new Map<string, ActiveSessionInfo>()
    for (const session of sharedSessions.filter(isVisibleActivitySession)) {
      activeSessionsByID.set(session.session_id, session)
    }
    const visibleActiveSessions = Array.from(activeSessionsByID.values())

    const builderStateSuffix = (tab?: ChatTab): string => {
      if (!tab?.hasRunningBgAgents) return ''
      return tab.isStreaming || tab.isSyntheticTurn ? ' · builder busy' : ' · builder idle'
    }

    const chatItems: ChatTabItem[] = Object.values(agentWorksTabs)
      .filter(tab => tab.metadata?.mode === 'multi-agent' && !tab.metadata?.isOrganizationAssistant)
      .sort((a, b) => a.createdAt - b.createdAt)
      .map(tab => {
        const activeSession = tab.sessionId ? visibleActiveSessions.find(session => session.session_id === tab.sessionId) : undefined
        const streamingLabel = tab.isStreaming ? 'Streaming...' : tab.isCompleted ? 'Completed' : tab.sessionId ? 'Active' : 'New'
        return {
          type: 'chat' as const,
          id: `chat:${tab.tabId}`,
          label: tab.name,
          subtitle: `Chat · ${streamingLabel}${builderStateSuffix(tab)}${activeSessionSuffix(activeSession)}`,
          isActive: productSurface === 'agentworks' && isChatMode && tab.tabId === activeTabId,
          lastAccessedAt: tab.lastAccessedAt || tab.createdAt || 0,
          tabId: tab.tabId,
          activeSession,
          hasLocalActivity: tabHasRunningWork(tab),
        }
      })

    const crewItems: CrewChatItem[] = Object.values(crewTabs)
      .filter(tab => !tab.metadata?.isScheduledRun && !tab.metadata?.isBotRun && !tab.metadata?.isViewOnly)
      .sort((a, b) => (b.lastAccessedAt || b.createdAt || 0) - (a.lastAccessedAt || a.createdAt || 0))
      .map(tab => {
        const activeSession = tab.sessionId ? visibleActiveSessions.find(session => session.session_id === tab.sessionId) : undefined
        const project = tab.metadata?.agentProfileIdentityName || tab.metadata?.agentProfileProjectTitle || 'Crew'
        const role = tab.metadata?.agentProfileBuilder ? 'Builder' : 'Chat'
        return {
          type: 'crew' as const,
          id: `crew:${tab.tabId}`,
          label: project,
          subtitle: `Crew · ${role}${builderStateSuffix(tab)}${activeSessionSuffix(activeSession)}`,
          isActive: productSurface === 'work' && tab.tabId === activeTabId,
          lastAccessedAt: tab.lastAccessedAt || tab.createdAt || 0,
          tabId: tab.tabId,
          activeSession,
          hasLocalActivity: tabHasRunningWork(tab),
          icon: tab.metadata?.agentProfileProjectIcon,
        }
      })

    const codeItems: CrewChatItem[] = Object.values(codeTabs)
      .filter(tab => !tab.metadata?.isScheduledRun && !tab.metadata?.isBotRun && !tab.metadata?.isViewOnly)
      .map(tab => {
        const activeSession = tab.sessionId ? visibleActiveSessions.find(session => session.session_id === tab.sessionId) : undefined
        return {
          type: 'code' as const,
          id: `code:${tab.tabId}`,
          label: tab.metadata?.agentProfileProjectTitle || tab.name || 'Code',
          // A side chat (PLAT-571) is named after itself so two chats of one project differ.
          subtitle: `Code${isWorkSideChatTab(tab) ? ` · ${tab.name}` : ''}${builderStateSuffix(tab)}${activeSessionSuffix(activeSession)}`,
          isActive: productSurface === 'code' && tab.tabId === activeTabId,
          lastAccessedAt: tab.lastAccessedAt || tab.createdAt || 0,
          tabId: tab.tabId,
          activeSession,
          hasLocalActivity: tabHasRunningWork(tab),
        }
      })
    const openCodeProjects = new Set(Object.values(codeTabs).map(tab => workProjectIdForTab(tab)).filter(Boolean))
    const directoryCodeItems: CrewChatItem[] = codeDirectory
      .filter(code => !openCodeProjects.has(code.id))
      .map(code => ({
        type: 'code' as const,
        id: `code-project:${code.id}`,
        label: code.title,
        subtitle: `Code · ${code.shared ? `shared by ${code.shared.ownerUsername || code.shared.ownerId}` : 'yours'}`,
        isActive: productSurface === 'code' && useProductSurfaceStore.getState().selectedCodeProjectId === code.id,
        lastAccessedAt: 0,
        projectId: code.id,
        hasLocalActivity: false,
      }))

    const openCrewProjects = new Set(Object.values(crewTabs).map(tab => workProjectIdForTab(tab)).filter(Boolean))
    const directoryCrewItems: CrewChatItem[] = crewDirectory
      .filter(crew => !openCrewProjects.has(crew.id))
      .map(crew => {
        const name = crew.identity?.name?.trim() || crew.title
        const owner = crew.shared ? `shared by ${crew.shared.ownerUsername || crew.shared.ownerId}` : 'yours'
        return {
          type: 'crew' as const,
          id: `crew-project:${crew.id}`,
          label: name,
          subtitle: `Crew · ${owner}${name !== crew.title ? ` · ${crew.title}` : ''}`,
          isActive: productSurface === 'work' && useProductSurfaceStore.getState().selectedWorkProjectId === crew.id,
          lastAccessedAt: 0,
          projectId: crew.id,
          hasLocalActivity: false,
          icon: crew.identity?.icon,
        }
      })

    const workflowItems: WorkflowItem[] = workflowPresets
      .filter(preset => preset.selectedFolder?.filepath)
      .map(preset => {
        const matchingActiveSessions = visibleActiveSessions.filter(session =>
          !isWorkProductSession(session) && workflowSessionMatchesPreset(session, preset, agentWorksTabs),
        )
        const activeSession = pickWorkflowActiveSession(matchingActiveSessions, preset, agentWorksTabs)
        const workflowTab = Object.values(agentWorksTabs).find(tab =>
          tab.metadata?.mode === 'workflow' && tab.metadata?.presetQueryId === preset.id,
        )
        const activeCountSuffix = matchingActiveSessions.length > 1 ? ` · ${matchingActiveSessions.length} active runs` : ''
        return {
          type: 'workflow' as const,
          id: `workflow:${preset.id}`,
          label: preset.label,
          subtitle: `${preset.workflowKind === 'relay' ? 'Relay' : 'Automation'} · ${preset.selectedFolder!.filepath}${builderStateSuffix(workflowTab)}${activeSessionSuffix(activeSession)}${activeCountSuffix}`,
          isActive: productSurface === workflowSurfaceForPreset(preset.id) && isWorkflowMode && preset.id === activePresetId,
          lastAccessedAt: recentPresetAccessedAt[preset.id] || (() => {
            const recentIndex = recentPresetOrder.indexOf(preset.id)
            return recentIndex >= 0 ? 1_000_000 - recentIndex : 0
          })(),
          preset,
          activeSession,
          hasLocalActivity: !!workflowTab && tabHasRunningWork(workflowTab),
        }
      })

    const listedTabIds = new Set([...chatItems, ...crewItems, ...codeItems].map(item => item.tabId))
    const activeItems: ActiveWorkItem[] = visibleActiveSessions
      .map(session => {
        const tab = findTabForSession(allTabs, session.session_id)
        const code = isCodeProductSession(session)
        const crew = !code && isWorkProductSession(session)
        const workflow = !crew && !code && isWorkflowSession(session)
        const status = activeSessionStatusLabel(session)
        const current = session.current_execution_name ? ` · ${session.current_execution_name}` : ''
        const origin = sessionOriginLabel(session)
        const coveredByTab = !!tab && listedTabIds.has(tab.tabId)
        const coveredByAutomation = !crew && !code && workflow &&
          workflowItems.some(item => workflowSessionMatchesPreset(session, item.preset, agentWorksTabs))
        return {
          type: 'active' as const,
          id: `active:${session.session_id}`,
          label: titleWithoutOrigin(activeSessionLabel(session), origin),
          subtitle: `${code ? 'Active Code work' : crew ? 'Active Crew work' : workflow ? 'Active automation' : 'Active chat'} · ${origin} · ${status}${current} · ${sessionShortId(session.session_id)}`,
          activeScopeOnly: coveredByTab || coveredByAutomation,
          isActive: !!tab && tab.tabId === activeTabId && productSurface === (code ? 'code' : crew ? 'work' : 'agentworks'),
          lastAccessedAt: tab?.lastAccessedAt || tab?.createdAt || Date.parse(session.last_activity || session.created_at || '') || 0,
          session,
          tabId: tab?.tabId,
          mode: workflow ? 'workflow' : 'multi-agent',
        }
      })

    workflowItems.sort((a, b) => {
      const aIdx = recentPresetOrder.indexOf(a.preset.id)
      const bIdx = recentPresetOrder.indexOf(b.preset.id)
      if (aIdx !== -1 && bIdx !== -1) return aIdx - bIdx
      if (aIdx !== -1) return -1
      if (bIdx !== -1) return 1
      return a.label.localeCompare(b.label)
    })

    const projectItems = [...activeItems, ...crewItems, ...codeItems, ...chatItems, ...workflowItems, ...directoryCrewItems, ...directoryCodeItems].sort((a, b) => {
      const aRunning = itemHasRunningWork(a)
      const bRunning = itemHasRunningWork(b)
      if (aRunning !== bRunning) return aRunning ? -1 : 1
      if (a.isActive !== b.isActive) return a.isActive ? -1 : 1
      if (a.lastAccessedAt !== b.lastAccessedAt) return b.lastAccessedAt - a.lastAccessedAt
      if (a.type !== b.type) return itemTypeRank(a) - itemTypeRank(b)
      return a.label.localeCompare(b.label)
    })
    const visibleProducts = new Set(navigationItems.flatMap(item => item.surface ? [item.surface] : []))
    return [...projectItems.filter(item => {
      if (item.type === 'crew') return visibleProducts.has('work')
      if (item.type === 'code') return visibleProducts.has('code')
      if (item.type === 'workflow') return visibleProducts.has(item.preset.workflowKind === 'relay' ? 'relays' : 'agentworks')
      if (item.type === 'active') {
        if (isWorkProductSession(item.session)) return visibleProducts.has('work')
        if (isCodeProductSession(item.session)) return visibleProducts.has('code')
      }
      return visibleProducts.has('agentworks') || visibleProducts.has('relays')
    }), ...navigationItems.filter(item => item.scope), ...navigationItems.filter(item => !item.scope)]
  }, [isOpen, isWorkflowMode, isChatMode, productSurface, activePresetId, chatTabs, activeSessions, activeTabId, workflowPresets, recentPresetOrder, recentPresetAccessedAt, crewDirectory, codeDirectory, codeAvailable, navigationItems])

  // Filter and sort
  const filteredItems = useMemo<QuickSwitcherItem[]>(() => {
    const rawQuery = query.toLowerCase().trim()
    const scopeMatch = rawQuery.match(/^@(active|workflows?|relays?|chats?|tabs|crew|code|products?|menus?)\s*/)
    const scope = scopeMatch?.[1] || scopeFilter
    const q = scopeMatch ? rawQuery.slice(scopeMatch[0].length).trim() : rawQuery
    const scoped = scope
      ? allItems.filter(item => {
          // Every running session once: its own row, plus tabs whose only
          // activity is local (no server session yet).
          if (scope === 'active') return item.type === 'active' || (!item.activeSession && item.hasLocalActivity)
          if (scope === 'workflow' || scope === 'workflows') return item.type === 'workflow' && item.preset.workflowKind !== 'relay'
          if (scope === 'relay' || scope === 'relays') return item.type === 'workflow' && item.preset.workflowKind === 'relay'
          if (scope === 'product' || scope === 'products') return item.type === 'product'
          if (scope === 'menu' || scope === 'menus') return item.type === 'menu'
          if (scope === 'crew') return item.type === 'crew' || (item.type === 'active' && !item.activeScopeOnly && isWorkProductSession(item.session))
          if (scope === 'code') return item.type === 'code' || (item.type === 'active' && !item.activeScopeOnly && isCodeProductSession(item.session))
          if (scope === 'chat' || scope === 'chats') return item.type === 'chat'
          return item.type === 'chat' || item.type === 'workflow' || item.type === 'crew' || item.type === 'code'
        })
      : allItems.filter(item => item.type !== 'active' || !item.activeScopeOnly)

    if (!q) return scoped

    const filtered = scoped.filter(item =>
      item.label.toLowerCase().includes(q) ||
      item.subtitle.toLowerCase().includes(q)
    )

    filtered.sort((a, b) => {
      const aExact = a.label.toLowerCase() === q
      const bExact = b.label.toLowerCase() === q
      if (aExact && !bExact) return -1
      if (!aExact && bExact) return 1
      const aStarts = a.label.toLowerCase().startsWith(q)
      const bStarts = b.label.toLowerCase().startsWith(q)
      if (aStarts && !bStarts) return -1
      if (!aStarts && bStarts) return 1
      return a.label.localeCompare(b.label)
    })

    return filtered
  }, [query, scopeFilter, allItems])

  // Read filteredItems via a ref so this effect does NOT fire on every new
  // array reference — only on real user intent changes (query, mode, open).
  // Otherwise streaming re-renders would snap the selection back on each tick.
  const filteredItemsRef = useRef(filteredItems)
  filteredItemsRef.current = filteredItems
  useEffect(() => {
    if (!query.trim() && !scopeFilter) {
      const items = filteredItemsRef.current
      const runningAlternative = items.findIndex(item => itemHasRunningWork(item) && !item.isActive)
      const firstRunning = items.findIndex(itemHasRunningWork)
      const firstNonActive = items.findIndex(item => item.type !== 'menu' && item.type !== 'product' && !item.isActive)
      setSelectedIndex(runningAlternative >= 0 ? runningAlternative : firstRunning >= 0 ? firstRunning : firstNonActive >= 0 ? firstNonActive : 0)
    } else {
      setSelectedIndex(0)
    }
  }, [isOpen, isChatMode, isWorkflowMode, query, scopeFilter])

  // Clamp (don't reset) when the list length changes so a narrowing filter
  // keeps a valid index without discarding the user's position.
  useEffect(() => {
    setSelectedIndex(prev => {
      if (filteredItems.length === 0) return 0
      return Math.min(prev, filteredItems.length - 1)
    })
  }, [filteredItems.length])

  useEffect(() => {
    if (listRef.current && selectedIndex >= 0) {
      const el = listRef.current.children[selectedIndex] as HTMLElement
      el?.scrollIntoView({ block: 'nearest', behavior: 'auto' })
    }
  }, [selectedIndex])

  const handleSelect = useCallback(async (item: QuickSwitcherItem) => {
    if (item.type === 'product' || item.type === 'menu') {
      if (item.scope) {
        const current = useProductSurfaceStore.getState().productSurface
        if (!quickNavigationItems(useAuthStore.getState().user, current).some(candidate => candidate.id === item.id)) return
        setScopeFilter(item.scope); setQuery(''); setSelectedIndex(0)
        if (listRef.current) listRef.current.scrollTop = 0
        searchInputRef.current?.focus()
        return
      }
      if (openQuickNavigation(item)) onClose()
      return
    }
    if (item.type === 'active') {
      // Shared path with the header activity monitor so opening the same session
      // behaves identically from either surface.
      await openGlobalActivitySession(item.session, {
        title: item.label,
        source: 'quick-switcher',
      })
      onClose()
      return
    }

    if (item.type === 'crew' && !item.tabId && item.projectId) {
      const surfaces = useProductSurfaceStore.getState()
      surfaces.setSelectedWorkProjectId(item.projectId)
      openProductWorkspace('work')
      onClose()
      return
    }

    if (item.type === 'code' && !item.tabId && item.projectId) {
      const surfaces = useProductSurfaceStore.getState()
      surfaces.setSelectedCodeProjectId(item.projectId)
      openProductWorkspace('code')
      onClose()
      return
    }

    if ((item.type === 'chat' || item.type === 'crew' || item.type === 'code') && item.tabId) {
      console.log(`%c[QuickSwitcher] Switching to chat tab: ${item.label} (${item.tabId})`, 'color: #FF9800; font-weight: bold')
      openGlobalTab(item.tabId)
      onClose()
      return
    }

    if (item.type !== 'workflow') {
      onClose()
      return
    }

    // Workflow switching
    console.log(`%c[QuickSwitcher] Switching to workflow: ${item.label?.slice(0,30)} (${item.id?.slice(0,8)})`, 'color: #FF9800; font-weight: bold')
    console.time('[QuickSwitcher] workflow-switch-total')

    useProductSurfaceStore.getState().setProductSurface(workflowSurfaceForPreset(item.preset.id))
    await openWorkflowPresetPage(item.preset, {
      activeSession: item.activeSession,
      title: item.label,
      source: 'quick-switcher',
    })
    console.timeEnd('[QuickSwitcher] workflow-switch-total')
    onClose()
  }, [onClose])

  const handleKeyDown = useCallback((e: React.KeyboardEvent) => {
    if (e.key === 'ArrowDown') {
      e.preventDefault()
      setSelectedIndex(prev => Math.min(prev + 1, filteredItems.length - 1))
    } else if (e.key === 'ArrowUp') {
      e.preventDefault()
      setSelectedIndex(prev => Math.max(prev - 1, 0))
    } else if (e.key === 'Enter') {
      e.preventDefault()
      if (filteredItems.length > 0 && selectedIndex >= 0 && selectedIndex < filteredItems.length) {
        void handleSelect(filteredItems[selectedIndex])
      }
    } else if (e.key === 'Escape') {
      e.preventDefault()
      onClose()
    }
  }, [filteredItems, selectedIndex, handleSelect, onClose])

  if (!isOpen) return null

  const placeholder = 'Search running work, projects, products, or menus...'
  const emptyText = query ? 'No matching items' : scopeFilter ? 'No items in this list' : 'No work to switch to. Search for a product or menu.'
  return (
    <div
      className="fixed inset-0 z-50 flex items-start justify-center pt-[20vh]"
      onClick={onClose}
    >
      {/* Backdrop */}
      <div className="absolute inset-0 bg-black/50" />

      {/* Dialog */}
      <div
        role="dialog" aria-modal="true" aria-label="Quick navigation"
        className="relative w-[min(46rem,calc(100vw-2rem))] bg-white dark:bg-gray-800 border border-gray-200 dark:border-gray-700 rounded-xl shadow-2xl overflow-hidden text-gray-900 dark:text-gray-100"
        onClick={e => e.stopPropagation()}
      >
        {/* Search input */}
        <div className="flex items-center gap-3 px-4 py-3 border-b border-gray-200 dark:border-gray-700">
          <Search className="w-5 h-5 text-gray-400 flex-shrink-0" />
          {scopeFilter && <button type="button" aria-label="Show all work and navigation" title="Clear filter"
            onClick={() => { setScopeFilter(null); if (listRef.current) listRef.current.scrollTop = 0; searchInputRef.current?.focus() }}
            className="flex shrink-0 items-center gap-1 rounded bg-secondary px-2 py-1 text-xs text-secondary-foreground">
            {navigationItems.find(item => item.scope === scopeFilter)?.label}<X className="h-3 w-3" aria-hidden="true" />
          </button>}
          <input
            ref={searchInputRef}
            type="text"
            placeholder={placeholder}
            value={query}
            onChange={e => setQuery(e.target.value)}
            onKeyDown={handleKeyDown}
            className="flex-1 bg-transparent text-sm text-foreground placeholder:text-muted-foreground focus:outline-none"
          />
          <kbd className="hidden sm:inline-flex px-1.5 py-0.5 text-[10px] font-mono text-gray-400 bg-gray-100 dark:bg-gray-700 rounded">
            ESC
          </kbd>
        </div>

        {/* Item list */}
        <div ref={listRef} className="overflow-y-auto max-h-[48vh]">
          {filteredItems.length === 0 ? (
            <div className="px-4 py-8 text-center text-muted-foreground text-sm">
              {emptyText}
            </div>
          ) : (
            filteredItems.map((item, index) => {
              const isSelected = index === selectedIndex
              const ItemIcon = item.type === 'workflow'
                ? Layers
                : item.type === 'product'
                  ? LayoutGrid
                : item.type === 'menu'
                  ? item.action === 'providers' ? Cpu : item.action === 'users' ? Users : item.action === 'mcp' ? Plug : item.action === 'schedules' ? CalendarClock : Activity
                : item.type === 'crew'
                  ? Users
                : item.type === 'code'
                  ? Code2
                : item.type === 'active'
                  ? Activity
                  : MessageSquare
              const activeSession = itemActiveSession(item)
              return (
                <div
                  key={item.id}
                  data-navigation-id={item.id}
                  className={`px-4 py-2.5 cursor-pointer flex items-center gap-3 transition-colors ${
                    isSelected
                      ? 'bg-blue-50 dark:bg-blue-900/30'
                      : 'hover:bg-gray-50 dark:hover:bg-gray-700/50'
                  }`}
                  onMouseEnter={() => setSelectedIndex(index)}
                  onMouseDown={e => { e.preventDefault(); void handleSelect(item) }}
                >
                  {item.type === 'product' || item.type === 'menu'
                    ? <QuickNavigationIcon item={item} />
                    : item.type === 'crew'
                    ? <EntityIdentityIcon icon={item.icon} label={item.label} />
                    : <ItemIcon className={`w-4 h-4 flex-shrink-0 ${item.isActive ? 'text-blue-500' : 'text-gray-400 dark:text-gray-500'}`} />}
                  <div className="flex-1 min-w-0">
                    <div className="flex items-center gap-2">
                      <span className={`text-sm font-medium truncate ${item.isActive ? 'text-blue-600 dark:text-blue-400' : 'text-gray-900 dark:text-gray-100'}`}>
                        {item.label}
                      </span>
                      <span className="text-[10px] px-1.5 py-0.5 rounded-full bg-gray-100 dark:bg-gray-700 text-gray-500 dark:text-gray-300 font-medium flex-shrink-0">
                        {(item.type === 'menu' || item.type === 'product') && item.scope ? 'browse' : item.type === 'workflow' && item.preset.workflowKind === 'relay' ? 'relay' : item.type}
                      </span>
                      {activeSession && item.type !== 'active' && (
                        runtimeNeedsUserInput(activeSession) ? (
                          <span className="text-[10px] px-1.5 py-0.5 rounded-full bg-amber-100 dark:bg-amber-900/40 text-amber-700 dark:text-amber-300 font-medium flex-shrink-0">
                            needs input
                          </span>
                        ) : (
                          <span className="text-[10px] px-1.5 py-0.5 rounded-full bg-emerald-100 dark:bg-emerald-900/40 text-emerald-700 dark:text-emerald-300 font-medium flex-shrink-0">
                            active
                          </span>
                        )
                      )}
                      {!activeSession && itemHasRunningWork(item) && (
                        <span className="text-[10px] px-1.5 py-0.5 rounded-full bg-emerald-100 dark:bg-emerald-900/40 text-emerald-700 dark:text-emerald-300 font-medium flex-shrink-0">
                          running
                        </span>
                      )}
                      {item.isActive && (
                        <span className="text-[10px] px-1.5 py-0.5 rounded-full bg-blue-100 dark:bg-blue-900/50 text-blue-600 dark:text-blue-400 font-medium flex-shrink-0">
                          current
                        </span>
                      )}
                    </div>
                    <div className="text-xs text-muted-foreground truncate">{item.subtitle}</div>
                  </div>
                </div>
              )
            })
          )}
        </div>

        {/* Footer */}
        <div className="border-t border-gray-200 bg-gray-50 dark:border-gray-700 dark:bg-gray-900/50">
          <div className="px-4 py-2 text-[11px] text-gray-400 dark:text-gray-500 flex flex-wrap items-center justify-between gap-x-3 gap-y-2">
            <div className="flex items-center gap-3 min-w-0">
              <span><kbd className="px-1 py-0.5 bg-gray-200 dark:bg-gray-600 rounded text-[10px]">↑↓</kbd> navigate</span>
              <span><kbd className="px-1 py-0.5 bg-gray-200 dark:bg-gray-600 rounded text-[10px]">↵</kbd> switch</span>
            </div>
            <TooltipProvider delayDuration={150}>
              <nav aria-label="Quick navigation shortcuts" className="order-last flex w-full min-w-0 flex-wrap items-center justify-center gap-1 sm:order-none sm:w-auto sm:flex-1">
                {footerItems.map(item => <Tooltip key={item.id}>
                  <TooltipTrigger asChild><button type="button" aria-label={item.label} title={item.label}
                    aria-pressed={item.scope ? scopeFilter === item.scope : undefined}
                    onClick={() => { void handleSelect(item) }}
                    className={`grid h-7 w-7 shrink-0 place-items-center rounded-md transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary ${item.scope && scopeFilter === item.scope ? 'bg-primary/10 text-primary' : 'text-muted-foreground hover:bg-secondary hover:text-foreground'}`}>
                    <QuickNavigationIcon item={item} />
                  </button></TooltipTrigger>
                  <TooltipContent side="top">{item.label}</TooltipContent>
                </Tooltip>)}
              </nav>
            </TooltipProvider>
            <span className="flex-shrink-0"><kbd className="px-1 py-0.5 bg-gray-200 dark:bg-gray-600 rounded text-[10px]">esc</kbd> close</span>
          </div>
        </div>
      </div>
    </div>
  )
}

export default QuickSwitcher
