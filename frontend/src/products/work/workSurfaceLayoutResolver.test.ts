import { describe, expect, it } from 'vitest'
import { CONTEXT_PANE_COLUMN, SIDE_PANE_COLUMN } from '../../components/workflow/workspaceLayoutResolver'
import { isCompactSidePaneView, resolveWorkSurfaceLayout } from './workSurfaceLayoutResolver'

describe('resolveWorkSurfaceLayout', () => {
  it('uses compact side pane for all non-Dashboard, non-Workflow, non-Context views', () => {
    for (const view of ['browser', 'files', 'shell', 'memory', 'database', 'costs', 'schedules', 'history', 'identity', 'mcp']) {
      expect(isCompactSidePaneView(view)).toBe(true)
    }
    for (const view of ['context', 'dashboard', 'workflow']) {
      expect(isCompactSidePaneView(view)).toBe(false)
    }
  })

  it('uses optimal compact width for compact side panes by default', () => {
    const layout = resolveWorkSurfaceLayout({ chatOpen: true, panelOpen: true, splitRatio: 0.5, compactSidePane: true })
    expect((layout.gridStyle as Record<string, string>)['--work-split-columns']).toBe(`minmax(240px, 1fr) ${SIDE_PANE_COLUMN}`)
  })

  it('honors a dragged sidePaneWidth px override when compactSidePane is active', () => {
    const layout = resolveWorkSurfaceLayout({ chatOpen: true, panelOpen: true, splitRatio: 0.5, compactSidePane: true, sidePaneWidth: 512 })
    expect((layout.gridStyle as Record<string, string>)['--work-split-columns']).toBe('minmax(240px, 1fr) 512px')
  })

  it('keeps compactContext width for the menu card and splitRatio for dashboards', () => {
    const ctx = resolveWorkSurfaceLayout({ chatOpen: true, panelOpen: true, splitRatio: 0.5, compactContext: true })
    expect((ctx.gridStyle as Record<string, string>)['--work-split-columns']).toBe(`minmax(240px, 1fr) ${CONTEXT_PANE_COLUMN}`)

    const dash = resolveWorkSurfaceLayout({ chatOpen: true, panelOpen: true, splitRatio: 0.4 })
    expect((dash.gridStyle as Record<string, string>)['--work-split-columns']).toBe('minmax(240px, 0.4fr) minmax(240px, 0.6fr)')
  })
})
