import { expect, it, vi } from 'vitest'
vi.mock('../services/api', () => ({ default: { post: vi.fn() }, agentApi: { updateGmailConnection: vi.fn().mockResolvedValue({}), startGmailConnectionAuth: vi.fn().mockResolvedValue({ auth_url: 'https://google.example/consent' }) } }))
import api, { agentApi } from '../services/api'
import { googleAppApi } from './googleApp'

it('reauthorizes the existing account without creating a replacement or changing its OAuth client', async () => {
  const result = await googleAppApi.reconnect('existing', { workspace_path: 'Workflow/support', allow_read_access: false, allow_agent_write_access: false, services: [{ service: 'docs', write: true }] })
  expect(agentApi.updateGmailConnection).toHaveBeenCalledExactlyOnceWith('existing', { allow_read_access: false, allow_agent_write_access: false, services: [{ service: 'docs', write: true }] })
  expect(agentApi.startGmailConnectionAuth).toHaveBeenCalledExactlyOnceWith('existing')
  expect(api.post).not.toHaveBeenCalled()
  expect(result).toEqual({ id: 'existing', auth_url: 'https://google.example/consent' })
})
