// @vitest-environment happy-dom
import React, {act} from 'react'
import {createRoot} from 'react-dom/client'
import {afterEach,expect,it,vi} from 'vitest'
import {TeamWayCard} from './TeamWay'
import {teamWay,teamOnlineItems} from './teamApi'
import {sendToMain} from '../../utils/agentPurposeDraft'
vi.mock('./teamApi',()=>({teamWay:vi.fn(),teamOnlineItems:vi.fn()}))
vi.mock('../../utils/agentPurposeDraft',()=>({sendToMain:vi.fn()}))
Object.assign(globalThis,{IS_REACT_ACT_ENVIRONMENT:true})
const host=document.createElement('div');document.body.append(host);const root=createRoot(host)
afterEach(async()=>{await act(async()=>root.render(null));vi.resetAllMocks()})

// Source tags must describe the server's login classification, not expose key/sign-in values.
it('shows the server source for shared connections and personal sign-ins without guessing an unknown source',async()=>{
 vi.mocked(teamWay).mockResolvedValue({items:[
  {id:'shared',name:'Shared app',kind:'app',login:'shared'},
  {id:'key',name:'EXAMPLE_KEY',kind:'secret',login:'shared'},
  {id:'personal',name:'Personal app',kind:'needs_sign_in',login:'sign_in',connected:false},
  {id:'unknown',name:'Unknown source',kind:'app'},
 ],on_team:[],rules:false,skills:[]})
 vi.mocked(teamOnlineItems).mockResolvedValue({home_project_id:'home',items:[]})
 await act(async()=>{root.render(<TeamWayCard target={{project:'crew'}} projectId="crew"/>);await Promise.resolve();await Promise.resolve()})
 const rows=[...host.querySelectorAll('li')]
 expect(rows[0].querySelector('[aria-label="Connection source"]')?.textContent).toBe('Team key')
 expect(rows[1].querySelector('[aria-label="Connection source"]')?.textContent).toBe('Team key')
 expect(rows[2].querySelector('[aria-label="Connection source"]')?.textContent).toBe('Your sign-in')
 expect(rows[3].querySelector('[aria-label="Connection source"]')).toBeNull()
 const connect=[...rows[2].querySelectorAll('button')].find(button=>button.textContent==='Connect Personal app')!
 await act(async()=>connect.click())
 expect(sendToMain).toHaveBeenCalledWith('crew',expect.stringContaining('Personal app'))
 expect(teamWay).toHaveBeenCalledTimes(1)
})
