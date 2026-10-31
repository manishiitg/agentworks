// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import BrowserConnectionsDialog from './BrowserConnectionsDialog'

const mocks = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), refetch: () => {} }))
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
    expect(dialog.querySelector('a[download]')?.textContent).toContain('Download extension 0.5.0')
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
    const rows = dialog.querySelectorAll('li')
    expect(rows[0].textContent).toContain('Offline')
    expect(rows[0].textContent).toContain('Update available: 0.5.0')
    expect(rows[0].querySelector('button')?.textContent).toBe('Disconnect')
    expect(rows[1].textContent).toContain('Connected')
    expect(rows[1].textContent).not.toContain('Update available')
    expect(rows[1].querySelector('button')).toBeNull()
    expect(dialog.querySelector('[role="alert"]')).toBeNull()
  } finally { await act(async () => root.unmount()) }
})

// PLAT-746-268: opening setup must never mint/copy a credential; only the connect step does.
it('keeps connection codes inside the explicit connect step', async () => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  mocks.get.mockResolvedValue({ data: { browsers: [] } })
  mocks.post.mockResolvedValue({ data: { token: 'test-connection-code' } })
  const host = document.createElement('div'), root = createRoot(host)
  const clipboard = vi.fn().mockResolvedValue(undefined)
  vi.stubGlobal('navigator', { clipboard: { writeText: clipboard } })
  try {
    await act(async () => root.render(<BrowserConnectionsDialog open onClose={vi.fn()} />))
    const dialog = document.querySelector('[role="dialog"]')!
    expect(mocks.post).not.toHaveBeenCalled()
    expect(dialog.querySelector('textarea')).toBeNull()
    await act(async () => { [...dialog.querySelectorAll('button')].find(button => button.textContent?.includes('Next'))!.click() })
    expect(mocks.post).not.toHaveBeenCalled()
    await act(async () => { [...dialog.querySelectorAll('button')].find(button => button.textContent === 'Copy connection code')!.click() })
    expect(clipboard).toHaveBeenCalledOnce()
    expect(mocks.post).toHaveBeenCalledWith('/api/browser/extension', { action: 'copy' }, { skipSessionContext: true })
    expect(dialog.querySelector('textarea')?.closest('details')?.open).toBe(false)
  } finally { await act(async () => root.unmount()); vi.unstubAllGlobals() }
})

// PLAT-746-269 / PR477: saved offline browsers resume without an explicit pairing attempt.
it('requires a named browser confirmation when an old browser reconnects during setup', async () => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  const oldBrowser = { id: 'old', name: 'Old Chrome', connected: false, enabled: true }
  mocks.get.mockResolvedValue({ data: { browsers: [oldBrowser] } })
  mocks.post.mockResolvedValue({ data: { token: 'test-connection-code' } })
  vi.stubGlobal('navigator', { clipboard: { writeText: vi.fn().mockResolvedValue(undefined) } })
  const host = document.createElement('div'), root = createRoot(host), selected = vi.fn().mockResolvedValue(true), closed = vi.fn()
  try {
    await act(async () => root.render(<BrowserConnectionsDialog open onClose={closed} onConnected={selected} />))
    const dialog = document.querySelector('[role="dialog"]')!
    await act(async () => { [...dialog.querySelectorAll('button')].find(button => button.textContent?.includes('Next'))!.click() })
    await act(async () => { [...dialog.querySelectorAll('button')].find(button => button.textContent === 'Copy connection code')!.click() })
    mocks.get.mockResolvedValue({ data: { browsers: [{ ...oldBrowser, connected: true }, { id: 'new', name: 'New Edge', connected: true, enabled: true }] } })
    await act(async () => { mocks.refetch() })
    expect(selected).not.toHaveBeenCalled()
    expect(closed).not.toHaveBeenCalled()
    expect(dialog.querySelector('[aria-label="Account browser connection code"]')).toBeTruthy()
    await act(async () => { [...dialog.querySelectorAll('button')].find(button => button.textContent === 'Use New Edge')!.click() })
    expect(selected).toHaveBeenCalledOnce()
    expect(selected).toHaveBeenCalledWith('new')
    expect(closed).toHaveBeenCalledOnce()
  } finally { await act(async () => root.unmount()); vi.unstubAllGlobals() }
})
