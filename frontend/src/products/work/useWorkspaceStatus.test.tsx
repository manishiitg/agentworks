// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
const mocks = vi.hoisted(() => ({ vault: vi.fn(), bots: vi.fn(), schedules: vi.fn(), whatsapp: vi.fn(), github: vi.fn() }))
vi.mock('../../services/api', () => ({ default: { get: mocks.github }, agentApi: { getWhatsAppStatus: mocks.whatsapp } }))
vi.mock('../../api/projectVault', () => ({ loadProjectVault: mocks.vault }))
vi.mock('../../api/scheduler', () => ({ schedulerApi: { listJobs: mocks.schedules } }))
vi.mock('../../api/conditionTriggers', () => ({ conditionTriggersApi: {} }))
vi.mock('../../api/productWebhooks', () => ({ productWebhooksApi: {} }))
vi.mock('../../components/integrations/ProductSlackPanel', () => ({ slackBotsApi: { list: mocks.bots }, slackBotName: (bot: any) => bot.identity?.name || bot.name }))
vi.mock('../../hooks/useLiveRefetch', () => ({ useLiveRefetch: () => {} }))
import { useWorkspaceStatus } from './useWorkspaceStatus'
import { githubConnectionChanged } from '../../utils/githubConnectionEvents'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
it('combines selected accounts and the real bot regardless of response order, and drops a late response from another scope', async () => {
 const admitted = ['files','mcp:connected','mcp:gmail','mcp:slack-account','mcp:slack-bot','mcp:whatsapp','mcp:github','schedules:schedules']
 let finishOld!: (value: any) => void
 mocks.vault.mockImplementation((path: string) => path === 'Crew/old' ? new Promise(resolve => { finishOld = resolve }) : Promise.resolve({ vaults:[{items:[
  {id:'chosen',name:'Chosen notes',kind:'mcp',selected:true,status:'connected'},
  {id:'google',name:'Default',account_email:'person@example.com',kind:'google',selected:true,status:'connected'},
  {id:'slack',name:'Personal Slack',app:'slack',kind:'mcp',selected:true,status:'connected'},
  {id:'unused',name:'Unused notes',kind:'mcp',selected:false,status:'connected'},
 ]}] }))
 mocks.github.mockResolvedValue({data:{connected:true,login:'chosen-account'}}).mockResolvedValueOnce({data:{connected:true,login:'old-account'}})
 mocks.whatsapp.mockResolvedValue({enabled:true,paired:true,connected:true})
 mocks.bots.mockResolvedValue({ selected_id:'bot', bots:[{id:'bot',name:'Saved alias',identity:{name:'Actual bot',verified:true},configured:true,enabled:true}] })
 mocks.schedules.mockResolvedValue({jobs:[{id:'paused',name:'Paused job',enabled:false,workspace_path:'Crew/current',next_run_at:'2030-01-01'}, {id:'other',name:'Other project job',enabled:true,workspace_path:'Crew/other'}]})
 let value: ReturnType<typeof useWorkspaceStatus> | undefined
 function Harness({scope, whatsapp = true, github = true}: {scope:string;whatsapp?:boolean;github?:boolean}) { value = useWorkspaceStatus({scope,workspacePath:`Crew/${scope}`,projectId:scope,admitted:admitted.filter(id=>(whatsapp || id!=='mcp:whatsapp') && (github || id!=='mcp:github'))}); return <pre>{JSON.stringify(value.snapshot)}</pre> }
 const host=document.createElement('div'); const root=createRoot(host)
 try {
  await act(async()=>root.render(<Harness scope="old"/>))
  await act(async()=>root.render(<Harness scope="current"/>))
  await act(async()=>finishOld({vaults:[{items:[{id:'old',name:'Old account',kind:'mcp',selected:true,status:'connected'}]}]}))
  expect(value?.snapshot?.apps?.map(item=>item.label)).toEqual(['Actual bot','Chosen notes','GitHub','Google','My Slack account','WhatsApp'])
  expect(value?.snapshot?.apps?.find(item=>item.id==='mcp:slack')?.destination).toBe('mcp:slack-account')
  expect(value?.snapshot?.apps?.find(item=>item.id==='google:google')?.detail).toContain('person@example.com')
  expect(value?.snapshot?.apps?.find(item=>item.id==='google:google')?.connected).toBe(true)
  expect(host.textContent).not.toContain('Unused notes'); expect(host.textContent).not.toContain('Old account')
  expect(value?.snapshot?.automations?.map(item=>item.detail)).toEqual(['Paused'])
  expect(host.textContent).not.toContain('Other project job')
  expect(host.textContent).not.toContain('old-account')
  expect(value?.snapshot?.apps?.find(item=>item.id==='github')).toMatchObject({connected:true,detail:'chosen-account · Connected'})
  expect(mocks.github).toHaveBeenCalledWith('/api/vault/github')
  mocks.github.mockResolvedValue({data:{connected:false}})
  await act(async()=>window.dispatchEvent(new Event(githubConnectionChanged)))
  expect(value?.snapshot?.apps?.find(item=>item.id==='github')).toMatchObject({connected:false,detail:'Not connected'})
  expect(value?.snapshot?.apps?.find(item=>item.id==='whatsapp')?.connected).toBe(true)
  mocks.whatsapp.mockResolvedValue({enabled:true,paired:true,connected:false})
  await act(async()=>value?.refresh())
  expect(value?.snapshot?.apps?.find(item=>item.id==='whatsapp')).toMatchObject({connected:false,detail:'Not connected'})
  const reads = mocks.whatsapp.mock.calls.length
  const githubReads = mocks.github.mock.calls.length
  await act(async()=>root.render(<Harness scope="current" whatsapp={false} github={false}/>))
  expect(mocks.whatsapp.mock.calls).toHaveLength(reads)
  expect(mocks.github.mock.calls).toHaveLength(githubReads)
  expect(value?.snapshot?.apps?.some(item=>item.id==='github')).toBe(false)
  expect(value?.snapshot?.apps?.some(item=>item.id==='whatsapp')).toBe(false)
 } finally { await act(async()=>root.unmount()) }
})

// PLAT-746-289: a slow GitHub or automation check must not hide ready connections.
it('publishes ready sources while others wait, retains them on refresh, and clears them on a project or admission change', async () => {
 vi.resetAllMocks()
 const pending = <T,>() => {
  let resolve!: (value: T) => void
  const promise = new Promise<T>(done => { resolve = done })
  return { promise, resolve }
 }
 const github = pending<{data:{connected:boolean;login:string}}>()
 const schedules = pending<{jobs:unknown[]}>()
 mocks.github.mockReturnValue(github.promise)
 mocks.schedules.mockReturnValue(schedules.promise)
 mocks.vault.mockResolvedValue({vaults:[{items:[{id:'notes',name:'Notes',kind:'mcp',selected:true,status:'connected'}]}]})
 let value: ReturnType<typeof useWorkspaceStatus> | undefined
 const all = ['mcp:connected','mcp:github','schedules:schedules']
 function Harness({ path = 'Crew/current', admitted = all }: {path?:string;admitted?:string[]}) {
  value = useWorkspaceStatus({scope:'same-account',workspacePath:path,projectId:path,admitted})
  return <pre>{JSON.stringify(value.snapshot)}</pre>
 }
 const host=document.createElement('div'); const root=createRoot(host)
 try {
  await act(async()=>root.render(<Harness/>))
  // Neither unresolved request can hold up the selected MCP result.
  expect(value?.snapshot?.apps?.map(row=>row.label)).toEqual(['Notes'])
  expect(value?.snapshot?.loaded).toContain('mcp:connected')
  expect(value?.snapshot?.loaded).not.toContain('mcp:github')
  await act(async()=>github.resolve({data:{connected:true,login:'selected-account'}}))
  expect(value?.snapshot?.apps?.map(row=>row.label)).toEqual(['GitHub','Notes'])
  await act(async()=>schedules.resolve({jobs:[]}))

  const refreshingGithub = pending<{data:{connected:boolean;login?:string}}>()
  const refreshingVault = pending<{vaults:{items:unknown[]}[]}>()
  mocks.github.mockReturnValue(refreshingGithub.promise)
  mocks.vault.mockReturnValue(refreshingVault.promise)
  await act(async()=>value?.refresh())
  expect(value?.snapshot?.apps?.map(row=>row.label)).toEqual(['GitHub','Notes'])
  await act(async()=>refreshingGithub.resolve({data:{connected:false}}))
  expect(value?.snapshot?.apps?.find(row=>row.id==='github')?.connected).toBe(false)
  expect(value?.snapshot?.apps?.some(row=>row.label==='Notes')).toBe(true)
  // An authoritative empty selection removes the previous source's rows.
  await act(async()=>refreshingVault.resolve({vaults:[{items:[]}]}))
  expect(value?.snapshot?.apps?.map(row=>row.label)).toEqual(['GitHub'])

  mocks.github.mockRejectedValue(new Error('Temporary failure'))
  await act(async()=>value?.refresh())
  expect(value?.snapshot?.apps?.map(row=>row.label)).toEqual(['GitHub'])
  expect(value?.snapshot?.errors).toContain('GitHub status could not be checked.')

  const late = pending<{data:{connected:boolean;login:string}}>()
  mocks.github.mockReturnValue(late.promise)
  await act(async()=>value?.refresh())
  const newVault = pending<{vaults:{items:unknown[]}[]}>()
  mocks.vault.mockReturnValue(newVault.promise)
  mocks.github.mockResolvedValue({data:{connected:true,login:'new-project-account'}})
  // A ready GitHub row cannot imply that the slower MCP inventory is empty.
  await act(async()=>root.render(<Harness path="Crew/other" admitted={['mcp:connected','mcp:github']}/>))
  expect(value?.snapshot?.apps?.map(row=>row.label)).toEqual(['GitHub'])
  expect(value?.snapshot?.loaded).not.toContain('mcp:connected')
  await act(async()=>late.resolve({data:{connected:true,login:'old-project-account'}}))
  expect(host.textContent).not.toContain('old-project-account')
  await act(async()=>newVault.resolve({vaults:[{items:[{id:'new',name:'Other notes',kind:'mcp',selected:true,status:'connected'}]}]}))
  expect(value?.snapshot?.apps?.map(row=>row.label)).toEqual(['GitHub','Other notes'])
  expect(value?.snapshot?.loaded).toContain('mcp:connected')
  await act(async()=>root.render(<Harness path="Crew/other" admitted={[]}/>))
  expect(value?.snapshot?.apps || []).toEqual([])
 } finally { await act(async()=>root.unmount()) }
})
