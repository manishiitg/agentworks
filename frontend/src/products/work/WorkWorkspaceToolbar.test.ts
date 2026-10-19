import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'

describe('WorkWorkspaceToolbar', () => {
  it('binds the Work chat to acknowledged workspace view controls', () => {
    const source = readFileSync('src/products/work/WorkSurface.tsx', 'utf8')

    expect(source).toContain('useWorkspaceUIControl(activeSessionId ?? undefined, workUIAdapter)')
    expect(source).toContain("'data-ui-workspace': selected.workspacePath")
    expect(source).toContain("'data-ui-view': workPresentationView(workspaceView, localCodeSession)")
    expect(source).toContain('data-ui-view-mounted')
    expect(source).toContain("report: 'dashboard'")
    expect(source).toContain("memory: 'memory'")
    expect(source).toContain("identity: 'identity'")
    expect(source).toContain("llm: 'identity'")
    expect(source).toContain("email: 'mcp'")
    // Preserve the Terminal wiring guard without the retired Ops-group layout.
    expect(source).toMatch(/<WorkWorkspaceToolbar[^>]*showShell=\{showShell\}/)
    const pane = readFileSync('src/products/work/WorkWorkspacePane.tsx', 'utf8')
    expect(pane).toContain("view === 'shell' && showShell")
    expect(pane).toContain('<CodeShellPanel projectId={projectId} />')
    expect(source).toContain('<WorkNewChatGuide product={product}')
  })


})
