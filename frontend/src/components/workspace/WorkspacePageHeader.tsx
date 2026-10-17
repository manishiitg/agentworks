import type { HTMLAttributes, ReactNode } from 'react'
import type { LucideIcon } from 'lucide-react'
import { cn } from '../../lib/utils'
import { WorkspaceBackButton } from './WorkspaceBackButton'
import { WorkspaceTopToolbar } from './WorkspaceTopToolbar'
import { useAppStore } from '../../stores/useAppStore'
import { useLLMStore } from '../../stores/useLLMStore'

/** Shared pages use the workspace toolbar's spacing and existing navigation controls. */
export function WorkspacePageHeader({ title, icon: Icon, description, onBack, showBack = true, below, page, children, className, ...props }: Omit<HTMLAttributes<HTMLElement>, 'title'> & {
  title: string
  icon?: LucideIcon
  description?: string
  onBack?: () => void
  showBack?: boolean
  below?: ReactNode
  page?: 'activity' | 'schedules' | 'providers' | 'users' | 'mcp' | 'vaults'
}) {
  const currentPage = useAppStore(state => state.adminPage ?? (state.showSchedulesOverview ? 'schedules' : state.showWorkflowsOverview ? 'activity' : null))
  const providers = useLLMStore(state => state.showLLMModal)
  // Visited pages intentionally stay mounted (including an in-progress provider login).
  // Only the page being shown may register the shared navigation slots.
  const navigationActive = providers ? page === 'providers' : page ? page === currentPage : currentPage === null
  return <header {...props} className={cn('shrink-0 border-b border-border', className)}>
    <WorkspaceTopToolbar navigationKind="page" navigationActive={navigationActive} className="w-full border-b-0 bg-transparent">
      {showBack && <WorkspaceBackButton iconOnly onBack={onBack} />}
      {Icon && <Icon className="h-4 w-4 shrink-0 text-muted-foreground" aria-hidden="true" />}
      <h1 className="min-w-0 truncate text-sm font-semibold text-foreground" title={title}>{title}</h1>
      {description && <p className="sr-only">{description}</p>}
      {children}
    </WorkspaceTopToolbar>
    {below && <div className="px-3 pb-2">{below}</div>}
  </header>
}
