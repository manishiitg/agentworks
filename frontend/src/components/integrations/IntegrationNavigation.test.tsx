// @vitest-environment happy-dom
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { integrationNavigationOptions } from './IntegrationNavigation'
import { projectIntegrationSections } from './integrationSections'
import { projectPluginTabs } from './ProjectPluginsPanel'
import { INTEGRATION_SECTIONS, RELAY_INTEGRATION_SECTION_IDS } from '../../products/productPanels'

beforeEach(() => {
  vi.stubEnv('VITE_DEPLOYMENT_MODE', 'server')
  window.__APP_RUNTIME_CONFIG__ = { enabledProductSurfaces: ['work', 'relays', 'knowledgebase', 'mcp-gateway'], gatewayUrl: 'https://example.com' }
})
afterEach(() => { delete window.__APP_RUNTIME_CONFIG__; vi.unstubAllEnvs() })

it.each([false, true])('shows resources the agent can use and leaves incoming channels to Use & share (relay=%s)', relay => {
  const sections = projectIntegrationSections(null, relay)
  const plugins = projectPluginTabs()
  const options = integrationNavigationOptions(sections, plugins)
  expect(options.map(item => item.value)).toEqual([...plugins.filter(item => item.value !== 'available' && item.value !== 'skills').map(item => item.value), ...sections.filter(item => !['apps', 'slack', 'whatsapp', 'cli'].includes(item.value)).map(item => item.value)])
  for (const absent of ['slack', 'whatsapp', 'cli']) expect(options.map(item => item.value)).not.toContain(absent)
  expect(options.find(item => item.value === 'connected')?.label).toBe('MCPs')
  if (relay) for (const absent of ['brain', 'folders', 'whatsapp', 'cli']) expect(options.map(item => item.value)).not.toContain(absent)
})

it('does not restore hidden product-backed or feature-disabled options', () => {
  const sections = projectIntegrationSections(['work'])
  const plugins = projectPluginTabs(['work']).filter(item => item.value === 'secrets')
  for (const absent of ['connected', 'available', 'skills', 'vault', 'brain']) expect(integrationNavigationOptions(sections, plugins).map(item => item.value)).not.toContain(absent)
  expect(integrationNavigationOptions(sections.filter(item => item.value !== 'apps'), plugins).map(item => item.value)).not.toContain('secrets')
})

