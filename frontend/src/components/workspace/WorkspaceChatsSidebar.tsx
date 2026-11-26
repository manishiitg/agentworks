import type { ReactNode } from 'react'
import { useAppStore } from '../../stores/useAppStore'
import { useLLMStore } from '../../stores/useLLMStore'
import { useProductSurfaceStore } from '../../stores/useProductSurfaceStore'
import { followAppLink, useProjectsPage } from '../../utils/appNavigation'
import { RuntimeBrandLogo } from '../branding/RuntimeBrandLogo'
import { DesktopConnections } from './DesktopConnections'
import { SidebarActions } from './SidebarActions'
import { useWorkspaceProjects } from './WorkspaceProjectsProvider'

export default function WorkspaceChatsSidebar({ toggle, footer }: { toggle?: ReactNode; footer?: ReactNode }) {
  const { projects } = useWorkspaceProjects()
  const product = useProductSurfaceStore(state => state.productSurface)
  const crewId = useProductSurfaceStore(state => state.selectedWorkProjectId)
  const globalPageOpen = useAppStore(state => state.showSchedulesOverview || state.adminPage !== null)
  const providersOpen = useLLMStore(state => state.showLLMModal)
  const projectsOpen = useProjectsPage()
  return <aside aria-label="Projects" className="flex min-h-0 w-full shrink-0 flex-col border-r border-border/70 bg-muted/20">
    <div className="flex h-[50px] shrink-0 items-center gap-2 px-3">
      <a href="/" onClick={followAppLink} aria-label="Open your last project" title="Open your last project" className="min-w-0 flex-1 rounded-md focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"><RuntimeBrandLogo variant="sidebar-header" /></a>
      {toggle}
    </div>
    <DesktopConnections top={<SidebarActions />} projects={projects.map(project => ({ ...project,
      selected: !projectsOpen && !globalPageOpen && !providersOpen && window.location.pathname !== '/dashboards' &&
        product === 'work' && crewId === project.id,
    }))} />
    {footer && <div className="shrink-0 border-t border-border/60 bg-background/60 p-2">{footer}</div>}
  </aside>
}
