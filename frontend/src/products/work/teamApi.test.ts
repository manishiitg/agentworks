// @vitest-environment happy-dom
import { expect, it, vi } from 'vitest'

const { request } = vi.hoisted(() => ({ request: vi.fn() }))
vi.mock('../../services/api', () => ({ default: { request }, getAuthToken: () => '', getDesktopApiBaseUrl: () => '', getLocalApiBaseUrl: () => '' }))

import { teamRequest } from './teamApi'

// #3760: a refused Team read was asked again every few seconds for an hour; a 403 is a steady answer until the Team changes.
it('answers a refused Team read without asking the server again until the Team changes', async () => {
  request.mockRejectedValue(Object.assign(new Error('not in this Team'), { response: { status: 403, data: { error: { code: 'team_forbidden' } } } }))
  await expect(teamRequest({ project: 'p' }, '/terminal-agents')).rejects.toThrow('not in this Team')
  await expect(teamRequest({ project: 'p' }, '/terminal-agents')).rejects.toThrow('not in this Team')
  expect(request).toHaveBeenCalledTimes(1)
  window.dispatchEvent(new CustomEvent('agentworks:team-updated'))
  request.mockResolvedValue({ data: { agents: [] } })
  await expect(teamRequest({ project: 'p' }, '/terminal-agents')).resolves.toEqual({ agents: [] })
  expect(request).toHaveBeenCalledTimes(2)
})
