import { beforeEach, expect, it, vi } from 'vitest'
import { defaultAppearance, type AppearanceDocument } from './appearanceTheme'

const api = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn() }))
vi.mock('../services/api', () => ({ default: api }))
import { useAppearanceStore } from './useAppearanceStore'

const theme = (name: string) => ({ ...defaultAppearance(), name })
beforeEach(() => { api.get.mockReset(); api.put.mockReset(); useAppearanceStore.getState().resetScope('s') })

// PLAT-746 theme review: a failed save must not leave a preview that blocks every later load (including an agent's
// change), and one saved theme that no longer validates must not hide the others.
it('drops the preview after a failed save so a later load shows the new look', async () => {
  api.put.mockRejectedValue(new Error('offline'))
  expect(await useAppearanceStore.getState().change('apply', theme('Draft'))).toBe(false)
  expect(useAppearanceStore.getState().preview).toBeNull()
  expect(useAppearanceStore.getState().error).toContain('previous look is back')
  api.get.mockResolvedValue({ data: { current: theme('From an agent'), saved: [] } })
  await useAppearanceStore.getState().load('s')
  expect(useAppearanceStore.getState().current.name).toBe('From an agent')
})

it('skips a saved theme that no longer validates instead of failing the whole load', async () => {
  const document = { current: theme('Fine'), saved: [theme('Good'), { name: 'Broken', mode: 'sepia' }, 'nonsense'] } as unknown as AppearanceDocument
  api.get.mockResolvedValue({ data: document })
  await useAppearanceStore.getState().load('s')
  const { current, saved, loaded, error } = useAppearanceStore.getState()
  expect([current.name, saved.map(item => item.name), loaded, error]).toEqual(['Fine', ['Good'], true, ''])
})
