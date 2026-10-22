import { describe, expect, it } from 'vitest'
import { MOBILE_PREVIEW_PANE_COLUMN } from '../../components/workflow/workspaceLayoutResolver'
import { resolveWorkSurfaceLayout } from './workSurfaceLayoutResolver'

describe('resolveWorkSurfaceLayout', () => {
  it('gives laptop dashboard preview the remaining width beside 480px chat, independent of saved split', () => {
    const layout = resolveWorkSurfaceLayout({ chatOpen: true, panelOpen: true, splitRatio: 0.8, laptopPreview: true })
    expect(layout.gridStyle).toEqual({ '--work-split-columns': 'min(480px, calc(100% - 240px)) minmax(240px, 1fr)' })
  })
  it('keeps the context bubble visible despite an older closed-pane preference', () => {
    const layout = resolveWorkSurfaceLayout({ chatOpen: true, panelOpen: false, splitRatio: 0.5, compactContext: true })
    expect(layout.showPanel).toBe(true)
    expect(layout.showDivider).toBe(false)
    expect(layout.gridStyle).toEqual({ '--work-split-columns': 'minmax(240px, 1fr) clamp(240px, 26vw, 316px)' })
  })
  it('gives context a compact column without a resize divider or chat border', () => {
    const layout = resolveWorkSurfaceLayout({ chatOpen: true, panelOpen: true, splitRatio: 0.8, mobilePreview: true, compactContext: true })
    expect(layout.gridStyle).toEqual({ '--work-split-columns': 'minmax(240px, 1fr) clamp(240px, 26vw, 316px)' })
    expect(layout.showDivider).toBe(false)
    expect(layout.chatClassName).not.toContain('border-r')
  })

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

  it('stacks chat above the panel below desktop width so neither is squeezed', () => {
    const layout = resolveWorkSurfaceLayout({ chatOpen: true, panelOpen: true, splitRatio: 0.4 })
    expect(layout.gridClassName).toContain('grid-rows-[auto_minmax(0,1fr)_minmax(0,1fr)]')
    expect(layout.gridClassName).toContain('md:grid-rows-[auto_minmax(0,1fr)]')
    expect(layout.panelClassName).toContain('row-start-3 md:row-start-2')
    expect(layout.chatClassName).toContain('row-start-2')
  })

  it('reserves no row for the hidden pane when a phone pane switch picks one pane', () => {
    const layout = resolveWorkSurfaceLayout({ chatOpen: true, panelOpen: true, splitRatio: 0.4, mobilePane: 'workspace' })
    expect(layout.gridClassName).toContain('grid-rows-[auto_minmax(0,1fr)]')
    expect(layout.gridClassName).not.toContain('grid-rows-[auto_minmax(0,1fr)_minmax(0,1fr)]')
    expect(layout.panelClassName).toContain('row-start-2')
    expect(layout.panelClassName).not.toContain('row-start-3')
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
