// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'

const state = vi.hoisted(() => ({ screen: 'denied' as string | undefined }))
vi.mock('../../services/api', () => ({ default: {
  get: async () => ({ data: {
    enabled: true, installed: true, active: false, platform: 'darwin', version: '1.0.2',
    permissions: { data: { accessibility: { state: 'granted' }, screen_recording: { state: state.screen } } },
  } }),
} }))
vi.mock('../../stores/useAuthStore', () => ({ useAuthStore: (select: (value: unknown) => unknown) => select({ user: { id: 'owner' } }) }))
vi.mock('../../stores/useWorkspaceConnectionStore', () => ({ useWorkspaceConnectionStore: (select: (value: unknown) => unknown) => select({ activeWorkspaceId: 'local' }) }))
vi.mock('../../utils/chatIdentity', () => ({ captureChatIdentity: () => 'chat', isChatIdentityCurrent: () => true }))
import { ComputerUsePanel } from './ComputerUsePanel'

// PLAT-856-437: Accessibility alone used to report Ready with screenshot access denied.
it('shows screenshot permission separately and never treats a denied or unknown grant as ready', async () => {
  vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true)
  const visible = vi.spyOn(document, 'hidden', 'get').mockReturnValue(false)
  try {
    for (const screen of ['denied', 'unknown', undefined, 'granted']) {
      state.screen = screen
      const host = document.createElement('div')
      const root = createRoot(host)
      try {
        await act(async () => root.render(<ComputerUsePanel projectId="example"/>))
        const rows = host.querySelector('[aria-label="Computer permissions"]')
        expect(rows?.textContent).toContain('App controlAllowed')
        expect(rows?.textContent).toContain(screen === 'granted' ? 'ScreenshotsAllowed' : screen === 'denied' ? 'ScreenshotsNot allowed' : 'ScreenshotsCould not check')
        expect(host.querySelector('[role="status"]')?.textContent === 'Ready').toBe(screen === 'granted')
      } finally { act(() => root.unmount()) }
    }
  } finally { visible.mockRestore(); vi.unstubAllGlobals() }
})
