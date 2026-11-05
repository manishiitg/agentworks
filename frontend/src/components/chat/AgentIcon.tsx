import { Loader2, Users } from 'lucide-react'
import type { AgentMenuState } from './agentMenuState'

/** The switch-agent key on this computer: ⌘ on a Mac, Ctrl elsewhere. */
export const AGENT_KEY = typeof navigator !== 'undefined' && /Mac|iPhone|iPad/.test(navigator.platform) ? '⌘' : 'Ctrl+'

/** An agent is its name only (owner, 5 Nov: no icons or letter tiles); a shared one gets this small people mark beside
 * its name (hover says who). */
export function AgentSharedMark({ shared }: { shared?: string }) {
  if (!shared) return null
  return <span role="img" aria-label={`Shared with ${shared}`} title={`Shared with ${shared}`} className="grid shrink-0 place-items-center text-muted-foreground"><Users aria-hidden="true" className="h-3 w-3" /></span>
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
