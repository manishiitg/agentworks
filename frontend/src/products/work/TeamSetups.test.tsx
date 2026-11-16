// @vitest-environment happy-dom
import React, {act} from 'react'
import {createRoot} from 'react-dom/client'
import {afterEach,expect,it,vi} from 'vitest'
import {TeamSetups,type TeamSetupList} from './TeamSetups'
import {teamRequest} from './teamApi'
vi.mock('./teamApi',()=>({teamRequest:vi.fn()}))
vi.mock('../../utils/agentPurposeDraft',()=>({fillMainDraft:vi.fn(()=>true)}))
vi.mock('../../stores/useChatStore',()=>({useChatStore:{getState:()=>({addToast:vi.fn()})}}))
Object.assign(globalThis,{IS_REACT_ACT_ENVIRONMENT:true})
const host=document.createElement('div');document.body.append(host);const root=createRoot(host)
const list:TeamSetupList={to_do:1,setups:[{id:'setup',title:'Example setup',revision:3,instruction_revision:9,instructions_available:true,done:1,total:2,for_you:true,assigned_to:[],people:[{person_id:'other',name:'Colleague',done:2,total:2},{person_id:'self',name:'You',done:1,total:2},{person_id:'empty',name:'Not checked',done:0,total:2}]}]}
const detail={...list.setups[0],instruction:{path:'team/setup.md',revision:9},instructions:'Use the shared instructions.',shared_by:'Colleague',items:[{id:'one',label:'Installed'},{id:'two',label:'Checked'}],my_items:{one:true},key_status:[{name:'EXAMPLE_KEY',state:'unavailable'}]}
const flush=async()=>{await Promise.resolve();await Promise.resolve()}
async function click(label:string){const button=[...host.querySelectorAll('button')].find(row=>row.textContent?.includes(label));expect(button).toBeTruthy();await act(async()=>{button!.click();await flush()})}
afterEach(async()=>{await act(async()=>root.render(null));vi.resetAllMocks()})
it('uses batch counts, never lets a person tick progress, and applies only a server-listed connected own target with retry dedupe',async()=>{
 const request=vi.mocked(teamRequest)
 request.mockImplementation(async(_target,suffix,_method,body)=>{
  if(suffix?.includes('?'))return detail
  if((body as {action?:string})?.action==='setup_targets')return {agents:[{id:'own',name:'Own agent',online:true},{id:'off',name:'Offline agent',online:false}]}
  throw new Error('Delivery uncertain; try again')
 })
 await act(async()=>root.render(<TeamSetups target={{project:'crew'}} projectId="crew" list={list} refreshKey={1}/>))
 expect(request).not.toHaveBeenCalled() // No per-row requests and no timer.
 expect(host.textContent).toContain('you: 1 of 2');expect(host.textContent).toContain('Colleague');expect(host.querySelector('[aria-label="Done"]')).toBeTruthy()
 const setupPeople=()=>host.querySelector('[aria-label="Who is set up"]')!
 expect(setupPeople().textContent).toContain('1 of 3')
 expect(setupPeople().textContent).not.toContain('EXAMPLE_KEY')
 await click('Example setup')
 expect(host.querySelector('input[type="checkbox"]')).toBeNull()
 expect(setupPeople().textContent).toContain('1 of 3')
 expect(setupPeople().textContent).not.toContain('Use the shared instructions.')
 expect(host.textContent).toContain('Unknown');expect(host.textContent).not.toContain('Missing')
 expect(host.querySelector('select')?.textContent).not.toContain('Offline agent')
 await click('Apply with my agent');await click('Apply with my agent')
 const calls=request.mock.calls.filter(call=>(call[3] as {action?:string})?.action==='setup_apply')
 expect(calls).toHaveLength(2)
 expect(calls[0][3]).toEqual(calls[1][3])
 expect(calls[0][3]).toMatchObject({target_agent_id:'own',setup_revision:3,instruction_revision:9})
 expect(request.mock.calls.some(call=>(call[3] as {action?:string})?.action==='setup_checklist')).toBe(false)
 await click('‹ Todos')
 await act(async()=>root.render(<TeamSetups target={{project:'crew'}} projectId="crew" list={{...list,setups:[{...list.setups[0],people:list.setups[0].people.map(person=>({...person,done:0}))}]}} refreshKey={2}/>))
 expect(setupPeople().textContent).toContain('0 of 3')
})
it('ignores a late detail after leaving the setup',async()=>{
 let resolve:(value:unknown)=>void=()=>{}
 vi.mocked(teamRequest).mockImplementation(async(_target,suffix)=>suffix?.includes('?')?await new Promise(r=>{resolve=r}):{agents:[]})
 await act(async()=>root.render(<TeamSetups target={{project:'crew'}} projectId="crew" list={list} refreshKey={1}/>))
 await click('Example setup');await click('‹ Todos')
 await act(async()=>{resolve(detail);await flush()})
 expect(host.textContent).not.toContain('Use the shared instructions.')
})

it('shows images saved in Team files next to the instructions and refuses a path that leaves the Team folder',async()=>{
 const request=vi.mocked(teamRequest)
 Object.assign(URL,{createObjectURL:vi.fn(()=>'blob:image'),revokeObjectURL:vi.fn()})
 request.mockImplementation(async(_target,suffix)=>{
  if(suffix?.startsWith('/file?'))return new Blob(['x'])
  if(suffix?.includes('?'))return {...detail,instructions:'![Shot](img/a.png)\n\n![Outside](../../secret.png)\n\n[Guide](docs/b.pdf)'}
  return {agents:[]}
 })
 await act(async()=>root.render(<TeamSetups target={{project:'crew'}} projectId="crew" list={list} refreshKey={1}/>))
 await click('Example setup');await act(async()=>{await flush();await flush()})
 const files=request.mock.calls.map(call=>call[1]).filter(suffix=>suffix?.startsWith('/file?'))
 expect(files).toEqual(['/file?path=team%2Fimg%2Fa.png']) // "../../secret.png" goes above the folder and is never requested
 expect(host.querySelector('img')?.getAttribute('src')).toBe('blob:image')
 expect([...host.querySelectorAll('button')].some(button=>button.textContent==='Guide')).toBe(true)
})
