// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

const hydrate = vi.hoisted(() => vi.fn())
vi.mock('../utils/sessionRestore', () => ({ hydrateTabEvents: hydrate }))
import { useFormattedTranscriptHydration } from './useFormattedTranscriptHydration'

let root: Root
beforeEach(() => {
  hydrate.mockReset().mockResolvedValue({})
  root = createRoot(document.createElement('div'))
})
afterEach(() => act(() => root.unmount()))

function Probe({ session = 'claude-chat', formatted = true, enabled = true, workspace = 'Code/demo' }) {
  useFormattedTranscriptHydration(session, formatted, workspace, enabled)
  return null
}

it('refreshes the same chat after every terminal visit, without fetching on terminal renders', async () => {
  await act(async () => root.render(<Probe />))
  expect(hydrate).toHaveBeenCalledTimes(1)
  for (let visit = 0; visit < 3; visit++) {
    await act(async () => root.render(<Probe formatted={false} />))
    await act(async () => root.render(<Probe formatted={false} />))
    expect(hydrate).toHaveBeenCalledTimes(visit + 1)
    await act(async () => root.render(<Probe />))
    expect(hydrate).toHaveBeenCalledTimes(visit + 2)
  }
  expect(hydrate).toHaveBeenLastCalledWith('claude-chat', expect.objectContaining({ workspacePath: 'Code/demo' }))
  // Re-entry must use the same per-tab projection as initial restore.
  expect(hydrate.mock.calls.every(call => call[1].compact === undefined)).toBe(true)
})

it('refreshes the newly visible conversation and workspace without a once-per-session cache', async () => {
  await act(async () => root.render(<Probe session="one" />))
  await act(async () => root.render(<Probe session="two" workspace="Crew/two" />))
  await act(async () => root.render(<Probe session="one" />))
  expect(hydrate.mock.calls.map(call => call[0])).toEqual(['one', 'two', 'one'])
  expect(hydrate.mock.calls[1][1].workspacePath).toBe('Crew/two')
})

it('leaves execution conversations to their own history transport', async () => {
  await act(async () => root.render(<Probe enabled={false} />))
  await act(async () => root.render(<Probe enabled={false} formatted={false} />))
  await act(async () => root.render(<Probe enabled={false} />))
  expect(hydrate).not.toHaveBeenCalled()
})
