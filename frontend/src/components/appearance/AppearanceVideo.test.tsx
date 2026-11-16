// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import { AppearanceVideo } from './AppearanceVideo'
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
