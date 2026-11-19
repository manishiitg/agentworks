// @vitest-environment happy-dom
import React, {act} from 'react'
import {createRoot} from 'react-dom/client'
import {afterEach,expect,it,vi} from 'vitest'
import {TeamCommands} from './TeamCommands'
import {teamRequest} from './teamApi'
import {commandsApi} from '../../api/commands'
vi.mock('./teamApi',()=>({teamRequest:vi.fn(),teamFailure:(e:Error)=>({message:e.message})}))
vi.mock('../../api/commands',()=>({commandsApi:{listCommands:vi.fn(),createCommand:vi.fn()}}))
vi.mock('../../commands',()=>({loadAndRegisterUserCommands:vi.fn()}))
vi.mock('../../stores/useChatStore',()=>({useChatStore:(select:(state:unknown)=>unknown)=>select({chatTabs:{t:{tabId:'t',metadata:{agentProfileId:'work',agentProfileProjectId:'crew',agentProfileWorkspace:'Crew/crew'}}}})}))
vi.mock('./workTabs',()=>({belongsToWorkProject:()=>true}))
Object.assign(globalThis,{IS_REACT_ACT_ENVIRONMENT:true})
const host=document.createElement('div');document.body.append(host);const root=createRoot(host)
const button=(text:string)=>[...host.querySelectorAll('button')].find(b=>b.textContent?.includes(text)||b.getAttribute('aria-label')?.includes(text))!
afterEach(async()=>{await act(async()=>root.render(null));vi.clearAllMocks()})

// #3311 Sharing and adding a slash command is optional both ways. Share sends only the name, description and prompt;
// Add copies it into your own commands, a name you already have must be changed first, and your own has no Add.
it('shares a command as text only and adds a teammate command under a name you do not have yet',async()=>{
 const file={kind:'command',name:'review',description:'Review my changes',prompt:'Review the files you changed.'}
 vi.mocked(teamRequest).mockImplementation(async(_t,suffix,method)=>{
  if(suffix==='/files')return {files:[{path:'docs/commands/aaa.json',hash:'h1',author:'Colleague',mine:false},{path:'docs/commands/bbb.json',hash:'h2',mine:true}]}
  if(method==='GET')return JSON.stringify(suffix?.includes('aaa')?file:{...file,name:'standup'})
  return ''
 })
 vi.mocked(commandsApi.listCommands).mockResolvedValue({total:1,commands:[{folder_name:'review',file_path:'',content:'My own review.',frontmatter:{name:'review',description:'Mine'}}]})
 await act(async()=>{root.render(<TeamCommands target={{project:'crew',folderId:'f'}} folder="docs"/>);for(let i=0;i<6;i++)await Promise.resolve()})
 expect(host.textContent).toContain('Shared by Colleague')
 expect(host.querySelector('[aria-label="Add standup to my commands"]')).toBeNull()
 await act(async()=>button('Share with the Team').click())
 const put=vi.mocked(teamRequest).mock.calls.find(call=>call[2]==='PUT')!
 expect(JSON.parse(put[3] as string)).toEqual({kind:'command',name:'review',description:'Mine',prompt:'My own review.'})
 await act(async()=>button('Add review to my commands').click())
 expect(host.textContent).toContain('You already have /review')
 expect(([...host.querySelectorAll('button')].filter(b=>b.textContent==='Add to my commands').pop() as HTMLButtonElement).disabled).toBe(true)
 const input=host.querySelector('input[aria-label="Command name"]') as HTMLInputElement
 await act(async()=>{Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value')!.set!.call(input,'team-review');input.dispatchEvent(new Event('input',{bubbles:true}))})
 await act(async()=>[...host.querySelectorAll('button')].filter(b=>b.textContent==='Add to my commands').pop()!.click())
 expect(commandsApi.createCommand).toHaveBeenCalledWith(expect.objectContaining({name:'team-review',content:expect.stringContaining('Review the files you changed.')}),'Crew/crew')
})
