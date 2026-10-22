// @vitest-environment happy-dom
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { projectConnectionSections } from './WorkspaceProjectNavigation'
beforeEach(() => {
  vi.stubEnv('VITE_DEPLOYMENT_MODE', 'server')
  window.__APP_RUNTIME_CONFIG__ = { deploymentMode: 'server', enabledProductSurfaces: ['agentworks', 'work', 'code', 'relays', 'knowledgebase', 'mcp-gateway'], gatewayUrl: 'https://example.com' }
})
afterEach(() => { delete window.__APP_RUNTIME_CONFIG__; vi.unstubAllEnvs() })
it('keeps one connection destination per resource, with skills and channels elsewhere', () => {
  const onOpen = vi.fn()
  const options = projectConnectionSections({ onOpen })
  expect(options.map(item => item.id)).toEqual(['mcp:connected', 'mcp:secrets', 'mcp:brain', 'mcp:gmail', 'mcp:folders'])
  options.find(item => item.id === 'mcp:secrets')!.onClick()
  expect(onOpen).toHaveBeenLastCalledWith('secrets')
  expect(projectConnectionSections({ relayMode: true, onOpen }).map(item => item.id)).toEqual(['mcp:connected', 'mcp:secrets', 'mcp:gmail'])
})
it('retains separate server features and permitted products', () => {
  const options = projectConnectionSections({ onOpen: vi.fn(), allowedProducts: ['work'], workMode: true, enabledPanels: new Set(['secrets']) })
  expect(options.map(item => item.id)).toEqual(['mcp:secrets'])
})
