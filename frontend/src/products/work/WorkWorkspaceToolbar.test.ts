import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'

describe('WorkWorkspaceToolbar', () => {
  it('wraps the toolbar instead of clipping it on phone widths', () => {
    const frame = readFileSync('src/components/workspace/WorkspaceToolbarFrame.tsx', 'utf8')
    const source = readFileSync('src/products/work/WorkWorkspacePane.tsx', 'utf8')
    // Below md the icon row wraps onto a second line: at 390 px the fixed
    // row ran to ~436 px and Integrations / Hide toolbar were unreachable.
    expect(source).toContain('max-md:flex-wrap')
    expect(frame).toContain('max-md:flex-wrap')
  })

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
    expect(source).toContain("bots: 'mcp'")
    expect(source).toContain("email: 'mcp'")
  })


})
