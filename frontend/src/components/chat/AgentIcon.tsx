import { Loader2 } from 'lucide-react'
import type { AgentMenuState } from './agentMenuState'

/** An agent's icon (owner, 4 Nov, agents design): its emoji when it has one, else its first letter, on a small quiet
 * rounded square. The same grey for every agent; status marks sit next to it, never on it. */
export function AgentIcon({ name, icon, size = 'sm', active = false }: { name: string; icon?: string; size?: 'sm' | 'md'; active?: boolean }) {
  const box = size === 'md' ? 'h-7 w-7 text-xs' : 'h-[18px] w-[18px] text-[10px]'
  return <span aria-hidden="true" className={`grid shrink-0 place-items-center rounded-md font-semibold ${box} ${active ? 'bg-neutral-300 text-neutral-900 dark:bg-[#3a3b42] dark:text-white' : 'bg-neutral-200 text-neutral-600 dark:bg-[#2a2b30] dark:text-[#c9c9cf]'}`}>
    {icon?.trim() || (name.trim()[0] || '?').toUpperCase()}
  </span>
}

/** The one mark an agent shows when something needs a look: working, needs you, failed, someone else asking it, or a
 * reply not read yet. Nothing when all is quiet. */
export function AgentMark({ state, asking }: { state: AgentMenuState; asking?: boolean }) {
  if (state === 'running') return <Loader2 role="img" aria-label="Working" className="h-3 w-3 shrink-0 animate-spin text-[hsl(var(--info))]" />
  const [label, tone] = state === 'waiting' ? ['Needs you', 'bg-[hsl(var(--warning))] motion-safe:animate-pulse']
    : state === 'failed' ? ['Failed', 'bg-destructive']
      : asking ? ['Someone is asking it now', 'bg-emerald-500 motion-safe:animate-pulse']
        : state === 'done' ? ['New reply', 'bg-[hsl(var(--info))]'] : ['', '']
  return label ? <span role="img" aria-label={label} title={label} className={`h-1.5 w-1.5 shrink-0 rounded-full ${tone}`} /> : null
}
