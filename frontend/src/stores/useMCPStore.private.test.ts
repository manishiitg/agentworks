// @vitest-environment happy-dom
import { beforeEach, expect, it, vi } from 'vitest'
const mocks = vi.hoisted(() => ({ getTools: vi.fn(), getToolDetail: vi.fn() }))
vi.mock('../services/api', () => ({ agentApi: mocks }))
vi.mock('../services/mcpConfigApi', () => ({ mcpConfigApi: { getServerLogs: vi.fn() } }))
import { useMCPStore } from './useMCPStore'
beforeEach(() => {
  vi.clearAllMocks()
  useMCPStore.persist.setOptions({ storage: { getItem: () => null, setItem: () => {}, removeItem: () => {} } })
  useMCPStore.getState().reset()
})
it("rejects a previous account's inventory and tool details after reset", async () => {
  let resolveTools!: (value: unknown) => void
  let resolveDetail!: (value: unknown) => void
  mocks.getTools.mockImplementation(() => new Promise(resolve => { resolveTools = resolve }))
  mocks.getToolDetail.mockImplementation(() => new Promise(resolve => { resolveDetail = resolve }))
  const inventory = useMCPStore.getState().refreshTools()
  const details = useMCPStore.getState().loadToolDetails('alice-private')
  useMCPStore.getState().reset()
  resolveTools([{ name: 'alice-private', server: 'alice-private', connection: 'connected' }])
  resolveDetail({ name: 'alice-private', tools: [{ name: 'private-project-name' }] })
  await Promise.all([inventory, details])
  expect(useMCPStore.getState().toolList).toEqual([])
  expect(useMCPStore.getState().toolDetails).toEqual({})
  expect(useMCPStore.getState().getAvailableServers()).toEqual([])
})
it('preserves explicit Vault selections when refreshing the private catalog', async () => {
  mocks.getTools.mockResolvedValue([{ name: 'mine', server: 'mine', connection: 'connected' }])
  useMCPStore.getState().setChatSelectedServers(['mine', 'vault_shared'])
  await useMCPStore.getState().refreshTools()
  expect(useMCPStore.getState().chatSelectedServers).toEqual(['mine', 'vault_shared'])
  expect(useMCPStore.persist.getOptions().partialize?.(useMCPStore.getState())).not.toHaveProperty('toolDetails')
})
