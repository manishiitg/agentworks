// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'

const read = vi.hoisted(() => vi.fn())
vi.mock('../../services/api', () => ({ agentApi: { agentGroups: read, editAgentGroups: vi.fn() } }))

import { useAgentGroups } from './agentGroups'

// Real bug (28 Nov): an agent made beside its maker joined the maker's group on the server, but the menu kept its
// old groups until the window regained focus, so the new agent showed at the top of the menu.
it('reads the groups again when an agent joins the list', async () => {
  read.mockResolvedValueOnce({ a: 'cli' }).mockResolvedValueOnce({ a: 'cli', b: 'cli' })
  let seen: Record<string, string> = {}
  const Probe = ({ roster }: { roster: string }) => { seen = useAgentGroups('crew', roster); return null }
  const root = createRoot(document.createElement('div'))
  await act(async () => { root.render(<Probe roster="a" />) })
  expect(seen).toEqual({ a: 'cli' })
  await act(async () => { root.render(<Probe roster="a b" />) })
  expect(seen).toEqual({ a: 'cli', b: 'cli' })
  expect(read).toHaveBeenCalledTimes(2)
  act(() => root.unmount())
})
