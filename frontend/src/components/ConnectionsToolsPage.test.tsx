// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import ConnectionsToolsPage from './ConnectionsToolsPage'
const state = vi.hoisted(() => ({ account: 'first', network: 'one', get: vi.fn(), personal: vi.fn(), canManage: true, admin: false }))
vi.mock('../stores/useAuthStore', () => ({ useAuthStore: (select: (s: unknown) => unknown) => select({ user: { id: state.account, is_admin: state.admin } }) }))
vi.mock('../stores/useWorkspaceConnectionStore', () => ({ useWorkspaceConnectionStore: (select: (s: unknown) => unknown) => select({ activeWorkspaceId: state.network }) }))
vi.mock('../services/api', () => ({ default: { get: state.get } }))
vi.mock('./workspace/WorkspacePageHeader', () => ({ WorkspacePageHeader: () => null }))
vi.mock('./integrations/OpenVaultButton', () => ({ OpenVaultButton: () => null, useCanManageVault: () => state.canManage }))
vi.mock('./integrations/PersonalAccountsPanel', () => ({ default: (props: unknown) => { state.personal(props); return <div>Personal accounts</div> } }))
vi.mock('./integrations/AccountSlackOverview', () => ({ AccountSlackOverview: () => { state.personal({ provider: 'slack' }); return <div>Slack as me and bots</div> } }))
vi.mock('./integrations/AccountConnectionPanels', () => ({ AccountWhatsAppPanel: () => <div>WhatsApp connection</div>, AccountGitHubPanel: () => <div>GitHub accounts</div>, AccountComputerUsePanel: () => <div>Computer use</div> }))
vi.mock('./secrets/SecretSelectionSection', () => ({ default: ({ mode }: { mode: string }) => <div data-vault-mode={mode}>Vault secrets manager</div> }))
vi.mock('./UsersPage', () => ({ UsersPageContent: () => <div>User management</div> }))
vi.mock('./McpConnectPage', () => ({ McpConnectPageContent: () => <div>AI tool connection setup</div> }))
vi.mock('./providers/SettingsAiAccounts', () => ({ default: () => <div>Compact AI account list</div> }))
vi.mock('./BrowserConnectionsDialog', () => ({ BrowserConnectionsPanel: () => null }))
const host = document.createElement('div')
const root = createRoot(host)
afterEach(() => { act(() => root.render(null)); state.get.mockReset(); state.personal.mockClear() })

// PLAT-746: reuse the real Vault surface only behind its existing management gate.
it('embeds Vault directly and removes it on account changes without management access', async () => {
  state.canManage = true
  await act(async () => root.render(<ConnectionsToolsPage />))
  const click = async (label: string) => { await act(async () => { const button = [...host.querySelectorAll('button')].find(node => node.textContent === label); expect(button).toBeTruthy(); button!.click() }) }
  expect(state.personal.mock.calls.map(([props]) => props.provider)).toEqual(expect.arrayContaining(['slack', 'google']))
  expect(state.personal.mock.calls.every(([props]) => props.workspacePath === undefined)).toBe(true)
  await click('Secrets')
  expect(host.querySelector('[data-vault-mode="vault"]')).toBeTruthy()
  expect(host.textContent).not.toContain('Open Vault')
  state.network = 'two'; state.account = 'second'; state.canManage = false
  await act(async () => root.render(<ConnectionsToolsPage />))
  await click('Secrets')
  expect(host.querySelector('[data-vault-mode]')).toBeNull()
  expect(host.textContent).toContain('Ask a Vault manager')
})

it('shows Slack bot identity and authorized project usage alongside Slack as me', async () => {
  const { AccountSlackOverview } = await vi.importActual<typeof import('./integrations/AccountSlackOverview')>('./integrations/AccountSlackOverview')
  state.get.mockResolvedValue({ data: { bots: [{ id: 'bot-one', can_manage: true, name: 'Saved helper', configured: true, enabled: true, scope_label: 'Crew · Example', identity: { name: 'Review helper', workspace: 'Example workspace', verified: true }, used_in: [{ label: 'Example project', kind: 'work' }] }] } })
  await act(async () => root.render(<AccountSlackOverview />))
  expect(state.personal.mock.calls.some(([props]) => props.heading === 'Slack as me')).toBe(true)
  expect(host.textContent).toContain('Review helper')
  expect(host.textContent).toContain('Example workspace')
  expect(host.textContent).toContain('Example project')
  expect(host.querySelector('details')?.open).toBe(false)
  expect(host.querySelector('summary')?.textContent).toContain('1 project')
  const configure = [...host.querySelectorAll('button')].find(button => button.textContent === 'Configure')
  expect(configure).toBeTruthy()
  await act(async () => configure!.click())
  expect(host.querySelector('form[aria-label="Save Slack bot"]')).toBeTruthy()
  expect(host.textContent).not.toContain('Used here')
})

it('keeps Customize user management behind the existing administrator gate', async () => {
  state.admin = true
  await act(async () => root.render(<ConnectionsToolsPage />))
  const users = [...host.querySelectorAll('button')].find(button => button.textContent === 'Users & access')
  expect(users).toBeTruthy()
  await act(async () => users!.click())
  expect(host.textContent).toContain('User management')
  state.admin = false
  await act(async () => root.render(<ConnectionsToolsPage />))
  expect(host.textContent).not.toContain('User management')
  expect([...host.querySelectorAll('button')].some(button => button.textContent === 'Users & access')).toBe(false)
  expect([...host.querySelectorAll('button')].some(button => button.textContent === 'Connect AI tools')).toBe(true)
})

// The approved layout has one Settings navigation and no nested provider page.
it('opens AI accounts in the same settings content area', async () => {
  await act(async () => root.render(<ConnectionsToolsPage />))
  const ai = [...host.querySelectorAll('button')].find(button => button.textContent === 'AI accounts')!
  await act(async () => ai.click())
  expect(host.querySelectorAll('nav[aria-label="Settings sections"]')).toHaveLength(1)
  expect(host.textContent).toContain('Compact AI account list')
  expect(host.textContent).not.toContain('AI models & accounts')
})
