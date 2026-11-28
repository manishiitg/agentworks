// @vitest-environment happy-dom
import React, {act} from 'react'
import {createRoot} from 'react-dom/client'
import {expect,it,vi} from 'vitest'
import {TeamWizard} from './TeamWizard'
import {finishTeamWizard} from './teamApi'
vi.mock('./teamApi',()=>({teamWizard:vi.fn(async()=>({new_team:true,default_level:'works',levels:[{key:'works',available:true}],people:[],folders:{default:'team',folders:['team']},agents:[{key:'main',name:'Main',is_default:true,in_team:false},{key:'new',name:'New agent',in_team:true}]})),finishTeamWizard:vi.fn(async()=>{}),teamFailure:(e:Error)=>e}))
Object.assign(globalThis,{IS_REACT_ACT_ENVIRONMENT:true})
// Main used to be forced on even when its saved Only me choice was returned.
it('shows the server choices and never forces private Main into the submitted setup',async()=>{
 const host=document.createElement('div');document.body.append(host);const root=createRoot(host)
 const click=async(label:string)=>{const button=[...host.querySelectorAll('button')].find(b=>b.textContent===label);expect(button).toBeTruthy();await act(async()=>button!.click())}
 try {
  await act(async()=>root.render(<TeamWizard target={{project:'crew'}} onNo={()=>{}}/>))
  await click('Continue');await click('Continue')
  const main=host.querySelector<HTMLButtonElement>('[aria-label="Main"]')!
  expect(main.disabled).toBe(false);expect(main.getAttribute('aria-checked')).toBe('false')
  expect(host.querySelector('[aria-label="New agent"]')?.getAttribute('aria-checked')).toBe('true')
  await click('Turn the Team on')
  expect(finishTeamWizard).toHaveBeenCalledWith({project:'crew'},expect.objectContaining({agents:['new']}))
 } finally {await act(async()=>root.unmount());host.remove()}
})
