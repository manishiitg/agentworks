import { afterEach, expect, it, vi } from 'vitest'
import { agentApi } from '../../services/api'
import type { AgentProfileSideChat } from '../../services/api-types'
import { invalidateChatIdentity } from '../../utils/chatIdentity'
import { clearSideChatListCache, listProjectSideChats } from './sideChatListCache'

const chat = (key: string): AgentProfileSideChat => ({
  conversation_key: key,
  name: key,
  session_id: `sess-${key}`,
})

afterEach(() => {
  clearSideChatListCache()
  vi.restoreAllMocks()
})

it('deduplicates concurrent and fresh reads and returns isolated copies', async () => {
  let resolveFetch!: (rows: AgentProfileSideChat[]) => void
  const spy = vi.spyOn(agentApi, 'listAgentProfileSideChats').mockImplementation(
    () => new Promise<AgentProfileSideChat[]>(resolve => { resolveFetch = resolve }),
  )

  const first = listProjectSideChats('work', 'proj-1', true)
  const second = listProjectSideChats('work', 'proj-1', true)
  expect(spy).toHaveBeenCalledOnce()

  resolveFetch([chat('main')])
  const [rowsA, rowsB] = await Promise.all([first, second])
  expect(rowsA).toEqual([chat('main')])
  expect(rowsB).toEqual([chat('main')])

  // Mutating one caller's returned array must not corrupt the cached copy.
  rowsA.push(chat('mutated'))
  const cached = await listProjectSideChats('work', 'proj-1', true)
  expect(spy).toHaveBeenCalledOnce()
  expect(cached).toEqual([chat('main')])

  // Force bypasses cache and refreshes it for subsequent callers.
  spy.mockResolvedValueOnce([chat('main'), chat('helper')])
  const forced = await listProjectSideChats('work', 'proj-1', true, true)
  expect(spy).toHaveBeenCalledTimes(2)
  expect(forced).toEqual([chat('main'), chat('helper')])

  const afterForce = await listProjectSideChats('work', 'proj-1', true)
  expect(spy).toHaveBeenCalledTimes(2)
  expect(afterForce).toEqual([chat('main'), chat('helper')])
})

it('isolates cached entries across chat identity changes', async () => {
  const spy = vi.spyOn(agentApi, 'listAgentProfileSideChats')
    .mockResolvedValueOnce([chat('account-a')])
    .mockResolvedValueOnce([chat('account-b')])

  expect(await listProjectSideChats('work', 'proj-1', true)).toEqual([chat('account-a')])
  invalidateChatIdentity()
  expect(await listProjectSideChats('work', 'proj-1', true)).toEqual([chat('account-b')])
  expect(spy).toHaveBeenCalledTimes(2)
})
