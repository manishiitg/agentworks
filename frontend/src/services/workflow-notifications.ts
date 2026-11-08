import type { WorkflowNotificationAccountChannelInfo, WorkflowNotificationState } from './api-types'
import { agentApi } from './api'

export type { WorkflowNotificationState } from './api-types'

export interface WorkflowNotificationInfo {
  breakAlerts: boolean
  scopeLabel: string
  effectiveState: WorkflowNotificationState
  gmail: WorkflowNotificationAccountChannelInfo | null
  /** One sender for this workflow's Notify emails; empty inherits the account default. */
  gmailConnectionId: string
  excludeChannels: string[]
}

export async function loadWorkflowNotificationInfo(workspacePath: string): Promise<WorkflowNotificationInfo> {
  const response = await agentApi.getWorkflowNotifications(workspacePath)
  return {
    breakAlerts: response.break_alerts !== false,
    scopeLabel: response.scope_label || response.workflow_label || workspacePath.split('/').filter(Boolean).pop() || 'Workflow',
    effectiveState: response.effective_state,
    gmail: response.account_channels.find(channel => channel.id === 'gmail') || null,
    gmailConnectionId: response.gmail_connection_id || '',
    excludeChannels: response.exclude_channels || [],
  }
}
