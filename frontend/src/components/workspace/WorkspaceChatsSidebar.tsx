import { requestServiceChat } from '../../utils/serviceChatNavigation'
import { useResumePreviousChat } from '../../hooks/useResumePreviousChat'
import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { CalendarClock, ChevronDown, Loader2, MessageCircle, MessageSquare, Search, Slack, Webhook, X } from 'lucide-react'
import { useShallow } from 'zustand/react/shallow'
import { useAuthStore } from '../../stores/useAuthStore'
import { useChatStore } from '../../stores/useChatStore'
import { useAppStore } from '../../stores/useAppStore'
import { useLLMStore } from '../../stores/useLLMStore'
import { useGlobalPresetStore } from '../../stores/useGlobalPresetStore'
import { useProductSurfaceStore } from '../../stores/useProductSurfaceStore'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'
import { useSidebarHistoryStore } from '../../stores/useSidebarHistoryStore'
import { sidebarExpansionKey, useSidebarExpansionStore } from '../../stores/useSidebarExpansionStore'
import { agentApi } from '../../services/api'
import { canCloseWorkspaceChatTab, closeWorkspaceChatTab } from '../../utils/closeWorkspaceChatTab'
import { schedulerApi } from '../../api/scheduler'
import type { ActiveSessionInfo, ChatHistorySession, ScheduledJob } from '../../services/api-types'
import {
  enabledProductSurfaces,
  intersectAllowedProductSurfaces,
  PRODUCT_SURFACE_LABELS,
} from '../../products/productSurfaceConfig'
import { useLiveRefetch } from '../../hooks/useLiveRefetch'
import { useWorkflowBuilderBindings } from '../../hooks/useWorkflowBuilderBindings'
import {
  sidebarChats,
  sidebarScope,
  sidebarWorkspaceGroups,
  sidebarChatDisplayTitle,
  isGeneratedSidebarPrompt,
  type SidebarChat,
  type SidebarProject,
} from '../../utils/sidebarChats'
import { runtimeTurnFailedUnseen } from '../../utils/runtimeActivity'
import { openGlobalActivitySession, openGlobalTab } from '../../utils/globalProductNavigation'
import { openProductWorkspace } from '../../utils/productWorkspaceNavigation'
import { followAppLink } from '../../utils/appNavigation'
import { captureChatIdentity, isChatIdentityCurrent } from '../../utils/chatIdentity'
import { ProductSurfaceIcon } from '../ProductSurfaceIcon'
import { EntityIdentityIcon } from '../ui/EntityIdentityIcon'
import { RuntimeBrandLogo } from '../branding/RuntimeBrandLogo'
import { NewProjectMenu } from './NewProjectMenu'
import { inspectionKey } from '../../utils/stepInspection'
import { workflowTriggerLabel } from '../../utils/workflowSessionKinds'
import { SharedToolShortcuts } from './SharedToolShortcuts'

// Stored chats only resolve each workflow's Builder and run chats; there is no Recent list.
const HISTORY_LIMIT = 30
const PROJECT_SIDEBAR_ORDER = ['work', 'agentworks', 'relays'] as const

function ProjectIcon({ icon, title, surface }: { icon?: string; title: string; surface: SidebarChat['surface'] }) {
  return <EntityIdentityIcon icon={icon} label={title} className="h-4 w-4" fallback={<ProductSurfaceIcon surface={surface} className="h-4 w-4 shrink-0" />} />
}

export default function WorkspaceChatsSidebar({ toggle, footer }: { toggle?: ReactNode; footer?: ReactNode }) {
  const user = useAuthStore((state) => state.user)
  const workspace = useWorkspaceConnectionStore((state) => state.activeWorkspaceId)
  const scope = `${workspace}:${user?.id || ''}`
  const { tabs, active, selectedTab } = useChatStore(
    useShallow((state) => ({
      tabs: state.chatTabs,
      active: state.activeSessionsCache,
      selectedTab: state.activeTabId,
    }))
  )
  const product = useProductSurfaceStore((state) => state.productSurface)
  const globalPageOpen = useAppStore(state => state.showWorkflowsOverview || state.showSchedulesOverview || state.adminPage !== null)
  const providersOpen = useLLMStore(state => state.showLLMModal)
  const recentPresetOrder = useGlobalPresetStore((state) => state.recentPresetOrder)
  const presets = useGlobalPresetStore((state) => state.workflowPresets)
  const [snapshot, setSnapshot] = useState<{
    scope: string
    sessions: ChatHistorySession[]
    error: boolean
  } | null>(null)
  const [directory, setDirectory] = useState<{
    scope: string
    projects: SidebarProject[]
    authoritative: boolean
  } | null>(null)
  const [scheduleCatalog, setScheduleCatalog] = useState<{
    scope: string
    jobs: ScheduledJob[]
  } | null>(null)
  const pendingHistory = useSidebarHistoryStore((state) => state.request)
  const resumeHistory = useResumePreviousChat()
  const [tick, setTick] = useState(0)
  const [directoryTick, setDirectoryTick] = useState(0)
  const [opening, setOpening] = useState<string | null>(null)
  const [closing, setClosing] = useState<string | null>(null)
  const expanded = useSidebarExpansionStore(state => state.expanded)
  const toggleExpanded = useSidebarExpansionStore(state => state.toggle)
  const allowed = intersectAllowedProductSurfaces(enabledProductSurfaces(), user?.allowed_products)
  useLiveRefetch(
    () => {
      setTick((value) => value + 1)
      void useChatStore.getState().getActiveSessions()
    },
    {
      kinds: ['sessions', 'schedules'],
      fallbackMs: 30000,
      safetyMs: 60000,
      minIntervalMs: 2000,
    }
  )
  useLiveRefetch(() => setDirectoryTick(value => value + 1), {
    kinds: ['sessions'], fallbackMs: 60000, safetyMs: 60000, minIntervalMs: 30000,
    enabled: allowed.includes('work'),
  })
  useEffect(() => {
    let disposed = false
    const identity = captureChatIdentity()
    void useChatStore.getState().getActiveSessions()
    const scopedPresets = [...presets]
      .sort((a, b) => {
        const ai = recentPresetOrder.indexOf(a.id),
          bi = recentPresetOrder.indexOf(b.id)
        return (ai < 0 ? 999 : ai) - (bi < 0 ? 999 : bi)
      })
      .filter(
        (preset) =>
          preset.selectedFolder?.filepath &&
          allowed.includes(preset.workflowKind === 'relay' ? 'relays' : 'agentworks') &&
          (!user?.allowed_workflow_ids || user.allowed_workflow_ids.includes(preset.id))
      )
      .slice(0, 6)
    void Promise.allSettled([
      agentApi.listChatHistorySessions(HISTORY_LIMIT),
      ...scopedPresets.map((preset) =>
        agentApi.listChatHistorySessions(5, 0, preset.selectedFolder!.filepath)
      ),
    ])
      .then((results) => {
        if (!disposed && isChatIdentityCurrent(identity))
          setSnapshot({
            scope,
            sessions: results.flatMap((result) =>
              result.status === 'fulfilled' ? result.value.sessions || [] : []
            ),
            error: results.some((result) => result.status === 'rejected'),
          })
      })
      .catch(() => {
        if (!disposed && isChatIdentityCurrent(identity))
          setSnapshot({ scope, sessions: [], error: true })
      })
    return () => {
      disposed = true
    }
  }, [scope, tick, presets.map((preset) => preset.id).join(','), allowed.join(',')])
  useEffect(() => {
    let disposed = false
    const identity = captureChatIdentity()
    if (!allowed.includes('work')) {
      setDirectory({ scope, projects: [], authoritative: true })
      return
    }
    void agentApi.listWorkspaceNavigation()
      .then(result => {
        if (!disposed && isChatIdentityCurrent(identity))
          setDirectory({
            scope,
            authoritative: true,
            projects: result.workspaces.filter(item => allowed.includes(item.profile_id)).map(item => ({
              id: item.id, workspaceId: item.workspace_id, workspacePath: item.workspace_path,
              aliases: item.aliases, title: item.name, icon: item.icon, surface: item.profile_id,
            })),
          })
      })
      .catch(() => {
        if (!disposed && isChatIdentityCurrent(identity)) setDirectory({ scope, projects: [], authoritative: false })
      })
    return () => {
      disposed = true
    }
  }, [scope, directoryTick, allowed.join(','), Object.keys(tabs).join(',')])
  useEffect(() => {
    let disposed = false
    const identity = captureChatIdentity()
    const entities = [
      ...(allowed.includes('agentworks') || allowed.includes('relays') ? ['workflow'] : []),
      ...(allowed.includes('work') ? ['product'] : []),
    ]
    void Promise.allSettled(
      entities.map((entity_type) => schedulerApi.listJobs({ entity_type, limit: 1000 }))
    ).then((results) => {
      if (!disposed && isChatIdentityCurrent(identity))
        setScheduleCatalog({
          scope,
          jobs: results.flatMap((result) =>
            result.status === 'fulfilled' ? result.value.jobs || [] : []
          ),
        })
    })
    return () => {
      disposed = true
    }
  }, [scope, tick, allowed.join(',')])
  useEffect(() => {
    const request = pendingHistory
    const tab = selectedTab ? tabs[selectedTab] : undefined
    const meta = tab?.metadata
    const profile = meta?.agentProfileId === 'caplayer' ? 'mcp-gateway' : meta?.agentProfileId
    if (
      !request ||
      product !== request.surface ||
      profile !== request.surface ||
      !tab ||
      (request.projectId && meta?.agentProfileProjectId !== request.projectId)
    )
      return
    if (!isChatIdentityCurrent(request.identity)) {
      useSidebarHistoryStore.setState({ request: null })
      return
    }
    const generation = useSidebarHistoryStore.getState().generation
    useSidebarHistoryStore.setState({ request: null })
    const isCurrent = () =>
      isChatIdentityCurrent(request.identity) &&
      useSidebarHistoryStore.getState().generation === generation &&
      useProductSurfaceStore.getState().productSurface === request.surface
    void resumeHistory(request.session, { isCurrent }).catch(() => {
      if (isCurrent()) useChatStore.getState().addToast('Could not open this saved chat.', 'error')
    })
  }, [pendingHistory, selectedTab, tabs, product, resumeHistory])
  const history = snapshot?.scope === scope ? snapshot : null
  const scopedSessions = [
    ...Object.values(tabs).map(tab => ({ id: tab.sessionId || tab.tabId, workflow: sidebarScope(tab.sessionId || '', tab.metadata?.agentProfileWorkspace,
      tab.metadata?.agentProfileId, tab.metadata?.presetQueryId, presets)?.workflowId })),
    ...active.map(session => ({ id: session.session_id, workflow: sidebarScope(session.session_id, session.workspace_path, '', session.preset_query_id, presets)?.workflowId })),
    ...(history?.sessions || []).map(session => ({ id: session.session_id, workflow: sidebarScope(session.session_id,
      session.workspace_path || session.runtime?.workspace_path, '', '', presets)?.workflowId })),
  ]
  const workflowIds = new Set(scopedSessions.map(session => session.workflow))
  const builderSessions: Record<string, string[]> = {}
  for (const id of [...workflowIds].filter((id): id is string => !!id).sort())
    builderSessions[id] = [...new Set(scopedSessions.filter(session => session.workflow === id).map(session => session.id))].sort()
  const builderPresets = presets.filter(preset => workflowIds.has(preset.id) &&
    allowed.includes(preset.workflowKind === 'relay' ? 'relays' : 'agentworks') &&
    (!user?.allowed_workflow_ids || user.allowed_workflow_ids.includes(preset.id)))
  const builderBindings = useWorkflowBuilderBindings(builderPresets, builderSessions)
  const rows = useMemo(
    () =>
      sidebarChats({
        tabs,
        active,
        recent: history?.sessions || [],
        presets,
        allowed,
        projects: directory?.scope === scope ? directory.projects : [],
        projectInventoryKnown: directory?.scope === scope && directory.authoritative,
        allowedWorkflowIds: user?.allowed_workflow_ids,
        jobs: scheduleCatalog?.scope === scope ? scheduleCatalog.jobs : [],
        builders: builderBindings,
      }),
    [
      tabs,
      active,
      history,
      presets,
      allowed.join(','),
      directory,
      scope,
      user?.allowed_workflow_ids,
      scheduleCatalog,
      builderBindings,
    ]
  )
  useEffect(() => {
    // Repair legacy generic schedule labels in the shared tab model so the
    // header, Ctrl+K and this sidebar retain the same recovered job identity.
    const state = useChatStore.getState()
    for (const row of rows) {
      if (!row.tabId || !row.scheduled || / · Scheduled run$/i.test(row.title)) continue
      const tab = state.chatTabs[row.tabId]
      if (!tab || tab.sessionId !== row.id || tab.name === row.title) continue
      if (!/^(Schedule|Scheduled run)$/i.test(tab.name) && !/ · Scheduled run$/i.test(tab.name))
        continue
      state.renameTab(row.tabId, row.title)
      state.setTabMetadata(row.tabId, { scheduledJobName: row.title })
    }
  }, [rows])
  const open = async (row: SidebarChat) => {
    if (
      !intersectAllowedProductSurfaces(
        enabledProductSurfaces(),
        useAuthStore.getState().user?.allowed_products
      ).includes(row.surface)
    )
      return
    const workflows = useAuthStore.getState().user?.allowed_workflow_ids
    if (
      (row.surface === 'agentworks' || row.surface === 'relays') &&
      workflows &&
      (!row.workflowId || !workflows.includes(row.workflowId))
    )
      return
    useSidebarHistoryStore.setState((state) => ({
      request: null,
      generation: state.generation + 1,
    }))
    setOpening(row.id)
    window.dispatchEvent(new Event('close-app-sidebar'))
    try {
      if (row.tabId) {
        openProductWorkspace(row.surface)
        const parent = useChatStore.getState().chatTabs[row.tabId]
        if (parent?.metadata?.stepInspections?.activeId) {
          useChatStore.getState().setTabMetadata(row.tabId, {
            stepInspections: { ...parent.metadata.stepInspections, activeId: null },
          })
        }
        openGlobalTab(row.tabId)
        return
      }
      if (row.history && row.surface !== 'agentworks' && row.surface !== 'relays') {
        const state = useProductSurfaceStore.getState()
        if (row.surface === 'work' && row.projectId) state.setSelectedWorkProjectId(row.projectId)
        useSidebarHistoryStore.setState({
          request: {
            surface: row.surface,
            projectId: row.projectId,
            session: row.history,
            identity: captureChatIdentity(),
          },
        })
        openProductWorkspace(row.surface)
        if (row.surface === 'knowledgebase' || row.surface === 'mcp-gateway') requestServiceChat(row.surface)
        return
      }
      const session: ActiveSessionInfo = row.activity || {
        session_id: row.id,
        observer_id: '',
        agent_mode: row.history?.agent_mode || 'workflow',
        status: row.history?.status || 'completed',
        title: row.title,
        workspace_path: row.workspace,
        created_at: row.history?.created_at || '',
        last_activity: row.history?.updated_at || '',
        bot_platform: row.history?.bot_platform,
        runtime: row.history?.runtime,
      }
      await openGlobalActivitySession(session, {
        title: row.title,
        source: 'chat-sidebar',
      })
    } finally {
      setOpening(null)
    }
  }
  const toggleSection = (id: string) =>
    toggleExpanded(sidebarExpansionKey(scope, 'project', id))
  const workspaces = sidebarWorkspaceGroups(rows, directory?.scope === scope ? directory.projects : [], presets)
  const renderChat = (row: SidebarChat, workspaceTitle?: string, genericChatNumber?: number, workspaceEntry?: { title: string; icon?: string }) => {
    const tab = row.tabId ? tabs[row.tabId] : undefined
    const inspecting = tab?.metadata?.stepInspections?.items.some(item => inspectionKey(item) === tab.metadata?.stepInspections?.activeId)
    const selected = row.tabId === selectedTab && product === row.surface && !globalPageOpen && !providersOpen && !inspecting
    // Red until the person opens the chat; open sets the tab's last-opened time.
    const unseenFailed = !!tab && row.tabId !== selectedTab && runtimeTurnFailedUnseen(row.activity, tab.lastAccessedAt)
    const scheduled = row.scheduled || / · Scheduled run$/i.test(row.title)
    const title = workspaceEntry?.title || sidebarChatDisplayTitle({ ...row, scheduled }, workspaceTitle, genericChatNumber)
    const duplicateWorkspaceName = workspaceEntry && workspaces.filter(item => item.title === workspaceEntry.title).length > 1
    const runChat = row.workflowSessionKind === 'run'
    const displayTime = scheduled || runChat ? row.runTime || row.createdTime || 0 : row.group === 'Recent' ? row.time : 0
    const date =
      displayTime > 0
        ? new Date(displayTime).toLocaleString(undefined, {
            month: 'short',
            day: 'numeric',
            hour: '2-digit',
            minute: '2-digit',
          })
        : ''
    const trigger = workflowTriggerLabel({ sessionId: row.id, triggeredBy: row.activity?.triggered_by })
    const origin = row.originLabel || (trigger === 'Scheduled' ? 'Schedule' : trigger) || ''
    const SourceIcon = origin === 'Slack' ? Slack : origin === 'WhatsApp' ? MessageCircle : trigger === 'Webhook' ? Webhook : scheduled ? CalendarClock : MessageSquare
    const sourceLabel = origin || (scheduled ? 'Schedule' : runChat ? 'Run' : 'Chat')
    // A schedule shows only in the hover label (owner, 2026-10-29); channels like Slack keep their icon.
    const hasSourceIcon = !!workspaceEntry || (!!origin && origin !== 'Schedule')
    // Repeat times only when they distinguish otherwise identical runs. Exact
    // timestamps and origins remain in the hover/accessibility labels.
    const repeatedRun = (scheduled || runChat) && rows.some(other => other.id !== row.id && (other.scheduled || other.workflowSessionKind === 'run') &&
      other.surface === row.surface && other.workflowId === row.workflowId &&
      other.projectId === row.projectId && other.workspace === row.workspace &&
      sidebarChatDisplayTitle(other, workspaceTitle) === title)
    const details = [
      duplicateWorkspaceName ? PRODUCT_SURFACE_LABELS[row.surface] : '',
      row.waiting ? 'Needs you' : '',
      repeatedRun ? date : '',
    ]
      .filter(Boolean)
      .join(' · ')
    const canClose = !!tab && canCloseWorkspaceChatTab(tab, tabs)
    // Someone else's chat in a shared project shows their first name.
    const startedBy = row.history?.user_id && user?.id && row.history.user_id !== user.id ? row.history.username?.trim().split(/[\s@]/)[0] : ''
    // One fixed slot at the row's right edge for every row, so spinners and dots line up; on hover-capable
    // devices the close x takes the same slot (phones never show it).
    const status = opening === row.id || (row.group === 'Running' && !row.waiting) ? (
      <Loader2 aria-label="Running" className="h-3 w-3 shrink-0 animate-spin text-[hsl(var(--info))]" />
    ) : row.waiting ? (
      <span
        aria-label="Needs input"
        className="h-1.5 w-1.5 shrink-0 rounded-full bg-[hsl(var(--warning))] motion-safe:animate-pulse"
      />
    ) : unseenFailed ? (
      <span aria-label="Failed" title="Failed" className="h-1.5 w-1.5 shrink-0 rounded-full bg-[hsl(var(--destructive))]" />
    ) : tab?.hasUnreadCompletion && !selected ? (
      <span aria-label="Completed" title="Completed" className="h-1.5 w-1.5 shrink-0 rounded-full bg-[hsl(var(--success))]" />
    ) : null
    return (
      <div key={row.id}>
      <div className="group relative">
        <button
          type="button"
          title={`${isGeneratedSidebarPrompt(row.title) ? title : row.title} · ${PRODUCT_SURFACE_LABELS[row.surface]}${date ? ` · ${date}` : ''}`}
          aria-label={`${title} · ${PRODUCT_SURFACE_LABELS[row.surface]}${sourceLabel !== 'Chat' ? ` · ${sourceLabel}` : ''}${date ? ` · ${date}` : ''}${row.waiting ? ' · Needs you' : row.group === 'Running' ? ' · Running' : ''}`}
          aria-current={selected ? 'page' : undefined}
          onClick={() => {
            void open(row).catch(() =>
              useChatStore.getState().addToast('Could not open this chat.', 'error')
            )
          }}
          className={`mb-0.5 flex min-h-9 w-full items-center gap-2 rounded-md px-2.5 py-2 text-left transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring pr-9 ${selected ? 'bg-foreground/[0.08] text-foreground shadow-[inset_2px_0_0_hsl(var(--primary))]' : 'text-foreground/75 [@media(hover:hover)]:hover:bg-muted [@media(hover:hover)]:hover:text-foreground'}`}
        >
          {workspaceEntry && <span className="shrink-0" title={sourceLabel} aria-hidden="true">
            <ProjectIcon icon={workspaceEntry.icon} title={workspaceEntry.title} surface={row.surface} />
          </span>}
          <span className="min-w-0 flex-1">
            <span className="flex items-center gap-1.5">
              <span className="truncate text-[13px] leading-5">{title}</span>
              {startedBy && <span className="shrink-0 rounded-full border border-border px-1.5 text-[10px] leading-4 text-muted-foreground">{startedBy}</span>}
            </span>
            {(details || (hasSourceIcon && !workspaceEntry)) && (
              <span
                className={`mt-1 flex items-center gap-1 truncate text-[10px] leading-3 ${row.waiting ? 'text-[hsl(var(--warning))]' : 'text-muted-foreground'}`}
              >
                {hasSourceIcon && !workspaceEntry && <span className="shrink-0" title={sourceLabel} aria-hidden="true"><SourceIcon className="h-3 w-3" /></span>}
                <span className="truncate">{details}</span>
              </span>
            )}
          </span>
        </button>
        {status && (
          <span
            className={`pointer-events-none absolute right-1 top-1/2 flex h-6 w-6 -translate-y-1/2 items-center justify-center ${canClose ? '[@media(hover:hover)]:group-hover:invisible [@media(hover:hover)]:group-focus-within:invisible' : ''}`}
          >
            {status}
          </span>
        )}
        {canClose && (
          <button
            type="button"
            aria-label={`Close ${title}`}
            disabled={closing === row.id}
            title={tab?.metadata?.isViewOnly ? 'Close tab · the run continues' : 'Close chat'}
            className="absolute right-1 top-1/2 h-6 w-6 -translate-y-1/2 items-center justify-center rounded text-muted-foreground hidden hover:bg-background [@media(hover:hover)]:flex hover:text-foreground [@media(hover:hover)]:opacity-0 [@media(hover:hover)]:group-hover:opacity-100 [@media(hover:hover)]:group-focus-within:opacity-100 focus:opacity-100 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
            onClick={() => {
              if (!row.tabId) return
              setClosing(row.id)
              void closeWorkspaceChatTab(row.tabId)
                .catch(() =>
                  useChatStore.getState().addToast('Could not close this chat. Try again.', 'error')
                )
                .finally(() => setClosing(null))
            }}
          >
            <X className="h-3 w-3" aria-hidden="true" />
          </button>
        )}
      </div>
      </div>
    )
  }
  const isResourceWorkspace = (surface: string) => surface === 'knowledgebase' || surface === 'mcp-gateway'
  const renderWorkspace = (workspace: (typeof workspaces)[number], index: number) => {
    if (workspace.surface === 'work' && workspace.chats.length === 0) return <button
      key={workspace.id}
      type="button"
      title={`Crew · ${workspace.title}`}
      onClick={() => {
        useProductSurfaceStore.getState().setSelectedWorkProjectId(workspace.id.slice('work:'.length))
        window.dispatchEvent(new Event('close-app-sidebar'))
        openProductWorkspace('work')
      }}
      className="mb-2 flex min-h-9 w-full items-center gap-2.5 rounded-lg px-2.5 py-2 text-left text-[13px] hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
    ><ProjectIcon {...workspace} /><span className="min-w-0 flex-1 truncate font-medium">{workspace.title}</span></button>
    const hidden = !expanded[sidebarExpansionKey(scope, 'project', workspace.id)]
    const running = workspace.chats.filter(
      (row) => row.group === 'Running' && !row.waiting
    ).length
    const waiting = workspace.chats.filter((row) => row.waiting).length
    const unnamedChats = workspace.chats
      .filter((row) => !row.workflowId && (row.title === workspace.title || /^(?:new project|new crew|new chat)$/i.test(row.title.trim())) && !row.scheduled)
      .sort((a, b) => Number(b.chatOrder?.isMainChat) - Number(a.chatOrder?.isMainChat) || a.id.localeCompare(b.id))
      .map((row) => row.id)
    const panelId = `sidebar-workspace-${index}`
    const only = workspace.chats.length === 1 ? workspace.chats[0] : null
    const onlyTab = only?.tabId ? tabs[only.tabId] : undefined
    const primaryOnly = only && !only.scheduled && only.workflowSessionKind !== 'run' && (!onlyTab || !canCloseWorkspaceChatTab(onlyTab, tabs)) && (only.builder || only.title === workspace.title)
    if (primaryOnly) return <div key={workspace.id}>{renderChat(only, undefined, undefined, workspace)}</div>
    return (
      <section key={workspace.id} className="mb-2">
        <button
          type="button"
          aria-expanded={!hidden}
          aria-controls={panelId}
          title={`${PRODUCT_SURFACE_LABELS[workspace.surface]} · ${workspace.title} · ${workspace.chats.length} ${workspace.chats.length === 1 ? 'chat' : 'chats'}${running ? ` · ${running} running` : ''}${waiting ? ` · ${waiting} need input` : ''}`}
          onClick={() => toggleSection(workspace.id)}
          className="group mb-0.5 flex min-h-9 w-full items-center gap-2.5 rounded-lg px-2.5 py-2 text-left text-[13px] hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
        >
          <ProjectIcon {...workspace} />
          <span className="min-w-0 flex-1 truncate font-medium">{workspace.title}</span>
          {workspaces.filter(item => item.title === workspace.title).length > 1 && <span className="text-[10px] text-muted-foreground">{PRODUCT_SURFACE_LABELS[workspace.surface]}</span>}
          {hidden && (running > 0 || waiting > 0) && <span
            className={`shrink-0 text-[10px] tabular-nums ${waiting ? 'text-[hsl(var(--warning))]' : running ? 'text-[hsl(var(--info))]' : 'text-muted-foreground/60'}`}
          >
            {[running ? `${running} working` : '', waiting ? `${waiting} needs you` : '']
              .filter(Boolean)
              .join(' · ')}
          </span>}
          <ChevronDown
            className={`h-3 w-3 shrink-0 text-muted-foreground/60 transition-transform ${hidden ? '-rotate-90' : ''}`}
            aria-hidden="true"
          />
        </button>
        <div id={panelId} hidden={hidden} className="ml-4 pl-1">
          {workspace.chats.map((row) =>
            renderChat(
              row,
              workspace.title,
              unnamedChats.length > 1 && unnamedChats.includes(row.id)
                ? unnamedChats.indexOf(row.id) + 1
                : undefined,
            )
          )}
        </div>
      </section>
    )
  }
  return (
    <aside
      aria-label="Chats across products"
      className="flex min-h-0 w-full shrink-0 flex-col border-r border-border/70 bg-muted/20"
    >
      <div className="flex h-[50px] shrink-0 items-center gap-2 px-3">
        <a href="/home" onClick={followAppLink} aria-label="Home" title="Home" className="min-w-0 flex-1 rounded-md focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
          <RuntimeBrandLogo variant="sidebar-header" />
        </a>
        <div className="flex shrink-0 items-center gap-1">
        {toggle}
        </div>
      </div>
      <div className="shrink-0 space-y-0.5 px-2 pb-1">
        <NewProjectMenu />
        <button
          type="button"
          aria-label="Search all chats (Ctrl+K or Command+K)"
          title="Search all chats · ⌘K / Ctrl+K"
          onClick={() => window.dispatchEvent(new CustomEvent('open-quick-switcher'))}
          className="flex h-9 w-full items-center gap-2.5 rounded-lg px-2.5 text-left text-[13px] text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
        >
          <Search className="h-3.5 w-3.5" />
          <span className="flex-1">Search chats</span>
          <kbd className="text-[10px] text-muted-foreground">{navigator.platform.includes('Mac') ? '⌘K' : 'Ctrl K'}</kbd>
        </button>
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto px-2 pb-3 pt-2">
        {/* One list of projects, most recent first; each project's icon says Crew, Goal or Relay (owner, 2026-10-29). */}
        {workspaces.map((workspace, index) => PROJECT_SIDEBAR_ORDER.some(surface => surface === workspace.surface) || !isResourceWorkspace(workspace.surface) ? renderWorkspace(workspace, index) : null)}
        {directory?.scope === scope && !directory.authoritative && (
          <button className="px-2 py-3 text-left text-xs text-muted-foreground underline" onClick={() => setDirectoryTick(value => value + 1)}>
            Some projects could not load. Try again.
          </button>
        )}
        {history?.error && (
          <button
            className="px-2 py-3 text-xs text-muted-foreground underline"
            onClick={() => setTick((value) => value + 1)}
          >
            Some chats could not load · Retry
          </button>
        )}
        {history && !history.error && !workspaces.length && directory?.scope === scope && directory.authoritative && (
          <p className="px-2 py-3 text-xs text-muted-foreground">No chats yet.</p>
        )}
      </div>
      <SharedToolShortcuts />
      {footer && <div className="shrink-0 border-t border-border/60 bg-background/60 p-2">{footer}</div>}
    </aside>
  )
}
