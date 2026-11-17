// @vitest-environment happy-dom
import React,{act} from 'react'
import {createRoot} from 'react-dom/client'
import {expect,it,vi} from 'vitest'
import {TeamPanelHeader} from './TeamPanelHeader'

it('keeps a many-person Team header bounded to three avatars and counts, with full names on hover',async()=>{
 const host=document.createElement('div'),root=createRoot(host)
 Object.assign(globalThis,{IS_REACT_ACT_ENVIRONMENT:true})
 const select=vi.fn()
 const people=Array.from({length:7},(_,i)=>({user_id:`person-${i}`,name:`Teammate ${i}`,active:true,can_remove:false,presence:{online:i===0,line:i===0?'Mac online':'Mac offline'}}))
 const view={team:{on:true,name:'Design workspace'},people,reachable:[{activity:'running',kind:'terminal'},{activity:'waiting',kind:'terminal'},{activity:'waiting',kind:'team_agent'}]}
 try {
  await act(async()=>root.render(<TeamPanelHeader view={view} onPersonSelect={select}/>))
  expect(host.textContent).toContain('Design workspace')
  expect(host.textContent).toContain('+4');expect(host.textContent).toContain('2 working');expect(host.textContent).not.toContain('7 people')
  const stack=host.querySelector('[role="group"][aria-label="Team people"]')!
  expect(stack.children).toHaveLength(4) // Three avatars and one overflow count, never seven names.
  expect(stack.querySelectorAll('button')).toHaveLength(4)
  const avatar=stack.querySelector<HTMLButtonElement>('button')!
  expect(avatar.style.width).toBe('20px');expect(avatar.style.height).toBe('20px')
  expect(avatar.style.minWidth).toBe('20px');expect(avatar.style.padding).toBe('0px')
  expect(stack.querySelectorAll('[aria-label="Mac online"]')).toHaveLength(1)
  await act(async()=>stack.querySelector('button')!.click());expect(select).toHaveBeenLastCalledWith('person-0')
  await act(async()=>stack.querySelector<HTMLButtonElement>('[aria-label="More Team people"]')!.click())
  await act(async()=>host.querySelector<HTMLButtonElement>('[aria-label="Show Teammate 6\'s agents"]')!.click());expect(select).toHaveBeenLastCalledWith('person-6')
  expect(host.querySelector('[title]')?.getAttribute('title')).toContain('Teammate 6')
  expect(host.textContent).not.toContain('Teammate')
  // Owner, 2026-11-17: the Team name opens the Team tab; the count still folds the section.
  const name=vi.fn(),toggle=vi.fn()
  await act(async()=>root.render(<TeamPanelHeader view={view} onName={name} onToggle={toggle}/>))
  await act(async()=>host.querySelector<HTMLButtonElement>('[title="Open Design workspace"]')!.click());expect(name).toHaveBeenCalledOnce();expect(toggle).not.toHaveBeenCalled()
  await act(async()=>host.querySelector<HTMLButtonElement>('[aria-label="Collapse Team"]')!.click());expect(toggle).toHaveBeenCalledOnce()
  await act(async()=>root.render(<TeamPanelHeader fallback="Team off"/>))
  expect(host.textContent).toBe('Team off');expect(host.querySelector('[role="img"]')).toBeNull()
 } finally {await act(async()=>root.unmount())}
})
