import { Globe, Loader2, Users, Zap } from 'lucide-react'
import type { AgentMenuState } from './agentMenuState'
import type { TeamTabSetup } from '../../products/work/teamApi'

/** The switch-agent key on this computer: ⌘ on a Mac, Ctrl elsewhere. */
export const AGENT_KEY = typeof navigator !== 'undefined' && /Mac|iPhone|iPad/.test(navigator.platform) ? '⌘' : 'Ctrl+'

/** The one setup icon right of an agent's name (owner, 6 Nov, approved mockup): a green globe when it is public on
 * AgentBridge (and so shared), people when it is shared with the team, nothing when only you use it. A small amber bolt
 * (a shape, never a dot, so it never reads as a message state) sits on it when something starts the agent on its own;
 * on an only-you agent the bolt stands alone. The server decides all of it (crew_tab_setup.go). */
export function AgentSetupMark({ setup }: { setup?: TeamTabSetup }) {
  if (!setup || (!setup.reach && !setup.starts_on_own)) return null
  const reach = setup.reach === 'agentbridge' ? 'Public on AgentBridge' : setup.reach === 'team' ? 'Shared with the team' : 'Only you'
  const label = setup.starts_on_own ? `${reach} · starts on its own` : reach
  const bolt = (className: string) => <Zap aria-hidden="true" className={`${className} fill-[hsl(var(--warning))] stroke-none text-[hsl(var(--warning))]`} />
  if (!setup.reach) return <span role="img" aria-label={label} title={label} className="grid shrink-0 place-items-center">{bolt('h-3 w-3')}</span>
  const Icon = setup.reach === 'agentbridge' ? Globe : Users
  return <span role="img" aria-label={label} title={label} className={`relative grid shrink-0 place-items-center ${setup.reach === 'agentbridge' ? 'text-emerald-500' : 'text-muted-foreground'}`}>
    <Icon aria-hidden="true" className="h-3 w-3" />
    {setup.starts_on_own && bolt('absolute -right-1.5 -top-1 h-2.5 w-2.5')}
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
