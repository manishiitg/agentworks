import {
  Globe,
  CheckCircle2,
  Clock3,
  AlertTriangle,
  XCircle,
  type LucideIcon
} from 'lucide-react'

// Shared publish-status presentation helpers used by both the WorkflowPublishView
// and the toolbar status dot, so the two never drift. Mirrors backupStatus.ts.

export const formatPublishStateLabel = (state?: string): string => {
  switch (state) {
    case 'not_configured':
      return 'Not configured'
    case 'configured_not_verified':
      return 'Not published yet'
    case 'publishing':
      return 'Publishing'
    case 'published':
      return 'Published'
    case 'failed':
      return 'Failed'
    default:
      return state || 'Unknown'
  }
}

export interface PublishStateVisual {
  Icon: LucideIcon
  badge: string
  icon: string
}

export const getPublishStateVisual = (state?: string): PublishStateVisual => {
  switch (state) {
    case 'published':
      return {
        Icon: CheckCircle2,
        badge: 'border-emerald-500/30 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300',
        icon: 'text-emerald-600 dark:text-emerald-300'
      }
    case 'publishing':
      return {
        Icon: Clock3,
        badge: 'border-sky-500/30 bg-sky-500/10 text-sky-700 dark:text-sky-300',
        icon: 'text-sky-600 dark:text-sky-300'
      }
    case 'configured_not_verified':
      return {
        Icon: AlertTriangle,
        badge: 'border-amber-500/30 bg-amber-500/10 text-amber-700 dark:text-amber-300',
        icon: 'text-amber-600 dark:text-amber-300'
      }
    case 'failed':
      return {
        Icon: XCircle,
        badge: 'border-destructive/30 bg-destructive/10 text-destructive',
        icon: 'text-destructive'
      }
    default:
      return {
        Icon: Globe,
        badge: 'border-border bg-muted text-muted-foreground',
        icon: 'text-muted-foreground'
      }
  }
}
