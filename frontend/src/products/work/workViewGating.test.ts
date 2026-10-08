import { describe, expect, it } from 'vitest'
import { isWorkIdentityTabEnabled, isWorkIntegrationTabEnabled, isWorkWorkspaceViewEnabled } from './workViewGating'

describe('isWorkWorkspaceViewEnabled', () => {
  it('enables everything without a panel allowlist', () => {
    expect(isWorkWorkspaceViewEnabled('identity')).toBe(true)
    expect(isWorkWorkspaceViewEnabled('mcp')).toBe(true)
    expect(isWorkWorkspaceViewEnabled('files')).toBe(true)
    expect(isWorkWorkspaceViewEnabled('memory')).toBe(true)
  })

  it('always enables Identity and gates Integrations on any constituent panel', () => {
    expect(isWorkWorkspaceViewEnabled('identity', new Set())).toBe(true)
    expect(isWorkWorkspaceViewEnabled('plan', new Set())).toBe(true)
    expect(isWorkWorkspaceViewEnabled('mcp', new Set())).toBe(false)
    expect(isWorkWorkspaceViewEnabled('mcp', new Set(['mcp']))).toBe(true)
    expect(isWorkWorkspaceViewEnabled('mcp', new Set(['skills']))).toBe(true)
    expect(isWorkWorkspaceViewEnabled('mcp', new Set(['bots']))).toBe(true)
    expect(isWorkWorkspaceViewEnabled('files', new Set(['files']))).toBe(true)
    expect(isWorkWorkspaceViewEnabled('memory', new Set(['memory']))).toBe(true)
    expect(isWorkWorkspaceViewEnabled('files', new Set(['mcp']))).toBe(false)
  })
})

describe('isWorkIdentityTabEnabled', () => {
  it('always enables General and gates the rest on their panel', () => {
    expect(isWorkIdentityTabEnabled('general', new Set())).toBe(true)
    expect(isWorkIntegrationTabEnabled('secrets', new Set())).toBe(false)
    expect(isWorkIntegrationTabEnabled('secrets', new Set(['secrets']))).toBe(true)
    expect(isWorkIntegrationTabEnabled('folders', new Set(['folders']))).toBe(true)
    expect(isWorkIntegrationTabEnabled('folders', new Set(['mcp']))).toBe(false)
    expect(isWorkIdentityTabEnabled('models', new Set(['models']))).toBe(true)
  })
})

describe('isWorkIntegrationTabEnabled', () => {
  it('gates channel tabs on the shared bots panel', () => {
    expect(isWorkIntegrationTabEnabled('apps', new Set(['mcp']))).toBe(true)
    expect(isWorkIntegrationTabEnabled('apps', new Set(['bots']))).toBe(false)
    expect(isWorkIntegrationTabEnabled('skills', new Set(['skills']))).toBe(true)
    expect(isWorkIntegrationTabEnabled('slack', new Set(['bots']))).toBe(true)
    expect(isWorkIntegrationTabEnabled('whatsapp', new Set(['bots']))).toBe(true)
    expect(isWorkIntegrationTabEnabled('gmail', new Set(['bots']))).toBe(true)
    expect(isWorkIntegrationTabEnabled('gmail', new Set(['mcp', 'skills']))).toBe(false)
    expect(isWorkIntegrationTabEnabled('cli', new Set(['bots']))).toBe(true)
    expect(isWorkIntegrationTabEnabled('cli', new Set())).toBe(true)
  })
})

describe('local-connected Code sessions', () => {
  it('limits local views to connection controls, Costs and Models even without a loaded feature list', () => {
    const serverPanels = new Set(['dashboard', 'database', 'schedules', 'triggers', 'bots', 'files', 'costs', 'models', 'mcp', 'skills', 'secrets'])
    for (const panels of [undefined, serverPanels]) {
      for (const view of ['dashboard', 'database', 'schedules', 'shell', 'browser', 'mcp', 'plan', 'memory', 'suggestions'] as const) {
        expect(isWorkWorkspaceViewEnabled(view, panels, true)).toBe(false)
      }
      for (const view of ['files', 'identity', 'costs'] as const) {
        expect(isWorkWorkspaceViewEnabled(view, panels, true)).toBe(true)
      }
    }
  })

  it('blocks every integration section in local mode', () => {
    const panels = new Set(['bots', 'mcp', 'skills', 'secrets', 'folders'])
    for (const tab of ['slack', 'whatsapp', 'gmail'] as const) {
      expect(isWorkIntegrationTabEnabled(tab, undefined, true)).toBe(false)
      expect(isWorkIntegrationTabEnabled(tab, panels, true)).toBe(false)
      expect(isWorkIntegrationTabEnabled(tab, panels, false)).toBe(true)
    }
    for (const tab of ['apps', 'skills', 'secrets', 'folders', 'cli'] as const) {
      expect(isWorkIntegrationTabEnabled(tab, panels, true)).toBe(false)
    }
    expect(isWorkWorkspaceViewEnabled('mcp', new Set(['bots']), true)).toBe(false)
  })
})
