// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

// Owner, 20 Nov (#3383): an agent starting or stopping on another network shows at once, from that network's own
// live stream, instead of after the next 10 s poll.
const { feeds, feed } = vi.hoisted(() => {
  const feeds = new Map<string, { status: string; changed?: () => void }>()
  const feed = (base: string) => {
    if (!feeds.has(base)) feeds.set(base, { status: 'live' })
    const entry = feeds.get(base)!
    return { getStatus: () => entry.status, subscribe: (_kinds: unknown, _scope: unknown, changed: () => void) => { entry.changed = changed; return () => { entry.changed = undefined } } }
  }
  return { feeds, feed }
})
vi.mock('../services/liveFeed', () => ({ liveFeed: feed('active'), connectionLiveFeed: (base: string) => feed(base) }))
vi.mock('../services/desktopConnections', () => ({ desktopRequest: (path = '') => Promise.resolve(path ? { status: 'connected', projects: [] } : { connections: [{ id: 'abc', name: 'Server', server: 'https://example.com' }] }) }))
vi.mock('../services/api', () => ({
  agentApi: { getHeaderSummary: () => Promise.resolve({ active_sessions: [] }) },
  getAuthToken: () => 'token', getDesktopApiBaseUrl: () => 'http://mac', getLocalApiBaseUrl: () => 'http://mac',
}))

import { useConnectedWorkPolling, useConnectedWorkStore } from './useConnectedWorkStore'

const reads: string[] = []
beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  vi.useFakeTimers()
  Object.assign(window, { electronAPI: { getDesktopConnection: () => 'local' } })
  vi.stubGlobal('fetch', vi.fn((url: string) => {
    reads.push(url)
    return Promise.resolve({ ok: true, json: () => Promise.resolve({ active_sessions: [{ session_id: 'running' }] }) })
  }))
})
afterEach(() => { vi.useRealTimers(); vi.unstubAllGlobals() })

function Menu() { useConnectedWorkPolling(); return null }

it('reads another network\'s activity as soon as its live stream says agents changed', async () => {
  const root = createRoot(document.createElement('div'))
  await act(async () => { root.render(<Menu />) })
  await act(async () => { await vi.advanceTimersByTimeAsync(10) })
  const remote = feeds.get('http://mac/api/connections/abc')
  expect(remote?.changed).toBeTypeOf('function')
  reads.length = 0
  await act(async () => { remote!.changed!(); await vi.advanceTimersByTimeAsync(2_100) })
  expect(reads).toEqual(['http://mac/api/connections/abc/api/header-summary'])
  expect(useConnectedWorkStore.getState().activity.abc?.sessions).toEqual([{ session_id: 'running' }])
  // While its stream is live, the 10 s poll leaves that network alone.
  reads.length = 0
  await act(async () => { await vi.advanceTimersByTimeAsync(10_000) })
  expect(reads).toEqual([])
  await act(async () => { root.unmount() })
  expect(remote?.changed).toBeUndefined()
})
