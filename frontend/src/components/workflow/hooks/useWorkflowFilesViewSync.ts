import { useEffect, useRef } from 'react'
import { isPreviewView, type CanvasViewId, type WorkspaceViewId } from '../workspaceViews'

interface Options {
  enabled: boolean
  activePresetId: string | null
  restoredPresetId: string | null
  workspaceMinimized: boolean
  workflowWorkspaceView: WorkspaceViewId | null
  lastCanvasView: CanvasViewId
  showWorkspacePane: boolean
  setWorkspaceMinimized: (value: boolean) => void
  setShowWorkspacePane: (value: boolean) => void
  setWorkflowWorkspaceView: (value: WorkspaceViewId | null) => void
}

export function useWorkflowFilesViewSync(options: Options): void {
  const { enabled, activePresetId, restoredPresetId, workspaceMinimized,
    workflowWorkspaceView, lastCanvasView, showWorkspacePane,
    setWorkspaceMinimized, setShowWorkspacePane, setWorkflowWorkspaceView } = options
  const previous = useRef<{ preset: string; minimized: boolean } | null>(null)
  useEffect(() => {
    if (!enabled || !activePresetId || restoredPresetId !== activePresetId) {
      previous.current = null
      return
    }
    const old = previous.current
    previous.current = { preset: activePresetId, minimized: workspaceMinimized }
    if (!old || old.preset !== activePresetId) {
      setWorkspaceMinimized((workflowWorkspaceView ?? lastCanvasView) !== 'files')
      return
    }
    if (old.minimized === workspaceMinimized) return
    if (!workspaceMinimized && !isPreviewView(workflowWorkspaceView) && (workflowWorkspaceView !== 'files' || !showWorkspacePane)) {
      setShowWorkspacePane(true)
      setWorkflowWorkspaceView('files')
    } else if (workspaceMinimized && workflowWorkspaceView === 'files') {
      setWorkflowWorkspaceView(lastCanvasView)
    }
  }, [enabled, activePresetId, restoredPresetId,
    workspaceMinimized, workflowWorkspaceView, lastCanvasView,
    showWorkspacePane, setWorkspaceMinimized, setShowWorkspacePane,
    setWorkflowWorkspaceView])
}
