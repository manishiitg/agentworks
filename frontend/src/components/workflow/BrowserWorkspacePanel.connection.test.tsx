// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import { TooltipProvider } from '../ui/tooltip'
import { BrowserWorkspacePanel } from './BrowserWorkspacePanel'
import type { BrowserEmptyStateActions } from './WorkflowLiveBrowser'

const mocks = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }))
vi.mock('../../services/api', () => ({ default: { defaults: {}, get: mocks.get, post: mocks.post }, getApiBaseUrl: () => 'http://localhost', getAuthToken: () => null }))
vi.mock('../../utils/workspacePaneChat', () => ({ sendWorkspacePaneMessageToChat: vi.fn() }))
vi.mock('./WorkspacePanelGuideButton', () => ({ WorkspacePanelGuideButton: () => null }))
vi.mock('./WorkflowLiveBrowser', () => ({ default: ({ emptyContent }: { emptyContent: React.ReactNode | ((actions: BrowserEmptyStateActions) => React.ReactNode) }) => typeof emptyContent === 'function' ? emptyContent({ startBrowser: vi.fn(), startingBrowser: false, canStart: true }) : emptyContent }))
vi.mock('../../utils/runtimeCapabilities', () => ({ isBrowserCDPEnabled: () => false }))

it('choosing Chrome in Crew reuses the account browser without copying another token', async () => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  const host = document.createElement('div')
  const root = createRoot(host)
  const clipboard = vi.fn()
  vi.stubGlobal('navigator', { userAgent: navigator.userAgent, clipboard: { writeText: clipboard } })
  mocks.get.mockResolvedValue({ data: { selected: false, connected: false, account_connected: true, tabs: 0 } })
  mocks.post.mockResolvedValue({ data: { selected: true, connected: true, account_connected: true, tabs: 0, workspace: 'Chats/Work/projects/crew-one' } })
  try {
    await act(async () => root.render(<TooltipProvider><BrowserWorkspacePanel workspacePath="Chats/Work/projects/crew-one" profileId="work" scopeNoun="project" browserMode="headless" onBrowserModeChange={vi.fn()} cdpPort={9222} onCdpPortChange={vi.fn()} cdpConnected={null} cdpError={null} cdpChecking={false} onCheckCdpConnection={vi.fn()} /></TooltipProvider>))
    const chrome = [...host.querySelectorAll('button')].find(button => button.textContent?.includes('Use my Chrome or Edge'))!
    expect(host.textContent).toContain('Your browser is connected to your account')
    expect(host.textContent).toContain('Recommended for signed-in websites')
    expect(host.textContent).toContain('Your helper can use your existing sign-ins and work in separate tabs for this project')
    expect(host.textContent).not.toContain('one-time setup')
    expect(mocks.post).not.toHaveBeenCalled()
    await act(async () => chrome.click())
    expect(mocks.post).toHaveBeenCalledWith('/api/browser/extension', { action: 'connect' }, expect.objectContaining({ params: { workspace_path: 'Chats/Work/projects/crew-one', profile_id: 'work' } }))
    expect(clipboard).not.toHaveBeenCalled()
    expect(host.textContent).toContain('Connected · 0 shared tabs')
    expect(host.querySelector('option[value="extension"]')?.textContent).toContain('Connected')
    expect(host.textContent).not.toContain('Install the extension')
    const copy = [...host.querySelectorAll('button')].find(button => button.textContent === 'Copy connection code')!
    mocks.post.mockResolvedValue({ data: { token: 'stable-account-token', scope: 'crew-one' } })
    await act(async () => copy.click())
    expect(mocks.post).toHaveBeenLastCalledWith('/api/browser/extension', { action: 'copy' }, expect.anything())
    expect(JSON.parse(clipboard.mock.calls[0][0]).token).toBe('stable-account-token')
    expect(host.textContent).toContain('Connected · 0 shared tabs')
    expect(host.textContent).not.toContain('Waiting for your browser')
    expect(host.textContent).not.toContain('Install the extension')
  } finally {
    await act(async () => root.unmount())
    vi.unstubAllGlobals()
  }
})
