// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import BrowserAutomationSettings from './BrowserAutomationSettings'
const cleanups: (() => void)[] = []
afterEach(() => { cleanups.splice(0).forEach(fn => fn()); vi.unstubAllGlobals(); delete (window as Window & { __APP_RUNTIME_CONFIG__?: unknown }).__APP_RUNTIME_CONFIG__ })
async function mount(cdpEnabled: boolean) {
  vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true)
  Object.assign(window, { __APP_RUNTIME_CONFIG__: { cdpEnabled } })
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  const change = vi.fn()
  cleanups.push(() => { act(() => root.unmount()); host.remove() })
  await act(async () => { root.render(<BrowserAutomationSettings browserMode="auto" onBrowserModeChange={change} cdpPort={9222} onCdpPortChange={vi.fn()} cdpConnected={null} cdpError={null} cdpChecking={false} onCheckCdpConnection={vi.fn()} />) })
  return { host, change }
}
it('keeps local setup behind a closed advanced disclosure and preserves browser choices', async () => {
  const { host, change } = await mount(true)
  expect(host.querySelector('details')?.open).toBe(false)
  expect(host.querySelector('summary')?.textContent).toBe('Advanced connection settings')
  expect(host.querySelectorAll('input[type="radio"]')).toHaveLength(0)
  const choice = host.querySelector('[aria-label="Browser choice"]') as HTMLSelectElement
  expect(choice.value).toBe('auto')
  await act(async () => { choice.value = 'headless'; choice.dispatchEvent(new Event('change', { bubbles: true })) })
  expect(change).toHaveBeenCalledWith('headless')
})
it('shows the server workspace browser without connection setup or redundant choices', async () => {
  const { host } = await mount(false)
  expect(host.querySelector('select')).toBeNull()
  expect(host.querySelector('details')).toBeNull()
  expect(host.textContent).toContain('Start it to visit a website, sign in, or teach your helper')
  expect(host.textContent).not.toMatch(/CDP|headless|Chromium/)
})
