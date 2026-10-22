import { describe, expect, it } from 'vitest'
import { MOBILE_PREVIEW_PANE_COLUMN, resolveWorkspaceLayout, type WorkspaceLayoutInput } from './workspaceLayoutResolver'

const base: WorkspaceLayoutInput = {
  showChatArea: true,
  showWorkspacePane: true,
  focusedPane: 'preview',
  reportPreviewPreference: 'tablet',
  workspaceSplitRatio: 0.5,
  isWorkspaceViewActive: true,
}

describe('resolveWorkspaceLayout', () => {
  it('gives laptop dashboard preview the remaining width beside 480px chat, independent of saved split', () => {
    const layout = resolveWorkspaceLayout({ ...base, reportPreviewPreference: 'desktop', workspaceSplitRatio: 0.8, laptopPreview: true })
    expect(layout.splitLayoutStyle).toEqual({ '--workflow-split-columns': 'min(480px, calc(100% - 240px)) minmax(240px, 1fr)' })
  })
  it('keeps the context bubble visible despite an older closed-pane preference', () => {
    const layout = resolveWorkspaceLayout({ ...base, compactContext: true, showWorkspacePane: false })
    expect(layout.workspacePaneVisible).toBe(true)
    expect(layout.renderCanvasStandalone).toBe(false)
    expect(layout.splitLayoutStyle).toEqual({ '--workflow-split-columns': 'minmax(240px, 1fr) clamp(240px, 26vw, 316px)' })
  })
  it('keeps context compact independently of saved preview size and drag ratio', () => {
    const layout = resolveWorkspaceLayout({ ...base, compactContext: true, reportPreviewPreference: 'mobile', workspaceSplitRatio: 0.8 })
    expect(layout.splitLayoutStyle).toEqual({ '--workflow-split-columns': 'minmax(240px, 1fr) clamp(240px, 26vw, 316px)' })
    expect(layout.chatPaneClassName).not.toContain('border-r')
    expect(layout.canvasPaneClassName).not.toContain('border-l')
  })

  it('renders a single flex column with no chat', () => {
    const layout = resolveWorkspaceLayout({ ...base, showChatArea: false })
    expect(layout.showChat).toBe(false)
    expect(layout.workspacePaneVisible).toBe(true)
    expect(layout.renderCanvasStandalone).toBe(false)
    expect(layout.splitLayoutClassName).toBe('flex-1 min-h-0 flex flex-col')
    expect(layout.splitLayoutStyle).toBeUndefined()
    expect(layout.canvasPaneClassName).toBe('flex-1 min-h-0 min-w-0')
  })

  it('hides the canvas and gives chat the full row when the pane is closed', () => {
    const layout = resolveWorkspaceLayout({ ...base, showWorkspacePane: false })
    expect(layout.workspacePaneVisible).toBe(false)
    expect(layout.renderCanvasStandalone).toBe(true)
    expect(layout.canvasPaneClassName).toBe('hidden')
    expect(layout.chatPaneClassName).toContain('flex col-start-1')
    expect(layout.chatPaneClassName.endsWith('flex-1')).toBe(true)
    expect(layout.splitLayoutStyle).toBeUndefined()
  })

  it('splits chat and canvas with the configured ratio', () => {
    const layout = resolveWorkspaceLayout({ ...base, workspaceSplitRatio: 0.4 })
    expect(layout.splitLayoutClassName).toContain('md:[grid-template-columns:var(--workflow-split-columns)]')
    expect(layout.splitLayoutStyle).toEqual({ '--workflow-split-columns': 'minmax(240px, 0.4fr) minmax(240px, 0.6fr)' })
  })

  it('keeps the canvas flex when focus moves to chat', () => {
    // Ask AI sends flip focusedPane to 'chat'. The canvas must restore flex
    // at md+, never md:block (which would collapse flex-1 scroll regions and
    // freeze right-pane scrolling).
    const layout = resolveWorkspaceLayout({ ...base, focusedPane: 'chat' })
    expect(layout.canvasPaneClassName).toContain('hidden md:flex')
    expect(layout.canvasPaneClassName).not.toContain('md:block')
    expect(layout.chatPaneClassName.startsWith('flex ')).toBe(true)
  })

  it('hides chat below md when focus is on the workspace', () => {
    const layout = resolveWorkspaceLayout({ ...base, focusedPane: 'preview' })
    expect(layout.chatPaneClassName.startsWith('hidden md:flex')).toBe(true)
    expect(layout.canvasPaneClassName).not.toContain('hidden')
  })

  it('widens chat for the mobile report tier', () => {
    const mobile = resolveWorkspaceLayout({ ...base, reportPreviewPreference: 'mobile' })
    expect(mobile.chatPaneClassName).toContain('flex-1 md:flex-[1.35]')
    const tablet = resolveWorkspaceLayout({ ...base, reportPreviewPreference: 'tablet' })
    expect(tablet.chatPaneClassName).toContain('flex-1 basis-1/2')
  })
  it('pins the workspace pane to phone width in Mobile preview, whatever the saved split', () => {
    const mobile = resolveWorkspaceLayout({ ...base, reportPreviewPreference: 'mobile', workspaceSplitRatio: 0.3 })
    expect(mobile.splitLayoutStyle).toEqual({ '--workflow-split-columns': `minmax(240px, 1fr) ${MOBILE_PREVIEW_PANE_COLUMN}` })
    const tablet = resolveWorkspaceLayout({ ...base, reportPreviewPreference: 'tablet', workspaceSplitRatio: 0.3 })
    expect(tablet.splitLayoutStyle).toEqual({ '--workflow-split-columns': 'minmax(240px, 0.3fr) minmax(240px, 0.7fr)' })
  })


  it('borders the canvas only for workspace views', () => {
    expect(resolveWorkspaceLayout(base).canvasPaneClassName).toContain('md:border-l md:border-border')
    expect(resolveWorkspaceLayout({ ...base, isWorkspaceViewActive: false }).canvasPaneClassName).not.toContain('md:border-l')
  })
})
