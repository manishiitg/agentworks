import { afterEach, expect, it, vi } from 'vitest'
import { randomUUID } from './randomUUID'

afterEach(() => vi.unstubAllGlobals())

// #2910: HTTP browsers omit randomUUID; sign-in requests still need a UUID.
it('generates v4 IDs without randomUUID, and keeps the native secure-context path', () => {
  const getRandomValues = vi.fn((bytes: Uint8Array) => {
    bytes.fill(255)
    return bytes
  })
  vi.stubGlobal('crypto', { getRandomValues })
  expect(randomUUID()).toBe('ffffffff-ffff-4fff-bfff-ffffffffffff')
  expect(getRandomValues).toHaveBeenCalledOnce()

  const native = vi.fn(() => 'd9428888-122b-4e1e-8a3e-2311b145ea86')
  vi.stubGlobal('crypto', { randomUUID: native, getRandomValues })
  expect(randomUUID()).toBe('d9428888-122b-4e1e-8a3e-2311b145ea86')
  expect(native).toHaveBeenCalledOnce()
  expect(getRandomValues).toHaveBeenCalledOnce()
})
