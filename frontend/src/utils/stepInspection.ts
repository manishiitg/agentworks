import type { ExecutionInspectionTarget } from '../services/api-types'
import { useChatStore, type ChatTab } from '../stores/useChatStore'

export function inspectionKey(target: ExecutionInspectionTarget): string {
 return JSON.stringify([target.workspace_path, target.session_id, target.execution_id])
}

export function readInspectionTarget(value: unknown): ExecutionInspectionTarget | null {
 if (!value || typeof value !== 'object') return null
 const target = value as ExecutionInspectionTarget
 if (!target.workspace_path?.startsWith('Workflow/') || !target.step_id || (target.workflow_kind && target.workflow_kind !== 'workflow') || (target.execution_kind && !['workflow_step', 'agent', 'scripted_step', 'agent_item'].includes(target.execution_kind))) return null
 return ['workspace_path', 'session_id', 'execution_id'].every(key => typeof target[key as keyof ExecutionInspectionTarget] === 'string' && !!target[key as keyof ExecutionInspectionTarget]) ? target : null
}

export function isWorkflowInspectionTab(tab: ChatTab | undefined | null): boolean {
 return tab?.metadata?.mode === 'workflow' && (!tab.metadata.agentProfileId || ['goals','workflow'].includes(tab.metadata.agentProfileId))
}

/** Only a card belonging to the selected parent chat may open an inspector. */
export function openStepInspection(target: ExecutionInspectionTarget): boolean {
 const chat = useChatStore.getState()
 const tab = chat.getActiveTab()
 if (!isWorkflowInspectionTab(tab) || !readInspectionTarget(target) || !tab || tab.sessionId !== target.session_id) return false
 const previous = tab.metadata?.stepInspections?.items ?? []
 const key = inspectionKey(target)
 const items = previous.some(item => inspectionKey(item) === key) ? previous : [...previous, target]
 chat.setTabMetadata(tab.tabId, { stepInspections: { items, activeId: key } })
 return true
}
