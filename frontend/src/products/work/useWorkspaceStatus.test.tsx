// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
const mocks = vi.hoisted(() => ({ vault: vi.fn(), bots: vi.fn(), schedules: vi.fn() }))
vi.mock('../../api/projectVault', () => ({ loadProjectVault: mocks.vault }))
vi.mock('../../api/scheduler', () => ({ schedulerApi: { listJobs: mocks.schedules } }))
vi.mock('../../api/conditionTriggers', () => ({ conditionTriggersApi: {} }))
vi.mock('../../api/productWebhooks', () => ({ productWebhooksApi: {} }))
vi.mock('../../components/integrations/ProductSlackPanel', () => ({ slackBotsApi: { list: mocks.bots }, slackBotName: (bot: any) => bot.identity?.name || bot.name }))
vi.mock('../../hooks/useLiveRefetch', () => ({ useLiveRefetch: () => {} }))
import { useWorkspaceStatus } from './useWorkspaceStatus'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
it('combines selected accounts and the real bot regardless of response order, and drops a late response from another scope', async () => {
 const admitted = ['files','mcp:connected','mcp:gmail','mcp:slack-account','mcp:slack-bot','schedules:schedules']
 let finishOld!: (value: any) => void
 mocks.vault.mockImplementation((path: string) => path === 'Crew/old' ? new Promise(resolve => { finishOld = resolve }) : Promise.resolve({ vaults:[{items:[
  {id:'chosen',name:'Chosen notes',kind:'mcp',selected:true,status:'connected'},
  {id:'google',name:'Default',account_email:'person@example.com',kind:'google',selected:true,status:'connected'},
  {id:'slack',name:'Personal Slack',app:'slack',kind:'mcp',selected:true,status:'connected'},
  {id:'unused',name:'Unused notes',kind:'mcp',selected:false,status:'connected'},
 ]}] }))
 mocks.bots.mockResolvedValue({ selected_id:'bot', bots:[{id:'bot',name:'Saved alias',identity:{name:'Actual bot',verified:true},configured:true,enabled:true}] })
 mocks.schedules.mockResolvedValue({jobs:[{id:'paused',name:'Paused job',enabled:false,workspace_path:'Crew/current',next_run_at:'2030-01-01'}, {id:'other',name:'Other project job',enabled:true,workspace_path:'Crew/other'}]})
 let value: ReturnType<typeof useWorkspaceStatus> | undefined
 function Harness({scope}: {scope:string}) { value = useWorkspaceStatus({scope,workspacePath:`Crew/${scope}`,projectId:scope,admitted}); return <pre>{JSON.stringify(value.snapshot)}</pre> }
 const host=document.createElement('div'); const root=createRoot(host)
 try {
  await act(async()=>root.render(<Harness scope="old"/>))
  await act(async()=>root.render(<Harness scope="current"/>))
  await act(async()=>finishOld({vaults:[{items:[{id:'old',name:'Old account',kind:'mcp',selected:true,status:'connected'}]}]}))
  expect(value?.snapshot?.apps?.map(item=>item.label)).toEqual(['Actual bot','Chosen notes','Google','My Slack account'])
  expect(value?.snapshot?.apps?.find(item=>item.id==='mcp:slack')?.destination).toBe('mcp:slack-account')
  expect(value?.snapshot?.apps?.find(item=>item.id==='google:google')?.detail).toContain('person@example.com')
  expect(value?.snapshot?.apps?.find(item=>item.id==='google:google')?.connected).toBe(true)
  expect(host.textContent).not.toContain('Unused notes'); expect(host.textContent).not.toContain('Old account')
  expect(value?.snapshot?.automations?.map(item=>item.detail)).toEqual(['Paused'])
  expect(host.textContent).not.toContain('Other project job')
 } finally { await act(async()=>root.unmount()) }
})
