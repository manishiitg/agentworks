// @vitest-environment happy-dom
import { afterEach, expect, it, vi } from 'vitest'
import { clearChatAccess, getChatAccessNotice, readChat } from './chatAccess'
import { SSEConnection } from '../../shared/session/sse'
const scope = vi.hoisted(() => ({ value: 1 }))
vi.mock('../utils/chatIdentity', () => ({ captureChatIdentity: () => scope.value, isChatIdentityCurrent: (v: number) => v === scope.value }))
afterEach(() => { scope.value++; vi.unstubAllGlobals(); vi.useRealTimers() })

it('stops forbidden reads until explicit server re-resolution, without leaking a refusal across accounts', async () => {
  const refused = vi.fn(async () => { throw { response: { status: 403 } } })
  await expect(readChat('stale', refused)).rejects.toBeDefined()
  await expect(readChat('stale', refused)).rejects.toBeDefined()
  expect(refused).toHaveBeenCalledTimes(1)
  expect(getChatAccessNotice('stale')).toContain('no longer have access')
  clearChatAccess('stale') // only successful explicit server resolve calls this in production
  await expect(readChat('stale', async () => 'admitted')).resolves.toBe('admitted')
  scope.value++
  await expect(readChat('stale', async () => 'different account')).resolves.toBe('different account')
  const unavailable = vi.fn(async () => { throw { response: { status: 503 } } })
  for (let i = 0; i < 2; i++) await expect(readChat('offline', unavailable)).rejects.toBeDefined()
  expect(unavailable).toHaveBeenCalledTimes(2)
  expect(getChatAccessNotice('offline')).toBeUndefined()
})

it('closes a 403 stream once without reconnect, online wake or polling fallback', async () => {
  const fetcher = vi.fn(async () => new Response('', { status: 403 }))
  vi.stubGlobal('fetch', fetcher)
  const forbidden = vi.fn(), fallback = vi.fn()
  const conn = new SSEConnection({ sessionId: 'stale', sinceIndex: 0, baseUrl: 'https://example.com', callbacks: { onMessage: vi.fn(), onStatusUpdate: vi.fn(), onForbidden: forbidden, onError: fallback } })
  try {
    await vi.waitFor(() => expect(forbidden).toHaveBeenCalledOnce())
    window.dispatchEvent(new Event('online'))
    expect(conn.isClosed).toBe(true)
    expect(fetcher).toHaveBeenCalledOnce()
    expect(fallback).not.toHaveBeenCalled()
  } finally { conn.close() }
})
