// @vitest-environment happy-dom
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { integrationShortcutContent, workspaceIntegrationShortcuts } from './workspaceIntegrationShortcuts'
import { collectContextShortcuts, contextShortcutOptions } from './workspaceShortcuts'

beforeEach(() => {
  vi.stubEnv('VITE_DEPLOYMENT_MODE', 'server')
  window.__APP_RUNTIME_CONFIG__ = { deploymentMode: 'server', enabledProductSurfaces: ['agentworks', 'work', 'code', 'relays', 'knowledgebase', 'mcp-gateway'], gatewayUrl: 'http://127.0.0.1:18193' }
})
afterEach(() => { delete window.__APP_RUNTIME_CONFIG__; vi.unstubAllEnvs() })
const options = (config: Partial<Parameters<typeof workspaceIntegrationShortcuts>[0]> = {}) => workspaceIntegrationShortcuts({ onOpen: vi.fn(), ...config })
const ids = (config: Partial<Parameters<typeof workspaceIntegrationShortcuts>[0]> = {}) => options(config).map(item => item.id)

it('puts the four main connections in the default bubble and keeps other allowed connections configurable', () => {
  const connections = options()
  expect(collectContextShortcuts(integrationShortcutContent(connections)).map(item => item.id)).toEqual(['mcp:connected', 'mcp:secrets', 'mcp:gmail', 'mcp:slack'])
  expect(contextShortcutOptions(integrationShortcutContent(connections), connections).map(item => item.id)).toEqual(expect.arrayContaining(['mcp:whatsapp', 'mcp:skills', 'mcp:available', 'mcp:vault', 'mcp:brain']))
})

it('uses the existing Relay integration inventory, including Slack but excluding Crew-only connections', () => {
  expect(ids({ relayMode: true })).toEqual(expect.arrayContaining(['mcp:connected', 'mcp:secrets', 'mcp:gmail', 'mcp:slack']))
  for (const id of ['mcp:brain', 'mcp:whatsapp', 'mcp:folders', 'mcp:cli']) expect(ids({ relayMode: true })).not.toContain(id)
})

it('honors separate MCP, secrets, skills and bot feature gates in Crew and Code', () => {
  const botOnly = ids({ workMode: true, enabledPanels: new Set(['bots']) })
  expect(botOnly).toEqual(expect.arrayContaining(['mcp:gmail', 'mcp:slack', 'mcp:whatsapp']))
  for (const id of ['mcp:connected', 'mcp:secrets', 'mcp:skills']) expect(botOnly).not.toContain(id)
  const secretsOnly = ids({ workMode: true, enabledPanels: new Set(['secrets']) })
  expect(secretsOnly).toContain('mcp:secrets')
  for (const id of ['mcp:connected', 'mcp:skills', 'mcp:gmail', 'mcp:slack']) expect(secretsOnly).not.toContain(id)
  const mcpOnly = ids({ workMode: true, enabledPanels: new Set(['mcp']) })
  expect(mcpOnly).toContain('mcp:connected')
  for (const id of ['mcp:secrets', 'mcp:skills', 'mcp:gmail', 'mcp:slack']) expect(mcpOnly).not.toContain(id)
})

it('does not advertise Brain or Vault beyond installation and account product access', () => {
  for (const id of ['mcp:brain', 'mcp:vault']) expect(ids({ allowedProducts: ['work'] })).not.toContain(id)
  window.__APP_RUNTIME_CONFIG__ = { deploymentMode: 'server', enabledProductSurfaces: ['work'] }
  for (const id of ['mcp:brain', 'mcp:vault']) expect(ids()).not.toContain(id)
})

it('targets existing section names rather than making new integration routes', () => {
  const onOpen = vi.fn()
  const connections = options({ onOpen })
  for (const section of ['connected', 'secrets', 'gmail', 'slack', 'whatsapp']) {
    connections.find(item => item.id === `mcp:${section}`)!.onClick!()
    expect(onOpen).toHaveBeenLastCalledWith(section)
  }
})
