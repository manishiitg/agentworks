// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import { usePanelSwitcherStore, useRegisterPanelSwitcher, type PanelSwitcherPanel } from './usePanelSwitcherStore'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

it('refreshes searchable section text without changing panel IDs, and releases its own registration', async () => {
  const host = document.createElement('div')
  const root = createRoot(host)
  const oldOpen = vi.fn()
  const newOpen = vi.fn()
  function Registration({ panels, open }: { panels: PanelSwitcherPanel[]; open: typeof oldOpen }) {
    useRegisterPanelSwitcher('work', panels, open)
    return null
  }
  const panel = (label: string, keywords: string): PanelSwitcherPanel[] => [{ id: 'connections', label: 'Connections', sections: [{ id: 'mcp', label, keywords }] }]
  try {
    await act(async () => root.render(<Registration panels={panel('Apps', 'tools')} open={oldOpen} />))
    await act(async () => root.render(<Registration panels={panel('MCP servers', 'integrations')} open={newOpen} />))
    const entry = usePanelSwitcherStore.getState().entries.work!
    expect(entry.panels[0].sections?.[0]).toMatchObject({ label: 'MCP servers', keywords: 'integrations' })
    entry.open('connections', 'mcp')
    expect(newOpen).toHaveBeenCalledWith('connections', 'mcp')
    expect(oldOpen).not.toHaveBeenCalled()
  } finally {
    await act(async () => root.unmount())
    expect(usePanelSwitcherStore.getState().entries.work).toBeUndefined()
  }
})
