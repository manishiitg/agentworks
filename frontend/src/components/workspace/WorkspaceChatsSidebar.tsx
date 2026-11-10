import { useEffect, useState, type ReactNode } from 'react'
import { Search } from 'lucide-react'
import { useAuthStore } from '../../stores/useAuthStore'
import { useAppStore } from '../../stores/useAppStore'
import { useLLMStore } from '../../stores/useLLMStore'
import { useProductSurfaceStore } from '../../stores/useProductSurfaceStore'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'
import { agentApi } from '../../services/api'
import { enabledProductSurfaces, intersectAllowedProductSurfaces } from '../../products/productSurfaceConfig'
import { useLiveRefetch } from '../../hooks/useLiveRefetch'
import { captureChatIdentity, isChatIdentityCurrent } from '../../utils/chatIdentity'
import { followAppLink } from '../../utils/appNavigation'
import { RuntimeBrandLogo } from '../branding/RuntimeBrandLogo'
import { DesktopConnections, useAllProjectsOpen } from './DesktopConnections'
import { NewProjectMenu } from './NewProjectMenu'

type Crew = Awaited<ReturnType<typeof agentApi.listWorkspaceNavigation>>['workspaces'][number]

/** The sidebar chooses a project; its chats belong to the header tabs. */
export default function WorkspaceChatsSidebar({ toggle, footer }: { toggle?: ReactNode; footer?: ReactNode }) {
  const allProjectsExpanded = useAllProjectsOpen()
  const user = useAuthStore(state => state.user)
  const workspace = useWorkspaceConnectionStore(state => state.activeWorkspaceId)
  const scope = `${workspace}:${user?.id || ''}`
  const product = useProductSurfaceStore(state => state.productSurface)
  const crewId = useProductSurfaceStore(state => state.selectedWorkProjectId)
  const globalPageOpen = useAppStore(state => state.showWorkflowsOverview || state.showSchedulesOverview || state.adminPage !== null)
  const providersOpen = useLLMStore(state => state.showLLMModal)
  const allowed = intersectAllowedProductSurfaces(enabledProductSurfaces(), user?.allowed_products)
  const [directory, setDirectory] = useState<{ scope: string; crews: Crew[]; error: boolean } | null>(null)
  const [refresh, setRefresh] = useState(0)
  useLiveRefetch(() => setRefresh(value => value + 1), {
    kinds: ['sessions'], fallbackMs: 60000, safetyMs: 60000, minIntervalMs: 30000,
    enabled: allowed.includes('work'),
  })
  useEffect(() => {
    let disposed = false
    const identity = captureChatIdentity()
    if (!allowed.includes('work')) {
      setDirectory({ scope, crews: [], error: false })
      return
    }
    void agentApi.listWorkspaceNavigation().then(result => {
      if (!disposed && isChatIdentityCurrent(identity)) setDirectory({ scope, crews: result.workspaces, error: false })
    }).catch(() => {
      if (!disposed && isChatIdentityCurrent(identity)) setDirectory({ scope, crews: [], error: true })
    })
    return () => { disposed = true }
  }, [scope, refresh, allowed.join(','), crewId])
  const current = directory?.scope === scope ? directory : null
  const projects = [
    ...(allowed.includes('work') ? (current?.crews || []).filter(crew => crew.profile_id === 'work').map(crew => ({ id: crew.id, name: crew.name, icon: crew.icon, mode: crew.mode, owned_by_caller: crew.owned_by_caller, owner_name: crew.owner_name, shared_line: crew.shared_line, personal_agent: crew.personal_agent, location: crew.location, mac: crew.mac, state: crew.state, where: crew.where, kind: 'crew' as const })) : []),
  ].sort((a, b) => a.name.localeCompare(b.name) || a.kind.localeCompare(b.kind) || a.id.localeCompare(b.id))

  return <aside aria-label="Projects" className="flex min-h-0 w-full shrink-0 flex-col border-r border-border/70 bg-muted/20">
    <div className="flex h-[50px] shrink-0 items-center gap-2 px-3">
      <a href="/home" onClick={followAppLink} aria-label="Home" title="Home" className="min-w-0 flex-1 rounded-md focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"><RuntimeBrandLogo variant="sidebar-header" /></a>
      {toggle}
    </div>
    <div className="shrink-0 space-y-0.5 px-2 pb-2">
      <button type="button" aria-label="Search all chats (Ctrl+K or Command+K)" title="Search all chats · ⌘K / Ctrl+K" onClick={() => window.dispatchEvent(new CustomEvent('open-quick-switcher'))}
        className="flex h-9 w-full items-center gap-2.5 rounded-lg px-2.5 text-left text-[13px] text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
        <Search className="h-3.5 w-3.5" /><span className="flex-1">Search</span><kbd className="text-[10px] text-muted-foreground">{navigator.platform.includes('Mac') ? '⌘K' : 'Ctrl K'}</kbd>
      </button>
      <NewProjectMenu />
    </div>
    <DesktopConnections projects={projects.map(project => ({ ...project,
      selected: !globalPageOpen && !providersOpen && window.location.pathname !== '/home' && window.location.pathname !== '/dashboards' &&
        product === 'work' && crewId === project.id,
    }))}>
      {!current && <p role="status" className="px-2.5 py-2 text-xs text-muted-foreground">Loading projects…</p>}
      {current?.error && <button type="button" className="px-2.5 py-2 text-left text-xs text-muted-foreground underline" onClick={() => setRefresh(value => value + 1)}>Could not load Crews. Try again.</button>}
    </DesktopConnections>
    {footer && <div hidden={!allProjectsExpanded} className="shrink-0 border-t border-border/60 bg-background/60 p-2">{footer}</div>}
  </aside>
}
