import { expect, it, vi } from 'vitest'
vi.mock('../services/api', () => ({ default: { post: vi.fn(async () => ({ data: { connection_id: 'saved', auth_url: 'https://google.example/consent' } })) }, agentApi: {} }))
import api from '../services/api'
import { googleAppApi } from './googleApp'

it('connects centrally and reconnects the saved ID with the exact access choices', async () => {
  const request = { workspace_path: '', allow_read_access: false, allow_agent_write_access: false, services: [{ service: 'docs', write: true }] }
  const result = await googleAppApi.reconnect('saved', request)
  expect(api.post).toHaveBeenLastCalledWith('/api/integrations/google/accounts', { ...request, action: 'reconnect', connection_id: 'saved' })
  expect(result).toEqual({ id: 'saved', auth_url: 'https://google.example/consent' })
  await googleAppApi.connectWithClient('local-app', { workspace_path: '', services: [] })
  expect(api.post).toHaveBeenLastCalledWith('/api/integrations/google/accounts', { action: 'connect', workspace_path: '', client_name: 'local-app', services: [] })
})
