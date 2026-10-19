import type { WorkflowBuilderSessionResponse } from '../services/api-types'
import type { ChatTab } from '../stores/useChatStore'

export type WorkflowBuilderBindings = Record<string, WorkflowBuilderSessionResponse | undefined>

/** UI projection only. The server owns both the current chat and blank drafts. */
export function isCurrentWorkflowBuilderTab(tab: ChatTab | undefined, binding: WorkflowBuilderSessionResponse | undefined): boolean {
  return !!tab?.sessionId && binding?.sessions?.[tab.sessionId]?.is_builder === true &&
    tab.metadata?.isViewOnly !== true && tab.metadata?.isExecutionRun !== true
}
