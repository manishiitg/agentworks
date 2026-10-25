import type { OrgDashboardNotification, OrgDashboardRouteNotifications } from '../services/api-types'

interface WorkflowActivitySummary {
  workspace_path: string
  run_summary?: OrgDashboardNotification
  recent?: OrgDashboardNotification[]
  by_route?: OrgDashboardRouteNotifications[]
}

interface WorkflowPendingInput {
  workspace_path: string
  created_at?: string
  updated_at?: string
}

const DAY_MS = 24 * 60 * 60 * 1000

function isRecentTimestamp(value: string | undefined, now: number, maxAgeMs: number): boolean {
  if (!value) return false
  const timestamp = Date.parse(value)
  return Number.isFinite(timestamp) && timestamp <= now && timestamp >= now - maxAgeMs
}

export function workflowHasActivity(
  workspacePath: string,
  notifications: { success?: boolean; workflows?: WorkflowActivitySummary[] },
  pendingInputs: Array<{ workspace_path: string }>,
): boolean {
  if (pendingInputs.some(input => input.workspace_path === workspacePath)) return true

  const workflow = notifications.workflows?.find(item => item.workspace_path === workspacePath)
  if (!workflow) return false
  if (workflow.run_summary || workflow.recent?.length) return true
  return workflow.by_route?.some(route => route.run_summary) ?? false
}

export function workflowHasRecentActivity(
  workspacePath: string,
  notifications: { workflows?: WorkflowActivitySummary[] },
  pendingInputs: WorkflowPendingInput[],
  now = Date.now(),
  maxAgeMs = DAY_MS,
): boolean {
  if (pendingInputs.some(input => input.workspace_path === workspacePath &&
    (isRecentTimestamp(input.updated_at, now, maxAgeMs) || isRecentTimestamp(input.created_at, now, maxAgeMs)))) return true

  const workflow = notifications.workflows?.find(item => item.workspace_path === workspacePath)
  if (!workflow) return false
  const summaries = [
    workflow.run_summary,
    ...(workflow.recent || []),
    ...(workflow.by_route || []).flatMap(route => [route.run_summary]),
  ]
  return summaries.some(summary => isRecentTimestamp(summary?.created_at, now, maxAgeMs))
}
