import { useEffect, useMemo, useState } from 'react'
import { Pin } from 'lucide-react'
import { useShallow } from 'zustand/react/shallow'
import type { AgentProfileSideChat } from '../../services/api-types'
import { listProjectSideChats } from '../../products/work/sideChatListCache'
import { useLiveRefetch } from '../../hooks/useLiveRefetch'
import { useChatStore } from '../../stores/useChatStore'
import { useProductSurfaceStore } from '../../stores/useProductSurfaceStore'
import { hasActiveSessionWork } from '../../utils/activitySessions'
import { runtimeNeedsUserInput } from '../../utils/runtimeActivity'
import { captureChatIdentity, isChatIdentityCurrent } from '../../utils/chatIdentity'
import { matchesWorkConversation, workConversationAgent } from '../../utils/workConversationKey'
import { openAgentTab } from '../../utils/openAgentTab'
import { openProductWorkspace } from '../../utils/productWorkspaceNavigation'
import { openAppProduct } from '../../utils/appNavigation'
import { shortAgo } from '../chat/AgentIcon'

export type SidebarViewMode = 'projects' | 'threads'
type ThreadStatus = 'running' | 'waiting' | 'idle'

type ThreadItem = {
  id: string
  projectId: string
  projectName: string
  chat: AgentProfileSideChat
  name: string
  status: ThreadStatus
  lastMessageAt?: string
  timestamp: number
}

const VIEW_STORAGE_PREFIX = 'agentworks.sidebar.view.'
const PINNED_THREADS_PREFIX = 'agentworks.sidebar.pinnedThreads.'

export function readSidebarViewMode(scope: string): SidebarViewMode {
  if (!scope) return 'projects'
  try {
    return window.localStorage.getItem(`${VIEW_STORAGE_PREFIX}${scope}`) === 'threads' ? 'threads' : 'projects'
  } catch {
    return 'projects'
  }
}

export function writeSidebarViewMode(scope: string, mode: SidebarViewMode) {
  if (!scope) return
  try {
    window.localStorage.setItem(`${VIEW_STORAGE_PREFIX}${scope}`, mode)
  } catch { /* ignore storage quota errors */ }
}

function readPinnedThreads(scope: string): string[] {
  if (!scope) return []
  try {
    const raw = window.localStorage.getItem(`${PINNED_THREADS_PREFIX}${scope}`)
    const parsed = raw ? JSON.parse(raw) : []
    return Array.isArray(parsed) ? parsed.filter((item): item is string => typeof item === 'string') : []
  } catch {
    return []
  }
}

function writePinnedThreads(scope: string, ids: string[]) {
  if (!scope) return
  try {
    window.localStorage.setItem(`${PINNED_THREADS_PREFIX}${scope}`, JSON.stringify(ids))
  } catch { /* ignore storage quota errors */ }
}

export function SidebarThreadsView({
  projects,
  scope,
}: {
  projects: ReadonlyArray<{ id: string; name: string }>
  scope: string
}) {
  const [pinnedIds, setPinnedIds] = useState<string[]>(() => readPinnedThreads(scope))
  const [reload, setReload] = useState(0)
  const [loaded, setLoaded] = useState<{ identity: number; byProject: Record<string, AgentProfileSideChat[]>; loading: boolean; error: boolean }>(() => ({
    identity: captureChatIdentity(),
    byProject: {},
    loading: projects.length > 0,
    error: false,
  }))

  useEffect(() => {
    setPinnedIds(readPinnedThreads(scope))
  }, [scope])

  const projectKey = projects.map(project => project.id).join(',')
  useLiveRefetch(() => setReload(value => value + 1), {
    kinds: ['sessions', 'crew_panel'],
    fallbackMs: 60_000,
    minIntervalMs: 5_000,
    enabled: projects.length > 0,
  })

  useEffect(() => {
    const ids = projectKey ? projectKey.split(',') : []
    const identity = captureChatIdentity()
    if (ids.length === 0) {
      setLoaded({ identity, byProject: {}, loading: false, error: false })
      return
    }
    let cancelled = false
    setLoaded(previous => ({
      identity,
      byProject: previous.identity === identity ? previous.byProject : {},
      loading: previous.identity !== identity || Object.keys(previous.byProject).length === 0,
      error: false,
    }))
    void Promise.allSettled(ids.map(async id => ({ id, rows: await listProjectSideChats('work', id, true, reload > 0) }))).then(results => {
      if (cancelled || !isChatIdentityCurrent(identity)) return
      const next: Record<string, AgentProfileSideChat[]> = {}
      let okCount = 0
      for (const result of results) {
        if (result.status === 'fulfilled') {
          next[result.value.id] = result.value.rows
          okCount += 1
        }
      }
      setLoaded(previous => {
        const merged = okCount > 0 ? next : previous.identity === identity ? previous.byProject : {}
        return { identity, byProject: merged, loading: false, error: okCount === 0 && Object.keys(merged).length === 0 }
      })
    })
    return () => { cancelled = true }
  }, [projectKey, reload])

  const sessions = useChatStore(state => state.activeSessionsCache)
  const activeTabMeta = useChatStore(useShallow(state => {
    const tab = state.activeTabId ? state.chatTabs[state.activeTabId] : undefined
    return {
      projectId: tab?.metadata?.agentProfileProjectId,
      conversationKey: tab?.metadata?.agentProfileConversationKey,
    }
  }))
  const selectedProjectId = useProductSurfaceStore(state => state.selectedWorkProjectId)

  const pinnedSet = useMemo(() => new Set(pinnedIds), [pinnedIds])
  const togglePin = (id: string) => {
    setPinnedIds(current => {
      const next = current.includes(id) ? current.filter(item => item !== id) : [id, ...current]
      writePinnedThreads(scope, next)
      return next
    })
  }

  const threads = useMemo<ThreadItem[]>(() => {
    const currentByProject = isChatIdentityCurrent(loaded.identity) ? loaded.byProject : {}
    const sessionById = new Map(sessions.map(session => [session.session_id, session]))
    const list: ThreadItem[] = []
    for (const project of projects) {
      const rows = currentByProject[project.id] || []
      for (const chat of rows) {
        if (chat.can_write === false) continue
        const id = `${project.id}:${chat.conversation_key}`
        const session = chat.session_id ? sessionById.get(chat.session_id) : undefined
        const status: ThreadStatus = session && runtimeNeedsUserInput(session)
          ? 'waiting'
          : session && hasActiveSessionWork(session)
            ? 'running'
            : 'idle'
        // Keep hidden agents out of the flat list unless they are actively working, waiting, or pinned.
        if (!chat.is_main && chat.pinned === false && status === 'idle' && !pinnedSet.has(id)) continue
        const lastMessageAt = chat.last_message_at
        const timestamp = Date.parse(lastMessageAt || '') || 0
        list.push({
          id,
          projectId: project.id,
          projectName: project.name,
          chat,
          name: chat.name || (chat.is_main ? 'Main' : 'Agent'),
          status,
          lastMessageAt,
          timestamp,
        })
      }
    }
    return list.sort((a, b) => b.timestamp - a.timestamp || a.name.localeCompare(b.name))
  }, [loaded, projects, sessions, pinnedSet])

  const openThread = (item: ThreadItem) => {
    const existing = Object.values(useChatStore.getState().chatTabs).find(tab =>
      matchesWorkConversation(tab, 'work', item.projectId, item.chat.conversation_key),
    )
    if (existing && useProductSurfaceStore.getState().selectedWorkProjectId === item.projectId) {
      openAgentTab(existing.tabId)
    } else {
      const agent = item.chat.is_main ? undefined : workConversationAgent(item.chat.conversation_key)
      openProductWorkspace('work')
      openAppProduct({ surface: 'work', id: item.projectId, agent })
    }
    window.dispatchEvent(new Event('close-app-sidebar'))
  }

  if (loaded.loading) {
    return <div aria-label="Loading threads" className="space-y-1.5 px-1 py-2">
      <div className="h-10 animate-pulse rounded-lg bg-muted" />
      <div className="h-10 animate-pulse rounded-lg bg-muted" />
      <div className="h-10 animate-pulse rounded-lg bg-muted" />
    </div>
  }

  if (loaded.error) {
    return <div role="alert" className="mx-1 my-2 rounded-lg border border-border bg-card p-3 text-xs text-muted-foreground">
      <p className="font-medium text-foreground">Couldn’t load recent threads</p>
      <p className="mt-0.5">Try again or switch back to Projects.</p>
      <button type="button" onClick={() => setReload(value => value + 1)}
        className="mt-2 rounded-md border border-border bg-muted px-2.5 py-1 text-xs font-medium text-foreground hover:bg-muted/80 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
        Try again
      </button>
    </div>
  }

  if (threads.length === 0) {
    return <div className="mx-1 my-2 rounded-lg border border-border bg-card p-3 text-xs text-muted-foreground">
      <p className="font-medium text-foreground">No threads yet</p>
      <p className="mt-0.5">Open a project and start an agent to see threads here.</p>
    </div>
  }

  const pinnedOrder = new Map(pinnedIds.map((id, index) => [id, index]))
  const pinned = threads.filter(item => pinnedSet.has(item.id)).sort((a, b) => (pinnedOrder.get(a.id) ?? 0) - (pinnedOrder.get(b.id) ?? 0))
  const attention = threads
    .filter(item => !pinnedSet.has(item.id) && (item.status === 'running' || item.status === 'waiting'))
    .sort((a, b) => Number(b.status === 'waiting') - Number(a.status === 'waiting') || b.timestamp - a.timestamp)
  const recent = threads.filter(item => !pinnedSet.has(item.id) && item.status === 'idle')

  const renderRow = (item: ThreadItem) => {
    const isPinned = pinnedSet.has(item.id)
    const isActive = selectedProjectId === item.projectId && activeTabMeta.projectId === item.projectId && activeTabMeta.conversationKey === item.chat.conversation_key
    const age = shortAgo(item.lastMessageAt)
    const statusSuffix = item.status === 'running' ? ' · Working' : item.status === 'waiting' ? ' · Needs you' : ''
    return <div key={item.id} className={`group/thread flex items-start gap-1 rounded-lg px-1 py-0.5 ${isActive ? 'bg-foreground/[0.06]' : 'hover:bg-muted'}`}>
      <button type="button" role="listitem" aria-current={isActive ? 'page' : undefined} onClick={() => openThread(item)}
        className="flex min-w-0 flex-1 items-start gap-2 rounded-md px-1.5 py-1 text-left focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
        <span aria-hidden="true" className={`mt-1.5 h-2 w-2 shrink-0 rounded-full ${item.status === 'running' ? 'bg-info motion-safe:animate-pulse' : item.status === 'waiting' ? 'bg-[hsl(var(--warning))]' : 'border border-border'}`} />
        <span className="min-w-0 flex-1">
          <span className="flex items-center gap-1.5">
            <span className="min-w-0 flex-1 truncate text-[13px] font-medium text-foreground" title={item.name}>{item.name}</span>
            {age && <span className="shrink-0 text-[11px] text-muted-foreground">{age}</span>}
          </span>
          <span className="block truncate text-[11px] text-muted-foreground" title={`${item.projectName}${statusSuffix}`}>{item.projectName}{statusSuffix}</span>
        </span>
      </button>
      <button type="button" onClick={() => togglePin(item.id)} aria-pressed={isPinned} aria-label={`${isPinned ? 'Unpin' : 'Pin'} ${item.name}`} title={isPinned ? 'Unpin thread' : 'Pin thread'}
        className={`mt-1 grid h-6 w-6 shrink-0 place-items-center rounded-md hover:bg-background focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring ${isPinned ? 'text-primary' : 'text-muted-foreground [@media(hover:hover)]:opacity-0 group-hover/thread:opacity-100 group-focus-within/thread:opacity-100'}`}>
        <Pin className={`h-3 w-3 ${isPinned ? 'fill-current' : ''}`} aria-hidden="true" />
      </button>
    </div>
  }

  return <div aria-label="Threads" className="space-y-2">
    {pinned.length > 0 && <section aria-label="Pinned threads" className="space-y-0.5">
      <h2 className="px-2 pb-0.5 pt-1 text-[11px] font-medium text-muted-foreground">Pinned</h2>
      <div role="list" className="space-y-0.5">{pinned.map(renderRow)}</div>
    </section>}
    {attention.length > 0 && <section aria-label="Active threads" className="space-y-0.5">
      <h2 className="px-2 pb-0.5 pt-1 text-[11px] font-medium text-muted-foreground">Active now</h2>
      <div role="list" className="space-y-0.5">{attention.map(renderRow)}</div>
    </section>}
    {recent.length > 0 && <section aria-label="Recent threads" className="space-y-0.5">
      <h2 className="px-2 pb-0.5 pt-1 text-[11px] font-medium text-muted-foreground">Recent</h2>
      <div role="list" className="space-y-0.5">{recent.map(renderRow)}</div>
    </section>}
  </div>
}
