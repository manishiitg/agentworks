import { openProductWorkspace } from '../../utils/productWorkspaceNavigation'
import { openAppProduct } from '../../utils/appNavigation'
import { EntityIdentityIcon } from '../ui/EntityIdentityIcon'

export function SidebarProjectRow({ name, kind, icon, selected, location, onOpen }: { name: string; kind: 'crew' | 'goal'; icon?: string; selected?: boolean; location?: string; onOpen: () => void }) {
  const label = kind === 'crew' ? 'Crew' : 'Goal'
  return <button type="button" title={`${label} · ${name}${location ? ` · ${location}` : ''}`} aria-label={`${name} · ${label}${location ? ` · ${location}` : ''}`} aria-current={selected ? 'page' : undefined} onClick={onOpen}
    className={`relative flex min-h-12 w-full flex-col justify-center rounded-lg py-2.5 pl-9 pr-3 text-left transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring ${selected ? 'bg-foreground/[0.06] text-foreground' : 'text-foreground/80 hover:bg-muted hover:text-foreground'}`}>
    {icon?.trim() && <EntityIdentityIcon icon={icon} label={name} className="absolute left-3 top-3 h-4 w-4" />}
    <span className={`block w-full truncate text-[13px] leading-5 ${selected ? 'font-medium' : 'font-normal'}`}>{name}</span>
    {location && <span className="mt-0.5 block w-full truncate text-[11px] font-normal leading-4 text-muted-foreground/80" title={location}>{location}</span>}
  </button>
}

/** Reopen the workspace even when an overlay kept its project URL unchanged. */
export function openSidebarProject(kind: 'crew' | 'goal', id: string) {
  const surface = kind === 'crew' ? 'work' : 'agentworks'
  window.dispatchEvent(new Event('close-app-sidebar'))
  openProductWorkspace(surface)
  openAppProduct({ surface, id })
}
