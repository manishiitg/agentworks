import { useEffect, useRef, useState } from 'react'
import { CalendarClock, Ellipsis, LayoutGrid, Plus, Search } from 'lucide-react'
import { useAuthStore } from '../../stores/useAuthStore'
import { useProductSurfaceStore } from '../../stores/useProductSurfaceStore'
import { useCrewAgentsStore } from '../chat/AgentCard'
import { openAppPage } from '../../utils/appNavigation'
import { quickNavigationItems, openQuickNavigation } from '../../utils/quickNavigation'
import { NewProjectMenu } from './NewProjectMenu'
import { openSidebarProject } from './SidebarServerGroup'

const rowClass = 'flex h-8 w-full min-w-0 items-center gap-2.5 rounded-lg px-2.5 text-left text-[13px] text-foreground/80 hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring'
export function SidebarActions() {
  const user = useAuthStore(state => state.user)
  const product = useProductSurfaceStore(state => state.productSurface)
  const project = useProductSurfaceStore(state => state.selectedWorkProjectId)
  const canAdd = useCrewAgentsStore(state => state.projectId === project && state.canAdd)
  const items = quickNavigationItems(user, product)
  const schedules = items.find(item => item.action === 'schedules')
  const [more, setMore] = useState(false)
  const box = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (!more) return
    const outside = (event: MouseEvent) => { if (!box.current?.contains(event.target as Node)) setMore(false) }
    const key = (event: KeyboardEvent) => { if (event.key === 'Escape') setMore(false) }
    document.addEventListener('mousedown', outside); document.addEventListener('keydown', key)
    return () => { document.removeEventListener('mousedown', outside); document.removeEventListener('keydown', key) }
  }, [more])
  const search = () => window.dispatchEvent(new CustomEvent('open-quick-switcher'))
  return <nav aria-label="Workspace navigation" className="shrink-0 space-y-0.5 px-2 pb-2">
    <button type="button" className={rowClass} onClick={() => {
      if (project && canAdd) {
        openSidebarProject('crew', project)
        requestAnimationFrame(() => window.dispatchEvent(new CustomEvent('crew-new-agent', { detail: { projectId: project } })))
      } else window.dispatchEvent(new CustomEvent('open-quick-switcher', { detail: { query: '@crew ' } }))
    }} title={canAdd ? 'New agent in this Crew' : 'Choose a Crew to add an agent'}><Plus aria-hidden="true" className="h-4 w-4" />New agent</button>
    <button type="button" className={rowClass} onClick={search}><Search aria-hidden="true" className="h-4 w-4" /><span className="flex-1">Search</span><kbd className="text-[10px] text-muted-foreground">{navigator.platform.includes('Mac') ? '⌘K' : 'Ctrl K'}</kbd></button>
    <button type="button" className={rowClass} onClick={() => { openAppPage('/dashboards'); window.dispatchEvent(new Event('close-app-sidebar')) }}><LayoutGrid aria-hidden="true" className="h-4 w-4" />Artifacts</button>
    {schedules && <button type="button" className={rowClass} onClick={() => { openQuickNavigation(schedules); window.dispatchEvent(new Event('close-app-sidebar')) }}><CalendarClock aria-hidden="true" className="h-4 w-4" />Schedules</button>}
    <div ref={box} className="relative">
      <button type="button" className={rowClass} aria-expanded={more} onClick={() => setMore(value => !value)}><Ellipsis aria-hidden="true" className="h-4 w-4" />More</button>
      {more && <div role="group" aria-label="More navigation" className="absolute left-0 top-full z-50 mt-1 w-56 rounded-xl border border-border bg-popover p-1 shadow-xl" onClick={() => setMore(false)}>
        <NewProjectMenu label="New Crew" />
        {items.filter(item => item.type === 'menu' && !item.scope && item.action !== 'create' && item.action !== 'schedules').map(item => <button key={item.id} type="button" className={rowClass} onClick={() => openQuickNavigation(item)}>{item.label}</button>)}
      </div>}
    </div>
  </nav>
}
