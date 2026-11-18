// @vitest-environment happy-dom
import React, {act} from 'react'
import {createRoot} from 'react-dom/client'
import {afterEach,expect,it,vi} from 'vitest'
import {TeamKit} from './TeamKit'
import {teamKit} from './teamApi'
import {sendToMain} from '../../utils/agentPurposeDraft'
vi.mock('./teamApi',()=>({teamKit:vi.fn(),teamRequest:vi.fn(()=>new Promise(()=>{})),changeTeamWay:vi.fn(),teamFailure:(e:Error)=>({message:e.message})}))
vi.mock('./TeamWay',()=>({TeamRoutinesCard:()=>null}))
vi.mock('./TeamSetups',()=>({TeamSetups:()=>null}))
vi.mock('../../utils/agentPurposeDraft',()=>({sendToMain:vi.fn(()=>true)}))
Object.assign(globalThis,{IS_REACT_ACT_ENVIRONMENT:true})
const host=document.createElement('div');document.body.append(host);const root=createRoot(host)
afterEach(async()=>{await act(async()=>root.render(null));vi.clearAllMocks()})

// #3141 The kit shows the server's own states and sends only the person's own requests: Apply what's missing uses the
// server's words, and a missing sign-in asks the person's own Main to connect it.
it('shows who has each item and sends Apply and Connect to the person own agent',async()=>{
 vi.mocked(teamKit).mockResolvedValue({people:[{person_id:'me',name:'Me',you:true},{person_id:'peer',name:'Colleague'}],have:1,total:2,missing:1,apply_ask:'Set me up with the Team kit.',items:[
  {section:'instructions',kind:'instructions',id:'TEAM_INSTRUCTIONS.md',name:'Team instructions',everyone:true,mine:'auto',by:'Owner',by_verb:'Written by'},
  {section:'tools',kind:'key',id:'key:EXAMPLE_KEY',name:'EXAMPLE_KEY',everyone:true,mine:'auto'},
  {section:'tools',kind:'sign_in',id:'sign_in:example',name:'Example app',mine:'missing',people:[{person_id:'me',state:'missing'},{person_id:'peer',state:'have'}]},
 ]})
 await act(async()=>{root.render(<TeamKit target={{project:'crew'}} projectId="crew" refreshKey={0}/>);await Promise.resolve();await Promise.resolve()})
 expect(host.textContent).toContain('You: 1 of 2 set up')
 expect(host.textContent).toContain('Written by Owner')
 expect(host.textContent).toContain('1 of 2')
 const button=(text:string)=>[...host.querySelectorAll('button')].find(b=>b.textContent?.includes(text))!
 await act(async()=>button("Apply what's missing").click())
 expect(sendToMain).toHaveBeenCalledWith('crew','Set me up with the Team kit.')
 await act(async()=>button('Example app').click())
 await act(async()=>button('Connect Example app').click())
 expect(sendToMain).toHaveBeenLastCalledWith('crew',expect.stringContaining('Example app'))
})
