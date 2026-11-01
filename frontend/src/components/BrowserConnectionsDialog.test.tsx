// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import BrowserConnectionsDialog, { BrowserConnectionsPanel } from './BrowserConnectionsDialog'

const mocks = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), open: vi.fn(() => true), close: vi.fn(), refetch: () => {} }))
vi.mock('../utils/signInWindow', () => ({ prepareSignInWindow: () => ({ open: mocks.open, close: mocks.close }) }))
vi.mock('../services/api', () => ({ default: { defaults: {}, get: mocks.get, post: mocks.post } }))
vi.mock('../hooks/useLiveRefetch', () => ({ useLiveRefetch: (refetch: () => void) => { mocks.refetch = refetch } }))
afterEach(() => vi.clearAllMocks())

it('shows the account pairing failure and installed/download versions without exposing the code', async () => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  mocks.get.mockResolvedValue({ data: {
    browsers: [], latest_extension_version: '0.5.0',
    pairing_error: { reason: 'extension_outdated', message: 'Download the extension, replace its folder and click Reload.', extension_version: '0.4.9', at: Date.now() },
  } })
  const host = document.createElement('div'), root = createRoot(host)
  try {
    await act(async () => root.render(<BrowserConnectionsDialog open onClose={vi.fn()} />))
    const dialog = document.querySelector('[role="dialog"]')!
    expect(dialog.querySelector('[role="alert"]')?.textContent).toContain('Update your browser extension')
    expect(dialog.textContent).toContain('replace its folder and click Reload')
    expect(dialog.textContent).toContain('Installed extension: 0.4.9 · Download: 0.5.0')
    expect(dialog.querySelector('a[download]')?.textContent).toContain('Download extension')
    expect(dialog.querySelector('textarea')).toBeNull()
    expect(mocks.post).not.toHaveBeenCalled()
    expect(mocks.get).toHaveBeenCalledWith('/api/browser/extension', { skipSessionContext: true })
  } finally { await act(async () => root.unmount()) }
})

it('keeps saved-browser controls and labels old versus unknown extension versions accurately', async () => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  mocks.get.mockResolvedValue({ data: {
    latest_extension_version: '0.5.0', pairing_error: null,
    browsers: [
      { id: 'chrome', name: 'Chrome', browser: 'Chrome', version: '0.4.9', connected: false, enabled: true },
      { id: 'edge', name: 'Edge', browser: 'Edge', connected: true, enabled: false },
    ],
  } })
  const host = document.createElement('div'), root = createRoot(host)
  try {
    await act(async () => root.render(<BrowserConnectionsDialog open onClose={vi.fn()} />))
    const dialog = document.querySelector('[role="dialog"]')!
    await act(async () => { [...dialog.querySelectorAll('button')].find(button => button.textContent === 'Back')!.click() })
    const rows = dialog.querySelectorAll('li')
    expect(rows[0].textContent).toContain('Offline')
    expect(rows[0].textContent).toContain('Update available: 0.5.0')
    expect(rows[0].querySelector('button')?.textContent).toBe('Disconnect')
    expect(rows[1].textContent).toContain('Disconnected')
    expect(rows[1].textContent).not.toContain('Update available')
    expect(rows[1].querySelector('button')).toBeNull()
    expect(dialog.querySelector('[role="alert"]')).toBeNull()
  } finally { await act(async () => root.unmount()) }
})

// PLAT-746-268: opening setup must never issue a handoff; only explicit Connect does.
it('opens a one-use browser handoff only after explicit Connect without clipboard access', async () => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  mocks.get.mockResolvedValue({ data: { browsers: [] } })
  mocks.post.mockResolvedValue({ data: { handoff: 'a'.repeat(43) } })
  const host = document.createElement('div'), root = createRoot(host)
  const clipboard = vi.fn().mockResolvedValue(undefined)
  vi.stubGlobal('navigator', { clipboard: { writeText: clipboard } })
  try {
    await act(async () => root.render(<BrowserConnectionsDialog open onClose={vi.fn()} />))
    const dialog = document.querySelector('[role="dialog"]')!
    expect(mocks.post).not.toHaveBeenCalled()
    expect(dialog.querySelector('textarea')).toBeNull()
    expect(mocks.post).not.toHaveBeenCalled()
    await act(async () => { [...dialog.querySelectorAll('button')].find(button => button.textContent === 'Connect browser')!.click() })
    expect(clipboard).not.toHaveBeenCalled()
    expect(mocks.open).toHaveBeenCalledWith(expect.stringContaining('/api/browser/extension/setup#handoff='))
    expect(mocks.post).toHaveBeenCalledWith('/api/browser/extension', { action: 'pair' }, { skipSessionContext: true })
    expect(dialog.querySelector('textarea')).toBeNull()
  } finally { await act(async () => root.unmount()); vi.unstubAllGlobals() }
})

// PLAT-746-270 / PR477: saved offline browsers resume without an explicit pairing attempt.
it.each(['dialog', 'panel'])('requires named browser confirmation when an old browser reconnects in the %s', async (surface) => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  const oldBrowser = { id: 'old', name: 'Old Chrome', connected: false, enabled: true }
  mocks.get.mockResolvedValue({ data: { browsers: [oldBrowser] } })
  mocks.post.mockResolvedValue({ data: { handoff: 'a'.repeat(43) } })
  vi.stubGlobal('navigator', { clipboard: { writeText: vi.fn().mockResolvedValue(undefined) } })
  const host = document.createElement('div'), root = createRoot(host), selected = vi.fn().mockResolvedValue(true), closed = vi.fn()
  try {
    await act(async () => root.render(surface === 'dialog' ? <BrowserConnectionsDialog open onClose={closed} onConnected={selected} /> : <BrowserConnectionsPanel open startSetup selectedBrowserId="old" onClose={closed} onConnected={selected} />))
    const dialog = surface === 'dialog' ? document.querySelector('[role="dialog"]')! : host
    await act(async () => { [...dialog.querySelectorAll('button')].find(button => button.textContent === 'Connect browser')!.click() })
    mocks.get.mockResolvedValue({ data: { browsers: [{ ...oldBrowser, connected: true }, { id: 'new', name: 'New Edge', connected: true, enabled: true }] } })
    await act(async () => { mocks.refetch() })
    expect(selected).not.toHaveBeenCalled()
    expect(closed).not.toHaveBeenCalled()
    expect(dialog.querySelector('textarea')).toBeNull()
    // PLAT-746-287: an available browser must not look selected before the person's choice.
    const choices = dialog.querySelector('[aria-label="Choose connected browser"]')!
    expect(choices.querySelectorAll('button')).toHaveLength(2)
    expect(choices.querySelectorAll('[aria-label="Selected"]')).toHaveLength(surface === 'panel' ? 1 : 0)
    expect(choices.querySelector('button:last-child [aria-label="Selected"]')).toBeNull()
    await act(async () => { [...dialog.querySelectorAll('button')].find(button => button.textContent === 'Use New Edge' || button.getAttribute('aria-label') === 'Use New Edge')!.click() })
    expect(selected).toHaveBeenCalledOnce()
    expect(selected).toHaveBeenCalledWith('new')
    expect(closed).toHaveBeenCalledOnce()
  } finally { await act(async () => root.unmount()); vi.unstubAllGlobals() }
})

// PLAT-746-279: reconnecting a saved browser starts setup without selecting it or preparing a code.
it('keeps saved-browser reconnect and per-browser actions separate from project selection', async () => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  mocks.post.mockClear()
  const browsers = [
    { id: 'chrome', name: 'Chrome', browser: 'Chrome', version: '0.5.0', connected: true, enabled: true },
    { id: 'edge', name: 'Edge', browser: 'Edge', version: '0.5.0', connected: false, enabled: false },
    { id: 'saved-edge', name: 'Saved Edge', browser: 'Edge', version: '0.5.0', connected: false, enabled: true },
  ]
  mocks.get.mockResolvedValue({ data: { browsers, latest_extension_version: '0.5.0' } })
  const host = document.createElement('div'), root = createRoot(host), selected = vi.fn()
  try {
    await act(async () => root.render(<BrowserConnectionsPanel open selectedBrowserId="chrome" onClose={vi.fn()} onConnected={selected} />))
    expect(host.querySelector('[aria-label="Selected"]')).toBeTruthy()
    expect(host.querySelector('button[aria-label="Use Chrome"]')?.hasAttribute('disabled')).toBe(true)
    expect(host.textContent).not.toContain('Connection details')
    expect(host.textContent).not.toContain('Disconnect')
    // PLAT-746-280: a closed, paired browser needs reopening, not another code.
    expect(host.textContent).toContain('Open Saved Edge to reconnect')
    expect(host.querySelector('[aria-label="Connect Saved Edge"]')).toBeNull()
    await act(async () => { (host.querySelector('[aria-label="Connect Edge"]') as HTMLButtonElement).click() })
    expect(host.querySelector('[aria-label="Connect browser"]')).toBeTruthy()
    expect(selected).not.toHaveBeenCalled()
    expect(mocks.post).not.toHaveBeenCalled()
    await act(async () => { [...host.querySelectorAll('button')].find(button => button.textContent === 'Back')!.click() })
    await act(async () => { (host.querySelector('[aria-label="Options for Chrome"]') as HTMLButtonElement).click() })
    expect(host.textContent).toContain('Connection details')
    const disconnect = [...host.querySelectorAll('button')].find(button => button.textContent === 'Disconnect')!
    mocks.post.mockResolvedValueOnce({ data: {} })
    await act(async () => disconnect.click())
    expect(mocks.post).toHaveBeenCalledWith('/api/browser/extension', { action: 'disconnect', browser_id: 'chrome' }, { skipSessionContext: true })
    expect(selected).not.toHaveBeenCalled()
  } finally { await act(async () => root.unmount()) }
})
