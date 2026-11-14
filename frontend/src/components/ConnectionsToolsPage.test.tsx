// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import ConnectionsToolsPage from './ConnectionsToolsPage'
const state = vi.hoisted(() => ({ account: 'first', network: 'one', get: vi.fn(), personal: vi.fn() }))
vi.mock('../stores/useAuthStore', () => ({ useAuthStore: (select: (s: unknown) => unknown) => select({ user: { id: state.account } }) }))
vi.mock('../stores/useWorkspaceConnectionStore', () => ({ useWorkspaceConnectionStore: (select: (s: unknown) => unknown) => select({ activeWorkspaceId: state.network }) }))
vi.mock('../services/api', () => ({ default: { get: state.get } }))
vi.mock('./workspace/WorkspacePageHeader', () => ({ WorkspacePageHeader: () => null }))
vi.mock('./integrations/OpenVaultButton', () => ({ OpenVaultButton: () => null }))
vi.mock('./integrations/PersonalAccountsPanel', () => ({ default: (props: unknown) => { state.personal(props); return <div>Personal accounts</div> } }))
vi.mock('./integrations/AccountConnectionPanels', () => ({ AccountWhatsAppPanel: () => <div>WhatsApp connection</div>, AccountGitHubPanel: () => <div>GitHub accounts</div>, AccountComputerUsePanel: () => <div>Computer use</div> }))
vi.mock('./BrowserConnectionsDialog', () => ({ BrowserConnectionsPanel: () => null }))
const host = document.createElement('div')
const root = createRoot(host)
afterEach(() => { act(() => root.render(null)); state.get.mockReset(); state.personal.mockClear() })

// PLAT-746: global setup must not inherit an active Crew or a previous account's inventory.
it('keeps personal setup unscoped and drops late secret names after changing network', async () => {
  let finish!: (value: unknown) => void
  state.get.mockImplementationOnce(() => new Promise(resolve => { finish = resolve }))
  state.get.mockResolvedValue({ data: [{ name: 'CURRENT_ACCOUNT_KEY' }] })
  await act(async () => root.render(<ConnectionsToolsPage />))
  const click = async (label: string) => { await act(async () => { const button = [...host.querySelectorAll('button')].find(node => node.textContent === label); expect(button).toBeTruthy(); button!.click() }) }
  expect(state.personal.mock.calls.map(([props]) => props.provider)).toEqual(expect.arrayContaining(['slack', 'google']))
  expect(state.personal.mock.calls.every(([props]) => props.workspacePath === undefined)).toBe(true)
  expect(host.textContent).toContain('WhatsApp connection')
  expect(host.textContent).toContain('GitHub accounts')
  expect(host.textContent).toContain('Computer use')
  expect(host.textContent).not.toContain('Manage')
  await click('Secrets')
  state.network = 'two'; state.account = 'second'
  await act(async () => root.render(<ConnectionsToolsPage />))
  await click('Secrets')
  await act(async () => finish({ data: [{ name: 'OLD_ACCOUNT_KEY' }] }))
  expect(host.textContent).toContain('CURRENT_ACCOUNT_KEY')
  expect(host.textContent).not.toContain('OLD_ACCOUNT_KEY')
  expect(state.get.mock.calls.every(([path, options]) => path === '/api/secrets/global' && options.skipSessionContext === true)).toBe(true)
})
