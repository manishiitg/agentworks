import { Zap, Search, Folder, Bot } from 'lucide-react'
import { useAuthStore } from '../../stores/useAuthStore'
import { useProductSurfaceStore } from '../../stores/useProductSurfaceStore'
import { openAppPage, useProjectsPage } from '../../utils/appNavigation'
import { quickNavigationItems, openQuickNavigation } from '../../utils/quickNavigation'
import { useWorkspaceProjects } from './WorkspaceProjectsProvider'
import { NewProjectMenu } from './NewProjectMenu'
import { isLocalDeployment } from '../../products/productSurfaceConfig'
import { useBotsPageStore } from '../../products/work/bots/botsPageStore'

const rowClass = 'flex h-[30px] w-full min-w-0 items-center gap-2.5 rounded-lg px-2.5 text-left text-[13px] text-foreground/80 hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring'
export function SidebarActions() {
  const { canBrowse } = useWorkspaceProjects()
  const projectsOpen = useProjectsPage()
  const user = useAuthStore(state => state.user)
  const product = useProductSurfaceStore(state => state.productSurface)
  const items = quickNavigationItems(user, product)
  const schedules = items.find(item => item.action === 'schedules')
  const botsOpen = useBotsPageStore(state => state.open)
  const setBotsOpen = useBotsPageStore(state => state.setOpen)
  return <nav aria-label="Workspace navigation" className="shrink-0 space-y-0.5 pb-2">
    <NewProjectMenu label="New project" />
    <button data-theme-row="navigation" type="button" className={rowClass} onClick={() => window.dispatchEvent(new CustomEvent('open-quick-switcher'))}><Search aria-hidden="true" className="h-4 w-4" /><span className="flex-1">Search</span><kbd className="text-[10px] text-muted-foreground">{navigator.platform.includes('Mac') ? '⌘K' : 'Ctrl K'}</kbd></button>
    {canBrowse && <button data-theme-row="navigation" type="button" className={rowClass} aria-current={projectsOpen && !botsOpen ? 'page' : undefined} onClick={() => { setBotsOpen(false); openAppPage('/projects'); window.dispatchEvent(new Event('close-app-sidebar')) }}><Folder aria-hidden="true" className="h-4 w-4" />Projects</button>}
    {/* Bots (#4545): agents others shared for you to use without joining their project. They live on the server. */}
    {!isLocalDeployment() && <button data-theme-row="navigation" type="button" className={rowClass} aria-current={botsOpen ? 'page' : undefined} onClick={() => { setBotsOpen(true); window.dispatchEvent(new Event('close-app-sidebar')) }}><Bot aria-hidden="true" className="h-4 w-4" />Bots</button>}
    {schedules && <button data-theme-row="navigation" type="button" className={rowClass} onClick={() => { setBotsOpen(false); openQuickNavigation(schedules); window.dispatchEvent(new Event('close-app-sidebar')) }}><Zap aria-hidden="true" className="h-4 w-4" />Workflows</button>}
  </nav>
}
