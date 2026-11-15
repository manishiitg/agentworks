import { beforeEach, expect, it, vi } from 'vitest'
import { defaultAppearance, resolveAppearanceProfile, type AppearanceDocument } from './appearanceTheme'

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

// Fonts and colour roles are the server's list (its guide), not a copy kept in the browser.
it('accepts the fonts and colour roles the server offers', async () => {
  const future = { ...theme('Future'), interface_font: 'future-sans', colors: { dark: { glow: '#112233' } } }
  api.get.mockResolvedValue({ data: { current: theme('Now'), saved: [future], guide: { fonts: ['system', 'future-sans'], code_fonts: ['system-mono'], colour_roles: ['background', 'glow'] } } })
  await useAppearanceStore.getState().load('s')
  expect(useAppearanceStore.getState().saved.map(item => item.name)).toEqual(['Future'])
  api.get.mockResolvedValue({ data: { current: theme('Now'), saved: [future], guide: { fonts: ['system'], code_fonts: ['system-mono'], colour_roles: ['background'] } } })
  await useAppearanceStore.getState().load('s', { force: true })
  expect(useAppearanceStore.getState().saved).toEqual([])
})

// One theme holds both variants: system follows the device, schedule uses local times.
it('resolves system and schedule policies to an effective light/dark profile', () => {
  const base = defaultAppearance()
  expect(resolveAppearanceProfile({ ...base, mode: 'light' }, true)).toBe('light')
  expect(resolveAppearanceProfile({ ...base, mode: 'dark' }, false)).toBe('dark')
  expect(resolveAppearanceProfile({ ...base, mode: 'system' }, true)).toBe('dark')
  expect(resolveAppearanceProfile({ ...base, mode: 'system' }, false)).toBe('light')
  const day = { ...base, mode: 'schedule' as const, light_at: '07:00', dark_at: '19:00' }
  const noon = new Date(2026, 5, 1, 12, 0)
  const night = new Date(2026, 5, 1, 22, 0)
  expect(resolveAppearanceProfile(day, true, noon)).toBe('light')
  expect(resolveAppearanceProfile(day, false, night)).toBe('dark')
  const overnight = { ...base, mode: 'schedule' as const, light_at: '19:00', dark_at: '07:00' }
  expect(resolveAppearanceProfile(overnight, false, night)).toBe('light')
  expect(resolveAppearanceProfile(overnight, true, noon)).toBe('dark')
})
