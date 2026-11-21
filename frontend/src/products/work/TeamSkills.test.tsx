// @vitest-environment happy-dom
import React, {act} from 'react'
import {createRoot} from 'react-dom/client'
import {afterEach,expect,it,vi} from 'vitest'
import {TeamSkills} from './TeamSkills'
import {teamRequest} from './teamApi'
import {sendToOpenChat} from '../../utils/agentPurposeDraft'
vi.mock('./teamApi',()=>({teamRequest:vi.fn(),teamFailure:(e:Error)=>({message:e.message})}))
vi.mock('../../utils/agentPurposeDraft',()=>({sendToOpenChat:vi.fn(()=>true)}))
vi.mock('../../stores/useChatStore',()=>({useChatStore:{getState:()=>({addToast:vi.fn()})}}))
Object.assign(globalThis,{IS_REACT_ACT_ENVIRONMENT:true})
const host=document.createElement('div');document.body.append(host);const root=createRoot(host)
const button=(text:string)=>[...host.querySelectorAll('button')].find(b=>b.textContent?.includes(text)||b.getAttribute('aria-label')?.includes(text))!
afterEach(async()=>{await act(async()=>root.render(null));vi.clearAllMocks()})

// #3459 A shared skill is optional both ways and nothing is installed from the screen: the screen lists it with its files
// and the key NAMES it needs, and Add / Share only ask the person's own agent (which shows every script first). Your own
// shared skill can be stopped, never added.
it('lists a shared skill with its files and key names, asks the own agent to add or share, and stops only your own',async()=>{
 const skill={kind:'skill',name:'deploy-staging',files:{'SKILL.md':'---\nname: deploy-staging\ndescription: Deploy staging safely\n---\nNeeds $SECRET_DEPLOY_KEY.','scripts/deploy.sh':'echo deploy'}}
 vi.mocked(teamRequest).mockImplementation(async(_t,suffix,method)=>{
  if(suffix==='/files')return {files:[{path:'docs/shared-skills/aaa.json',hash:'h1',author:'Colleague',mine:false},{path:'docs/shared-skills/bbb.json',hash:'h2',mine:true}]}
  if(method==='GET')return JSON.stringify(suffix?.includes('bbb')?{...skill,name:'release-notes',files:{'SKILL.md':'Notes'}}:skill)
  return ''
 })
 await act(async()=>{root.render(<TeamSkills target={{project:'crew',folderId:'f'}} projectId="crew" folder="docs"/>);for(let i=0;i<6;i++)await Promise.resolve()})
 expect(host.textContent).toContain('Deploy staging safely')
 expect(host.textContent).toContain('scripts/deploy.sh')
 expect(host.textContent).toContain('Needs key DEPLOY_KEY')
 expect(host.querySelector('[aria-label="Add release-notes to my skills"]')).toBeNull()
 await act(async()=>button('Add deploy-staging to my skills').click())
 expect(sendToOpenChat).toHaveBeenCalledWith('crew',expect.stringContaining('docs/shared-skills/aaa.json'))
 await act(async()=>button('Share one of my skills').click())
 expect(sendToOpenChat).toHaveBeenLastCalledWith('crew',expect.stringContaining('Sharing a skill with the Team'))
 await act(async()=>button('Stop sharing release-notes').click())
 expect(vi.mocked(teamRequest).mock.calls.find(call=>call[2]==='DELETE')![1]).toContain('docs%2Fshared-skills%2Fbbb.json')
})
