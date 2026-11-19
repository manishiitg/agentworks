import { LoadingIndicator } from '../ui/LoadingIndicator'
import { Clock, Cloud, Globe, Pencil, Users } from 'lucide-react'
import type { AgentMenuState } from './agentMenuState'
import type { TeamTabPresence, TeamTabSetup } from '../../products/work/teamApi'

/** The switch-agent key on this computer: ⌘ on a Mac, Ctrl elsewhere. */
export const AGENT_KEY = typeof navigator !== 'undefined' && /Mac|iPhone|iPad/.test(navigator.platform) ? '⌘' : 'Ctrl+'

/** Sharing and automation are independent: private agents keep their schedule/trigger
 * marks too. The server supplies enabled automations; this only chooses their glyphs. */
/** The one sharing mark per agent: shared with the team shows its mode from the server row (answers only, or can make
 * changes with a small pencil), plus chosen people when only they can ask. */
export function AgentSetupMark({ setup, canChange, chosen, automationOnly = false }: { setup?: TeamTabSetup; canChange?: boolean; chosen?: boolean; automationOnly?: boolean }) {
  if (!setup) return null
  const active = setup.starts_on_own ? setup.automations.filter(item => item.on) : []
  const showReach = !automationOnly && !!setup.reach
  if (!showReach && !active.length) return null
  const reach = setup.reach === 'agentbridge' ? 'Public on AgentBridge' : setup.reach === 'team' ? `Shared with team · ${canChange ? 'can make changes' : 'answers only'}${chosen ? ' · chosen people' : ''}` : 'Only you'
  const label = [...(automationOnly ? [] : [reach]), ...active.map(item => `${item.kind}: ${item.name} · ${item.line}`)].join('\n')
  const ReachIcon = setup.reach === 'agentbridge' ? Globe : Users
  return <span role="img" aria-label={label} title={label} className="inline-flex shrink-0 items-center gap-1.5">
    {showReach && <span className="relative inline-flex"><ReachIcon aria-hidden="true" className={`h-3 w-3 ${setup.reach === 'agentbridge' ? 'text-emerald-500' : canChange ? 'text-primary' : 'text-muted-foreground'}`} />
      {setup.reach === 'team' && canChange && <Pencil aria-hidden="true" className="absolute -bottom-1 -right-1 h-2 w-2 text-primary" />}</span>}
    {/* One clock for every automation (schedule, Watcher, webhook), shown on hover except in the chat header; the title names each (owner, 18 Nov). */}
    {active.length > 0 && <Clock aria-hidden="true" className={`h-3 w-3 text-[hsl(var(--warning))] ${automationOnly ? '' : 'invisible group-hover:visible group-focus-within:visible'}`} />}
  </span>
}

/** Where an agent runs, from the server's row (owner, 15 Nov): a cloud for an agent on the server; nothing on a Mac.
 * The left menu and the chat header use this one mark. */
export function AgentPlaceMark({ place }: { place?: 'mac' | 'server' }) {
  if (place !== 'server') return null
  return <span title="Runs on the server, always available" aria-label="Runs on the server, always available" role="img" className="shrink-0"><Cloud aria-hidden="true" className="h-3 w-3 text-sky-500" /></span>
}

/** The dot by a teammate's name (owner, 6 Nov): green with their Mac on, blue with it off while their shared agents
 * answer from the server, grey when they wait for it; nothing when the server gives no presence (their agents work on
 * the server). The server picks the tone (crew_team_tab.go). */
export function PersonPresenceDot({ presence, className = 'h-1.5 w-1.5' }: { presence?: TeamTabPresence; className?: string }) {
  if (!presence) return null
  const tone = presence.online ? 'bg-emerald-500' : presence.tone === 'server' ? 'bg-sky-500' : 'bg-muted-foreground/50'
  return <span role="img" aria-label={presence.line} title={presence.line} className={`${className} shrink-0 rounded-full ${tone}`} />
}

/** The one mark an agent shows, the same as the chat tabs have always shown (owner, 4 Nov): the shared indicator while it works,
 * a pulsing amber ring when it needs you, red when it failed (until opened), green when a reply is not read yet, and a
 * green pulse while someone else is asking it. Nothing when all is quiet. */
export function AgentMark({ state, asking, dot = false }: { state: AgentMenuState; asking?: boolean; dot?: boolean }) {
  // The left menu's quieter form (PLAT-746): one small dot per state, working pulsing gently (still under reduced motion).
  if (dot && state === 'running') return <span data-theme-agent-state="running" role="img" aria-label="Working" title="Working" className="h-2 w-2 shrink-0 rounded-full bg-info motion-safe:animate-pulse" />
  if (dot && state === 'waiting') return <span data-theme-agent-state="waiting" role="img" aria-label="Needs you" title="Needs you" className="h-2 w-2 shrink-0 rounded-full bg-[hsl(var(--warning))]" />
  if (state === 'running') return <LoadingIndicator data-theme-agent-state="running" role="img" aria-label="Working" className="h-3 w-3 shrink-0 text-info" />
  if (state === 'waiting') return <span data-theme-agent-state="waiting" role="img" aria-label="Needs you" title="Needs you" className="relative grid h-3 w-3 shrink-0 place-items-center">
    <span className="absolute inline-flex h-3 w-3 animate-ping rounded-full bg-[hsl(var(--warning))] opacity-60" />
    <span className="relative h-2.5 w-2.5 rounded-full bg-[hsl(var(--warning))]" />
  </span>
  const [label, tone] = state === 'failed' ? ['Failed', 'bg-[hsl(var(--destructive))]']
    : asking ? ['Someone is asking it now', 'bg-emerald-500 motion-safe:animate-pulse']
      : state === 'done' ? ['Completed — open to mark as seen', 'bg-[hsl(var(--success))]'] : ['', '']
  return label ? <span data-theme-agent-state={state || undefined} role="img" aria-label={label} title={label} className={`h-2 w-2 shrink-0 rounded-full ${tone}`} /> : null
}

/** How long ago, short: "now", "5m", "3h", "yesterday", "4d", or a date. Empty when unknown. */
export function shortAgo(value?: string, now = Date.now()) {
  const at = Date.parse(value || '')
  if (!Number.isFinite(at) || at <= 0) return ''
  const minutes = Math.max(0, Math.floor((now - at) / 60_000))
  if (minutes < 1) return 'now'
  if (minutes < 60) return `${minutes}m`
  if (minutes < 24 * 60) return `${Math.floor(minutes / 60)}h`
  if (minutes < 48 * 60) return 'yesterday'
  if (minutes < 7 * 24 * 60) return `${Math.floor(minutes / (24 * 60))}d`
  return new Date(at).toLocaleDateString(undefined, { month: 'short', day: 'numeric' })
}
