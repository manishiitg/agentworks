import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'

describe('WorkWorkspaceToolbar', () => {
  it('keeps primary views visible and groups Files, Database and Costs under Ops', () => {
    const source = readFileSync('src/products/work/WorkWorkspacePane.tsx', 'utf8')
    const identity = readFileSync('src/products/work/WorkIdentityPanel.tsx', 'utf8')

    expect(source).not.toContain('label="Views"')
    expect(source).toContain('label="Ops"')
    expect(source).toContain("open={openGroup === 'ops'}")
    expect(source).toContain('label="Setup" open')
    expect(source).not.toContain("openGroup === 'setup'")
    expect(source).not.toContain("current === 'setup' ? null : 'setup'")
    expect(source).toContain("OPS_BUTTONS.some(item => item.id === view) ? 'ops'")
    expect(source).toContain("visibleViews.filter(item => item.id !== 'dashboard').map")
    expect(source.indexOf("const OPS_BUTTONS")).toBeLessThan(source.indexOf("id: 'files', label: 'Files'"))
    expect(source.indexOf("const OPS_BUTTONS")).toBeLessThan(source.indexOf("id: 'database', label: 'Database'"))
    expect(source.indexOf("const OPS_BUTTONS")).toBeLessThan(source.indexOf("id: 'costs', label: 'Costs and usage'"))
    expect(source.indexOf("id: 'costs', label: 'Costs and usage'")).toBeLessThan(source.indexOf('const SETUP_BUTTONS'))
    expect(source).not.toContain("id: 'history'")
    expect(source).toContain("id: 'schedules', label: 'Automation'")
    expect(source).toContain("id: 'memory', label: 'Memory'")
    expect(source).toContain("id: 'plan', label: 'Plan'")
    expect(source).toContain("id: 'identity', label: 'Identity'")
    expect(source).toContain("id: 'mcp', label: 'Integrations'")
    expect(source).toContain('title="Setup: identity and integrations"')
    expect(source).toContain('<AutomationHubPanel')
    expect(source).not.toContain('botContent=')
    expect(source).toContain("productTriggerScope={enabledPanels?.has('triggers')")
    expect(identity).toContain('showAdditionalGroup')
  })

  it('binds the Work chat to acknowledged workspace view controls', () => {
    const source = readFileSync('src/products/work/WorkSurface.tsx', 'utf8')

    expect(source).toContain('useWorkspaceUIControl(activeSessionId ?? undefined, workUIAdapter)')
    expect(source).toContain('data-ui-workspace={selected.workspacePath}')
    expect(source).toContain('data-ui-view={workPresentationView(workspaceView)}')
    expect(source).toContain('data-ui-view-mounted')
    expect(source).toContain("report: 'dashboard'")
    expect(source).toContain("memory: 'memory'")
    expect(source).toContain("identity: 'identity'")
    expect(source).toContain("llm: 'identity'")
    expect(source).toContain("bots: 'mcp'")
    expect(source).toContain("email: 'mcp'")
    expect(source).toContain('landingContent={<WorkNewChatGuide sharedBy={')
    expect(source).toContain('This is the persistent conversation for this Crew project.')
  })

  it('keeps a saved Crew view ahead of the content-based landing view', () => {
    const source = readFileSync('src/products/work/WorkSurface.tsx', 'utf8')

    expect(source).toContain("const WORK_VIEW_PREFERENCE_KEY = 'work_workspace_view'")
    expect(source).toContain("if (saved === 'history') return 'schedules'")
    expect(source).toContain('if (saved && saved in WORK_UI_PRESENTATION_VIEWS) return WORK_UI_PRESENTATION_VIEWS[saved as WorkUIPresentationView]')
    expect(source).toContain('writeWorkWorkspaceView(selected?.id, view)')
    expect(source).toContain('const savedView = readWorkWorkspaceView(selected?.id)')
    expect(source).toContain('loadWorkspaceLandingView(landingWorkspacePath, { dashboardAllowed })')
    expect(source).toContain("savedView ?? 'identity'")
  })
})
