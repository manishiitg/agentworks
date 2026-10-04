import { expect, it, vi } from 'vitest'
vi.mock('../services/api', () => ({ default: { post: vi.fn() }, agentApi: { createGmailConnection: vi.fn().mockResolvedValue({ id: 'new' }), updateGmailConnection: vi.fn().mockResolvedValue({}), startGmailConnectionAuth: vi.fn().mockResolvedValue({ auth_url: 'https://google.example/consent' }) } }))
import api, { agentApi } from '../services/api'
import { googleAppApi } from './googleApp'

it('reauthorizes the existing account without creating a replacement or changing its OAuth client', async () => {
  const result = await googleAppApi.reconnect('existing', { workspace_path: 'Workflow/support', allow_read_access: false, allow_agent_write_access: false, services: [{ service: 'docs', write: true }] })
  expect(agentApi.updateGmailConnection).toHaveBeenCalledExactlyOnceWith('existing', { allow_read_access: false, allow_agent_write_access: false, services: [{ service: 'docs', write: true }], services_set: true })
  expect(agentApi.startGmailConnectionAuth).toHaveBeenCalledExactlyOnceWith('existing')
  expect(api.post).not.toHaveBeenCalled()
  expect(result).toEqual({ id: 'existing', auth_url: 'https://google.example/consent' })
})

it('creates a connection with the selected named app and workspace scope', async () => {
  vi.clearAllMocks()
  await googleAppApi.connectWithClient('local-client', { workspace_path: 'Chats/Code/projects/mine', allow_read_access: false, services: [] })
  expect(agentApi.createGmailConnection).toHaveBeenCalledWith({ client_name: 'local-client', workspace_path: 'Chats/Code/projects/mine', display_name: 'Google account', allow_read_access: false, services: [] })
  expect(agentApi.startGmailConnectionAuth).toHaveBeenCalledWith('new')
  expect(api.post).not.toHaveBeenCalled()
})

it('distinguishes clearing services from leaving their settings unchanged', async () => {
  vi.clearAllMocks()
  await googleAppApi.reconnect('existing', { workspace_path: 'Workflow/support', services: [] })
  expect(agentApi.updateGmailConnection).toHaveBeenLastCalledWith('existing', expect.objectContaining({ services: [], services_set: true }))
  await googleAppApi.reconnect('existing', { workspace_path: 'Workflow/support', allow_read_access: true })
  expect(agentApi.updateGmailConnection).toHaveBeenLastCalledWith('existing', expect.objectContaining({ services: undefined, services_set: false }))
})
