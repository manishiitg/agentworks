import { useEffect, useRef } from 'react'
import { hasSavedWorkflowWorkspaceView, useWorkflowStore } from '../../../stores/useWorkflowStore'
import { useGlobalPresetStore } from '../../../stores/useGlobalPresetStore'
import type { WorkspaceViewId } from '../workspaceViews'

// Decide once when a workflow opens. A later background decision should not
// pull the user away from the view they chose, and navigation while the first
// count request is in flight takes precedence over this default. A view the
// user saved for this workflow (restored on refresh, PLAT-551) also wins: the
// default is only for a workflow with no remembered view; the toolbar badge
// still shows pending decisions.
export function useDefaultHumanActionsView(workspacePath: string | null | undefined, loaded: boolean, count: number): void {
  const handledFor = useRef<string | null>(null)
  const entryView = useRef<{ workspacePath: string; view: WorkspaceViewId | null } | null>(null)
  useEffect(() => {
    if (workspacePath) entryView.current = { workspacePath, view: useWorkflowStore.getState().workflowWorkspaceView }
  }, [workspacePath])
  useEffect(() => {
    if (!workspacePath || !loaded || handledFor.current === workspacePath) return
    handledFor.current = workspacePath
    const presetId = useGlobalPresetStore.getState().activePresetIds.workflow
    if (presetId && hasSavedWorkflowWorkspaceView(presetId)) return
    if (count > 0 && entryView.current?.workspacePath === workspacePath
      && useWorkflowStore.getState().workflowWorkspaceView === entryView.current.view) {
      useWorkflowStore.getState().openWorkspaceView('human-actions')
    }
  }, [workspacePath, loaded, count])
}
