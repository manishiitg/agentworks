// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import { AppearanceVideo } from './AppearanceVideo'
import { readFileSync } from 'node:fs'
import postcss from 'postcss'
import { defaultAppearance } from '../../stores/appearanceTheme'
import { useAppearanceStore } from '../../stores/useAppearanceStore'
// Network transport is the boundary; do not initialize unrelated app stores.
vi.mock('../../services/api', () => ({ default: { get: vi.fn(), put: vi.fn() } }))
vi.mock('../../stores/useAuthStore', () => ({ useAuthStore: (select: (s: unknown) => unknown) => select({ user: { id: 'person' } }) }))
vi.mock('../../stores/useWorkspaceConnectionStore', () => ({ useWorkspaceConnectionStore: (select: (s: unknown) => unknown) => select({ activeWorkspaceId: 'server' }) }))
afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals(); useAppearanceStore.getState().resetScope(''); delete document.documentElement.dataset.appearanceVideo })
it('pauses passive video on hidden/reduced motion, disposes its source, and refuses a stale account scope', async () => {
  const media = new EventTarget() as EventTarget & { matches: boolean }
  media.matches = false
  vi.stubGlobal('matchMedia', () => media)
  vi.spyOn(document, 'hidden', 'get').mockReturnValue(false)
  const play = vi.spyOn(HTMLMediaElement.prototype, 'play').mockResolvedValue()
  const pause = vi.spyOn(HTMLMediaElement.prototype, 'pause').mockImplementation(() => {})
  vi.spyOn(HTMLMediaElement.prototype, 'load').mockImplementation(() => {})
  const theme = { ...defaultAppearance(), background_video: { url: 'https://example.com/space.mp4', dim: .7, playback_rate: .5 } }
  useAppearanceStore.setState({ current: theme, scope: JSON.stringify(['server', 'person']) })
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  await act(async () => root.render(<AppearanceVideo />))
  const video = host.querySelector('video')!
  expect(play).toHaveBeenCalled()
  expect(video.muted).toBe(true)
  expect(video.controls).toBe(false)
  await act(async () => video.dispatchEvent(new Event('loadeddata')))
  expect(document.documentElement.hasAttribute('data-appearance-video')).toBe(true)
  vi.spyOn(document, 'hidden', 'get').mockReturnValue(true)
  const before = play.mock.calls.length
  await act(async () => document.dispatchEvent(new Event('visibilitychange')))
  expect(pause).toHaveBeenCalled()
  expect(play.mock.calls.length).toBe(before)
  vi.spyOn(document, 'hidden', 'get').mockReturnValue(false)
  media.matches = true
  await act(async () => media.dispatchEvent(new Event('change')))
  expect(play.mock.calls.length).toBe(before)
  await act(async () => useAppearanceStore.setState({ scope: 'other-person' }))
  expect(host.querySelector('video')).toBeNull()
  expect(video.hasAttribute('src')).toBe(false)
  expect(document.documentElement.hasAttribute('data-appearance-video')).toBe(false)
  await act(async () => root.unmount()); host.remove()
})

// A local clip uses an authenticated bounded download, never a login in its URL.
it('revokes an owned media blob on account change and ignores a late response', async () => {
  const { default: api } = await import('../../services/api')
  const objectURL = vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:personal-video')
  const revoke = vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {})
  vi.spyOn(HTMLMediaElement.prototype, 'play').mockResolvedValue()
  vi.spyOn(HTMLMediaElement.prototype, 'pause').mockImplementation(() => {})
  vi.spyOn(HTMLMediaElement.prototype, 'load').mockImplementation(() => {})
  const media = new EventTarget() as EventTarget & { matches: boolean }; media.matches = false
  vi.stubGlobal('matchMedia', () => media)
  vi.mocked(api.get).mockResolvedValue({ data: new Blob(['clip'], { type: 'video/mp4' }) } as Awaited<ReturnType<typeof api.get>>)
  const theme = { ...defaultAppearance(), background_video: { asset_id: 'a'.repeat(64), dim: .7, playback_rate: .5 } }
  useAppearanceStore.setState({ current: theme, scope: JSON.stringify(['server', 'person']) })
  const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
  await act(async () => root.render(<AppearanceVideo />))
  expect(api.get).toHaveBeenCalledWith(`/api/appearance/videos/${'a'.repeat(64)}`, expect.objectContaining({ responseType: 'blob', skipSessionContext: true }))
  expect(host.querySelector('video')?.getAttribute('src')).toBe('blob:personal-video')
  await act(async () => useAppearanceStore.setState({ scope: 'other-person' }))
  expect(revoke).toHaveBeenCalledWith('blob:personal-video')
  expect(host.querySelector('video')).toBeNull()
  let resolve!: (value: Awaited<ReturnType<typeof api.get>>) => void
  vi.mocked(api.get).mockImplementation(() => new Promise(r => { resolve = r }) as ReturnType<typeof api.get>)
  await act(async () => useAppearanceStore.setState({ scope: JSON.stringify(['server', 'person']) }))
  await act(async () => root.unmount())
  await act(async () => resolve({ data: new Blob(['late'], { type: 'video/mp4' }) } as Awaited<ReturnType<typeof api.get>>))
  expect(objectURL).toHaveBeenCalledTimes(1)
  host.remove()
})

// Read the actual sheet: Vitest disables CSS imports, including ?raw.
it('keeps dialog max-width utilities above the viewport fallback and excludes wallpaper', () => {
  const selectors: string[] = []
  const appCSS = readFileSync('src/App.css', 'utf8')
  postcss.parse(appCSS).walkRules(rule => {
    if (rule.nodes.some(node => node.type === 'decl' && node.prop === 'max-width')) selectors.push(rule.selector)
  })
  expect(selectors.length).toBeGreaterThan(0)
  const wallpaper = document.createElement('div'); wallpaper.className = 'fixed inset-0'; wallpaper.setAttribute('data-theme-video', '')
  wallpaper.append(document.createElement('video'), document.createElement('div'))
  for (const child of wallpaper.children) expect(selectors.some(selector => child.matches(selector))).toBe(false)
  wallpaper.removeAttribute('data-theme-video')
  for (const child of wallpaper.children) expect(selectors.some(selector => child.matches(selector))).toBe(true)

  // #2939: App.css loads after Tailwind. A :not outside :where raised the
  // fallback's specificity and replaced max-w-lg with the viewport width.
  const style = document.createElement('style')
  style.textContent = '.max-w-lg { max-width: 32rem; } .max-w-2xl { max-width: 42rem; }\n' + appCSS
  const overlay = document.createElement('div'); overlay.className = 'fixed inset-0'
  const dialog = document.createElement('div'); dialog.setAttribute('role', 'dialog')
  const control = document.createElement('div')
  overlay.append(dialog); document.head.append(style); document.body.append(overlay, control)
  try {
    for (const className of ['max-w-lg', 'max-w-2xl']) {
      dialog.className = control.className = className
      const intended = getComputedStyle(control).maxWidth
      expect(intended).not.toBe('')
      expect(getComputedStyle(dialog).maxWidth).toBe(intended)
    }
    dialog.className = ''
    expect(getComputedStyle(dialog).maxWidth).not.toBe('')
  } finally { style.remove(); overlay.remove(); control.remove() }
})
