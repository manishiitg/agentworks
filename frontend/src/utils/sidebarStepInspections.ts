import type { ExecutionInspectionTarget, PollingEvent, SessionExecutionTreeResponse, WorkspaceStepInspection } from '../services/api-types'
import { inspectionKey, readInspectionTarget } from './stepInspection'
import { isInspectableBackgroundActivity } from './terminalEventTranscript'

export interface SidebarStepInspection {
  target: ExecutionInspectionTarget
  status?: string
}

/** Only explicit receipts for this exact parent and workspace are navigable. */
export function sidebarStepInspections(
  sessionId: string,
  workspace: string,
  tree?: SessionExecutionTreeResponse,
  saved: ExecutionInspectionTarget[] = [],
  events: PollingEvent[] = [],
): SidebarStepInspection[] {
  const items = new Map<string, SidebarStepInspection>()
  const add = (value: unknown, status?: string) => {
    const target = readInspectionTarget(value)
    if (!target || target.execution_kind === 'agent_item' || target.session_id !== sessionId || target.workspace_path !== workspace) return false
    items.set(inspectionKey(target), { target, status })
    return true
  }
  saved.forEach(target => add(target))
  // Durable chat cards carry the same receipt, including completed dispatches
  // that no longer exist in the server's in-memory execution registry.
  for (const event of events) {
    if ((event.session_id && event.session_id !== sessionId) || !isInspectableBackgroundActivity(event)) continue
    const outer = event.data as { data?: unknown }
    const payload = (outer?.data && typeof outer.data === 'object' ? outer.data : outer) as Record<string, unknown>
    const receipt = (payload?.metadata as Record<string, unknown> | undefined)?.inspection
    const status = event.type === 'background_agent_started' ? 'running' : event.type === 'background_agent_terminated' ? 'canceled' : typeof payload?.status === 'string' ? payload.status : 'completed'
    add(receipt, status)
  }
  if (tree?.session_id === sessionId && tree.root?.session_id === sessionId) {
    const pending = [tree.root]
    const seen = new Set<string>()
    while (pending.length && seen.size < 1000) {
      const node = pending.shift()!
      if (node.session_id !== sessionId || seen.has(node.execution_id)) continue
      seen.add(node.execution_id)
      // Tasks inside a step remain in its existing inspector, not siblings
      // competing with the step in global navigation.
      if (!add(node.inspection, node.status)) pending.push(...node.children || [])
    }
  }
  return [...items.values()].sort((a, b) =>
    Number(b.status === 'running') - Number(a.status === 'running') ||
    a.target.started_at.localeCompare(b.target.started_at) ||
    a.target.execution_id.localeCompare(b.target.execution_id))
}

export function sidebarInspectionReceipts(session: string, workspace: string, receipts: WorkspaceStepInspection[] = []): SidebarStepInspection[] {
  return receipts.flatMap(receipt => {
    const target = readInspectionTarget(receipt.inspection)
    return target && target.execution_kind !== 'agent_item' && target.session_id === session && target.workspace_path === workspace &&
      receipt.parent.session_id === session && receipt.parent.workspace_path === workspace
      ? [{ target, status: receipt.status }] : []
  })
}
