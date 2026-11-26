// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import { ThemeProvider } from '../../contexts/ThemeContext.tsx'
import { defaultAppearance } from '../../stores/appearanceTheme'
import { useAppearanceStore } from '../../stores/useAppearanceStore'
import AppearancePreviewPage from './AppearancePreviewPage'

const fixture = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn() }))
vi.mock('../../services/api', () => ({ default: { get: fixture.get, put: fixture.put } }))
vi.mock('../../stores/useAuthStore', () => ({ useAuthStore: (select: (state: unknown) => unknown) => select({ user: { id: 'owner' } }) }))
vi.mock('../../stores/useWorkspaceConnectionStore', () => ({ useWorkspaceConnectionStore: (select: (state: unknown) => unknown) => select({ activeWorkspaceId: 'local' }) }))
vi.mock('../../services/liveFeed', () => ({ liveFeed: { subscribe: () => () => {} } }))
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
afterEach(() => { vi.useRealTimers(); window.history.replaceState(null, '', '/') })

// #3085: an agent's preview shows only in the preview tab, in the asked-for mode,
// without changing the person's saved look; the tab writes nothing but its report.
it('shows the agent preview in the requested mode, keeps the saved look and sends one preview report', async () => {
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout', 'setInterval', 'clearInterval'] })
  window.history.replaceState(null, '', '/appearance/preview?mode=light')
  const saved = { ...defaultAppearance(), name: 'Saved', mode: 'dark' as const }
  const preview = { ...defaultAppearance(), name: 'Draft', mode: 'dark' as const, css: 'input[value="x"] { color: red; } [data-app-main] { color: inherit; }' }
  fixture.get.mockResolvedValue({ data: { current: saved, saved: [saved], preview, guide: { hooks: ['[data-theme-message-role="user"]', '[data-theme-tool-state]'] } } })
  fixture.put.mockResolvedValue({ data: {} })
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  try {
    await act(async () => root.render(<ThemeProvider><AppearancePreviewPage /></ThemeProvider>))
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(host.textContent).toContain('Draft')
    expect(document.documentElement.dataset.appearance).toBe('light')
    expect(useAppearanceStore.getState().current).toMatchObject({ name: 'Saved', mode: 'dark' })
    await act(async () => { await vi.advanceTimersByTimeAsync(2100) })
    expect(fixture.put).toHaveBeenCalledTimes(1)
    const [, body] = fixture.put.mock.calls[0]
    expect(body.operation).toBe('report')
    expect(body.report).toMatchObject({ theme: 'Draft', source: 'preview', mode: 'light' })
    expect(body.report.dropped).toHaveLength(1)
    expect(body.report.hooks['[data-theme-message-role="user"]']).toBeGreaterThan(0)
    expect(body.report.hooks['[data-theme-tool-state]']).toBe(3)
    // Polling the same document again sends nothing new.
    await act(async () => { await vi.advanceTimersByTimeAsync(6100) })
    expect(fixture.put).toHaveBeenCalledTimes(1)
  } finally {
    await act(async () => root.unmount())
    host.remove()
  }
})

// #4075: the preview has a right-panel stub and the same html[data-right-panel-open] hook as the app.
it('toggles the stub right panel and its html hook', async () => {
  window.history.replaceState(null, '', '/appearance/preview?panel=closed')
  const saved = { ...defaultAppearance(), name: 'Saved' }
  fixture.get.mockResolvedValue({ data: { current: saved, saved: [saved], guide: { hooks: [] } } })
  fixture.put.mockResolvedValue({ data: {} })
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  try {
    await act(async () => root.render(<ThemeProvider><AppearancePreviewPage /></ThemeProvider>))
    expect(document.documentElement.dataset.rightPanelOpen).toBe('false')
    expect(host.querySelector('[aria-label="Sample workspace panel"]')).toBeNull()
    const toggle = [...host.querySelectorAll('button')].find(button => button.textContent === 'Open right panel')!
    await act(async () => toggle.click())
    expect(document.documentElement.dataset.rightPanelOpen).toBe('true')
    expect(host.querySelector('[aria-label="Sample workspace panel"]')).not.toBeNull()
  } finally {
    await act(async () => root.unmount())
    host.remove()
  }
  expect(document.documentElement.dataset.rightPanelOpen).toBeUndefined()
})
