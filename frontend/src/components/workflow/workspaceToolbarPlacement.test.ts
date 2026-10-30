import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'

describe('workflow workspace placement', () => {
  it('uses the context control for both Goals and Flows while preserving the Run-only Flow dashboard', () => {
    const toolbar = readFileSync('src/components/workflow/canvas/WorkflowToolbar.tsx', 'utf8')
    const layout = readFileSync('src/components/workflow/WorkflowLayout.tsx', 'utf8')
    expect(toolbar).toContain('<WorkspaceContextButton')
    expect(toolbar).not.toContain('WorkflowContextTools')
    expect(toolbar).not.toContain('<WorkspaceToolbarFrame')
    expect(toolbar).not.toContain('<ReportDocumentSwitcher')
    expect(layout).toContain("compactContext: workflowWorkspaceView === 'context'")
    expect(layout).toContain("relayMode && workspacePath && activeWorkflowAccess !== 'owner' && activeWorkflowAccess !== 'write'")
    expect(layout).toContain('defaultRelayDashboard dashboardConversation allowContentQuestions={false}')
  })

  it('uses Dashboard for first-time AgentWorks users while preserving saved views', () => {
    const store = readFileSync('src/stores/useWorkflowStore.ts', 'utf8')

    expect(store).toContain("normalizeCanvasViewId(getWorkflowStorageItem(LEGACY_CANVAS_VIEW_MODE_KEY)) ?? 'report'")
    expect(store).toContain("readWorkflowWorkspaceView(presetId) ?? (viewProduct === 'relays' ? 'flow' : 'report')")
    expect(store).toContain('saved?.lastCanvasView ?? loadLegacyWorkspaceViewByPreset()[presetId] ?? null')
    expect(store).toContain('return readWorkspaceViewPreference(workflowViewProduct(), presetId, normalizeWorkflowWorkspaceView')
  })
})
