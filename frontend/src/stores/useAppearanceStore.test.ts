import { beforeEach, expect, it, vi } from 'vitest'
import { defaultAppearance, type AppearanceDocument } from './appearanceTheme'

const api = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn() }))
vi.mock('../services/api', () => ({ default: api }))
import { useAppearanceStore } from './useAppearanceStore'

const theme = (name: string) => ({ ...defaultAppearance(), name })
beforeEach(() => { api.get.mockReset(); api.put.mockReset(); useAppearanceStore.getState().resetScope('s') })

// PLAT-746 theme review: a failed save must not leave a preview that blocks every later load (including an agent's
// change).
it('drops the preview after a failed save so a later load shows the new look', async () => {
  api.put.mockRejectedValue(new Error('offline'))
  expect(await useAppearanceStore.getState().change('apply', theme('Draft'))).toBe(false)
  expect(useAppearanceStore.getState().preview).toBeNull()
  expect(useAppearanceStore.getState().error).toContain('previous look is back')
  api.get.mockResolvedValue({ data: { current: theme('From an agent'), saved: [] } })
  await useAppearanceStore.getState().load('s')
  expect(useAppearanceStore.getState().current.name).toBe('From an agent')
})

// #3085: a theme with a field this browser does not know was silently replaced by Default.
// The server decides what is valid; only entries that cannot render at all are left out.
it('keeps themes with unknown fields and skips only entries that cannot render', async () => {
  const document = { current: { ...theme('Future'), sparkle: { level: 3 } }, saved: [theme('Good'), 'nonsense', null] } as unknown as AppearanceDocument
  api.get.mockResolvedValue({ data: document })
  await useAppearanceStore.getState().load('s')
  const { current, saved, loaded, error } = useAppearanceStore.getState()
  expect([current.name, saved.map(item => item.name), loaded, error]).toEqual(['Future', ['Good'], true, ''])
})

// The big document is not refetched on every refocus, only when stale or when a live change says so.
it('reuses a fresh document on refocus and reloads when forced', async () => {
  api.get.mockResolvedValue({ data: { current: theme('One'), saved: [] } })
  await useAppearanceStore.getState().load('s')
  await useAppearanceStore.getState().load('s')
  expect(api.get).toHaveBeenCalledTimes(1)
  await useAppearanceStore.getState().load('s', { force: true })
  expect(api.get).toHaveBeenCalledTimes(2)
  vi.useFakeTimers({ now: Date.now() + 61_000 })
  try { await useAppearanceStore.getState().load('s') } finally { vi.useRealTimers() }
  expect(api.get).toHaveBeenCalledTimes(3)
})
