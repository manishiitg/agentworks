import { Sparkles } from 'lucide-react'
import { cn } from '../../lib/utils'

/** The server marks the person's own AgentWorks Crew with role "agentworks"; lists only read it for icon, label and order. */
export const AGENTWORKS_ROLE = 'agentworks'
export const AGENTWORKS_SUBTITLE = 'Your own agent'

export const isOwnAgentWorks = (item: { role?: string; shared?: unknown }) => item.role === AGENTWORKS_ROLE && !item.shared

/** Puts the person's AgentWorks first and keeps the rest in their order. */
export function agentWorksFirst<T>(items: T[], isAgentWorks: (item: T) => boolean): T[] {
  return [...items.filter(isAgentWorks), ...items.filter(item => !isAgentWorks(item))]
}

export function AgentWorksIcon({ className = '' }: { className?: string }) {
  return (
    <span className={cn('inline-flex h-5 w-5 shrink-0 items-center justify-center rounded bg-primary/10 text-primary', className)} aria-hidden="true">
      <Sparkles className="h-3.5 w-3.5" />
    </span>
  )
}
