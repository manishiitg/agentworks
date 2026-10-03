import { afterEach, describe, expect, it, vi } from 'vitest'

vi.mock('../services/api', () => ({ getApiBaseUrl: () => '', getAuthToken: () => null }))

import { loadWorkflowProductCommands, loadAgentworksProductCommands, resetAgentworksProductCommandsCache } from './agentworksProductData'

describe('AgentWorks product commands loader', () => {
  afterEach(() => { resetAgentworksProductCommandsCache(); vi.unstubAllGlobals() })

  it('asks once per page and treats a missing profile as no commands', async () => {
    const fetchMock = vi.fn(async () => new Response('', { status: 404 }))
    vi.stubGlobal('fetch', fetchMock)
    expect(await loadAgentworksProductCommands()).toEqual([])
    expect(await loadAgentworksProductCommands()).toEqual([])
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it('retries after a transient failure', async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(new Response('', { status: 502 }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ commands: [] }), { status: 200 }))
    vi.stubGlobal('fetch', fetchMock)
    await expect(loadAgentworksProductCommands()).rejects.toThrow()
    await expect(loadAgentworksProductCommands()).resolves.toEqual([])
    expect(fetchMock).toHaveBeenCalledTimes(2)
  })
})


it('loads and caches Relay commands separately from AgentWorks', async () => {
  resetAgentworksProductCommandsCache()
  const fetchMock = vi.fn(async (url: string) => new Response(JSON.stringify({ commands: [{
    name: url.includes('/relays?') ? 'publish' : 'design-dashboard', prompt: url,
  }] }), { status: 200 }))
  vi.stubGlobal('fetch', fetchMock)
  try {
    expect((await loadWorkflowProductCommands('relays'))[0].name).toBe('publish')
    expect((await loadAgentworksProductCommands())[0].name).toBe('design-dashboard')
    await loadWorkflowProductCommands('relays')
    expect(fetchMock).toHaveBeenCalledTimes(2)
    expect(fetchMock).toHaveBeenCalledWith('/api/agent-profiles/relays?version=1', expect.anything())
  } finally { resetAgentworksProductCommandsCache(); vi.unstubAllGlobals() }
})
