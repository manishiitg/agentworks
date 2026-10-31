import { useEffect, useMemo, useState } from 'react'
import { ArrowUpRight, Loader2, RefreshCw, Search } from 'lucide-react'
import { useShallow } from 'zustand/react/shallow'
import { agentApi } from '../services/api'
import type { ChatHistorySession } from '../services/api-types'
import { useAuthStore } from '../stores/useAuthStore'
import { useChatStore } from '../stores/useChatStore'
import { useGlobalPresetStore } from '../stores/useGlobalPresetStore'
import { useWorkspaceConnectionStore } from '../stores/useWorkspaceConnectionStore'
import { navigationProductSurfaceIDs, PRODUCT_SURFACE_LABELS } from '../products/productSurfaceConfig'
import { captureChatIdentity, isChatIdentityCurrent } from '../utils/chatIdentity'
import { sidebarChats, sidebarWorkspaceGroups, type SidebarProject } from '../utils/sidebarChats'
import { productRoutePath } from '../utils/productRoutes'
import { followAppLink } from '../utils/appNavigation'
import { EntityIdentityIcon } from './ui/EntityIdentityIcon'
import { ProductSurfaceIcon } from './ProductSurfaceIcon'
import { hasDesktopConnections } from '../stores/useConnectedWorkStore'
import { ConnectedHomeRecentWork } from './ConnectedHomeRecentWork'

export function HomeRecentWork() {
  return hasDesktopConnections() ? <ConnectedHomeRecentWork /> : <CurrentHomeRecentWork />
}

function CurrentHomeRecentWork() {
  const user = useAuthStore(state => state.user)
  const server = useWorkspaceConnectionStore(state => state.activeWorkspaceId)
  const scope = `${server}:${user?.id || ''}`
  const allowed = navigationProductSurfaceIDs(user?.allowed_products)
  const { tabs, active } = useChatStore(useShallow(state => ({ tabs: state.chatTabs, active: state.activeSessionsCache })))
  const presets = useGlobalPresetStore(state => state.workflowPresets)
  const [snapshot, setSnapshot] = useState<{ scope: string; projects: SidebarProject[]; recent: ChatHistorySession[]; inventoryKnown: boolean; error: boolean }>()
  const [revision, setRevision] = useState(0)
  const [query, setQuery] = useState('')
  useEffect(() => {
    let disposed = false
    const identity = captureChatIdentity()
    void Promise.allSettled([
      agentApi.listChatHistorySessions(30),
      allowed.includes('work') ? agentApi.listWorkspaceNavigation() : Promise.resolve({ workspaces: [] }),
    ]).then(([history, directory]) => {
      if (disposed || !isChatIdentityCurrent(identity)) return
      setSnapshot({ scope,
        recent: history.status === 'fulfilled' ? history.value.sessions || [] : [],
        projects: directory.status === 'fulfilled' ? directory.value.workspaces.filter(item => allowed.includes(item.profile_id)).map(item => ({ id: item.id, workspaceId: item.workspace_id, workspacePath: item.workspace_path, title: item.name, icon: item.icon, surface: item.profile_id })) : [],
        inventoryKnown: directory.status === 'fulfilled', error: history.status === 'rejected' || directory.status === 'rejected',
      })
    })
    return () => { disposed = true }
  }, [scope, allowed.join('|'), revision])
  const current = snapshot?.scope === scope ? snapshot : undefined
  const rows = useMemo(() => {
    if (!current) return []
    const chats = sidebarChats({ tabs, active, recent: current.recent, projects: current.projects, projectInventoryKnown: current.inventoryKnown, presets, allowed, allowedWorkflowIds: user?.allowed_workflow_ids })
    return sidebarWorkspaceGroups(chats, current.projects, presets, true).flatMap(group => {
      if (!['work', 'agentworks', 'relays'].includes(group.surface)) return []
      const row = group.chats.find(item => item.projectId || item.workflowId)
      const project = current.projects.find(item => `${item.surface}:${item.id}` === group.id)
      const id = row?.projectId || row?.workflowId || project?.id
      return id ? [{ ...group, href: productRoutePath({ surface: group.surface, id }), waiting: group.chats.some(item => item.waiting) }] : []
    })
  }, [current, tabs, active, presets, allowed.join('|'), user?.allowed_workflow_ids])
  const canSearch = rows.length > 20
  const filter = canSearch ? query.trim().toLocaleLowerCase() : ''
  useEffect(() => { if (!canSearch) setQuery('') }, [canSearch])
  // Work that needs you comes first; otherwise keep the recent order.
  const visible = rows.filter(row => `${row.title} ${PRODUCT_SURFACE_LABELS[row.surface]}`.toLocaleLowerCase().includes(filter)).sort((a, b) => Number(b.waiting) - Number(a.waiting)).slice(0, filter ? 30 : 8)
  return <section aria-labelledby="home-recent-title" className="border-t border-border pt-5">
    <div className="flex flex-wrap items-center justify-between gap-3"><h2 id="home-recent-title" className="text-sm font-semibold">Recent work</h2>
      <div className="flex min-w-0 items-center gap-2">{canSearch && <label className="flex items-center gap-2"><Search aria-hidden="true" className="h-3.5 w-3.5 text-muted-foreground" /><input aria-label="Search recent work" value={query} onChange={event => setQuery(event.target.value)} placeholder="Find your work" className="h-8 min-w-0 w-40 bg-transparent text-xs outline-none focus:underline sm:w-48" /></label>}<button type="button" title="Refresh recent work" aria-label="Refresh recent work" onClick={() => setRevision(value => value + 1)} className="grid h-8 w-8 shrink-0 place-items-center rounded-md text-muted-foreground hover:bg-muted"><RefreshCw aria-hidden="true" className="h-3.5 w-3.5" /></button></div>
    </div>
    {!current ? <p role="status" className="flex items-center gap-2 py-5 text-xs text-muted-foreground"><Loader2 aria-hidden="true" className="h-3.5 w-3.5 animate-spin" />Loading your work…</p> : <>
      {current.error && <p role="status" className="py-2 text-xs text-muted-foreground">Some recent work could not be loaded. Refresh to try again.</p>}
      {visible.length ? <ul className="mt-2 divide-y divide-border">{visible.map(row => <li key={row.id}><a href={row.href} onClick={followAppLink} className="flex min-h-14 items-center gap-3 rounded-lg px-2 py-2 hover:bg-muted/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
        <EntityIdentityIcon icon={row.icon} label={row.title} fallback={<ProductSurfaceIcon surface={row.surface} className="h-4 w-4" />} />
        <span className="min-w-0 flex-1"><span className="block truncate text-sm font-medium">{row.title}</span><span className="block text-xs text-muted-foreground">{PRODUCT_SURFACE_LABELS[row.surface]}</span></span>
        {row.waiting ? <span className="text-xs text-amber-500">Needs you</span> : row.running ? <span className="flex items-center gap-1.5 text-xs text-primary"><Loader2 aria-hidden="true" className="h-3 w-3 animate-spin" />Working</span> : row.latestActivity > 0 && <span className="hidden text-xs text-muted-foreground sm:block">{new Date(row.latestActivity).toLocaleDateString(undefined, { month: 'short', day: 'numeric' })}</span>}
        <ArrowUpRight aria-hidden="true" className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
      </a></li>)}</ul> : <p className="py-5 text-xs text-muted-foreground">{filter ? 'No work matches your search.' : 'Your work will appear here. Start with an idea above or open a project from the sidebar.'}</p>}
    </>}
  </section>
}
