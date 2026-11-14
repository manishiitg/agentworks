import { CalendarClock, LayoutGrid, Search, SlidersHorizontal } from 'lucide-react'
import { useAuthStore } from '../../stores/useAuthStore'
import { useProductSurfaceStore } from '../../stores/useProductSurfaceStore'
import { openAppPage } from '../../utils/appNavigation'
import { quickNavigationItems, openQuickNavigation } from '../../utils/quickNavigation'
import { openWorkspaceSettings } from '../../utils/workspaceSettingsNavigation'
import { NewProjectMenu } from './NewProjectMenu'

const rowClass = 'flex h-8 w-full min-w-0 items-center gap-2.5 rounded-lg px-2.5 text-left text-[13px] text-foreground/80 hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring'
export function SidebarActions() {
  const user = useAuthStore(state => state.user)
  const product = useProductSurfaceStore(state => state.productSurface)
  const items = quickNavigationItems(user, product)
  const schedules = items.find(item => item.action === 'schedules')
  return <nav aria-label="Workspace navigation" className="shrink-0 space-y-0.5 px-2 pb-2">
    <NewProjectMenu label="New project" />
    <button type="button" className={rowClass} onClick={() => window.dispatchEvent(new CustomEvent('open-quick-switcher'))}><Search aria-hidden="true" className="h-4 w-4" /><span className="flex-1">Search</span><kbd className="text-[10px] text-muted-foreground">{navigator.platform.includes('Mac') ? '⌘K' : 'Ctrl K'}</kbd></button>
    <button type="button" className={rowClass} onClick={() => { openAppPage('/dashboards'); window.dispatchEvent(new Event('close-app-sidebar')) }}><LayoutGrid aria-hidden="true" className="h-4 w-4" />Artifacts</button>
    {schedules && <button type="button" className={rowClass} onClick={() => { openQuickNavigation(schedules); window.dispatchEvent(new Event('close-app-sidebar')) }}><CalendarClock aria-hidden="true" className="h-4 w-4" />Schedules</button>}
    {items.some(item => item.action === 'providers') && <button type="button" className={rowClass} onClick={() => { openWorkspaceSettings('connections'); window.dispatchEvent(new Event('close-app-sidebar')) }}><SlidersHorizontal aria-hidden="true" className="h-4 w-4" />Settings</button>}
  </nav>
}
