// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import { ChatChangesRow } from './ChatChangesRow'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
// PLAT-746-324: server-authorized chat usage chooses the group; navigation
// must preserve the actual worktree rather than opening the project root.
it('opens the server-listed worktree and leaves unrelated repositories collapsed', async () => {
 const host=document.createElement('div');const root=createRoot(host);const open=vi.fn()
 try {
  await act(async()=>root.render(<ChatChangesRow onOpen={open} section={{id:'changes',title:'Changes',detail:'2 files',rows:[
   {id:'api',path:'api',label:'API',detail:'fix/api · 2 changed',used_in_chat:true},
   {id:'web',path:'web',label:'Web',detail:'fix/web · Clean',used_in_chat:true},
   {id:'other',path:'other',label:'Other',detail:'main · Clean'},
  ]}}/>))
  expect(host.textContent).toContain('2 files');expect(host.querySelector('details')?.open).toBe(false)
  await act(async()=>[...host.querySelectorAll('button')].find(b=>b.textContent?.includes('fix/web'))!.click())
  expect(open).toHaveBeenCalledWith('web')
  await act(async()=>root.render(<ChatChangesRow onOpen={open} section={{id:'changes',title:'Changes',detail:'0 files',rows:[]}}/>))
  expect(host.textContent).toBe('')
 } finally {await act(async()=>root.unmount())}
})
