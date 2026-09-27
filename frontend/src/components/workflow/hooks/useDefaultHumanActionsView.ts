import { useEffect, useRef } from 'react'
import { useWorkflowStore } from '../../../stores/useWorkflowStore'
import type { WorkspaceViewId } from '../workspaceViews'

// Decide once when a workflow opens. A later background decision should not
// pull the user away from the view they chose, and navigation while the first
// count request is in flight takes precedence over this default.
export function useDefaultHumanActionsView(workspacePath: string | null | undefined, loaded: boolean, count: number): void {
  const handledFor = useRef<string | null>(null)
  const entryView = useRef<{ workspacePath: string; view: WorkspaceViewId | null } | null>(null)
  useEffect(() => {
    if (workspacePath) entryView.current = { workspacePath, view: useWorkflowStore.getState().workflowWorkspaceView }
  }, [workspacePath])
  useEffect(() => {
    if (!workspacePath || !loaded || handledFor.current === workspacePath) return
    handledFor.current = workspacePath
    if (count > 0 && entryView.current?.workspacePath === workspacePath
      && useWorkflowStore.getState().workflowWorkspaceView === entryView.current.view) {
      useWorkflowStore.getState().openWorkspaceView('human-actions')
    }
  }, [workspacePath, loaded, count])
}
