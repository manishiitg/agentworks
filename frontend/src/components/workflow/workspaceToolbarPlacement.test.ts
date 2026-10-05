import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'

describe('workflow Ask AI placement', () => {
  it('keeps Ask AI inside every selected view instead of the global toolbar', () => {
    const toolbar = readFileSync('src/components/workflow/canvas/WorkflowToolbar.tsx', 'utf8')
    const host = readFileSync('src/components/workflow/canvas/WorkspaceViewHost.tsx', 'utf8')

    expect(toolbar).not.toContain('<AskAIButton')
    expect(host).not.toContain('data-ui-view-assistant')
    expect(host).toContain('headerAction={askAI(')
    expect(host).toContain('assistantControl={workspacePath ? (')
  })

  it('keeps Views and icon-only Setup expanded, with Ops collapsed by default', () => {
    const toolbar = readFileSync('src/components/workflow/canvas/WorkflowToolbar.tsx', 'utf8')

    expect(toolbar).not.toContain('label="Tools"')
    expect(toolbar).toContain('label="Views"')
    expect(toolbar).toContain('label="Ops"')
    expect(toolbar).toContain('const [operationsOpen, setOperationsOpen] = useState(false)')
    expect(toolbar).toContain('open={operationsOpen}')
    expect(toolbar).toContain('onToggle={() => setOperationsOpen(open => !open)}')
    expect(toolbar).toContain('label="Setup"')
    expect(toolbar).toContain('hideLabel')
    expect(toolbar.match(/hideToggleWhenOpen/g)).toHaveLength(2)
    expect(toolbar).not.toContain('openToolbarMenu')
    expect(toolbar).not.toContain('toggleToolbarMenu')
    expect(toolbar).toMatch(/label="Setup"[\s\S]*?hideToggleWhenOpen[\s\S]*?open/)
    expect(toolbar).toContain('<WorkspaceToolbarGroup')
    expect(toolbar).toContain('<ToolbarInlineItem')
    expect(toolbar).not.toContain('ToolbarPopoverGroup')
    expect(toolbar).not.toContain('ToolbarPopoverItem')
    expect(toolbar).not.toContain('role="menu"')
  })

  it('keeps report separate, Knowledge visible, Costs and Execution logs in Ops, and Playbooks in Setup', () => {
    const toolbar = readFileSync('src/components/workflow/canvas/WorkflowToolbar.tsx', 'utf8')

    // Browser stays visible beside Pulse; Plan lives in Ops (a Relay keeps its Graph in Views).
    expect(toolbar).toContain("PRIMARY_TOOLBAR_VIEW_IDS = new Set<WorkspaceViewId>(['pulse', 'browser'])")
    expect(toolbar).toContain("new Set<WorkspaceViewId>(['flow', 'workshop', 'knowledge', 'costs', 'execution-logs', 'files', 'backup', 'publish', 'notify'])")
    expect(toolbar).toContain("playbooks: 'Playbooks'")
    expect(toolbar).toContain("mcp: 'Integrations'")
    expect(toolbar).toContain("identity: 'Identity'")
    expect(toolbar).not.toContain("bots: 'Bots'")
    expect(toolbar).not.toContain("email: 'Gmail'")
    expect(toolbar).not.toContain("secrets: 'Secrets'")
    expect(toolbar).not.toContain("folders: 'Folders'")
    expect(toolbar).toContain("view.toolbarGroup === 'capabilities'")
    expect(toolbar).toContain('PRIMARY_TOOLBAR_VIEW_IDS.has(view.id)')
    expect(toolbar).toContain("relayMode ? view.id === 'flow'")
    expect(toolbar.indexOf('<ReportDocumentSwitcher')).toBeLessThan(toolbar.indexOf('aria-label={pendingDecisionCount'))
    expect(toolbar.indexOf('<WorkflowActivityButton')).toBeGreaterThan(toolbar.indexOf('aria-label={pendingDecisionCount'))
    expect(toolbar.indexOf('<WorkflowActivityButton')).toBeLessThan(toolbar.indexOf('workspaceViewDefinitions.map'))
  })

  it('uses Dashboard for first-time AgentWorks users while preserving saved views', () => {
    const store = readFileSync('src/stores/useWorkflowStore.ts', 'utf8')

    expect(store).toContain("normalizeCanvasViewId(getWorkflowStorageItem(LEGACY_CANVAS_VIEW_MODE_KEY)) ?? 'report'")
    expect(store).toContain("loadLegacyWorkspaceViewByPreset()[presetId] ?? 'report'")
    expect(store).toContain('persistedUIState.workflowWorkspaceView ??')
  })
})
