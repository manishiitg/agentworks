import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'

describe('WorkWorkspaceToolbar', () => {
  it('keeps primary views visible and groups Files, Database and Costs under Ops', () => {
    const source = readFileSync('src/products/work/WorkWorkspacePane.tsx', 'utf8')
    const identity = readFileSync('src/products/work/WorkIdentityPanel.tsx', 'utf8')

    expect(source).not.toContain('label="Views"')
    expect(source).toContain('label="Ops"')
    // Ops and Setup are always open and show icons only (no labels).
    expect(source).toContain('label="Ops" open hideToggleWhenOpen')
    expect(source).toContain('label="Setup" open hideToggleWhenOpen')
    expect(source).toContain('label="Setup" open')

    expect(source).not.toContain("current === 'setup' ? null : 'setup'")
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
    expect(source).toContain("'Setup: identity and integrations'")
    // A Code adds a terminal to Ops (owner only; see showShell).
    expect(source).toContain("id: 'shell', label: 'Terminal'")
    // The toolbar only shows it when told to: forgetting this prop hid the Terminal button (2026-10-03).
    const surface = readFileSync('src/products/work/WorkSurface.tsx', 'utf8')
    expect(surface).toMatch(/<WorkWorkspaceToolbar[^>]*showShell=\{showShell\}/)
    // ...and the pane has to render the panel when it is chosen (the render line was lost in the same port).
    expect(source).toContain('<CodeShellPanel projectId={projectId} />')
    expect(source).toContain('<AutomationHubPanel')
    expect(source).not.toContain('botContent=')
    expect(source).toContain("productTriggerScope={enabledPanels?.has('triggers')")
    expect(identity).toContain('showAdditionalGroup')
  })

  it('binds the Work chat to acknowledged workspace view controls', () => {
    const source = readFileSync('src/products/work/WorkSurface.tsx', 'utf8')

    expect(source).toContain('useWorkspaceUIControl(activeSessionId ?? undefined, workUIAdapter)')
    expect(source).toContain("'data-ui-workspace': selected.workspacePath")
    expect(source).toContain("'data-ui-view': workPresentationView(workspaceView)")
    expect(source).toContain('data-ui-view-mounted')
    expect(source).toContain("report: 'dashboard'")
    expect(source).toContain("memory: 'memory'")
    expect(source).toContain("identity: 'identity'")
    expect(source).toContain("llm: 'identity'")
    expect(source).toContain("bots: 'mcp'")
    expect(source).toContain("email: 'mcp'")
    expect(source).toContain('landingContent={<WorkNewChatGuide product={product} sharedBy={')
    expect(source).toContain('This is the persistent conversation for this ${product.noun} project.')
  })


})
