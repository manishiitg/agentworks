// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import { AppearanceVideo } from './AppearanceVideo'
import appCSS from '../../App.css?raw'
import { defaultAppearance, validateTheme } from '../../stores/appearanceTheme'
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
  const theme = validateTheme({ ...defaultAppearance(), background_video: { url: 'https://example.com/space.mp4', dim: .7, playback_rate: .5 } })
  useAppearanceStore.setState({ current: theme, scope: JSON.stringify(['server', 'person']) })
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  await act(async () => root.render(<AppearanceVideo />))
  const video = host.querySelector('video')!
  expect(play).toHaveBeenCalled()
  expect(video.muted).toBe(true)
  expect(video.controls).toBe(false)
  // PLAT-746-1040: generic modal clamps left a bright right/bottom video strip.
  const sheet = new CSSStyleSheet(); sheet.replaceSync(appCSS)
  const sizingSelectors = (rules: CSSRuleList): string[] => Array.from(rules).flatMap(rule => {
    if (rule instanceof CSSStyleRule && rule.style.getPropertyValue('max-width')) return [rule.selectorText]
    if ('cssRules' in rule) return sizingSelectors((rule as CSSGroupingRule).cssRules)
    return []
  })
  const selectors = sizingSelectors(sheet.cssRules)
  expect(selectors.length).toBeGreaterThan(0)
  for (const child of host.querySelector('[data-theme-video]')!.children) {
    expect(selectors.some(selector => child.matches(selector))).toBe(false)
  }
  const modal = document.createElement('div'); modal.className = 'fixed inset-0'
  const card = document.createElement('div'); modal.append(card)
  expect(selectors.some(selector => card.matches(selector))).toBe(true)
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
  for (const url of ['file:///private/movie.mov', 'javascript:alert(1)', 'https://person:password@example.com/a.mp4']) expect(() => validateTheme({ ...theme, background_video: { ...theme.background_video, url } })).toThrow()
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
  const theme = validateTheme({ ...defaultAppearance(), background_video: { asset_id: 'a'.repeat(64), dim: .7, playback_rate: .5 } })
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
