import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'

// Every right-pane header stays fixed while its content scrolls (the Pulse
// structure): the header renders outside the overflow-y-auto container,
// never inside a scrolling root and never via sticky.
describe('fixed pane headers', () => {
  it('keeps Setup section headers outside the scroll container', () => {
    const panel = readFileSync('src/components/workflow/WorkflowCapabilitiesPanel.tsx', 'utf8')
    const section = panel.split('\n').find(line => line.includes('<section className='))
    expect(section).toBeDefined()
    expect(section).not.toContain('overflow-y-auto')
    const content = panel.split('\n').find(line => line.includes("(section === 'mcp' || section === 'identity') ?"))
    expect(content).toBeDefined()
    expect(content).toContain("'min-h-0 flex-1 overflow-y-auto'")
    expect(content).not.toContain("'shrink-0'")
  })

  it('uses the view icon in the Workshop header, not the agent avatar', () => {
    const hub = readFileSync('src/components/automation/AutomationHubPanel.tsx', 'utf8')
    expect(hub).toContain('icon={Zap}')
    expect(hub).not.toContain('<EntityIdentityIcon')
  })

  it('titles the Workshop header with the view name and opens on Schedules', () => {
    const hub = readFileSync('src/components/automation/AutomationHubPanel.tsx', 'utf8')
    expect(hub).toContain("title={relayMode ? 'Relay' : 'Automation'}")
    expect(hub).not.toContain('title={entityLabel}')
    expect(hub).toContain("initialSection = 'schedules'")
  })

  it('keeps the Webhooks header outside the scroll container', () => {
    const view = readFileSync('src/components/workflow/WorkflowAPITriggersView.tsx', 'utf8')
    const root = view.split('\n').find(line => line.includes('flex h-full min-h-0 w-full max-w-none flex-col'))
    expect(root).toBeDefined()
    expect(root).toContain('flex-col')
    expect(root).not.toContain('overflow-y-auto')
    expect(view).toContain('min-h-0 flex-1 overflow-x-hidden overflow-y-auto')
    const headerBlock = view.slice(view.indexOf('<WorkspaceViewHeader'), view.indexOf('/>', view.indexOf('<WorkspaceViewHeader')))
    expect(headerBlock).not.toMatch(/^\s*sticky\s*$/m)
  })
})
