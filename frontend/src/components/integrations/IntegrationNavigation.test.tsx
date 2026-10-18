// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { IntegrationNavigation, integrationNavigationOptions } from './IntegrationNavigation'
import { projectIntegrationSections } from './integrationSections'
import { projectPluginTabs } from './ProjectPluginsPanel'
import { INTEGRATION_SECTIONS, RELAY_INTEGRATION_SECTION_IDS } from '../../products/productPanels'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const cleanups: (() => void)[] = []
const originalScroll = HTMLElement.prototype.scrollIntoView
beforeEach(() => {
  vi.stubEnv('VITE_DEPLOYMENT_MODE', 'server')
  window.__APP_RUNTIME_CONFIG__ = { enabledProductSurfaces: ['work', 'relays', 'knowledgebase', 'mcp-gateway'], gatewayUrl: 'https://example.com' }
  HTMLElement.prototype.scrollIntoView = vi.fn()
})
afterEach(() => { cleanups.splice(0).forEach(cleanup => cleanup()); HTMLElement.prototype.scrollIntoView = originalScroll; delete window.__APP_RUNTIME_CONFIG__; vi.unstubAllEnvs() })

it.each([false, true])('shows resources the agent can use and leaves incoming channels to Use & share (relay=%s)', relay => {
  const sections = projectIntegrationSections(null, relay)
  const plugins = projectPluginTabs()
  const options = integrationNavigationOptions(sections, plugins)
  expect(options.map(item => item.value)).toEqual([...plugins.filter(item => item.value !== 'available').map(item => item.value), ...sections.filter(item => !['apps', 'whatsapp', 'cli'].includes(item.value)).map(item => item.value)])
  for (const absent of ['whatsapp', 'cli']) expect(options.map(item => item.value)).not.toContain(absent)
  expect(options.find(item => item.value === 'connected')?.label).toBe('MCPs')
  if (relay) for (const absent of ['brain', 'folders', 'whatsapp', 'cli']) expect(options.map(item => item.value)).not.toContain(absent)
})

it('does not restore hidden product-backed or feature-disabled options', () => {
  const sections = projectIntegrationSections(['work'])
  const plugins = projectPluginTabs(['work']).filter(item => item.value === 'secrets')
  for (const absent of ['connected', 'available', 'skills', 'vault', 'brain']) expect(integrationNavigationOptions(sections, plugins).map(item => item.value)).not.toContain(absent)
  expect(integrationNavigationOptions(sections.filter(item => item.value !== 'apps'), plugins).map(item => item.value)).not.toContain('secrets')
})

it('keeps Relay search destinations consistent with its admitted connection inventory', () => {
  const visible = INTEGRATION_SECTIONS.filter(section => RELAY_INTEGRATION_SECTION_IDS.has(section.id))
  // The registry is built at module load; deployment-backed plugin entries may
  // already be absent before this test installs its server runtime config.
  const registeredPlugins = projectPluginTabs().filter(tab => INTEGRATION_SECTIONS.some(section => section.id === tab.value))
  expect(new Set(visible.map(section => section.id))).toEqual(new Set(['use-share', 'cli', ...projectIntegrationSections(null, true).map(section => section.value), ...registeredPlugins.map(tab => tab.value)]))
  expect(visible.map(section => section.id)).not.toContain('slack-app')
  expect(visible.find(section => section.id === 'slack')?.label).toBe('Slack')
  expect(visible.find(section => section.id === 'apps')?.label).toBe('Connection overview')
})

it('keeps setup choices behind one action, supports keyboard selection and returns to Connections', async () => {
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host); cleanups.push(() => { act(() => root.unmount()); host.remove() })
  const options = integrationNavigationOptions(projectIntegrationSections(), projectPluginTabs())
  const select = vi.fn(), back = vi.fn()
  const render = (overview: boolean, value = 'connected') => act(() => root.render(<IntegrationNavigation options={options} value={value} onChange={select} overview={overview} onOverview={back} />))
  render(true)
  expect([...host.querySelectorAll('button')].map(button => button.textContent)).toEqual(['Add connection'])
  expect(host.querySelector('[aria-pressed]')).toBeNull()
  await act(async () => host.querySelector<HTMLButtonElement>('button')!.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true })))
  const slack = [...document.querySelectorAll<HTMLElement>('[role="menuitem"]')].find(item => item.textContent === 'Slack')!
  expect(slack).toBeDefined()
  await act(async () => slack.click())
  expect(select).toHaveBeenLastCalledWith('slack')
  expect(document.querySelector('[role="menu"]')).toBeNull()
  render(false, 'slack')
  expect(host.textContent).toContain('Connections/Slack')
  await act(async () => host.querySelector<HTMLButtonElement>('button')!.click())
  expect(back).toHaveBeenCalledOnce()
})
