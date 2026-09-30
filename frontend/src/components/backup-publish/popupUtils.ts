import type {
  WorkflowBackupDestination,
  WorkflowBackupDestinationStatus,
  WorkflowPublishDestination,
  WorkflowPublishDestinationStatus,
} from '../../services/api-types'

/** Per-strategy Ask AI wiring. The body builds one message per strategy row. */
export interface StrategyAskContext {
  workspacePath: string | null
  /** e.g. "back up this workflow with" / "publish this workflow to" */
  strategyVerb: string
}

export const formatRelativeTime = (dateStr?: string): string => {
  if (!dateStr) return 'Never'
  const date = new Date(dateStr)
  if (Number.isNaN(date.getTime())) return 'Unknown'
  const diffMs = Date.now() - date.getTime()
  const diffMin = Math.floor(diffMs / 60000)
  if (diffMin < 1) return 'just now'
  if (diffMin < 60) return `${diffMin}m ago`
  const diffHr = Math.floor(diffMin / 60)
  if (diffHr < 24) return `${diffHr}h ago`
  const diffDay = Math.floor(diffHr / 24)
  if (diffDay < 30) return `${diffDay}d ago`
  return date.toLocaleDateString()
}

export const extractErrorMessage = (err: unknown, fallback: string): string => {
  const maybe = err as { response?: { data?: { error?: string; message?: string } | string }; message?: string }
  const data = maybe.response?.data
  if (typeof data === 'string') return data
  return data?.message || data?.error || maybe.message || fallback
}

export const coverageText = (items?: string[]): string => {
  if (!items || items.length === 0) return 'Coverage not specified'
  return items.join(', ')
}

export const backupDestinationTitle = (destination: WorkflowBackupDestination): string => {
  if (destination.repo) return destination.repo
  if (destination.bucket) return destination.prefix ? `${destination.bucket}/${destination.prefix}` : destination.bucket
  return destination.id || destination.provider || destination.type
}

export const publishDestinationTitle = (destination: WorkflowPublishDestination): string =>
  destination.site || destination.public_base_url || destination.url || destination.id || destination.provider

export const findBackupDestinationStatus = (
  statuses: WorkflowBackupDestinationStatus[] | undefined,
  destination: WorkflowBackupDestination
): WorkflowBackupDestinationStatus | undefined => statuses?.find(status => status.id === destination.id)

export const findPublishDestinationStatus = (
  statuses: WorkflowPublishDestinationStatus[] | undefined,
  destination: WorkflowPublishDestination
): WorkflowPublishDestinationStatus | undefined => statuses?.find(status => status.id === destination.id)
