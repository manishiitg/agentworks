import { SharedPeopleIcon } from './SharedPeopleIcon'
import { PinnedCrewOrderMenu } from './PinnedCrewOrderMenu'
import { ServerIdentityIcon } from './ServerIdentityIcon'
import { openConnectedServers } from './ConnectedServersMenuItem'
import { openProductWorkspace } from '../../utils/productWorkspaceNavigation'
import { openAppProduct } from '../../utils/appNavigation'
import { ChevronRight, LayoutDashboard, Laptop, Network } from 'lucide-react'
import { ProductSurfaceIcon } from '../ProductSurfaceIcon'
import { EntityIdentityIcon } from '../ui/EntityIdentityIcon'

export function SidebarProjectRow({ name, kind, icon, selected, location, remote, serverColor, serverId, mode, shared_line, stateLine, offline, onMac, shared_agents, showLocation, showIcon = true, pinned, onTogglePin, onOpen, activityLabel, activityTitle, agentsExpanded, onToggleAgents, onMoveUp, onMoveDown, onNewAgent, onImportLocalChat, onDiscoverSkills, onRemoveOnlineCopy, owned_by_caller }: { onNewAgent?: () => void; onImportLocalChat?: () => void; onDiscoverSkills?: () => void; onRemoveOnlineCopy?: () => void; activityTitle?: string; shared_line?: string; onMoveUp?: () => void; onMoveDown?: () => void; agentsExpanded?: boolean; onToggleAgents?: () => void; activityLabel?: string; showLocation?: boolean; showIcon?: boolean; pinned?: boolean; onTogglePin?: () => void; where?: 'mac' | 'web'; shared_agents?: number; name: string; kind: 'crew' | 'goal'; icon?: string; selected?: boolean; location?: string; remote?: boolean; serverColor?: string; serverId?: string; mode?: 'builder' | 'run'; owned_by_caller?: boolean; owner_name?: string; stateLine?: string; offline?: boolean; onMac?: 'on' | 'off'; onOpen: () => void }) {
  const highlight = selected && agentsExpanded !== true
  // The open project is a heading over its agents (left menu v2, PLAT-746): its name, how many agents, and the fold
  // caret; clicking it folds or unfolds them (the project is already open).
  const heading = !!selected && !!onToggleAgents
  const sharedDashboard = kind === 'crew' && mode === 'run'
  // A shared Crew shows a small people icon; whose it is (as the server words it) shows on hover (owner, 10 Nov).
  const subline = stateLine
  const label = sharedDashboard ? 'Shared artifact' : kind === 'crew' ? 'Project' : 'Goal'
  // The laptop badge names whose Mac the Crew lives on: another person's Mac Crew says their Mac (owner, 12 Nov).
  const macHome = owned_by_caller === false ? 'On their Mac' : 'On your Mac'
  return <div data-theme-row="project" className={`group/crew flex ${subline ? 'min-h-12 py-1' : 'h-8 max-md:h-10'} w-full items-center rounded-lg ${offline ? 'opacity-60' : ''} text-foreground ${highlight ? 'bg-foreground/[0.06]' : 'hover:bg-muted'}`}>
    <button type="button" title={`${label} · ${name}${shared_line ? ` · ${shared_line}` : ''}${location ? ` · ${location}` : ''}`} aria-label={`${name} · ${label}${location ? ` · ${location}` : ''}`} aria-current={selected ? 'page' : undefined} aria-expanded={heading ? agentsExpanded : undefined} onClick={heading ? onToggleAgents : onOpen}
      className="flex h-full min-w-0 flex-1 items-center gap-2 rounded-lg px-2 text-left focus:outline-none focus-visible:ring-2 focus-visible:ring-ring">
      {showIcon && <span className="flex h-4 w-4 shrink-0 items-center justify-center"><EntityIdentityIcon icon={icon} label={name} className="h-4 w-4" fallback={sharedDashboard ? <LayoutDashboard className="h-4 w-4" /> : <ProductSurfaceIcon surface={kind === 'crew' ? 'work' : 'agentworks'} className="h-4 w-4" />} /></span>}
      <span className="min-w-0 flex-1"><span className={`flex min-w-0 items-center gap-1.5 text-[13px] leading-5 font-semibold`}><span className="truncate">{name}</span>{shared_line && !heading && <SharedPeopleIcon sharedLine={shared_line} people={[]} />}</span>{subline && <span className="block truncate text-[11px] leading-4 text-muted-foreground">{subline}</span>}</span>
    </button>
    {/* A folded project's own work, as one dot like its agents' (working pulses gently). */}
    {activityLabel === 'Running' ? <span role="img" data-theme-agent-state="running" title={activityTitle || 'Working'} aria-label={activityTitle || 'Working'} className="mr-2 h-2 w-2 shrink-0 rounded-full bg-info motion-safe:animate-pulse" />
      : activityLabel ? <span role="status" data-theme-agent-state="waiting" title={activityLabel} aria-label={activityLabel} className="mr-2 h-2 w-2 shrink-0 rounded-full bg-[hsl(var(--warning))]" /> : null}
    {/* A Crew that lives on its owner's Mac (the server's row says so): a laptop with the Mac's on/off dot. */}
    {shared_agents ? <span title={`${shared_agents} ${shared_agents === 1 ? 'agent' : 'agents'} others can message`} className="mr-1.5 shrink-0 rounded-full bg-muted px-1.5 text-[10px] leading-4 text-muted-foreground">{shared_agents} shared</span> : null}
    {agentsExpanded !== true && onMac && <span title={onMac === 'on' ? macHome : `${macHome} · Mac off`} aria-label={onMac === 'on' ? macHome : `${macHome}, Mac off`} className="relative mr-2 inline-flex shrink-0 text-muted-foreground">
      <Laptop className="h-3.5 w-3.5" aria-hidden="true" />
      <span aria-hidden="true" className={`absolute -bottom-0.5 -right-0.5 h-1.5 w-1.5 rounded-full ring-1 ring-background ${onMac === 'on' ? 'bg-emerald-500' : 'bg-muted-foreground'}`} />
    </span>}
    {agentsExpanded !== true && showLocation && location && <span role="img" aria-label={remote ? `Network: ${location}` : location} title={location} className="inline-flex shrink-0 items-center pr-1 text-muted-foreground">
      {remote ? <Network className="h-3.5 w-3.5" aria-hidden="true" /> : <Laptop className="h-3.5 w-3.5" aria-hidden="true" />}
    </span>}
    {/* Row actions show on hover, open Crew included (owner, 10 Nov). */}
    {(onMoveUp || onMoveDown || onNewAgent || onImportLocalChat || onDiscoverSkills || onRemoveOnlineCopy || onTogglePin) && <PinnedCrewOrderMenu name={name} pinned={pinned} onTogglePin={onTogglePin} onMoveUp={onMoveUp} onMoveDown={onMoveDown} onNewAgent={onNewAgent} onImportLocalChat={onImportLocalChat} onDiscoverSkills={onDiscoverSkills} onRemoveOnlineCopy={onRemoveOnlineCopy} />}
    {/* The caret only closes an open project's agents; opening the project opens them (owner, 10 Nov). */}
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
