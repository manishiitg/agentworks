import { createPortal } from 'react-dom'
import { useEffect, useLayoutEffect, useRef, useState } from 'react'
import { CalendarClock, Ellipsis, LayoutGrid, Search } from 'lucide-react'
import { useAuthStore } from '../../stores/useAuthStore'
import { useProductSurfaceStore } from '../../stores/useProductSurfaceStore'
import { openAppPage } from '../../utils/appNavigation'
import { quickNavigationItems, openQuickNavigation } from '../../utils/quickNavigation'
import { NewProjectMenu } from './NewProjectMenu'

const rowClass = 'flex h-8 w-full min-w-0 items-center gap-2.5 rounded-lg px-2.5 text-left text-[13px] text-foreground/80 hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring'
export function SidebarActions() {
  const user = useAuthStore(state => state.user)
  const product = useProductSurfaceStore(state => state.productSurface)
  const items = quickNavigationItems(user, product)
  const schedules = items.find(item => item.action === 'schedules')
  const [more, setMore] = useState(false)
  const box = useRef<HTMLDivElement>(null)
  const panel = useRef<HTMLDivElement>(null)
  const moreButton = useRef<HTMLButtonElement>(null)
  const [position, setPosition] = useState({ top: 0, left: 0 })
  useLayoutEffect(() => {
    if (!more) return
    const rect = moreButton.current?.getBoundingClientRect()
    const height = panel.current?.getBoundingClientRect().height || 0
    if (rect) setPosition({ top: Math.max(8, rect.bottom + height + 4 < window.innerHeight ? rect.bottom + 4 : rect.top - height - 4), left: Math.max(8, Math.min(rect.left, window.innerWidth - 232)) })
  }, [more])
  useEffect(() => {
    if (!more) return
    const outside = (event: MouseEvent) => { if (!box.current?.contains(event.target as Node) && !panel.current?.contains(event.target as Node)) setMore(false) }
    const key = (event: KeyboardEvent) => { if (event.key === 'Escape') setMore(false) }
    const scroll = (event: Event) => { if (!(event.target instanceof Node) || !panel.current?.contains(event.target)) setMore(false) }
    window.addEventListener('scroll', scroll, true)
    const resize = () => setMore(false)
    window.addEventListener('resize', resize)
    document.addEventListener('mousedown', outside); document.addEventListener('keydown', key)
    return () => { window.removeEventListener('scroll', scroll, true); window.removeEventListener('resize', resize); document.removeEventListener('mousedown', outside); document.removeEventListener('keydown', key) }
  }, [more])
  const search = () => window.dispatchEvent(new CustomEvent('open-quick-switcher'))
  return <nav aria-label="Workspace navigation" className="shrink-0 space-y-0.5 px-2 pb-2">
    <NewProjectMenu label="New project" />
    <button type="button" className={rowClass} onClick={search}><Search aria-hidden="true" className="h-4 w-4" /><span className="flex-1">Search</span><kbd className="text-[10px] text-muted-foreground">{navigator.platform.includes('Mac') ? '⌘K' : 'Ctrl K'}</kbd></button>
    <button type="button" className={rowClass} onClick={() => { openAppPage('/dashboards'); window.dispatchEvent(new Event('close-app-sidebar')) }}><LayoutGrid aria-hidden="true" className="h-4 w-4" />Artifacts</button>
    {schedules && <button type="button" className={rowClass} onClick={() => { openQuickNavigation(schedules); window.dispatchEvent(new Event('close-app-sidebar')) }}><CalendarClock aria-hidden="true" className="h-4 w-4" />Schedules</button>}
    <div ref={box} className="relative">
      <button ref={moreButton} type="button" className={rowClass} aria-expanded={more} onClick={() => setMore(value => !value)}><Ellipsis aria-hidden="true" className="h-4 w-4" />More</button>
      {more && createPortal(<div ref={panel} role="group" aria-label="More navigation" style={position} className="fixed z-[100] max-h-[calc(100dvh-16px)] w-56 overflow-y-auto rounded-xl border border-border bg-popover p-1 shadow-xl" onClick={() => setMore(false)}>
        {items.filter(item => item.type === 'menu' && !item.scope && item.action !== 'create' && item.action !== 'schedules').map(item => <button key={item.id} type="button" className={rowClass} onClick={() => openQuickNavigation(item)}>{item.label}</button>)}
      </div>, document.body)}
    </div>
  </nav>
}
