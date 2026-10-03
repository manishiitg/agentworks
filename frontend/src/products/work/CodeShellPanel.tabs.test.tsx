// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

// The terminal library needs a real canvas; the panel's tabs and menu do not. Each fake terminal records its stream url.
const opened: string[] = []
vi.mock('@xterm/xterm', () => ({
  Terminal: class {
    cols = 80; rows = 24; options: Record<string, unknown> = {}; unicode = { activeVersion: '' }
    loadAddon() {} open() {} focus() {} write() {} clear() {} dispose() {} paste() {}
    getSelection() { return '' }
    attachCustomKeyEventHandler() {}
    onData() { return { dispose() {} } }
    onResize() { return { dispose() {} } }
  },
}))
vi.mock('@xterm/addon-fit', () => ({ FitAddon: class { fit() {} findNext() {} findPrevious() {} clearDecorations() {} onContextLoss() {} dispose() {} } }))
vi.mock('@xterm/addon-search', () => ({ SearchAddon: class { fit() {} findNext() {} findPrevious() {} clearDecorations() {} onContextLoss() {} dispose() {} } }))
vi.mock('@xterm/addon-unicode11', () => ({ Unicode11Addon: class { fit() {} findNext() {} findPrevious() {} clearDecorations() {} onContextLoss() {} dispose() {} } }))
vi.mock('@xterm/addon-web-links', () => ({ WebLinksAddon: class { fit() {} findNext() {} findPrevious() {} clearDecorations() {} onContextLoss() {} dispose() {} } }))
vi.mock('@xterm/addon-webgl', () => ({ WebglAddon: class { fit() {} findNext() {} findPrevious() {} clearDecorations() {} onContextLoss() {} dispose() {} } }))
vi.mock('@xterm/xterm/css/xterm.css', () => ({}))
const posted: string[] = []
vi.mock('../../services/api', () => ({
  default: { post: (url: string) => { posted.push(url); return Promise.resolve({}) } },
  getApiBaseUrl: () => 'https://x.test',
  getAuthToken: () => null,
}))
vi.mock('../../hooks/useTheme', () => ({ useTheme: () => ({ theme: 'dark' }) }))

import { CodeShellPanel } from './CodeShellPanel'

class FakeSocket {
  static OPEN = 1
  readyState = 0
  binaryType = ''
  onopen: (() => void) | null = null
  onmessage: (() => void) | null = null
  onclose: (() => void) | null = null
  constructor(url: string) { opened.push(url) }
  send() {}
  close() {}
}

let container: HTMLDivElement
let root: Root
beforeEach(() => {
  opened.length = 0
  posted.length = 0
  const values = new Map<string, string>()
  vi.stubGlobal('localStorage', { getItem: (k: string) => values.get(k) ?? null, setItem: (k: string, v: string) => { values.set(k, v) }, removeItem: (k: string) => { values.delete(k) }, clear: () => values.clear() })
  vi.stubGlobal('WebSocket', FakeSocket)
  vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} })
  container = document.createElement('div')
  document.body.appendChild(container)
  root = createRoot(container)
})
afterEach(async () => {
  await act(async () => { root.unmount() })
  container.remove()
  vi.unstubAllGlobals()
})

const click = async (el: Element | null) => { await act(async () => { (el as HTMLElement).dispatchEvent(new MouseEvent('click', { bubbles: true })) }) }

describe('Code terminal tabs', { timeout: 20000 }, () => {
  it('opens up to three terminals, each its own shell, and closing one stops it', async () => {
    await act(async () => { root.render(<CodeShellPanel projectId="p1" />) })
    expect(opened.length).toBe(1)
    expect(opened[0]).not.toContain('tab=')

    const add = () => container.querySelector('[data-testid="code-shell-new-tab"]')
    await click(add())
    await click(add())
    expect(opened.some(url => url.includes('tab=2'))).toBe(true)
    expect(opened.some(url => url.includes('tab=3'))).toBe(true)
    expect((add() as HTMLButtonElement).disabled).toBe(true)
    // Only the active terminal is shown.
    expect(container.querySelectorAll('[data-testid="code-shell-panel"].flex').length).toBe(1)

    await click(container.querySelector('[aria-label="Close Terminal 2"]'))
    expect(posted).toContain('/api/agent-profiles/code/projects/p1/shell/stop?tab=2')
    expect(JSON.parse(localStorage.getItem('code_terminal_tabs:p1') || '{}').tabs).toEqual([1, 3])
  })

  it('keeps the actions in one menu with their shortcuts', async () => {
    await act(async () => { root.render(<CodeShellPanel projectId="p2" />) })
    const visible = container.querySelector('[data-testid="code-shell-panel"].flex') as HTMLElement
    await click(visible.querySelector('[data-testid="code-shell-menu"]'))
    const menu = visible.querySelector('[role="menu"]')
    expect(menu?.textContent).toContain('Copy selection')
    expect(menu?.textContent).toContain('Clear screen')
    expect(menu?.textContent).toContain('New terminal')
    expect(menu?.textContent).toMatch(/Ctrl\+Shift\+C|⌘C/)
  })
})
