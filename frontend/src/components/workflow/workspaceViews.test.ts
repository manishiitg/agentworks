import { expect, it } from 'vitest'
import { resolveWorkspaceView } from './workspaceViews'
import { resolveWorkspaceLayout, MOBILE_PREVIEW_PANE_COLUMN } from './workspaceLayoutResolver'

// PLAT-746-471 / PR1556: a saved retired graph rendered Dashboard while its
// parent still used the non-dashboard split, hiding device/width controls.
it('uses dashboard layout for a saved retired plan and preserves the chosen width', () => {
  const view = resolveWorkspaceView('flow', false)
  expect(view).toBe('report')
  const layout = (device: 'tablet' | 'mobile') => resolveWorkspaceLayout({
    showChatArea: true, showWorkspacePane: true, focusedPane: 'preview',
    reportPreviewPreference: view === 'report' ? device : 'desktop',
    workspaceSplitRatio: view === 'report' ? 0.35 : 0.5,
    isWorkspaceViewActive: true,
  })
  expect(layout('tablet').splitLayoutStyle).toEqual({ '--workflow-split-columns': 'minmax(240px, 0.35fr) minmax(240px, 0.65fr)' })
  expect(layout('mobile').splitLayoutStyle).toEqual({ '--workflow-split-columns': `minmax(240px, 1fr) ${MOBILE_PREVIEW_PANE_COLUMN}` })
  expect(resolveWorkspaceView('flow', true)).toBe('flow')
})
