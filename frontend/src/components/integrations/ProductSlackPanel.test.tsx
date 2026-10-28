// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import { ProductSlackPanel } from './ProductSlackPanel'

const api = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), draft: vi.fn() }))
vi.mock('../../services/api', () => ({ default: { get: api.get, post: api.post }, agentApi: {} }))
vi.mock('../../stores/useWorkspaceConnectionStore',()=>({useWorkspaceConnectionStore:(select:any)=>select({activeWorkspaceId:'server'})}))
vi.mock('../../stores/useAuthStore',()=>({useAuthStore:(select:any)=>select({user:{is_admin:false}})}))
vi.mock('./useVaultSetupDraft',()=>({useVaultSetupDraft:()=>({open:api.draft,error:''})}))
vi.mock('../admin/SlackAdminPanel',()=>({default:()=> <div data-admin-bot-settings/>}))
vi.mock('./SlackAccountsPanel', () => ({ default: () => <p>Personal account controls</p> }))
vi.mock('../workflow/bots/SlackBotWhere', () => ({ SlackBotWhere: () => null }))
vi.mock('../workflow/bots/SlackSlugsSection', () => ({ SlackSlugsSection: () => null, slackErrorText: (_error: unknown, message: string) => message }))
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

it('keeps the bot form readable and preserves server selection restrictions and direct bot management without a duplicate chat action', async () => {
  api.get.mockResolvedValue({ data: { bots: [{ id: 'one', name: 'Team bot' }], selected_id: '', can_select: false, allow_bots: true } })
  const host = document.createElement('div'), root = createRoot(host)
  try {
    await act(async () => root.render(<ProductSlackPanel workspacePath="Crew/one" section="bot" />))
    expect(host.firstElementChild?.className).toContain('max-w-3xl')
    expect(host.textContent).toContain('Let people message this Crew')
    expect(host.textContent).toContain('Each connection keeps its own permissions')
    expect([...host.querySelectorAll('button')].find(button => button.textContent === 'Use here')?.disabled).toBe(true)
    expect(host.querySelector<HTMLDetailsElement>('[aria-label="Personal Slack connection"]')?.open).toBe(false)
    expect(host.textContent).not.toContain('Manage bots in chat')
    expect(host.querySelector('[aria-label="Slack bot choices"]')).not.toBeNull()
    expect(api.post).not.toHaveBeenCalled()
    api.get.mockResolvedValue({ data: { bots: [], selected_id: '', allow_bots: false } })
    await act(async () => root.render(<ProductSlackPanel key="other" workspacePath="Crew/other" section="bot" />))
    expect(host.querySelector('select')).toBeNull()
    expect(host.textContent).not.toContain('Manage bots in chat')
  } finally { await act(async () => root.unmount()); vi.clearAllMocks() }
})

it('shows Slack identity instead of Default without claiming delivery and keeps checks manager-only', async () => {
 api.get.mockResolvedValue({data:{bots:[{id:'one',name:'Default',configured:true,enabled:true,can_manage:false,identity:{name:'workspace-helper',workspace:'Example workspace',verified:true}}],selected_id:'one',can_select:false,allow_bots:true}})
 const host=document.createElement('div'),root=createRoot(host)
 try {
  await act(async()=>root.render(<ProductSlackPanel workspacePath="Crew/one" section="bot"/>))
  expect(host.textContent).toContain('workspace-helper')
  expect(host.textContent).toContain('Example workspace')
  expect(host.textContent).toContain('Identity verified · Delivery not checked')
  expect(host.textContent).not.toContain('Default')
  expect(host.querySelector('select')).toBeNull()
  expect([...host.querySelectorAll('button')].some(button=>button.textContent?.includes('Check connection'))).toBe(false)
  expect(host.querySelector('[role="tab"]')).toBeNull()
  await act(async()=>host.querySelector<HTMLElement>('[aria-label="Personal Slack connection"] > summary')!.click())
  expect(host.querySelector<HTMLDetailsElement>('[aria-label="Personal Slack connection"]')!.open).toBe(true)
  expect(host.textContent).toContain('Personal account controls')
  expect(api.post).not.toHaveBeenCalled()
 } finally {await act(async()=>root.unmount());vi.clearAllMocks()}
})


it('lists the existing shared bot with project bots, usage and selection through the same saved connection', async () => {
 const state = {
  bots: [
   { id: 'shared', name: 'Team helper', shared: true, default: true, can_manage: true, can_select: true, configured: true, enabled: true, used_in: [{label: 'Example Goal', kind: 'workflows'}] },
   { id: 'project', name: 'Project helper', shared: false, default: false, can_manage: true, can_select: true, configured: true, enabled: false, used_in: [{label: 'Example Crew', kind: 'crew'}] },
  ], selected_id: 'shared', can_select: true, can_create_company: true, allow_bots: true,
 }
 api.get.mockResolvedValue({data: state})
 api.post.mockResolvedValue({data: {...state, selected_id: 'project'}})
 const host=document.createElement('div'),root=createRoot(host)
 try {
  await act(async()=>root.render(<ProductSlackPanel workspacePath="Crew/example" section="bot"/>))
  expect(host.textContent).toContain('Team helper')
  expect(host.textContent).toContain('Project helper')
  expect(host.textContent).toContain('Example Goal')
  expect(host.textContent).toContain('Example Crew')
  expect(host.textContent).toContain('Paused')
  expect(host.textContent).not.toContain('Shared Slack bot setup')
  expect(host.textContent).not.toContain('Add or manage bots')
  expect(host.querySelectorAll('[aria-label="Slack bot choices"]').length).toBe(1)
  await act(async()=>[...host.querySelectorAll('button')].find(button=>button.textContent==='Settings')!.click())
  expect(host.querySelector('[data-admin-bot-settings]')).not.toBeNull()
  await act(async()=>[...host.querySelectorAll('button')].find(button=>button.textContent==='Back to bots')!.click())
  await act(async()=>[...host.querySelectorAll('button')].find(button=>button.textContent==='Use here')!.click())
  expect(api.post).toHaveBeenCalledWith('/api/integrations/slack/bots',{action:'select',workspace_path:'Crew/example',connection_id:'project'})
  expect(api.post).toHaveBeenCalledTimes(1)
  api.get.mockResolvedValue({data:{...state,scopes:[{workspace_path:'Crew/example',label:'Example Crew'}]}})
  await act(async()=>[...host.querySelectorAll('button')].find(button=>button.textContent==='Add new')!.click())
  expect(host.querySelector('form[aria-label="Save Slack bot"]')).not.toBeNull()
  expect(host.querySelector<HTMLSelectElement>('[aria-label="Bot management scope"]')!.value).toBe('Crew/example')
  expect([...host.querySelectorAll('button')].find(button=>button.textContent==='Save bot')?.disabled).toBe(true)
  expect(api.post).toHaveBeenCalledTimes(1)
 } finally {await act(async()=>root.unmount());vi.clearAllMocks()}
})
