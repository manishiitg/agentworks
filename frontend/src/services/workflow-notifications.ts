import type { WorkflowNotificationAccountChannelInfo, WorkflowNotificationDestinationInfo, WorkflowNotificationState } from './api-types'
import { agentApi } from './api'

export type { WorkflowNotificationState } from './api-types'

export interface WorkflowNotificationInfo {
  breakAlerts: boolean
  scopeLabel: string
  effectiveState: WorkflowNotificationState
  slackWebhook: WorkflowNotificationDestinationInfo
  gmail: WorkflowNotificationAccountChannelInfo | null
  // Per-workflow content preferences from workflow.json notifications.
  runSummaryInstructions: string
  runSummaryChannels: string[]
  // Who each summary is emailed to. Empty means summary email delivery is off.
  runSummaryRecipients: string[]
  /** One sender for this workflow's Notify emails; empty inherits the account default. */
  gmailConnectionId: string
  /** Which Gmail account(s) each summary sends FROM; empty means inherit. */
  runSummaryGmailConnectionIds: string[]
  // Slack channels per summary, as webhook secret names (one webhook = one channel).
  runSummarySlackWebhooks: string[]
  excludeChannels: string[]
  blockRecipients: string[]
}

export async function loadWorkflowNotificationInfo(workspacePath: string): Promise<WorkflowNotificationInfo> {
  const response = await agentApi.getWorkflowNotifications(workspacePath)
  const slackWebhook = response.destinations.find(destination => destination.type === 'slack_webhook') || {
    id: 'workflow-slack-webhook',
    type: 'slack_webhook',
    label: 'Workflow Slack webhook',
    state: response.effective_state,
  }
  return {
    breakAlerts: response.break_alerts !== false,
    scopeLabel: response.scope_label || response.workflow_label || workspacePath.split('/').filter(Boolean).pop() || 'Workflow',
    effectiveState: response.effective_state,
    slackWebhook,
    gmail: response.account_channels.find(channel => channel.id === 'gmail') || null,
    runSummaryInstructions: response.run_summary_instructions || '',
    runSummaryChannels: response.run_summary_channels || [],
    runSummaryRecipients: response.run_summary_recipients || [],
    gmailConnectionId: response.gmail_connection_id || '',
    runSummaryGmailConnectionIds: response.run_summary_gmail_connection_ids || [],
    runSummarySlackWebhooks: response.run_summary_slack_webhooks || [],
    excludeChannels: response.exclude_channels || [],
    blockRecipients: response.block_recipients || [],
  }
}
