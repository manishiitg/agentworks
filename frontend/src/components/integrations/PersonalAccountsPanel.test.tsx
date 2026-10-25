// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
const mocks = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(async () => ({ data: {} })), openVault: vi.fn() }))
vi.mock('../../services/api', () => ({ default: mocks, getApiBaseUrl: () => 'https://app.example.com' }))
vi.mock('../../stores/useAppStore', () => ({ useAppStore: (select: (state: unknown) => unknown) => select({ setAdminPage: mocks.openVault }) }))
vi.mock('../../stores/useAuthStore', () => ({ useAuthStore: (select: (state: unknown) => unknown) => select({ user: { id: 'builder' } }) }))
vi.mock('../../products/work/GoogleAccountConnect', () => ({ GoogleAccountConnect: () => <div>Central Google sign-in</div> }))
import PersonalAccountsPanel from './PersonalAccountsPanel'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
afterEach(() => vi.clearAllMocks())

it.each(['google', 'slack'] as const)('uses the same saved-account actions for %s without product sign-in', async provider => {
  mocks.get.mockResolvedValue({ data: { can_connect: true, accounts: [{ id: 'saved', owner: 'builder', label: 'Work account', connected: true, selected: false, can_add: true, can_manage: true, can_reconnect: true }], notice: 'Existing item grant' } })
  const host = document.createElement('div'); const root = createRoot(host)
  try {
    await act(async () => root.render(<PersonalAccountsPanel provider={provider} workspacePath="Workflow/orders" />))
    expect(host.textContent).not.toContain('Central Google sign-in')
    expect(host.textContent).not.toContain('Reconnect')
    expect(host.textContent).not.toContain('Disconnect everywhere')
    await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === 'Use here')!.click())
    expect(mocks.post).toHaveBeenLastCalledWith(`/api/integrations/${provider}/accounts`, { action: 'attach', workspace_path: 'Workflow/orders', connection_id: 'saved', connection_owner: 'builder' })
    await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === `Connect or manage ${provider === 'google' ? 'Google' : 'Slack'} accounts`)!.click())
    expect(mocks.get).toHaveBeenCalledWith(`/api/integrations/${provider}/accounts`, {params: {workspace_path: ''}})
  } finally { act(() => root.unmount()) }
})

it('manages personal accounts centrally without selecting a product', async () => {
  mocks.get.mockResolvedValue({ data: { can_connect: true, accounts: [] } })
  const host = document.createElement('div'); const root = createRoot(host)
  try {
    await act(async () => root.render(<PersonalAccountsPanel />))
    expect(host.textContent).toContain('Choose which projects can use each account.')
    expect(host.textContent).toContain('Central Google sign-in')
    await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === 'Connect Slack account')!.click())
    expect(mocks.post).toHaveBeenLastCalledWith('/api/integrations/slack/accounts', { action: 'connect', workspace_path: '', connection_id: undefined, connection_owner: undefined })
  } finally { act(() => root.unmount()) }
})

it('connecting personal Google exposes no mailbox automation', async () => {
 mocks.get.mockResolvedValue({data:{can_connect:true,accounts:[]}})
 const host=document.createElement('div');const root=createRoot(host)
 try {await act(async()=>root.render(<PersonalAccountsPanel provider="google"/>));expect(host.textContent).not.toContain('Use for incoming email');expect(host.textContent).not.toContain('New address');expect(host.textContent).toContain('Central Google sign-in')}finally {await act(async()=>root.unmount())}
})
