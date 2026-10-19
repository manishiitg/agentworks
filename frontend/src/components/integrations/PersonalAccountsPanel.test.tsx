// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
const mocks = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(async () => ({ data: {} })), openVault: vi.fn() }))
vi.mock('../../services/api', () => ({ default: mocks, getApiBaseUrl: () => 'https://app.example.com' }))
vi.mock('../../stores/useAppStore', () => ({ useAppStore: (select: (state: unknown) => unknown) => select({ setAdminPage: mocks.openVault }) }))
vi.mock('../../stores/useAuthStore', () => ({ useAuthStore: (select: (state: unknown) => unknown) => select({ user: { id: 'builder' } }) }))
vi.mock('../../products/work/GoogleAccountConnect', () => ({ GoogleAccountConnect: () => <div>Central Google sign-in</div> }))
vi.mock('../workflow/bots/gmailAskAI', () => ({ useGmailInboundUIEnabled: () => true }))
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
    await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === 'Manage accounts in My Vault')!.click())
    expect(mocks.openVault).toHaveBeenCalledWith('vaults')
  } finally { act(() => root.unmount()) }
})

it('manages personal accounts centrally without selecting a product', async () => {
  mocks.get.mockResolvedValue({ data: { can_connect: true, accounts: [] } })
  const host = document.createElement('div'); const root = createRoot(host)
  try {
    await act(async () => root.render(<PersonalAccountsPanel />))
    expect(host.textContent).toContain('separate from your shared vaults')
    expect(host.textContent).toContain('Central Google sign-in')
    await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === 'Connect Slack account')!.click())
    expect(mocks.post).toHaveBeenLastCalledWith('/api/integrations/slack/accounts', { action: 'connect', workspace_path: '', connection_id: undefined, connection_owner: undefined })
  } finally { act(() => root.unmount()) }
})

it('selects a saved account for one inbox without attaching it to any product', async () => {
 const account={id:'saved',owner:'builder',label:'owner@example.com',connected:true,can_use_inbox:true,google:{email:'owner@example.com',allow_read_access:true,auth:{scopes:[]}}}
 mocks.get.mockResolvedValue({data:{can_connect:false,accounts:[account],incoming_email:{inbox:null,bindings:[]}}})
 const host=document.createElement('div');const root=createRoot(host)
 try {
 await act(async()=>root.render(<PersonalAccountsPanel provider="google"/>))
 await act(async()=>[...host.querySelectorAll('button')].find(b=>b.textContent==='Use for incoming email')!.click())
 expect(mocks.post).toHaveBeenLastCalledWith('/api/integrations/google/accounts',{action:'select_inbox',workspace_path:'',connection_id:'saved',connection_owner:'builder'})
 mocks.get.mockResolvedValue({data:{can_connect:false,accounts:[account],incoming_email:{inbox:{id:'inbox',connection_id:'saved',address:'owner+agent-random@example.com'},receiving_account:'owner@example.com',bindings:[]}}})
 await act(async()=>window.dispatchEvent(new Event('focus')))
 expect(host.textContent).toContain('Anyone who emails this address')
 await act(async()=>[...host.querySelectorAll('button')].find(b=>b.textContent==='New address')!.click())
 expect(mocks.post).toHaveBeenLastCalledWith('/api/integrations/google/accounts',{action:'rotate_inbox',workspace_path:'',connection_id:undefined,connection_owner:undefined})
 } finally { await act(async()=>root.unmount()) }
})
