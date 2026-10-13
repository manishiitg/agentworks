import './PreviousChatHistoryPanel.css'
import React, { useCallback, useEffect, useMemo, useState } from 'react'
import { ArrowUpRight, Bot, CalendarClock, Code2, Copy, Loader2, MessageSquare, MoreHorizontal, Paperclip, Pencil, Trash2, Webhook, type LucideIcon } from 'lucide-react'
import { agentApi } from '../services/api'
import { schedulerApi } from '../api/scheduler'
import { productWebhooksApi, type ProductTriggerScope } from '../api/productWebhooks'
import {
  type ChatHistorySession,
  type ScheduledJob,
  type ScheduledJobRun,
} from '../services/api-types'
import { useChatStore } from '../stores/useChatStore'
import { workflowTriggerLabel } from '../utils/workflowSessionKinds'
import { isScheduledChatHistorySession } from '../utils/chatHistoryOpenDisposition'
import { chatHistoryWorkshopMode } from '../utils/chatHistoryWorkshopMode'
import { chatHistoryRuntimeLabel, chatHistoryRuntimeShortLabel } from '../utils/chatHistoryRuntimeLabel'
import { chatHistorySessionTitle } from '../utils/chatHistoryTitle'
import { type ScheduleActivityItem } from '../utils/scheduleRunPresentation'
import { ScheduleRunCard } from './ScheduleRunCard'
import { copyToClipboard } from '../utils/textUtils'
import ConfirmationDialog from './ui/ConfirmationDialog'
import {
  CHAT_HISTORY_CLEANUP_AGE_OPTIONS,
  type ChatHistoryCleanupAgeDays,
  CleanupOldChatsDropdown,
} from './CleanupOldChatsDropdown'

const PAGE_SIZE = 5
// Keep the first paint cheap. Workflow builder conversations can be large, and
// the backend has to parse each returned file to build previews. The panel only
// renders five rows at a time, so loading 100 upfront makes the spinner feel
// stuck on workflows with months of schedule/pulse history.
const FETCH_LIMIT = 25

type PreviousChatKind = 'chat' | 'schedule' | 'bot' | 'webhook'
type PreviousChatFilter = PreviousChatKind
type EmptyStateIcon = LucideIcon

const emptyStateContent: Record<PreviousChatFilter, {
  icon: EmptyStateIcon
  title: string
  body: string
}> = {
  chat: {
    icon: MessageSquare,
    title: 'No conversation history yet',
    body: 'Your main Chat is the continuing conversation. Earlier saved conversations will appear here for reference.',
  },
  schedule: {
    icon: CalendarClock,
    title: 'No scheduled chats yet',
    body: 'Use the Schedules control in the top bar to create a recurring task. After a run starts, the latest scheduled chat will appear here.',
  },
  webhook: {
    icon: Webhook,
    title: 'No trigger runs yet',
    body: 'Ask the workflow builder chat to create a trigger. Its executions will appear here when an external service calls it.',
  },
  bot: {
    icon: Bot,
    title: 'No bot chats yet',
    body: 'Use the Bot connector button in the top bar to connect and configure a bot. Sessions started or resumed from that bot will appear here.',
  },
}

const firstRunHints: Array<{
  icon: EmptyStateIcon
  label: string
  body: string
}> = [
  {
    icon: MessageSquare,
    label: 'Chat',
    body: 'Use the main Chat for the continuing conversation. This view keeps its earlier history.',
  },
  {
    icon: CalendarClock,
    label: 'Schedules',
    body: 'Run recurring work on a schedule and review the latest run.',
  },
  {
    icon: Bot,
    label: 'Bots',
    body: 'Use the Bot connector button to configure the external bot.',
  },
]

export function chatHistoryConversationPath(session: ChatHistorySession): string {
  if (session.conversation_path) return session.conversation_path
  const userId = session.user_id || 'default'
  return `_users/${userId}/chat_history/${session.session_id}/conversation.json`
}

function ChatHistoryRuntimeBadge({ session }: { session: ChatHistorySession }) {
  const fullLabel = chatHistoryRuntimeLabel(session)
  const shortLabel = chatHistoryRuntimeShortLabel(session)
  if (!fullLabel || !shortLabel) return null

  return (
    <span
      className="inline-flex min-w-0 items-center gap-0.5 text-[9px] text-muted-foreground/70"
      title={fullLabel}
      aria-label={`Coding agent and model: ${fullLabel}`}
    >
      <Code2 className="h-2.5 w-2.5 shrink-0" />
      <span className="truncate whitespace-nowrap">{shortLabel}</span>
    </span>
  )
}

export { chatHistoryRuntimeLabel, chatHistoryRuntimeShortLabel } from '../utils/chatHistoryRuntimeLabel'

function chatHistoryRuntimeTransport(session: ChatHistorySession): string {
  const runtime = session.runtime
  const transport = runtime?.transport?.trim().toLowerCase()
  if (transport) return transport
  return runtime?.agent_session_handle?.provider?.transport?.trim().toLowerCase() || ''
}

export function chatHistorySupportsNativeResume(session: ChatHistorySession): boolean {
  const runtime = session.runtime
  if (!runtime || runtime.kind !== 'coding_agent') return false
  if (runtime.resume_supported === false) return false
  const handle = runtime.agent_session_handle?.provider
  return Boolean(
    runtime.resume_supported ||
    runtime.external_session_id?.trim() ||
    runtime.project_dir_id?.trim() ||
    handle?.native_session_id?.trim() ||
    handle?.project_dir_id?.trim()
  )
}

export function chatHistoryUsesTerminalRestore(session: ChatHistorySession): boolean {
  const runtime = session.runtime
  if (!runtime || runtime.kind !== 'coding_agent') return false
  return chatHistoryRuntimeTransport(session) === 'tmux'
}

export function chatHistoryWorkshopModeLabel(session: ChatHistorySession): string | undefined {
  const raw = (session.runtime?.workshop_mode || session.workshop_mode || '').trim().toLowerCase()
  if (!raw) return undefined
  return chatHistoryWorkshopMode(session) === 'run' ? 'Run' : 'Workshop'
}

const formatChatTime = (value?: string): string => {
  if (!value) return 'Unknown time'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return 'Unknown time'
  return date.toLocaleString([], {
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  })
}

const formatMessageCount = (count?: number): string | undefined => {
  if (typeof count !== 'number') return undefined
  const formatted = new Intl.NumberFormat().format(count)
  return `${formatted} ${count === 1 ? 'message' : 'messages'}`
}

const botPlatformFromSessionID = (sessionId: string): string => {
  const match = sessionId.match(/^bot-([^-]+)--/)
  return match?.[1] || ''
}

const formatBotPlatform = (platform?: string): string => {
  const normalized = (platform || '').trim().toLowerCase()
  if (!normalized) return 'Bot'
  if (normalized === 'slack') return 'Slack'
  if (normalized === 'whatsapp') return 'WhatsApp'
  return normalized.charAt(0).toUpperCase() + normalized.slice(1)
}

const botActorFromEmail = (email?: string): string => {
  const value = (email || '').trim()
  const atIndex = value.indexOf('@')
  if (atIndex > 0) return value.slice(0, atIndex)
  return value
}

const botActorFromUserID = (userID?: string): string => {
  const value = (userID || '').trim()
  if (!value) return ''
  if (value.includes('@')) return botActorFromEmail(value)
  const normalized = value.toLowerCase()
  for (const suffix of ['-gmail-com', '-googlemail-com', '-outlook-com', '-hotmail-com', '-yahoo-com']) {
    if (normalized.endsWith(suffix) && value.length > suffix.length) {
      return value.slice(0, -suffix.length)
    }
  }
  return value
}

const botSessionSourceLabel = (session: ChatHistorySession): string | undefined => {
  if (getChatKind(session) !== 'bot') return undefined
  const platform = formatBotPlatform(session.bot_platform || botPlatformFromSessionID(session.session_id))
  const actor = (
    botActorFromEmail(session.bot_user_email) ||
    (session.bot_user_name || '').trim() ||
    botActorFromUserID(session.bot_user_id) ||
    botActorFromUserID(session.user_id) ||
    (session.username || '').trim()
  )
  return actor ? `${platform} · ${actor}` : platform
}

const sameWorkspace = (left?: string, right?: string): boolean => {
  const normalize = (value?: string) => (value || '').trim().replace(/\\/g, '/').replace(/^\/+|\/+$/g, '')
  return Boolean(normalize(left) && normalize(left) === normalize(right))
}

const sessionHasMessages = (session: ChatHistorySession): boolean => {
  return (session.message_count ?? 0) > 0 || (session.preview_messages?.length ?? 0) > 0 || !!session.query?.trim()
}

const isSessionOlderThanDays = (session: ChatHistorySession, days: number): boolean => {
  const timestamp = Date.parse(session.updated_at || session.created_at || '')
  if (Number.isNaN(timestamp)) return false
  return timestamp < Date.now() - days * 24 * 60 * 60 * 1000
}

// Mirrors the backend's terminal-status gate: cleanup only ever counts runs
// the server would actually delete.
const isTerminalScheduledRun = (run: ScheduledJobRun): boolean => {
  const status = (run.status || '').toLowerCase()
  return status !== '' && status !== 'running' && status !== 'queued' &&
    status !== 'waiting_for_capacity' && status !== 'waiting_for_workflow'
}

const isRunOlderThanDays = (run: ScheduledJobRun, days: number): boolean => {
  const timestamp = Date.parse(run.started_at || '')
  if (Number.isNaN(timestamp)) return false
  return timestamp < Date.now() - days * 24 * 60 * 60 * 1000
}

const getChatKind = (session: ChatHistorySession): PreviousChatKind => {
  if (workflowTriggerLabel({ sessionId: session.session_id }) === 'Webhook') return 'webhook'
  if (isScheduledChatHistorySession(session)) return 'schedule'
  if (session.session_id.startsWith('bot-')) return 'bot'
  return 'chat'
}

const mergeSessions = (current: ChatHistorySession[], next: ChatHistorySession[]): ChatHistorySession[] => {
  const byId = new Map<string, ChatHistorySession>()
  for (const session of [...current, ...next]) {
    byId.set(session.session_id, session)
  }
  return Array.from(byId.values()).sort((a, b) =>
    Date.parse(b.updated_at || b.created_at || '') - Date.parse(a.updated_at || a.created_at || '')
  )
}

const ChatRowActionsMenu: React.FC<{
  sessionId: string
  canRename: boolean
  canDelete: boolean
  isDeleting: boolean
  open: boolean
  onToggle: () => void
  onClose: () => void
  onRename: () => void
  onDelete: () => void
}> = ({ sessionId, canRename, canDelete, isDeleting, open, onToggle, onClose, onRename, onDelete }) => {
  const addToast = useChatStore(state => state.addToast)

  const handleCopy = async () => {
    const copied = await copyToClipboard(sessionId)
    onClose()
    addToast(copied ? 'Copied session ID' : 'Copy failed', copied ? 'success' : 'error')
  }

  return (
    <div className="relative">
      <button
        type="button"
        onClick={(event) => { event.stopPropagation(); onToggle() }}
        aria-haspopup="menu"
        aria-expanded={open}
        aria-label="Chat actions"
        title="Chat actions"
        className="inline-flex items-center rounded border border-border bg-background p-1 text-muted-foreground opacity-70 transition-colors hover:text-foreground group-hover:opacity-100"
      >
        <MoreHorizontal className="h-3.5 w-3.5" />
      </button>
      {open && (
        <>
          <button
            type="button"
            aria-label="Close chat actions"
            className="fixed inset-0 z-30 cursor-default bg-transparent"
            onClick={onClose}
          />
          <div
            role="menu"
            className="absolute right-0 top-full z-40 mt-1 w-44 overflow-hidden rounded-md border border-border bg-popover py-1 text-popover-foreground shadow-lg"
          >
            {canRename && (
              <button
                type="button"
                role="menuitem"
                onClick={() => { onClose(); onRename() }}
                className="flex w-full items-center gap-2 px-2 py-1.5 text-left text-xs hover:bg-muted"
              >
                <Pencil className="h-3.5 w-3.5" /> Rename chat
              </button>
            )}
            <button
              type="button"
              role="menuitem"
              onClick={() => void handleCopy()}
              className="flex w-full items-center gap-2 px-2 py-1.5 text-left text-xs hover:bg-muted"
            >
              <Copy className="h-3.5 w-3.5" /> Copy session ID
            </button>
            {canDelete && (
              <button
                type="button"
                role="menuitem"
                disabled={isDeleting}
                onClick={() => { onClose(); onDelete() }}
                className="flex w-full items-center gap-2 px-2 py-1.5 text-left text-xs text-destructive hover:bg-destructive/10 disabled:cursor-not-allowed disabled:opacity-50"
              >
                {isDeleting ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Trash2 className="h-3.5 w-3.5" />} Delete chat
              </button>
            )}
          </div>
        </>
      )}
    </div>
  )
}

const PreviousChatEmptyState: React.FC<{
  filter: PreviousChatFilter
  hasAnySessions: boolean
  hasCurrentSession?: boolean
  fallbackText: string
  recentOnly?: boolean
}> = ({ filter, hasAnySessions, hasCurrentSession = false, fallbackText, recentOnly = false }) => {
  const content = hasCurrentSession && filter === 'chat'
    ? {
        ...emptyStateContent.chat,
        title: 'Current chat is open',
        body: 'Continue in the main Chat. Earlier saved conversations will appear here for reference.',
      }
    : hasAnySessions
    ? emptyStateContent[filter]
    : { ...emptyStateContent[filter], body: emptyStateContent[filter].body || fallbackText }
  const Icon = content.icon

  return (
    <div className="px-3 py-4">
      <div className="rounded-md border border-dashed border-border bg-muted/15 px-3 py-4">
        <div className="flex items-start gap-3">
          <div className="flex h-8 w-8 shrink-0 items-center justify-center rounded-md border border-border bg-background text-muted-foreground">
            <Icon className="h-4 w-4" />
          </div>
          <div className="min-w-0 flex-1">
            <div className="text-sm font-medium text-foreground">{content.title}</div>
            <p className="mt-1 max-w-xl text-xs leading-5 text-muted-foreground">{content.body || fallbackText}</p>

            {!recentOnly && !hasAnySessions && !hasCurrentSession && (
              <div className={`mt-3 grid gap-x-4 gap-y-2 border-t border-border/70 pt-3 ${recentOnly ? 'grid-cols-1' : 'sm:grid-cols-3'}`}>
                {firstRunHints
                  .filter(({ label }) => !recentOnly || label === 'Chat')
                  .map(({ icon: HintIcon, label, body }) => (
                  <div key={label} className="min-w-0">
                    <div className="flex items-center gap-1.5 text-xs font-medium text-foreground">
                      <HintIcon className="h-3.5 w-3.5 text-muted-foreground" />
                      <span>{label}</span>
                    </div>
                    <p className="mt-1 text-[11px] leading-4 text-muted-foreground">{body}</p>
                  </div>
                ))}
              </div>
            )}
          </div>
        </div>
      </div>
    </div>
  )
}

interface PreviousChatHistoryPanelProps {
  workspacePath?: string
  activeSessionId?: string
  title?: string
  emptyText?: string
  actionLabel?: string
  onHasChatsChange?: (hasChats: boolean, isLoaded?: boolean) => void
  onSelectSession: (session: ChatHistorySession) => void | Promise<void>
  /** Dense layout for the narrow ~360px chat rail: icon-only filters + actions,
   *  single tight meta line, no runtime/workshop chips or message preview. */
  compact?: boolean
  /** Fill the available chat surface for landing dashboards. */
  fill?: boolean
  /** Render every history row already fetched instead of paginating the UI. */
  showAll?: boolean
  /** Keep the shared history UI while hiding automation-only filters. */
  recentOnly?: boolean
  /** Fetch automation-created conversations and offer the origin filters. */
  includeAutomationChats?: boolean
  /** History browser only: no rename, delete, or cleanup actions. */
  readOnly?: boolean
  /** Keep history management read-only while still allowing a conversation to
   *  open in its own tab. */
  allowOpen?: boolean
  /** Use the row as the open action instead of a separate Open button. */
  openOnRowClick?: boolean
  /** Show one automation run feed without the general history filters. */
  runOnly?: 'schedule' | 'webhook'
  /** Scheduler ownership for the run feed. */
  runEntityType?: 'workflow' | 'product'
  /** Crew trigger scope used to identify which automation created a chat. */
  productTriggerScope?: ProductTriggerScope
  /** External refresh signal: the Automation hub bumps this from its header. */
  refreshToken?: number
}

export const PreviousChatHistoryPanel: React.FC<PreviousChatHistoryPanelProps> = ({
  workspacePath,
  activeSessionId,
  title = 'Previous chats',
  emptyText = 'No previous chats yet.',
  actionLabel = 'Open',
  onHasChatsChange,
  onSelectSession,
  compact = false,
  fill = false,
  showAll = false,
  recentOnly = false,
  includeAutomationChats = false,
  readOnly = false,
  allowOpen = false,
  openOnRowClick = false,
  runOnly,
  runEntityType = 'workflow',
  productTriggerScope,
  refreshToken = 0,
}) => {
  const [sessions, setSessions] = useState<ChatHistorySession[]>([])
  const [isLoading, setIsLoading] = useState(true)
  const [isCleanupLoading, setIsCleanupLoading] = useState(false)
  const [deletingSessionIds, setDeletingSessionIds] = useState<Set<string>>(() => new Set())
  const [activeFilter, setActiveFilter] = useState<PreviousChatFilter>(runOnly || 'chat')
  const [selectedBotChannel, setSelectedBotChannel] = useState('all')
  const [visibleCount, setVisibleCount] = useState(PAGE_SIZE)
  const [scheduleJobs, setScheduleJobs] = useState<ScheduledJob[]>([])
  // Both trigger feeds use recorded runs, ordered by recency.
  const [scheduleRunsByJob, setScheduleRunsByJob] = useState<Record<string, ScheduledJobRun[]>>({})
  const [isLoadingScheduleActivity, setIsLoadingScheduleActivity] = useState(false)
  const [automationSourceBySession, setAutomationSourceBySession] = useState<Record<string, { kind: 'schedule' | 'trigger'; label: string }>>({})
  const [openRowMenuSessionId, setOpenRowMenuSessionId] = useState<string | null>(null)
  const [runsRefreshToken, setRunsRefreshToken] = useState(0)
  const [isCleanupRunsLoading, setIsCleanupRunsLoading] = useState(false)
  const [pendingRunCleanup, setPendingRunCleanup] = useState<{ days: ChatHistoryCleanupAgeDays; count: number } | null>(null)
  const [scheduleJobsWorkspacePath, setScheduleJobsWorkspacePath] = useState('')
  const addToast = useChatStore(state => state.addToast)

  useEffect(() => {
    let cancelled = false
    setSessions([])
    setScheduleJobs([])
    setScheduleRunsByJob({})
    setScheduleJobsWorkspacePath('')
    setActiveFilter(runOnly || 'chat')
    setSelectedBotChannel('all')
    setVisibleCount(PAGE_SIZE)
    setIsLoading(true)

    // Fetch ordinary chats separately. A schedule-heavy workflow can have many
    // newer schedule transcripts, so a mixed newest-N page would otherwise
    // hide every older chat before the Recent filter gets a chance to run.
    const sessionsRequest = runOnly
      ? Promise.resolve([] as ChatHistorySession[])
      : recentOnly && !includeAutomationChats
      ? agentApi.listChatHistorySessions(FETCH_LIMIT, 0, workspacePath, 'chat')
          .then(chatResponse => chatResponse.sessions || [])
      : Promise.all([
          agentApi.listChatHistorySessions(FETCH_LIMIT, 0, workspacePath),
          agentApi.listChatHistorySessions(FETCH_LIMIT, 0, workspacePath, 'chat'),
        ]).then(([allResponse, chatResponse]) => mergeSessions(allResponse.sessions || [], chatResponse.sessions || []))

    sessionsRequest
      .then(nextSessions => {
        if (cancelled) return
        setSessions(nextSessions)
      })
      .catch(() => {
        if (cancelled) return
        setSessions([])
        addToast('Failed to load previous chats', 'error')
      })
      .finally(() => {
        if (!cancelled) setIsLoading(false)
      })

    return () => { cancelled = true }
  }, [addToast, includeAutomationChats, recentOnly, runOnly, workspacePath, refreshToken])

  const showRunActivity = activeFilter === 'schedule' || activeFilter === 'webhook'
  useEffect(() => {
    if (recentOnly && !includeAutomationChats) {
      setScheduleJobs([])
      setScheduleRunsByJob({})
      setScheduleJobsWorkspacePath('')
      return
    }
    if (!workspacePath) {
      setScheduleJobs([])
      setScheduleRunsByJob({})
      setScheduleJobsWorkspacePath('')
      return
    }
    let cancelled = false
    let inFlight = false
    const refresh = async (initial = false) => {
      if (inFlight) return
      inFlight = true
      if (initial) setIsLoadingScheduleActivity(true)
      try {
        const response = await schedulerApi.listJobs({ entity_type: runEntityType, limit: 100 })
        const jobs = (response.jobs || []).filter(job => sameWorkspace(job.workspace_path, workspacePath))
        const results = await Promise.allSettled(jobs.map(job => schedulerApi.getJobRuns(job.id, 30)))
        if (cancelled) return
        setScheduleJobs(jobs)
        setScheduleRunsByJob(previous => Object.fromEntries(jobs.map((job, index) => {
          const result = results[index]
          return [job.id, result.status === 'fulfilled' ? result.value.runs || [] : previous[job.id] || []]
        })))
        if (results.some(result => result.status === 'rejected') && initial) addToast('Some run histories could not be loaded', 'error')
        setScheduleJobsWorkspacePath(workspacePath)
      } catch {
        if (!cancelled && initial) addToast('Failed to load run activity', 'error')
      } finally {
        inFlight = false
        if (!cancelled) {
          setIsLoadingScheduleActivity(false)
        }
      }
    }
    void refresh(true)
    const timer = showRunActivity ? window.setInterval(() => { if (!document.hidden) void refresh() }, 10000) : undefined
    return () => { cancelled = true; if (timer !== undefined) window.clearInterval(timer) }
  }, [addToast, includeAutomationChats, recentOnly, runEntityType, workspacePath, showRunActivity, refreshToken, runsRefreshToken])

  // Crew automation sessions are ordinary product conversations. Join the
  // durable run indexes by session id so the chat list can name its source.
  useEffect(() => {
    if (!recentOnly || !workspacePath || !productTriggerScope) {
      setAutomationSourceBySession({})
      return
    }
    let cancelled = false
    void (async () => {
      try {
        const [jobsResponse, triggersResponse] = await Promise.all([
          schedulerApi.listJobs({ entity_type: 'product', limit: 100 }),
          productWebhooksApi.list(productTriggerScope),
        ])
        const jobs = (jobsResponse.jobs || []).filter(job => sameWorkspace(job.workspace_path, workspacePath))
        const triggers = triggersResponse.triggers || []
        const [jobRuns, triggerRuns] = await Promise.all([
          Promise.all(jobs.map(async job => ({ job, runs: (await schedulerApi.getJobRuns(job.id, 30)).runs || [] }))),
          Promise.all(triggers.map(async trigger => ({ trigger, runs: (await productWebhooksApi.runs(productTriggerScope, trigger.id, 30)).runs || [] }))),
        ])
        if (cancelled) return
        const sources: Record<string, { kind: 'schedule' | 'trigger'; label: string }> = {}
        for (const { job, runs } of jobRuns) {
          for (const run of runs) if (run.session_id) sources[run.session_id] = { kind: 'schedule', label: job.name }
        }
        for (const { trigger, runs } of triggerRuns) {
          for (const run of runs) if (run.session_id) sources[run.session_id] = { kind: 'trigger', label: trigger.name }
        }
        setAutomationSourceBySession(sources)
      } catch {
        if (!cancelled) setAutomationSourceBySession({})
      }
    })()
    return () => { cancelled = true }
  }, [productTriggerScope, recentOnly, workspacePath, refreshToken])

  const visibleSessions = useMemo(
    () => sessions.filter(session => session.session_id !== activeSessionId),
    [activeSessionId, sessions]
  )
  const hasCurrentSession = useMemo(
    // The active persistent chat is commonly omitted from the history index
    // until its next durable write. Its tab/session identity is authoritative;
    // requiring a matching history row made Workshop incorrectly claim that
    // there was no conversation while the conversation was open beside it.
    () => !!activeSessionId,
    [activeSessionId],
  )

  const botChannels = useMemo(() => {
    const channels = new Set<string>()
    for (const session of visibleSessions) {
      if (getChatKind(session) !== 'bot') continue
      const key = (session.bot_platform || botPlatformFromSessionID(session.session_id)).trim().toLowerCase()
      channels.add(key || 'bot')
    }
    return Array.from(channels).sort()
  }, [visibleSessions])

  const filterCounts = useMemo(() => {
    const counts: Record<PreviousChatFilter, number> = {
      chat: 0,
      schedule: 0,
      webhook: 0,
      bot: 0,
    }
    for (const session of visibleSessions) {
      counts[getChatKind(session)] += 1
    }
    return counts
  }, [visibleSessions])

  const filteredSessions = useMemo(() => {
    if (activeFilter === 'bot' && selectedBotChannel !== 'all') {
      return visibleSessions.filter(session =>
        getChatKind(session) === 'bot' &&
        ((session.bot_platform || botPlatformFromSessionID(session.session_id)).trim().toLowerCase() || 'bot') === selectedBotChannel
      )
    }
    return visibleSessions.filter(session => getChatKind(session) === activeFilter)
  }, [activeFilter, selectedBotChannel, visibleSessions])

  const oldVisibleSessionCounts = useMemo(
    () => CHAT_HISTORY_CLEANUP_AGE_OPTIONS.reduce((counts, days) => {
      counts[days] = visibleSessions.filter(session => sessionHasMessages(session) && isSessionOlderThanDays(session, days)).length
      return counts
    }, {} as Record<ChatHistoryCleanupAgeDays, number>),
    [visibleSessions]
  )
  const hasOldVisibleSessions = useMemo(
    () => CHAT_HISTORY_CLEANUP_AGE_OPTIONS.some(days => oldVisibleSessionCounts[days] > 0),
    [oldVisibleSessionCounts]
  )

  const displayedSessions = useMemo(
    () => showAll ? filteredSessions : filteredSessions.slice(0, visibleCount),
    [filteredSessions, showAll, visibleCount]
  )

  const scheduleDataMatchesWorkspace = sameWorkspace(scheduleJobsWorkspacePath, workspacePath)

  // A flat, cross-schedule feed sorted by actual run recency — not grouped by
  // job. Mirrors the Recent tab's own sort-by-recency-and-paginate shape.
  const flattenedScheduleRuns = useMemo(() => {
    const items: { job: ScheduledJob; run: ScheduledJobRun }[] = []
    for (const job of scheduleJobs) {
      for (const run of scheduleRunsByJob[job.id] || []) {
        items.push({ job, run })
      }
    }
    return items.sort((a, b) => Date.parse(b.run.started_at || '') - Date.parse(a.run.started_at || ''))
  }, [scheduleJobs, scheduleRunsByJob])

  const isRunFilter = activeFilter === 'schedule' || activeFilter === 'webhook'
  const isWebhookRun = ({ job, run }: { job: ScheduledJob; run: ScheduledJobRun }) =>
    job.schedule_type === 'webhook' || workflowTriggerLabel({ sessionId: run.session_id, triggeredBy: run.trigger_source }) === 'Webhook'
  const webhookRuns = flattenedScheduleRuns.filter(isWebhookRun)
  const scheduledRuns = flattenedScheduleRuns.filter(item => !isWebhookRun(item))
  const filteredRuns = activeFilter === 'webhook' ? webhookRuns : scheduledRuns
  const displayedScheduleRuns = showAll ? filteredRuns : filteredRuns.slice(0, visibleCount)
  const filteredJobs = scheduleJobs.filter(job => (job.schedule_type === 'webhook') === (activeFilter === 'webhook'))

  const oldRunCounts = CHAT_HISTORY_CLEANUP_AGE_OPTIONS.reduce((counts, days) => {
    counts[days] = filteredRuns.filter(({ run }) => isTerminalScheduledRun(run) && isRunOlderThanDays(run, days)).length
    return counts
  }, {} as Record<ChatHistoryCleanupAgeDays, number>)

  const displayFilterCounts = {
    ...filterCounts,
    schedule: scheduleDataMatchesWorkspace && !isLoadingScheduleActivity ? scheduledRuns.length : '…',
    webhook: scheduleDataMatchesWorkspace && !isLoadingScheduleActivity ? webhookRuns.length : '…',
  }

  const sessionsByID = useMemo(
    () => new Map(visibleSessions.map(session => [session.session_id, session])),
    [visibleSessions]
  )

  const totalForActiveFilter = isRunFilter ? filteredRuns.length : filteredSessions.length
  const displayedCountForActiveFilter = isRunFilter ? displayedScheduleRuns.length : displayedSessions.length

  useEffect(() => {
    setVisibleCount(PAGE_SIZE)
  }, [activeFilter])

  useEffect(() => {
    onHasChatsChange?.(!isLoading && visibleSessions.length > 0, !isLoading)
  }, [isLoading, onHasChatsChange, visibleSessions.length])

  const handleSelect = useCallback((session: ChatHistorySession) => {
    void onSelectSession(session)
  }, [onSelectSession])

  const handleDeleteSession = useCallback(async (session: ChatHistorySession) => {
    const title = chatHistorySessionTitle(session, 80)
    if (!window.confirm(`Delete this chat?\n\n${title}`)) return

    const sessionId = session.session_id
    setDeletingSessionIds(current => {
      const next = new Set(current)
      next.add(sessionId)
      return next
    })
    try {
      await agentApi.deleteChatHistorySession(sessionId, workspacePath)
      setSessions(current => current.filter(item => item.session_id !== sessionId))
      addToast('Deleted previous chat', 'success')
    } catch {
      addToast('Failed to delete previous chat', 'error')
    } finally {
      setDeletingSessionIds(current => {
        const next = new Set(current)
        next.delete(sessionId)
        return next
      })
    }
  }, [addToast, workspacePath])

  const handleRenameSession = useCallback(async (session: ChatHistorySession) => {
    const requested = window.prompt('Name this chat', chatHistorySessionTitle(session, 120))
    if (requested === null) return
    const title = requested.replace(/\s+/g, ' ').trim()
    if (!title) {
      addToast('Enter a name for this chat.', 'info')
      return
    }
    try {
      const response = await agentApi.renameChatHistorySession(session.session_id, title, workspacePath)
      setSessions(current => current.map(item => item.session_id === session.session_id
        ? { ...item, title: response.title }
        : item))
      const store = useChatStore.getState()
      const matchingTab = Object.values(store.chatTabs).find(tab => tab.sessionId === session.session_id)
      if (matchingTab) store.renameTab(matchingTab.tabId, response.title)
      addToast('Chat renamed', 'success')
    } catch {
      addToast('Failed to rename chat', 'error')
    }
  }, [addToast, workspacePath])

  const openScheduleActivity = useCallback((item: ScheduleActivityItem) => {
    const sessionID = item.run?.session_id
    const session: ChatHistorySession | undefined = sessionID ? sessionsByID.get(sessionID) || {
      session_id: sessionID,
      created_at: item.run!.started_at,
      updated_at: item.run!.completed_at || item.run!.started_at,
      query: item.job.name,
      agent_mode: 'workflow',
      workshop_mode: 'run',
    } : undefined
    if (!session) {
      addToast('This run has no restorable conversation record', 'info')
      return
    }
    void onSelectSession({ ...session, run_folder: item.run?.run_folder || session.run_folder })
  }, [addToast, onSelectSession, sessionsByID])


  const handleCleanupOldChats = useCallback(async (olderThanDays: ChatHistoryCleanupAgeDays) => {
    const scopeLabel = workspacePath || 'all chats'
    const oldSessionCount = oldVisibleSessionCounts[olderThanDays] || 0
    if (oldSessionCount === 0) {
      addToast(`No chats older than ${olderThanDays} days`, 'info')
      return
    }
    if (!window.confirm(`Delete ${oldSessionCount} chat${oldSessionCount === 1 ? '' : 's'} older than ${olderThanDays} days from ${scopeLabel}? This cannot be undone.`)) return

    setIsCleanupLoading(true)
    try {
      const response = await agentApi.cleanupChatHistorySessions(olderThanDays, workspacePath)
      const deletedCount = response.result?.deleted_count ?? 0
      const [allResponse, chatResponse] = await Promise.all([
        agentApi.listChatHistorySessions(FETCH_LIMIT, 0, workspacePath),
        agentApi.listChatHistorySessions(FETCH_LIMIT, 0, workspacePath, 'chat'),
      ])
      setSessions(mergeSessions(allResponse.sessions || [], chatResponse.sessions || []))
      addToast(
        deletedCount === 0
          ? `No chats older than ${olderThanDays} days`
          : `Deleted ${deletedCount} chat${deletedCount === 1 ? '' : 's'} older than ${olderThanDays} days`,
        'success'
      )
    } catch {
      addToast('Failed to delete old chats', 'error')
    } finally {
      setIsCleanupLoading(false)
    }
  }, [addToast, oldVisibleSessionCounts, workspacePath])

  // Run-record cleanup is workflow-only for now: product run history lives in
  // a runtime workspace this panel cannot address. The delivery-history feed
  // opts in via runOnly even though it renders readOnly: there are no session
  // rows there, so run cleanup is that feed's own management action.
  const showRunsCleanup = isRunFilter && runEntityType === 'workflow' && !!workspacePath && (!readOnly || runOnly)

  const handleSelectRunCleanup = (days: ChatHistoryCleanupAgeDays) => {
    const count = oldRunCounts[days] || 0
    if (count === 0) {
      addToast(`No runs older than ${days} days`, 'info')
      return
    }
    setPendingRunCleanup({ days, count })
  }

  const handleConfirmRunCleanup = useCallback(async () => {
    if (!pendingRunCleanup || !workspacePath) return
    const scheduleIds = filteredJobs.map(job => job.id).filter(Boolean)
    if (scheduleIds.length === 0) {
      addToast('No runs to delete', 'info')
      setPendingRunCleanup(null)
      return
    }
    setIsCleanupRunsLoading(true)
    try {
      const response = await schedulerApi.cleanupJobRuns({
        workspace_path: workspacePath,
        older_than_days: pendingRunCleanup.days,
        schedule_ids: scheduleIds.join(','),
      })
      const deletedCount = response.deleted_count ?? 0
      setRunsRefreshToken(token => token + 1)
      addToast(
        deletedCount === 0
          ? `No runs older than ${pendingRunCleanup.days} days`
          : `Deleted ${deletedCount} run record${deletedCount === 1 ? '' : 's'} older than ${pendingRunCleanup.days} days`,
        'success'
      )
    } catch {
      addToast('Failed to delete old runs', 'error')
    } finally {
      setIsCleanupRunsLoading(false)
      setPendingRunCleanup(null)
    }
  }, [addToast, filteredJobs, pendingRunCleanup, workspacePath])

  const ActionIcon = actionLabel.toLowerCase() === 'attach' ? Paperclip : ArrowUpRight
  // This row is a content filter inside the conversation hub, not another set
  // of active chats. The main Chat owns the one continuing conversation;
  // History contains earlier saved conversations for reference.
  const filterItems = [
    { filter: 'chat' as const, label: 'History', icon: MessageSquare },
    { filter: 'schedule' as const, label: 'Schedules', icon: CalendarClock },
    { filter: 'bot' as const, label: 'Bots', icon: Bot },
    { filter: 'webhook' as const, label: 'Triggers', icon: Webhook },
  ].filter(({ filter }) => runOnly ? filter === runOnly : (!recentOnly || includeAutomationChats || filter === 'chat'))
  const showPanelHeader = Boolean((!compact && title) || (!isLoading && (
    filterItems.length > 1 || (!readOnly && !isRunFilter && hasOldVisibleSessions)
  )))

  return (
    <div className={`chat-history-panel min-w-0 w-full ${fill ? 'flex min-h-0 flex-1 flex-col overflow-hidden' : 'shrink-0'} border-b border-border bg-background`}>
      <div className={`${fill ? 'flex min-h-0 flex-1 flex-col' : ''} w-full`}>
        {showPanelHeader && <div className={`flex flex-wrap items-center border-b border-border ${compact ? 'gap-2 px-3 py-2' : 'gap-3 px-4 py-3'} ${compact || !title ? 'justify-end' : 'justify-between'}`}>
          {/* The "Previous … chats" heading is redundant in the compact rail —
              the filter pills + list make the purpose obvious — so hide it there. */}
          {!compact && title && (
            <div className="flex min-w-0 items-center gap-2 text-sm">
              <MessageSquare className="h-4 w-4 shrink-0 text-muted-foreground/80" />
              <span className="truncate font-medium text-foreground">{runOnly ? title : activeFilter === 'webhook' ? 'Trigger activity' : activeFilter === 'schedule' ? 'Schedule activity' : title}</span>
            </div>
          )}

          {!isLoading && (
              <div className="flex max-w-full flex-wrap items-center justify-end gap-2">
                {filterItems.length > 1 && (
              <div className="flex max-w-full items-center gap-0.5 overflow-x-auto rounded-md border border-border bg-muted/30 p-0.5">
                {filterItems.map(({ filter, label, icon: Icon }) => {
                const isActive = activeFilter === filter
                return (
                  <button
                    key={filter}
                    aria-label={label}
                    aria-pressed={isActive}
                    title={`${label} (${displayFilterCounts[filter]})`}
                    type="button"
                    onClick={() => { setActiveFilter(filter); setSelectedBotChannel('all') }}
                    className={`inline-flex shrink-0 items-center gap-1.5 rounded-md text-xs font-medium transition-colors ${compact ? 'px-2 py-1' : 'px-3 py-1.5'} ${
                      isActive
                        ? 'bg-background text-foreground shadow-sm ring-1 ring-border/40'
                        : 'text-muted-foreground hover:bg-background/60 hover:text-foreground'
                    }`}
                  >
                    <Icon className="h-3.5 w-3.5" />
                    {!compact && <span className="chat-history-filter-label">{label}</span>}
                    <span className={`chat-history-filter-count min-w-4 rounded-full px-1 py-0.5 text-center text-[10px] leading-none ${
                      isActive
                        ? 'bg-muted text-foreground'
                        : 'bg-background/60 text-muted-foreground'
                    }`}>
                      {displayFilterCounts[filter]}
                    </span>
                  </button>
                )
                })}
              </div>
                )}
                {activeFilter === 'bot' && botChannels.length > 1 && (
                  <select
                    aria-label="Filter bots by channel"
                    value={selectedBotChannel}
                    onChange={(event) => setSelectedBotChannel(event.target.value)}
                    className="max-w-full rounded-md border border-border bg-background px-2 py-1 text-xs text-foreground focus:outline-none focus:ring-2 focus:ring-primary/50"
                  >
                    <option value="all">All channels</option>
                    {botChannels.map((channel) => (
                      <option key={channel} value={channel}>{formatBotPlatform(channel)}</option>
                    ))}
                  </select>
                )}
              {!readOnly && !isRunFilter && hasOldVisibleSessions && (
                <CleanupOldChatsDropdown
                  counts={oldVisibleSessionCounts}
                  isLoading={isCleanupLoading || isLoading}
                  onSelect={handleCleanupOldChats}
                />
              )}
              {showRunsCleanup && (
                <CleanupOldChatsDropdown
                  counts={oldRunCounts}
                  isLoading={isCleanupRunsLoading || isLoadingScheduleActivity}
                  onSelect={handleSelectRunCleanup}
                  title="Delete old runs"
                />
              )}
            </div>
          )}
        </div>}

        {isLoading ? (
          <div className="px-3 py-3 text-xs text-muted-foreground">Loading conversation history...</div>
        ) : isRunFilter ? (
          <div className={`${fill ? 'min-h-0 flex-1 overflow-y-auto' : ''}`}>
            {isLoadingScheduleActivity ? (
              <div className="flex items-center gap-2 px-3 py-4 text-xs text-muted-foreground">
                <Loader2 className="h-3.5 w-3.5 animate-spin" />
                <span>Loading {activeFilter === 'webhook' ? 'trigger' : 'schedule'} activity...</span>
              </div>
            ) : filteredJobs.length === 0 ? (
              <div className="px-3 py-4 text-sm text-muted-foreground">{activeFilter === 'webhook' ? 'No triggers configured. Ask the workflow builder chat to create one.' : 'No schedules are configured for this workflow yet.'}</div>
            ) : filteredRuns.length === 0 ? (
              <div className="px-3 py-4 text-sm text-muted-foreground">{runOnly ? emptyText : activeFilter === 'webhook' ? 'No trigger runs recorded yet.' : 'No scheduled runs recorded yet.'}</div>
            ) : (
              <div className="divide-y divide-border">
                {displayedScheduleRuns.map(({ job, run }) => (
                  <div key={run.id} className="px-3 py-3 transition-colors hover:bg-muted/20">
                    <ScheduleRunCard
                      job={job}
                      run={run}
                      resolveSession={r => (r.session_id ? sessionsByID.get(r.session_id) : undefined)}
                      onOpen={readOnly && !allowOpen ? undefined : r => openScheduleActivity({ id: r.id, job, run: r, kind: 'run', occurredAt: r.started_at })}
                      onDelete={r => {
                        const session = r.session_id ? sessionsByID.get(r.session_id) : undefined
                        if (session) void handleDeleteSession(session)
                      }}
                      deletingRunIds={deletingSessionIds}
                      compact={compact}
                      showScheduleName
                      showCopySessionId
                      openLabel={actionLabel}
                    />
                  </div>
                ))}
              </div>
            )}
          </div>
        ) : visibleSessions.length === 0 ? (
          <PreviousChatEmptyState
            filter={activeFilter}
            hasAnySessions={false}
            hasCurrentSession={hasCurrentSession}
            fallbackText={emptyText}
            recentOnly={recentOnly}
          />
        ) : filteredSessions.length === 0 ? (
          <PreviousChatEmptyState
            filter={activeFilter}
            hasAnySessions
            fallbackText={`No previous ${activeFilter} chats yet.`}
            recentOnly={recentOnly}
          />
        ) : (
          <div className={`${fill ? 'min-h-0 flex-1 overflow-y-auto' : ''} divide-y divide-border`}>
            {displayedSessions.map(session => {
              const runtimeLabel = chatHistoryRuntimeLabel(session)
              const isDeleting = deletingSessionIds.has(session.session_id)
              const canResume = !readOnly && session.can_resume !== false
              const canDelete = !readOnly && session.can_delete !== false
              const timeLabel = formatChatTime(session.updated_at || session.created_at)
              const messageCountLabel = formatMessageCount(session.message_count)
              const botSourceLabel = botSessionSourceLabel(session)
              const automationSource = automationSourceBySession[session.session_id]
              const chatKind = getChatKind(session)
              const sourceBadge = automationSource
                ? {
                    label: `${automationSource.kind === 'trigger' ? 'Trigger' : 'Schedule'} · ${automationSource.label}`,
                    icon: automationSource.kind === 'trigger' ? Webhook : CalendarClock,
                  }
                : chatKind === 'webhook'
                  ? { label: 'Trigger', icon: Webhook }
                  : chatKind === 'schedule'
                    ? { label: 'Schedule', icon: CalendarClock }
                    : chatKind === 'bot'
                      ? { label: botSourceLabel ? `Bot · ${botSourceLabel}` : 'Bot', icon: Bot }
                      : {
                          label: session.username && session.username !== 'System / legacy'
                            ? `Chat · ${session.username}`
                            : 'Chat',
                          icon: MessageSquare,
                        }
              const SourceIcon = sourceBadge.icon

              return (
                <div key={session.session_id} className="group bg-background transition-colors hover:bg-muted/20">
                  <div className={`flex items-start ${compact ? 'gap-2 px-2.5 py-2' : 'gap-3 px-4 py-4'}`}>
                    <button
                      type="button"
                      onClick={() => { if (openOnRowClick || !readOnly || allowOpen) handleSelect(session) }}
                      className="min-w-0 flex-1 text-left"
                    >
                      <div className="line-clamp-1 text-sm font-medium text-foreground">{chatHistorySessionTitle(session)}</div>
                      <div className={`flex min-w-0 flex-nowrap items-center overflow-hidden text-muted-foreground/80 ${compact ? 'mt-0.5 gap-x-2 text-[9px]' : 'mt-1.5 gap-x-3 text-xs'}`}>
                        <span className="inline-flex shrink-0 items-center gap-1">
                          <CalendarClock className="h-3 w-3 shrink-0" />
                          <span className="whitespace-nowrap">{timeLabel}</span>
                        </span>
                        {messageCountLabel && (
                          <span className="inline-flex items-center gap-1">
                            <MessageSquare className="h-3 w-3 shrink-0" />
                            <span>{messageCountLabel}</span>
                          </span>
                        )}
                        <span
                          className={`inline-flex min-w-0 max-w-full items-center gap-1 rounded border border-border/70 bg-muted/30 ${compact ? 'px-1.5 py-0.5' : 'px-2 py-1'}`}
                          title={sourceBadge.label}
                        >
                          <SourceIcon className="h-3 w-3 shrink-0" />
                          <span className="truncate">{sourceBadge.label}</span>
                        </span>
                        {runtimeLabel && (
                          <ChatHistoryRuntimeBadge session={session} />
                        )}
                      </div>
                    </button>

                    <div className="flex shrink-0 items-center gap-1">
                      <ChatRowActionsMenu
                        sessionId={session.session_id}
                        canRename={canResume}
                        canDelete={canDelete}
                        isDeleting={isDeleting}
                        open={openRowMenuSessionId === session.session_id}
                        onToggle={() => setOpenRowMenuSessionId(current => current === session.session_id ? null : session.session_id)}
                        onClose={() => setOpenRowMenuSessionId(null)}
                        onRename={() => { void handleRenameSession(session) }}
                        onDelete={() => { void handleDeleteSession(session) }}
                      />
                      {(!openOnRowClick && (!readOnly || allowOpen)) && (
                        <button
                          type="button"
                          onClick={() => handleSelect(session)}
                          title={readOnly ? 'Open in new tab' : canResume ? actionLabel : 'Open saved conversation'}
                          aria-label={readOnly ? 'Open in new tab' : canResume ? actionLabel : 'Open saved conversation'}
                          className="inline-flex items-center gap-1 rounded border border-border bg-background px-2 py-1 text-xs font-medium text-muted-foreground opacity-80 transition-colors hover:border-primary/40 hover:text-foreground group-hover:opacity-100"
                        >
                          <ActionIcon className="h-3.5 w-3.5" />
                          {!compact && <span>{readOnly ? 'Open' : canResume ? actionLabel : 'Open'}</span>}
                        </button>
                      )}
                    </div>
                  </div>

                </div>
              )
            })}
          </div>
        )}

        {!showAll && !isLoading && totalForActiveFilter > displayedCountForActiveFilter && (
          <div className="border-t border-border px-3 py-2">
            <button
              type="button"
              onClick={() => setVisibleCount(count => count + PAGE_SIZE)}
              className="inline-flex items-center gap-1 rounded border border-border bg-background px-2 py-1 text-xs font-medium text-muted-foreground transition-colors hover:border-primary/40 hover:text-foreground"
            >
              <span>Load {PAGE_SIZE} more</span>
            </button>
          </div>
        )}
      </div>
      {pendingRunCleanup && (
        <ConfirmationDialog
          isOpen
          onClose={() => { if (!isCleanupRunsLoading) setPendingRunCleanup(null) }}
          onConfirm={() => void handleConfirmRunCleanup()}
          title="Delete old runs"
          message={`Delete ${pendingRunCleanup.count} ${activeFilter === 'webhook' ? 'trigger' : 'scheduled'} run record${pendingRunCleanup.count === 1 ? '' : 's'} older than ${pendingRunCleanup.days} days? Run folders and conversation transcripts are kept. This cannot be undone.`}
          confirmText="Delete runs"
          type="danger"
          isLoading={isCleanupRunsLoading}
          loadingText="Deleting runs..."
        />
      )}
    </div>
  )
}
