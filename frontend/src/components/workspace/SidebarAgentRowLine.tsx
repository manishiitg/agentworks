import { GripVertical } from 'lucide-react'
import type { ComponentProps } from 'react'
import { AgentInTeamMark } from '../chat/AgentInTeamMark'
import { AgentMark, AgentPlaceMark, AgentSetupMark, shortAgo } from '../chat/AgentIcon'
import { CodingCliIcon } from '../chat/CodingCliIcon'
import type { AgentMenuState } from '../chat/agentMenuState'
import { sidebarAgentRowClass } from './sidebarAgentLayout'

/** A teammate's initial, like the Team panel's person mark. */
export function PersonInitial({ name }: { name: string }) {
  return <span aria-hidden="true" title={`${name}'s agent`} className="grid h-4 w-4 shrink-0 place-items-center rounded-full bg-primary text-[9px] font-bold text-primary-foreground">{name.trim().charAt(0).toUpperCase() || '?'}</span>
}

/** The theme hooks every agent row carries, on its outer box. */
export const agentRowThemeProps = (state: AgentMenuState, active: boolean) =>
  ({ 'data-theme-row': 'agent', 'data-theme-agent-state': state || 'quiet', 'data-theme-agent-selected': active ? 'true' : 'false' })

/** Up to this age the last-active time always shows; older only on hover (owner, 20 Nov). */
export const RECENT_AGO_MS = 3 * 60 * 60_000

/** One agent row for every project in the left menu, open or not, so a row looks and behaves the same
 * after switching projects. Each list passes what it knows; a mark it does not know is simply left out. */
export function AgentRowLine({ name, title, person, inTeam, setup, place, status, lastAt, provider, state, asking, active = false, grip = false, onClick }: {
  name: string
  title?: string
  person?: string
  inTeam?: { on?: boolean; mostIn: boolean }
  setup?: ComponentProps<typeof AgentSetupMark>['setup']
  place?: 'mac' | 'server'
  status?: string
  /** When it was last active; left out while it shows a mark or status. */
  lastAt?: string
  /** Its coding program, shown on hover (owner, 20 Nov). */
  provider?: string
  state: AgentMenuState
  asking?: boolean
  active?: boolean
  grip?: boolean
  onClick: () => void
}) {
  const ago = shortAgo(lastAt)
  const recent = Date.now() - Date.parse(lastAt || '') < RECENT_AGO_MS
  return <button type="button" role="listitem" title={title || undefined} aria-current={active ? 'page' : undefined} onClick={onClick}
    className={`${sidebarAgentRowClass} gap-2 focus:outline-none focus-visible:ring-2 focus-visible:ring-ring max-md:pr-11 ${active ? 'bg-foreground/[0.06] font-medium text-foreground' : 'text-muted-foreground hover:bg-muted hover:text-foreground'}`}>
    {grip && <GripVertical aria-hidden="true" className="h-3 w-3 shrink-0 cursor-grab text-muted-foreground" />}
    {person && <PersonInitial name={person} />}
    <span className="block min-w-0 truncate">{name}</span>
    {inTeam && <AgentInTeamMark on={inTeam.on} mostIn={inTeam.mostIn} />}
    <AgentSetupMark setup={setup} />
    {/* Where it runs (owner, 15 Nov); terminal sessions keep their program mark. */}
    <AgentPlaceMark place={place} />
    {status && <span className="shrink-0 text-[10px] text-muted-foreground">{status}</span>}
    <span className="flex-1" />
    <CodingCliIcon provider={provider} className="hidden shrink-0 items-center group-hover:inline-flex group-focus-within:inline-flex" />
    {ago && <span className={`shrink-0 text-[11px] text-muted-foreground ${recent ? '' : 'hidden group-hover:inline group-focus-within:inline'}`}>{ago}</span>}
    <AgentMark state={state} asking={asking} dot />
  </button>
}
