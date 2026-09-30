import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'

describe('Crew MCP tab parity with workflow', () => {
  it('shows This project / Platform connected shelves under the one place-MCP screen', () => {
    const crew = readFileSync('src/products/work/WorkIntegrationsPanel.tsx', 'utf8')
    const workflow = readFileSync('src/components/workflow/WorkflowCapabilitiesPanel.tsx', 'utf8')

    // Same information architecture as the workflow MCP tab: the project's
    // own selection first, then the shared platform registry, then the
    // browser for connecting new apps.
    expect(crew).toContain('This project')
    expect(crew).toContain('Platform connected')
    expect(crew).toContain('Shared with everyone. Tick one to let this project use it.')
    // One connect surface: the place-MCP screen. The old platform browser
    // ("Connect a new app") is gone so a Crew never shows two MCP UIs.
    expect(crew).not.toContain('Connect a new app')
    expect(crew).not.toContain('ConnectorsBrowser')
    expect(crew).toContain('<PlaceMcpSection')
    // Same shelf component as the workflow tab.
    expect(workflow).toContain('<ToolSelectionSection')
    expect(crew).toContain('<ToolSelectionSection')
  })

  it('keeps crew shelves server-level since the runtime ignores per-tool picks', () => {
    const crew = readFileSync('src/products/work/WorkIntegrationsPanel.tsx', 'utf8')
    const selection = readFileSync('src/components/ToolSelectionSection.tsx', 'utf8')

    expect(crew).toContain('hideToolDetails')
    expect(selection).toContain('hideToolDetails')
    // Workflow keeps the default (per-tool expansion); the opt-in must not
    // change its behavior.
    const workflow = readFileSync('src/components/workflow/WorkflowCapabilitiesPanel.tsx', 'utf8')
    expect(workflow).not.toContain('hideToolDetails')
  })
})
