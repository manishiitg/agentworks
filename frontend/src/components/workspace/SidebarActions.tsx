import { Zap, Search, Folder, Bot } from 'lucide-react'
import { useAuthStore } from '../../stores/useAuthStore'
import { useProductSurfaceStore } from '../../stores/useProductSurfaceStore'
import { openAppPage, useProjectsPage } from '../../utils/appNavigation'
import { quickNavigationItems, openQuickNavigation } from '../../utils/quickNavigation'
import { useWorkspaceProjects } from './WorkspaceProjectsProvider'
import { NewProjectMenu } from './NewProjectMenu'
import { isLocalDeployment } from '../../products/productSurfaceConfig'
import { useBotsPageStore } from '../../products/work/bots/botsPageStore'
import { useBotCount } from '../../products/work/bots/useBotCount'
import { connectedLocations, useConnectedWorkPolling, useConnectedWorkStore } from '../../stores/useConnectedWorkStore'

const rowClass = 'flex h-[30px] w-full min-w-0 items-center gap-2.5 rounded-lg px-2.5 text-left text-[13px] text-foreground/80 hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring'
export function SidebarActions() {
  const { canBrowse } = useWorkspaceProjects()
  const projectsOpen = useProjectsPage()
  const user = useAuthStore(state => state.user)
  const product = useProductSurfaceStore(state => state.productSurface)
  const items = quickNavigationItems(user, product)
  const schedules = items.find(item => item.action === 'schedules')
  const botsOpen = useBotsPageStore(state => state.open)
  const botsDirectory = useBotsPageStore(state => state.open && !state.chat)
  const setBotsOpen = useBotsPageStore(state => state.setOpen)
  const showBots = useBotsPageStore(state => state.show)
  // Bots live on the server (#4545). The Mac app shows the ones on the server it is signed in to.
  const local = isLocalDeployment()
  const botsServer = useConnectedWorkStore(state => local ? connectedLocations(state).find(connection => connection.id !== 'local' && state.inventory[connection.id]?.status === 'connected')?.name ?? '' : null)
  useConnectedWorkPolling(local)
  // The row shows how many bots are shared with you; the server's name stays in the tooltip.
  const botCount = useBotCount(botsServer === '' ? null : botsServer ?? 'server')
  return <nav aria-label="Workspace navigation" className="shrink-0 space-y-0.5 pb-2">
    <NewProjectMenu label="New project" />
    <button data-theme-row="navigation" type="button" className={rowClass} onClick={() => window.dispatchEvent(new CustomEvent('open-quick-switcher'))}><Search aria-hidden="true" className="h-4 w-4" /><span className="flex-1">Search</span><kbd className="text-[10px] text-muted-foreground">{navigator.platform.includes('Mac') ? '⌘K' : 'Ctrl K'}</kbd></button>
    {canBrowse && <button data-theme-row="navigation" type="button" className={rowClass} aria-current={projectsOpen && !botsOpen ? 'page' : undefined} onClick={() => { setBotsOpen(false); openAppPage('/projects'); window.dispatchEvent(new Event('close-app-sidebar')) }}><Folder aria-hidden="true" className="h-4 w-4" />Projects</button>}
    {/* Bots (#4545): the company's directory of shared agents, on the server (from a Mac: the server it is signed in to). */}
    {botsServer !== '' && <button data-theme-row="navigation" type="button" className={rowClass} aria-current={botsDirectory ? 'page' : undefined} title={botsServer ? `Shared agents on ${botsServer}` : undefined} onClick={() => { showBots(); window.dispatchEvent(new Event('close-app-sidebar')) }}><Bot aria-hidden="true" className="h-4 w-4" /><span className="flex-1">Bots</span>{botCount > 0 && <span className="text-[10px] tabular-nums text-muted-foreground">{botCount}</span>}</button>}
    {schedules && <button data-theme-row="navigation" type="button" className={rowClass} onClick={() => { setBotsOpen(false); openQuickNavigation(schedules); window.dispatchEvent(new Event('close-app-sidebar')) }}><Zap aria-hidden="true" className="h-4 w-4" />Workflows</button>}
  </nav>
}
