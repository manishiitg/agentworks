// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import { BrowserWorkspacePanel } from './BrowserWorkspacePanel'
import { WorkspaceViewActions } from './WorkspaceViewActions'
const mocks = vi.hoisted(() => ({ learn: undefined as undefined | ((message: string) => unknown), send: vi.fn() }))
vi.mock('./ChromeExtensionConnection', () => ({ ChromeExtensionConnection: () => null, useChromeExtensionConnection: () => ({ status: { selected: false, connected: false, tabs: 0 }, loading: false, busy: false }) }))
vi.mock('../../utils/workspacePaneChat', () => ({ sendWorkspacePaneMessageToChat: mocks.send }))
vi.mock('./WorkflowLiveBrowser', () => ({ default: (props: { onLearn?: (message: string) => unknown }) => { mocks.learn = props.onLearn; return null } }))
const cleanups: (() => void)[] = []
afterEach(() => { cleanups.splice(0).forEach(fn => fn()); vi.clearAllMocks(); mocks.learn = undefined })
async function mount(onAsk?: (message: string) => Promise<void>) {
 Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
 const root = createRoot(document.createElement('div'))
 cleanups.push(() => act(() => root.unmount()))
 await act(async () => root.render(<BrowserWorkspacePanel workspacePath="Workflow/one" browserMode="auto" onBrowserModeChange={vi.fn()} cdpPort={9222} onCdpPortChange={vi.fn()} cdpConnected={null} cdpError={null} cdpChecking={false} onCheckCdpConnection={vi.fn()} assistantControl={<WorkspaceViewActions workspacePath="Workflow/one" message="Browser help" onAsk={onAsk} onRefresh={vi.fn()} />} />))
}
it('sends a finished workflow demonstration to its helper without a custom Ask AI override', async () => {
 await mount()
 await mocks.learn?.('Review the saved demonstration')
 expect(mocks.send).toHaveBeenCalledWith({ workspacePath: 'Workflow/one', message: 'Review the saved demonstration' })
})
it('preserves the product helper routing override', async () => {
 const ask = vi.fn().mockResolvedValue(undefined)
 await mount(ask)
 await mocks.learn?.('Review the project demonstration')
 expect(ask).toHaveBeenCalledWith('Review the project demonstration')
 expect(mocks.send).not.toHaveBeenCalled()
})
