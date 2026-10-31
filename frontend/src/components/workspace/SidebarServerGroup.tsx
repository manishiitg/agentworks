import { ServerIdentityIcon } from './ServerIdentityIcon'
import { openConnectedServers } from './ConnectedServersMenuItem'
import { openProductWorkspace } from '../../utils/productWorkspaceNavigation'
import { openAppProduct } from '../../utils/appNavigation'
import { LayoutDashboard } from 'lucide-react'
import { ProductSurfaceIcon } from '../ProductSurfaceIcon'
import { EntityIdentityIcon } from '../ui/EntityIdentityIcon'

export function SidebarProjectRow({ name, kind, icon, selected, location, remote, serverColor, serverId, mode, onOpen }: { name: string; kind: 'crew' | 'goal'; icon?: string; selected?: boolean; location?: string; remote?: boolean; serverColor?: string; serverId?: string; mode?: 'builder' | 'run'; onOpen: () => void }) {
  const sharedDashboard = kind === 'crew' && mode === 'run'
  const label = sharedDashboard ? 'Shared dashboard' : kind === 'crew' ? 'Crew' : 'Goal'
  return <div className={`flex h-10 w-full items-center rounded-lg ${selected ? 'bg-foreground/[0.06] text-foreground' : 'text-foreground/80 hover:bg-muted hover:text-foreground'}`}>
    <button type="button" title={`${label} · ${name}${location ? ` · ${location}` : ''}`} aria-label={`${name} · ${label}${location ? ` · ${location}` : ''}`} aria-current={selected ? 'page' : undefined} onClick={onOpen}
      className="flex h-full min-w-0 flex-1 items-center gap-2 rounded-lg px-3 text-left focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
      <span className="flex h-4 w-4 shrink-0 items-center justify-center"><EntityIdentityIcon icon={icon} label={name} className="h-4 w-4" fallback={sharedDashboard ? <LayoutDashboard className="h-4 w-4" /> : <ProductSurfaceIcon surface={kind === 'crew' ? 'work' : 'agentworks'} className="h-4 w-4" />} /></span>
      <span className={`min-w-0 flex-1 truncate text-[13px] leading-5 ${selected ? 'font-medium' : 'font-normal'}`}>{name}</span>
    </button>
    {remote && serverId && <button type="button" aria-label={`Server: ${location || 'Connected server'}`} title={location || 'Connected server'}
      onClick={() => openConnectedServers('', serverId)} className="mr-1 flex h-8 w-7 shrink-0 items-center justify-center rounded-md hover:bg-foreground/5 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
      <ServerIdentityIcon connectionId={serverId} hidden className={`h-3 w-3 ${serverColor || 'text-sky-600 dark:text-sky-400'}`} />
    </button>}
  </div>
}

/** Reopen the workspace even when an overlay kept its project URL unchanged. */
export function openSidebarProject(kind: 'crew' | 'goal', id: string) {
  const surface = kind === 'crew' ? 'work' : 'agentworks'
  window.dispatchEvent(new Event('close-app-sidebar'))
  openProductWorkspace(surface)
  openAppProduct({ surface, id })
}
