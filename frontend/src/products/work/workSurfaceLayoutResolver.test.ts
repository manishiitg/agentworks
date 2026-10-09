import { describe, expect, it } from 'vitest'
import { MOBILE_PREVIEW_PANE_COLUMN } from '../../components/workflow/workspaceLayoutResolver'
import { resolveWorkSurfaceLayout } from './workSurfaceLayoutResolver'

describe('resolveWorkSurfaceLayout', () => {
  it('splits chat and panel with the configured ratio', () => {
    const layout = resolveWorkSurfaceLayout({ chatOpen: true, panelOpen: true, splitRatio: 0.4 })
    expect(layout.showChat).toBe(true)
    expect(layout.showPanel).toBe(true)
    expect(layout.showDivider).toBe(true)
    expect(layout.gridClassName).toContain('md:[grid-template-columns:var(--work-split-columns)]')
    expect(layout.gridStyle).toEqual({ '--work-split-columns': 'minmax(240px, 0.4fr) minmax(240px, 0.6fr)' })
    expect(layout.toolbarClassName).toContain('md:col-span-2')
    expect(layout.chatClassName).toContain('md:border-b-0 md:border-r')
    expect(layout.panelClassName).toContain('md:col-start-2')
  })

  it('gives chat the single column when the panel is closed', () => {
    const layout = resolveWorkSurfaceLayout({ chatOpen: true, panelOpen: false, splitRatio: 0.5 })
    expect(layout.showDivider).toBe(false)
    expect(layout.gridClassName).not.toContain('md:[grid-template-columns')
    expect(layout.gridStyle).toBeUndefined()
    expect(layout.chatClassName).not.toContain('md:border-r')
  })

  it('gives the panel the single column when chat is closed', () => {
    const layout = resolveWorkSurfaceLayout({ chatOpen: false, panelOpen: true, splitRatio: 0.5 })
    expect(layout.showChat).toBe(false)
    expect(layout.showDivider).toBe(false)
    expect(layout.panelClassName).toContain('col-start-1')
    expect(layout.panelClassName).not.toContain('md:col-start-2')
  })

  it('pins the panel to phone width in Mobile preview', () => {
    const layout = resolveWorkSurfaceLayout({ chatOpen: true, panelOpen: true, splitRatio: 0.3, mobilePreview: true })
    expect(layout.gridStyle).toEqual({ '--work-split-columns': `minmax(240px, 1fr) ${MOBILE_PREVIEW_PANE_COLUMN}` })
  })

  it('follows the width the person dragged the rail to in Mobile preview', () => {
    const layout = resolveWorkSurfaceLayout({ chatOpen: true, panelOpen: true, splitRatio: 0.3, mobilePreview: true, mobilePaneWidth: 410.4 })
    expect(layout.gridStyle).toEqual({ '--work-split-columns': 'minmax(240px, 1fr) 410px' })
  })
})
