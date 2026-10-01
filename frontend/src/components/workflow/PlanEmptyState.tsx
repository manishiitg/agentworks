import { ArrowRight, Route, type LucideIcon } from 'lucide-react'
import type { ReactNode } from 'react'

type PlanEmptyStateProps = {
  kind: 'plan' | 'graph'
  title: string
  description: string
  actionLabel?: string
  onAction?: () => void
  icon?: LucideIcon
  secondaryActions?: ReactNode
}

/** Shared first step for AgentWorks, Crew, and Relay plan views. */
export function PlanEmptyState({
  kind,
  title,
  description,
  actionLabel,
  onAction,
  icon: Icon = Route,
  secondaryActions,
}: PlanEmptyStateProps) {
  return (
    <div className="flex h-full min-h-0 items-center justify-center overflow-y-auto bg-background px-5 py-8 sm:px-8">
      <div className="w-full max-w-md rounded-2xl border border-border bg-card px-6 py-7 text-left shadow-sm sm:px-8 sm:py-8">
        <span className="inline-flex h-11 w-11 items-center justify-center rounded-xl border border-primary/20 bg-primary/10 text-primary">
          <Icon className="h-5 w-5" aria-hidden="true" />
        </span>
        <p className="mt-6 text-xs font-semibold uppercase tracking-widest text-primary">{kind}</p>
        <h3 className="mt-2 text-xl font-semibold tracking-tight text-foreground">{title}</h3>
        <p className="mt-2 max-w-sm text-sm leading-6 text-muted-foreground">{description}</p>
        {onAction && actionLabel && <button
          type="button"
          onClick={onAction}
          className="mt-6 inline-flex min-h-10 items-center justify-center gap-2 rounded-lg bg-primary px-4 py-2 text-sm font-semibold text-primary-foreground shadow-sm transition-colors hover:bg-primary/90 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2"
        >
          {actionLabel}
          <ArrowRight className="h-4 w-4" aria-hidden="true" />
        </button>}
        {secondaryActions && <div className="mt-6 flex flex-wrap items-center gap-2 border-t border-border pt-4">{secondaryActions}</div>}
      </div>
    </div>
  )
}
