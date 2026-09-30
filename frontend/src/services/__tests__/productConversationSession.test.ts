// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { InternalAxiosRequestConfig } from 'axios'

vi.mock('../llm-config-api', () => ({}))
import api, { agentApi } from '../api'
import { useChatStore } from '../../stores/useChatStore'

describe('product conversation session selection', () => {
  const originalAdapter = api.defaults.adapter
  beforeEach(() => {
    vi.stubGlobal('localStorage', { getItem: () => null, setItem: vi.fn(), removeItem: vi.fn() })
  })
  afterEach(() => {
    api.defaults.adapter = originalAdapter
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
  })

  function captureRequest() {
    vi.spyOn(useChatStore.getState(), 'getActiveTab').mockReturnValue({ sessionId: 'another-project-chat' } as never)
    const adapter = vi.fn(async (config: InternalAxiosRequestConfig) => ({
      config, status: 200, statusText: 'OK', headers: {},
      data: { session_id: 'canonical-project-chat', conversation_id: 'c', conversation_key: 'project-1' },
    }))
    api.defaults.adapter = adapter
    return adapter
  }

  it.each(['code', 'work'])('opens a %s project without sending the active tab session', async profile => {
    const adapter = captureRequest()
    const result = await agentApi.resolveAgentProfileConversation(profile, { conversation_key: 'project-1' })
    const request = adapter.mock.calls[0][0]
    expect(request.url).toBe(`/api/agent-profiles/${profile}/conversation`)
    expect(request.headers.toJSON()['X-Session-ID']).toBeUndefined()
    expect(result.session_id).toBe('canonical-project-chat')
  })

  it('preserves an explicitly requested existing conversation', async () => {
    const adapter = captureRequest()
    await agentApi.resolveAgentProfileConversation('code', { conversation_key: 'project-1' }, 'requested-chat')
    expect(adapter.mock.calls[0][0].headers.toJSON()['X-Session-ID']).toBe('requested-chat')
  })

  it('still supplies the active session to ordinary session-scoped requests', async () => {
    const adapter = captureRequest()
    await api.get('/api/test')
    expect(adapter.mock.calls[0][0].headers.toJSON()['X-Session-ID']).toBe('another-project-chat')
  })
})
