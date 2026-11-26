import { expect, it } from 'vitest'
import { formatRelativeIso, formatRelativeSeconds } from './relativeTime'

it('formats compact relative Unix seconds consistently', () => {
  const now = 1_700_000_000
  expect(formatRelativeSeconds(undefined, { nowSeconds: now })).toBe('')
  expect(formatRelativeSeconds(null, { nowSeconds: now })).toBe('')
  expect(formatRelativeSeconds(Number.NaN, { nowSeconds: now })).toBe('')
  expect(formatRelativeSeconds(now + 120, { nowSeconds: now })).toBe('now')
  expect(formatRelativeSeconds(now - 30, { nowSeconds: now })).toBe('now')
  expect(formatRelativeSeconds(now - 5 * 60, { nowSeconds: now })).toBe('5m')
  expect(formatRelativeSeconds(now - 3 * 3600, { nowSeconds: now })).toBe('3h')
  expect(formatRelativeSeconds(now - 4 * 86400, { nowSeconds: now })).toBe('4d')
})

it('formats long relative Unix seconds with ago suffix, weeks, and months', () => {
  const now = 1_700_000_000
  expect(formatRelativeSeconds(now - 30, { nowSeconds: now, style: 'long' })).toBe('now')
  expect(formatRelativeSeconds(now - 5 * 60, { nowSeconds: now, style: 'long' })).toBe('5m ago')
  expect(formatRelativeSeconds(now - 2 * 3600, { nowSeconds: now, style: 'long' })).toBe('2h ago')
  expect(formatRelativeSeconds(now - 3 * 86400, { nowSeconds: now, style: 'long' })).toBe('3d ago')
  expect(formatRelativeSeconds(now - 35 * 86400, { nowSeconds: now, style: 'long' })).toBe('5w ago')
  expect(formatRelativeSeconds(now - 90 * 86400, { nowSeconds: now, style: 'long' })).toBe('3mo ago')
})

it('formats ISO timestamps and ignores empty or invalid values', () => {
  const nowMs = Date.parse('2026-11-26T12:00:00Z')
  expect(formatRelativeIso(undefined, { nowMs })).toBe('')
  expect(formatRelativeIso('', { nowMs })).toBe('')
  expect(formatRelativeIso('not-a-date', { nowMs })).toBe('')
  expect(formatRelativeIso('2026-11-26T11:59:30Z', { nowMs })).toBe('now')
  expect(formatRelativeIso('2026-11-26T11:45:00Z', { nowMs })).toBe('15m')
  expect(formatRelativeIso('2026-11-26T09:00:00Z', { nowMs })).toBe('3h')
  expect(formatRelativeIso('2026-11-24T12:00:00Z', { nowMs })).toBe('2d')
})
