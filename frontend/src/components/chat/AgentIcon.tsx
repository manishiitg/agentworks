import { Clock, Globe, Loader2, Users, Zap } from 'lucide-react'
import type { AgentMenuState } from './agentMenuState'
import type { TeamTabPresence, TeamTabSetup } from '../../products/work/teamApi'

/** The switch-agent key on this computer: ⌘ on a Mac, Ctrl elsewhere. */
export const AGENT_KEY = typeof navigator !== 'undefined' && /Mac|iPhone|iPad/.test(navigator.platform) ? '⌘' : 'Ctrl+'

/** Sharing and automation are independent: private agents keep their schedule/trigger
 * marks too. The server supplies enabled automations; this only chooses their glyphs. */
export function AgentSetupMark({ setup }: { setup?: TeamTabSetup }) {
  if (!setup) return null
  const active = setup.starts_on_own ? setup.automations.filter(item => item.on) : []
  const schedules = active.filter(item => item.kind === 'Schedule')
  const triggers = active.filter(item => item.kind !== 'Schedule')
  if (!setup.reach && !active.length) return null
  const reach = setup.reach === 'agentbridge' ? 'Public on AgentBridge' : setup.reach === 'team' ? 'Shared with the team' : 'Only you'
  const label = [reach, ...active.map(item => `${item.kind}: ${item.name} · ${item.line}`)].join('\n')
  const ReachIcon = setup.reach === 'agentbridge' ? Globe : Users
  return <span role="img" aria-label={label} title={label} className="inline-flex shrink-0 items-center gap-1.5">
    {setup.reach && <ReachIcon aria-hidden="true" className={`h-3 w-3 ${setup.reach === 'agentbridge' ? 'text-emerald-500' : 'text-muted-foreground'}`} />}
    {schedules.length > 0 && <Clock aria-hidden="true" className="h-3.5 w-3.5 text-[hsl(var(--warning))]" />}
    {triggers.length > 0 && <Zap aria-hidden="true" className="h-3.5 w-3.5 text-[hsl(var(--warning))]" />}
  </span>
}

/** The dot by a teammate's name (owner, 6 Nov): green with their Mac on, blue with it off while their shared agents
 * answer from the server, grey when they wait for it; nothing when the server gives no presence (their agents work on
 * the server). The server picks the tone (crew_team_tab.go). */
export function PersonPresenceDot({ presence, className = 'h-1.5 w-1.5' }: { presence?: TeamTabPresence; className?: string }) {
  if (!presence) return null
  const tone = presence.online ? 'bg-emerald-500' : presence.tone === 'server' ? 'bg-sky-500' : 'bg-muted-foreground/50'
  return <span role="img" aria-label={presence.line} title={presence.line} className={`${className} shrink-0 rounded-full ${tone}`} />
}

/** The one mark an agent shows, the same as the chat tabs have always shown (owner, 4 Nov): a spinner while it works,
 * a pulsing amber ring when it needs you, red when it failed (until opened), green when a reply is not read yet, and a
 * green pulse while someone else is asking it. Nothing when all is quiet. */
export function AgentMark({ state, asking }: { state: AgentMenuState; asking?: boolean }) {
  if (state === 'running') return <Loader2 role="img" aria-label="Working" className="h-3 w-3 shrink-0 animate-spin text-[hsl(var(--info))]" />
  if (state === 'waiting') return <span role="img" aria-label="Needs you" title="Needs you" className="relative grid h-3 w-3 shrink-0 place-items-center">
    <span className="absolute inline-flex h-3 w-3 animate-ping rounded-full bg-[hsl(var(--warning))] opacity-60" />
    <span className="relative h-2.5 w-2.5 rounded-full bg-[hsl(var(--warning))]" />
  </span>
  const [label, tone] = state === 'failed' ? ['Failed', 'bg-[hsl(var(--destructive))]']
    : asking ? ['Someone is asking it now', 'bg-emerald-500 motion-safe:animate-pulse']
      : state === 'done' ? ['Completed — open to mark as seen', 'bg-[hsl(var(--success))]'] : ['', '']
  return label ? <span role="img" aria-label={label} title={label} className={`h-2 w-2 shrink-0 rounded-full ${tone}`} /> : null
}
