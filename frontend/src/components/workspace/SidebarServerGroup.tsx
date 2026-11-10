import type { ReactNode } from 'react'
import { PinnedCrewOrderMenu } from './PinnedCrewOrderMenu'
import { ServerIdentityIcon } from './ServerIdentityIcon'
import { openConnectedServers } from './ConnectedServersMenuItem'
import { openProductWorkspace } from '../../utils/productWorkspaceNavigation'
import { openAppProduct } from '../../utils/appNavigation'
import { ChevronRight, LayoutDashboard, Laptop, Loader2, Network, Pin } from 'lucide-react'
import { ProductSurfaceIcon } from '../ProductSurfaceIcon'
import { EntityIdentityIcon } from '../ui/EntityIdentityIcon'

export function SidebarProjectRow({ name, kind, icon, selected, location, remote, serverColor, serverId, mode, owned_by_caller, owner_name, personal_agent, shared_line, groupsMenu, stateLine, offline, onMac, shared_agents, where, showLocation, showIcon = true, pinned, onTogglePin, onOpen, activityLabel, agentsExpanded, onToggleAgents, onMoveUp, onMoveDown }: { groupsMenu?: ReactNode; shared_line?: string; onMoveUp?: () => void; onMoveDown?: () => void; agentsExpanded?: boolean; onToggleAgents?: () => void; activityLabel?: string; showLocation?: boolean; showIcon?: boolean; pinned?: boolean; onTogglePin?: () => void; personal_agent?: boolean; where?: 'mac' | 'web'; shared_agents?: number; name: string; kind: 'crew' | 'goal'; icon?: string; selected?: boolean; location?: string; remote?: boolean; serverColor?: string; serverId?: string; mode?: 'builder' | 'run'; owned_by_caller?: boolean; owner_name?: string; stateLine?: string; offline?: boolean; onMac?: 'on' | 'off'; onOpen: () => void }) {
  const highlight = selected && agentsExpanded !== true
  const sharedDashboard = kind === 'crew' && mode === 'run'
  // A Team member's working copy says whose Crew it is, as the server words it (owner, 10 Nov).
  const subline = stateLine || shared_line || (personal_agent ? 'Your agent' : undefined)
  const label = personal_agent ? 'Your AgentWorks agent' : sharedDashboard ? 'Shared dashboard' : kind === 'crew' ? 'Crew' : 'Goal'
  return <div className={`group/crew flex ${subline ? 'min-h-12 py-1' : 'h-10'} w-full items-center rounded-lg ${offline ? 'opacity-60' : ''} ${highlight ? 'bg-foreground/[0.06] text-foreground' : 'text-foreground/80 hover:bg-muted hover:text-foreground'}`}>
    <button type="button" title={`${label} · ${name}${location ? ` · ${location}` : ''}`} aria-label={`${name} · ${label}${location ? ` · ${location}` : ''}`} aria-current={selected ? 'page' : undefined} onClick={onOpen}
      className="flex h-full min-w-0 flex-1 items-center gap-2 rounded-lg px-3 text-left focus:outline-none focus-visible:ring-2 focus-visible:ring-ring">
      {showIcon && <span className="flex h-4 w-4 shrink-0 items-center justify-center"><EntityIdentityIcon icon={icon} label={name} className="h-4 w-4" fallback={personal_agent ? <ProductSurfaceIcon surface="agentworks" className="h-4 w-4" /> : sharedDashboard ? <LayoutDashboard className="h-4 w-4" /> : <ProductSurfaceIcon surface={kind === 'crew' ? 'work' : 'agentworks'} className="h-4 w-4" />} /></span>}
      <span className="min-w-0 flex-1"><span className={`block truncate text-[13px] leading-5 ${agentsExpanded || selected ? 'font-medium' : 'font-normal'}`}>{name}</span>{owned_by_caller === false && owner_name && <span className="block truncate text-[11px] leading-3 text-muted-foreground">{owner_name}{where ? ` · ${where === 'mac' ? 'On your Mac' : 'On the web'}` : ''}</span>}{subline && <span className="block truncate text-[11px] leading-4 text-muted-foreground">{subline}</span>}</span>
    </button>
    {activityLabel === 'Running' ? <Loader2 role="img" aria-label="Working" className="mr-2 h-3 w-3 shrink-0 animate-spin text-[hsl(var(--info))]" />
      : activityLabel ? <span role="status" title={activityLabel} aria-label={activityLabel} className="mr-2 h-2 w-2 shrink-0 rounded-full bg-amber-400" /> : null}
    {/* A Crew that lives on its owner's Mac (the server's row says so): a laptop with the Mac's on/off dot. */}
    {shared_agents ? <span title={`${shared_agents} ${shared_agents === 1 ? 'agent' : 'agents'} others can message`} className="mr-1.5 shrink-0 rounded-full bg-muted px-1.5 text-[10px] leading-4 text-muted-foreground">{shared_agents} shared</span> : null}
    {agentsExpanded !== true && onMac && <span title={onMac === 'on' ? 'On your Mac' : 'On your Mac · Mac off'} aria-label={onMac === 'on' ? 'On your Mac' : 'On your Mac, Mac off'} className="relative mr-2 inline-flex shrink-0 text-muted-foreground">
      <Laptop className="h-3.5 w-3.5" aria-hidden="true" />
      <span aria-hidden="true" className={`absolute -bottom-0.5 -right-0.5 h-1.5 w-1.5 rounded-full ring-1 ring-background ${onMac === 'on' ? 'bg-emerald-500' : 'bg-muted-foreground'}`} />
    </span>}
    {agentsExpanded !== true && showLocation && location && <span role="img" aria-label={remote ? `Network: ${location}` : location} title={location} className="inline-flex shrink-0 items-center pr-1 text-muted-foreground">
      {remote ? <Network className="h-3.5 w-3.5" aria-hidden="true" /> : <Laptop className="h-3.5 w-3.5" aria-hidden="true" />}
    </span>}
    {agentsExpanded !== true && onTogglePin && <button type="button" aria-label={`${pinned ? 'Unpin' : 'Pin'} ${name}${location ? ` · ${location}` : ''}`} aria-pressed={Boolean(pinned)} title={pinned ? 'Unpin from Pinned' : 'Pin to top'} onClick={onTogglePin}
      className={`mr-1 flex h-8 w-7 shrink-0 items-center justify-center rounded-md text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring ${pinned ? '' : 'md:opacity-0 md:group-hover/crew:opacity-100 md:focus-visible:opacity-100'}`}>
      <Pin className="h-3.5 w-3.5" fill={pinned ? 'currentColor' : 'none'} aria-hidden="true" />
    </button>}
    {agentsExpanded !== true && groupsMenu}
    {(onMoveUp || onMoveDown) && <PinnedCrewOrderMenu name={name} onMoveUp={onMoveUp} onMoveDown={onMoveDown} />}
    {onToggleAgents && <button type="button" aria-label={`${agentsExpanded ? 'Hide' : 'Show'} agents for ${name}`} aria-expanded={agentsExpanded} title={agentsExpanded ? 'Hide agents' : 'Show agents'} onClick={onToggleAgents}
      className="mr-1 flex h-8 w-7 shrink-0 items-center justify-center rounded-md text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
      <ChevronRight className={`h-3.5 w-3.5 transition-transform ${agentsExpanded ? 'rotate-90' : ''}`} aria-hidden="true" />
    </button>}
    {!showLocation && remote && serverId && <button type="button" aria-label={`Server: ${location || 'Connected server'}`} title={location || 'Connected server'}
      onClick={() => openConnectedServers('', serverId)} className="mr-1 flex h-8 w-7 shrink-0 items-center justify-center rounded-md hover:bg-foreground/5 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
      <ServerIdentityIcon connectionId={serverId} hidden className={`h-3 w-3 ${serverColor || 'text-slate-500/75 dark:text-slate-400/75'}`} />
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
