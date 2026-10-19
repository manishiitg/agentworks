// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import BrowserConnectionsDialog from './BrowserConnectionsDialog'

const mocks = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }))
vi.mock('../services/api', () => ({ default: { defaults: {}, get: mocks.get, post: mocks.post } }))
vi.mock('../hooks/useLiveRefetch', () => ({ useLiveRefetch: vi.fn() }))
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
    expect(rows[0].textContent).toContain('Offline · Extension 0.4.9')
    expect(rows[0].textContent).toContain('Update available: 0.5.0')
    expect(rows[0].querySelector('button')?.textContent).toBe('Disconnect')
    expect(rows[1].textContent).toContain('Connected · Extension version unknown')
    expect(rows[1].textContent).not.toContain('Update available')
    expect(rows[1].querySelector('button')).toBeNull()
    expect(dialog.querySelector('[role="alert"]')).toBeNull()
  } finally { await act(async () => root.unmount()) }
})
