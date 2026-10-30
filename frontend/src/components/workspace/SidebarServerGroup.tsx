import { openProductWorkspace } from '../../utils/productWorkspaceNavigation'
import { openAppProduct } from '../../utils/appNavigation'
import { EntityIdentityIcon } from '../ui/EntityIdentityIcon'
import { ProductSurfaceIcon } from '../ProductSurfaceIcon'

export function SidebarProjectRow({ name, kind, icon, selected, location, onOpen }: { name: string; kind: 'crew' | 'goal'; icon?: string; selected?: boolean; location?: string; onOpen: () => void }) {
  const label = kind === 'crew' ? 'Crew' : 'Goal'
  return <button type="button" title={`${label} · ${name}${location ? ` · ${location}` : ''}`} aria-label={`${name} · ${label}${location ? ` · ${location}` : ''}`} aria-current={selected ? 'page' : undefined} onClick={onOpen}
    className={`flex h-9 w-full items-center gap-2 rounded-lg px-2.5 text-left text-[13px] transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring ${selected ? 'bg-foreground/[0.08] font-medium text-foreground' : 'text-foreground/75 hover:bg-muted hover:text-foreground'}`}>
    <EntityIdentityIcon icon={icon} label={name} className="h-4 w-4" fallback={<ProductSurfaceIcon surface={kind === 'crew' ? 'work' : 'agentworks'} className="h-4 w-4 shrink-0" />} />
    <span className="min-w-0 flex-1 truncate">{name}</span>
    {location && <span className="w-14 shrink-0 truncate text-right text-[10px] font-normal text-muted-foreground/70" title={location}>{location}</span>}
  </button>
}

/** Reopen the workspace even when an overlay kept its project URL unchanged. */
export function openSidebarProject(kind: 'crew' | 'goal', id: string) {
  const surface = kind === 'crew' ? 'work' : 'agentworks'
  window.dispatchEvent(new Event('close-app-sidebar'))
  openProductWorkspace(surface)
  openAppProduct({ surface, id })
}
