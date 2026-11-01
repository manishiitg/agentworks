// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import { TooltipProvider } from '../ui/tooltip'
import { BrowserWorkspacePanel } from './BrowserWorkspacePanel'
import type { BrowserEmptyStateActions } from './WorkflowLiveBrowser'

const mocks = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), open: vi.fn(() => true) }))
vi.mock('../../utils/signInWindow', () => ({ prepareSignInWindow: () => ({ open: mocks.open, close: vi.fn() }) }))
vi.mock('../../services/api', () => ({ default: { defaults: {}, get: mocks.get, post: mocks.post }, getApiBaseUrl: () => 'http://localhost', getAuthToken: () => null }))
vi.mock('../../utils/workspacePaneChat', () => ({ sendWorkspacePaneMessageToChat: vi.fn() }))
vi.mock('./WorkflowLiveBrowser', () => ({ default: ({ overview, overviewFooter }: { overview?: (actions: BrowserEmptyStateActions) => React.ReactNode; overviewFooter?: React.ReactNode }) => <div aria-label="Browser content">{overview?.({ startBrowser: vi.fn(), startingBrowser: false, canStart: true })}{overviewFooter}</div> }))
vi.mock('../../utils/runtimeCapabilities', () => ({ isBrowserCDPEnabled: () => false }))

it('choosing Chrome in Crew reuses the account browser without copying another token', async () => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  const host = document.createElement('div')
  const root = createRoot(host)
  const clipboard = vi.fn()
  vi.stubGlobal('navigator', { userAgent: navigator.userAgent, clipboard: { writeText: clipboard } })
  const browsers = [{ id: 'chrome-one', name: 'My Chrome', browser: 'Chrome', connected: true, enabled: true }, { id: 'edge-two', name: 'My Edge', browser: 'Edge', connected: true, enabled: true }]
  mocks.get.mockResolvedValue({ data: { selected: false, connected: false, account_connected: true, tabs: 0, browsers } })
  mocks.post.mockResolvedValue({ data: { selected: true, connected: true, account_connected: true, browser_id: 'chrome-one', tabs: 1, tab_titles: ['Example page'], browsers, workspace: 'Crew/example-crew-one' } })
  try {
    await act(async () => root.render(<TooltipProvider><BrowserWorkspacePanel workspacePath="Crew/example-crew-one" profileId="work" scopeNoun="project" browserMode="headless" onBrowserModeChange={vi.fn()} cdpPort={9222} onCdpPortChange={vi.fn()} cdpConnected={null} cdpError={null} cdpChecking={false} onCheckCdpConnection={vi.fn()} /></TooltipProvider>))
    expect(host.textContent).not.toContain('Save browser choice')
    expect(host.querySelector('[role="dialog"]')).toBeNull()
    expect(mocks.post).not.toHaveBeenCalled()
    expect([...host.querySelectorAll('button')].some(button => button.textContent === 'Change')).toBe(false)
    const chrome = [...host.querySelectorAll('button')].find(button => button.textContent?.includes('My Chrome'))!
    await act(async () => chrome.click())
    expect(mocks.post).toHaveBeenCalledWith('/api/browser/extension', { action: 'connect', browser_id: 'chrome-one' }, expect.objectContaining({ params: { workspace_path: 'Crew/example-crew-one', profile_id: 'work' } }))
    expect(clipboard).not.toHaveBeenCalled()
    // PLAT-746-281: tabs belong under their browser in the same panel body.
    const tabs = host.querySelector('[aria-label="Project browser tabs"]')!
    expect(tabs.textContent).toContain('Example page')
    expect(tabs.closest('[aria-label="Saved browsers"]')).toBeTruthy()
    expect(tabs.closest('[aria-label="Browser content"]')).toBeTruthy()
    expect(host.textContent).toContain('My Chrome')
    expect([...host.querySelectorAll('button')].some(button => button.textContent === 'Change')).toBe(false)
    expect(host.textContent).not.toContain('Install the extension')
    const manage = [...host.querySelectorAll('button')].find(button => button.textContent === 'Add browser')!
    expect(manage).toBeTruthy()
    await act(async () => manage.click())
    // PLAT-746-275: setup belongs to this pane, never a separate portal/dialog.
    expect(document.querySelector('[role="dialog"]')).toBeNull()
    const setup = host.querySelector('[aria-label="Connect browser"]')!
    expect(setup).toBeTruthy()
    expect(setup.querySelector('a[download]')).toBeTruthy()
    expect(setup.closest('[aria-label="Browser content"]')).toBeTruthy()
    expect(host.querySelector('[aria-label="Saved browsers"]')).toBeNull()
    expect(clipboard).not.toHaveBeenCalled()
    mocks.post.mockResolvedValueOnce({ data: { handoff: 'a'.repeat(43) } })
    await act(async () => { [...setup.querySelectorAll('button')].find(button => button.textContent === 'Connect browser')!.click() })
    expect(mocks.post).toHaveBeenLastCalledWith('/api/browser/extension', { action: 'pair' }, { skipSessionContext: true })
    expect(clipboard).not.toHaveBeenCalled()
    expect(mocks.open).toHaveBeenCalledWith(expect.stringContaining('/api/browser/extension/setup#handoff='))
    await act(async () => { [...setup.querySelectorAll('button')].find(button => button.textContent === 'Back')!.click() })
    expect(host.querySelector('[aria-label="Project browser tabs"]')?.textContent).toContain('Example page')
    expect(host.querySelector('textarea')).toBeNull()
    expect(document.querySelector('[role="dialog"]')).toBeNull()
  } finally {
    await act(async () => root.unmount())
    vi.unstubAllGlobals()
  }
})

// PLAT-856-311: even an unknown-version rejected connection needs a usable,
// server-selected download step; the panel must not guess from version text.
it('shows the backend update step before a browser can connect', async () => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  const host = document.createElement('div')
  const root = createRoot(host)
  mocks.post.mockClear()
  mocks.get.mockResolvedValue({ data: { selected: false, connected: false, tabs: 0,
    update_needed: true, update_url: 'https://example.com/api/downloads/chrome-extension.zip', browsers: [] } })
  try {
    await act(async () => root.render(<TooltipProvider><BrowserWorkspacePanel workspacePath="Crew/example" profileId="work" browserMode="extension" onBrowserModeChange={vi.fn()} cdpConnected={null} cdpError={null} cdpChecking={false} onCheckCdpConnection={vi.fn()} /></TooltipProvider>))
    const download = host.querySelector('a[download]') as HTMLAnchorElement
    expect(download?.href).toBe('https://example.com/api/downloads/chrome-extension.zip')
    expect(mocks.post).not.toHaveBeenCalled()
  } finally { await act(async () => root.unmount()) }
})

// PLAT-746-268: a failed save must leave the old browser selected and permit retry.
it('applies direct browser selection immediately and retains the saved browser on failure', async () => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  mocks.post.mockReset()
  const status = { mode: 'extension', selected: true, connected: true, browser_id: 'chrome', tabs: 0, allowed_modes: ['extension', 'cdp'], browsers: [{ id: 'chrome', name: 'Chrome', browser: 'Chrome', connected: true, enabled: true }] }
  mocks.get.mockResolvedValue({ data: status })
  mocks.post.mockRejectedValueOnce(new Error('Unavailable')).mockResolvedValueOnce({ data: { ...status, mode: 'cdp', selected: false, connected: false } })
  const host = document.createElement('div'), root = createRoot(host), changed = vi.fn()
  try {
    await act(async () => root.render(<BrowserWorkspacePanel workspacePath="Crew/example" profileId="work" browserMode="extension" onBrowserModeChange={changed} cdpPort={9222} cdpConnected={true} cdpError={null} cdpChecking={false} onCheckCdpConnection={vi.fn()} />))
    expect([...host.querySelectorAll('button')].some(button => button.textContent === 'Change')).toBe(false)
    await act(async () => { (host.querySelector('[aria-label="Use Chrome Direct"]') as HTMLButtonElement).click() })
    expect(changed).not.toHaveBeenCalled()
    expect(host.querySelector('[aria-label="Choose browser"]')).toBeTruthy()
    expect(host.querySelector('[role="alert"]')).toBeTruthy()
    await act(async () => { (host.querySelector('[aria-label="Use Chrome Direct"]') as HTMLButtonElement).click() })
    expect(mocks.post).toHaveBeenLastCalledWith('/api/browser/extension', { action: 'workspace', mode: 'cdp', port: 9222 }, expect.anything())
    expect(changed).toHaveBeenCalledWith('cdp')
    // The active direct connection stays identifiable after leaving secondary options.
    expect(host.querySelector('[aria-label="Open Chrome Direct"]')).toBeTruthy()
    expect(host.querySelector('[aria-label="Choose browser"]')).toBeTruthy()
  } finally { await act(async () => root.unmount()) }
})

// PLAT-746-275: the inline account browser controls remain Builder-only.
it('does not load personal browser connections inside a shared Run pane', async () => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  mocks.get.mockClear(); mocks.post.mockClear()
  const host = document.createElement('div'), root = createRoot(host)
  try {
    await act(async () => root.render(<BrowserWorkspacePanel readOnly workspacePath="Crew/shared-example" profileId="work" browserMode="extension" onBrowserModeChange={vi.fn()} cdpConnected={null} cdpError={null} cdpChecking={false} onCheckCdpConnection={vi.fn()} />))
    expect(host.querySelector('[aria-label="Browser connections"]')).toBeNull()
    expect([...host.querySelectorAll('button')].some(button => /Change|Connect browser/.test(button.textContent || ''))).toBe(false)
    expect(mocks.get).not.toHaveBeenCalled()
    expect(mocks.post).not.toHaveBeenCalled()
  } finally { await act(async () => root.unmount()) }
})
