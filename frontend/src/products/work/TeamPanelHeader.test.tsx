// @vitest-environment happy-dom
import React,{act} from 'react'
import {createRoot} from 'react-dom/client'
import {expect,it} from 'vitest'
import {TeamPanelHeader} from './TeamPanelHeader'
import type {TeamTabView} from './teamApi'

it('keeps a many-person Team header bounded to three avatars and counts, with full names on hover',async()=>{
 const host=document.createElement('div'),root=createRoot(host)
 Object.assign(globalThis,{IS_REACT_ACT_ENVIRONMENT:true})
 const people=Array.from({length:7},(_,i)=>({user_id:`person-${i}`,name:`Teammate ${i}`,active:true,can_remove:false}))
 const view={team:{on:true,name:'Design workspace'},people,reachable:[{activity:'running'},{activity:'waiting',kind:'terminal'},{activity:'waiting',kind:'team_agent'}]} as TeamTabView
 try {
  await act(async()=>root.render(<TeamPanelHeader view={view}/>))
  expect(host.textContent).toContain('Design workspace')
  expect(host.textContent).toContain('+4');expect(host.textContent).toContain('7 people · 2 working')
  const stack=host.querySelector('[role="img"]')!
  expect(stack.children).toHaveLength(4) // Three avatars and one overflow count, never seven names.
  expect(stack.getAttribute('aria-label')).toBe(people.map(person=>person.name).join(', '))
  expect(host.querySelector('[title]')?.getAttribute('title')).toContain('Teammate 6')
  expect(host.textContent).not.toContain('Teammate')
  await act(async()=>root.render(<TeamPanelHeader fallback="Team off"/>))
  expect(host.textContent).toBe('Team off');expect(host.querySelector('[role="img"]')).toBeNull()
 } finally {await act(async()=>root.unmount())}
})
