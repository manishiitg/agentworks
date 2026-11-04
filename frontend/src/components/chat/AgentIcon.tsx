import { Loader2, Users } from 'lucide-react'
import type { AgentMenuState } from './agentMenuState'

/** An agent's icon (owner, 4 Nov, agents design): its emoji when it has one, else its first letter, on a small quiet
 * rounded square. The same grey for every agent; status marks sit next to it, never on it. */
/** The switch-agent key on this computer: ⌘ on a Mac, Ctrl elsewhere. */
export const AGENT_KEY = typeof navigator !== 'undefined' && /Mac|iPhone|iPad/.test(navigator.platform) ? '⌘' : 'Ctrl+'

/** shared: who it is shared with; a small people badge sits at the icon's corner (hover says who). */
export function AgentIcon({ name, icon, size = 'sm', active = false, round = false, shared }: { name: string; icon?: string; size?: 'sm' | 'md'; active?: boolean; round?: boolean; shared?: string }) {
  const box = size === 'md' ? 'h-7 w-7 text-xs' : 'h-[18px] w-[18px] text-[10px]'
  // round: a Crew's own letter, set apart from its agents' squares.
  const shape = round ? 'rounded-full border border-border bg-transparent text-foreground' : `rounded-md ${active ? 'bg-neutral-300 text-neutral-900 ring-1 ring-primary/50 dark:bg-[#3a3b42] dark:text-white' : 'bg-neutral-200 text-neutral-600 dark:bg-[#2a2b30] dark:text-[#c9c9cf]'}`
  return <span className={`relative grid shrink-0 place-items-center font-semibold ${box} ${shape}`}>
    <span aria-hidden="true">{icon?.trim() || (name.trim()[0] || '?').toUpperCase()}</span>
    {shared && <span role="img" aria-label={`Shared with ${shared}`} title={`Shared with ${shared}`} className="absolute -bottom-1 -right-1 grid h-3 w-3 place-items-center rounded-full bg-background">
      <Users aria-hidden="true" className="h-2 w-2 text-muted-foreground" />
    </span>}
  </span>
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
