import {
  CheckCircle2,
  Clock3,
  AlertTriangle,
  XCircle,
  Info,
  type LucideIcon
} from 'lucide-react'

export const formatBackupStateLabel = (state?: string): string => {
  switch (state) {
    case 'loading':
      return 'Checking status'
    case 'not_configured':
      return 'Not configured'
    case 'local_only':
      return 'Local only'
    case 'configured_not_verified':
      return 'Needs verification'
    case 'running':
      return 'Running'
    case 'healthy':
      return 'Healthy'
    case 'partial':
      return 'Partial'
    case 'failed':
      return 'Failed'
    case 'skipped':
      return 'Skipped'
    default:
      return state || 'Unknown'
  }
}

export interface BackupStateVisual {
  Icon: LucideIcon
  badge: string
  icon: string
}

export const getBackupStateVisual = (state?: string): BackupStateVisual => {
  switch (state) {
    case 'not_configured':
    case 'local_only':
      return {
        Icon: AlertTriangle,
        badge: 'border-red-500/30 bg-red-500/10 text-red-700 dark:text-red-300',
        icon: 'text-red-600 dark:text-red-300'
      }
    case 'healthy':
      return {
        Icon: CheckCircle2,
        badge: 'border-emerald-500/30 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300',
        icon: 'text-emerald-600 dark:text-emerald-300'
      }
    case 'running':
      return {
        Icon: Clock3,
        badge: 'border-sky-500/30 bg-sky-500/10 text-sky-700 dark:text-sky-300',
        icon: 'text-sky-600 dark:text-sky-300'
      }
    case 'partial':
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
        Icon: Info,
        badge: 'border-border bg-muted text-muted-foreground',
        icon: 'text-muted-foreground'
      }
  }
}
