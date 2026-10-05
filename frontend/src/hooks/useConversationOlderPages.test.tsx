// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import { useConversationOlderPages } from './useConversationOlderPages'
import type { PollingEvent } from '../services/api-types'

it('keeps loaded history and cursors across main/run switches, including late responses', () => {
  vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true)
  const root = createRoot(document.createElement('div'))
  let history!: ReturnType<typeof useConversationOlderPages>
  function Probe({ session }: { session: string }) { history = useConversationOlderPages(session); return null }
  const main = { id: 'main-message', type: 'user_message' } as PollingEvent
  const run = { id: 'run-message', type: 'user_message' } as PollingEvent
  try {
    act(() => root.render(<Probe session="main" />))
    act(() => history.updatePage('main', page => ({ ...page, events: [main], pagination: { hasMore: false, nextOffset: 10 } })))
    act(() => root.render(<Probe session="run" />))
    expect(history.page.events).toEqual([])
    act(() => history.updatePage('run', page => ({ ...page, events: [run], pagination: { hasMore: true, nextOffset: 100 } })))
    // A request for the old chat finishes after the user has moved away.
    act(() => history.updatePage('main', page => ({ ...page, loading: false })))
    expect(history.page.events).toEqual([run])
    act(() => root.render(<Probe session="main" />))
    expect(history.page.events).toEqual([main])
    expect(history.page.pagination).toEqual({ hasMore: false, nextOffset: 10 })
    act(() => root.render(<Probe session="run" />))
    expect(history.page.pagination?.nextOffset).toBe(100)
  } finally { act(() => root.unmount()); vi.unstubAllGlobals() }
})
