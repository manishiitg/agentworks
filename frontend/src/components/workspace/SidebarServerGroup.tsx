import { Server } from 'lucide-react'
import { openProductWorkspace } from '../../utils/productWorkspaceNavigation'
import { openAppProduct } from '../../utils/appNavigation'
import { ProductSurfaceIcon } from '../ProductSurfaceIcon'
import { EntityIdentityIcon } from '../ui/EntityIdentityIcon'

export function SidebarProjectRow({ name, kind, icon, selected, location, remote, onOpen }: { name: string; kind: 'crew' | 'goal'; icon?: string; selected?: boolean; location?: string; remote?: boolean; onOpen: () => void }) {
  const label = kind === 'crew' ? 'Crew' : 'Goal'
  return <button type="button" title={`${label} · ${name}${location ? ` · ${location}` : ''}`} aria-label={`${name} · ${label}${location ? ` · ${location}` : ''}`} aria-current={selected ? 'page' : undefined} onClick={onOpen}
    className={`flex h-10 w-full items-center gap-2 rounded-lg px-3 text-left transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring ${selected ? 'bg-foreground/[0.06] text-foreground' : 'text-foreground/80 hover:bg-muted hover:text-foreground'}`}>
    <span className="flex h-4 w-4 shrink-0 items-center justify-center"><EntityIdentityIcon icon={icon} label={name} className="h-4 w-4" fallback={<ProductSurfaceIcon surface={kind === 'crew' ? 'work' : 'agentworks'} className="h-4 w-4" />} /></span>
    <span className={`min-w-0 flex-1 truncate text-[13px] leading-5 ${selected ? 'font-medium' : 'font-normal'}`}>{name}</span>
    {remote && <Server aria-label="On server" className="h-3 w-3 shrink-0 text-muted-foreground/60" />}
  </button>
}

/** Reopen the workspace even when an overlay kept its project URL unchanged. */
export function openSidebarProject(kind: 'crew' | 'goal', id: string) {
  const surface = kind === 'crew' ? 'work' : 'agentworks'
  window.dispatchEvent(new Event('close-app-sidebar'))
  openProductWorkspace(surface)
  openAppProduct({ surface, id })
}
