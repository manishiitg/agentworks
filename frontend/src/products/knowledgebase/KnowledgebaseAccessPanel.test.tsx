// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { it, expect, vi } from 'vitest'
const prepare=vi.hoisted(()=>vi.fn().mockResolvedValue({proposal_id:'p'}))
vi.mock('../../services/knowledgebaseApi',()=>({knowledgebaseApi:{prepareAccess:prepare},knowledgebaseError:(e:Error)=>e.message}))
import { KnowledgebaseAccessPanel } from './KnowledgebaseAccessPanel'
Object.assign(globalThis,{IS_REACT_ACT_ENVIRONMENT:true})
it('prepares exact versioned grants, prevents inherited removals, and offers readers no mutations',async()=>{
 const host=document.createElement('div');document.body.append(host);const root=createRoot(host);const done=vi.fn()
 const access={folder_path:'Team',effective_role:'owner' as const,acl_version:'acl-42',identities:[{id:'bob',name:'Bob',type:'user'}],grants:[{identity_id:'bob',role:'reader' as const,folder_path:'',inherited:true}]}
 try{
  await act(async()=>root.render(<KnowledgebaseAccessPanel access={access} onPrepared={done} onFolder={()=>{}}/>))
  expect([...host.querySelectorAll('button')].some(b=>b.textContent==='Remove')).toBe(false)
  const select=host.querySelector('select')!
  await act(async()=>{select.value='bob';select.dispatchEvent(new Event('change',{bubbles:true}))})
  await act(async()=>{host.querySelector('form')!.dispatchEvent(new Event('submit',{bubbles:true,cancelable:true}))})
  expect(prepare).toHaveBeenCalledWith(expect.objectContaining({action:'grant',folder_path:'Team',identity_id:'bob',role:'Reader',expected_acl_version:'acl-42'}))
  expect(done).toHaveBeenCalledOnce();expect(host.textContent).toContain('Access has not changed yet')
  await act(async()=>root.render(<KnowledgebaseAccessPanel access={{...access,effective_role:'reader'}} onPrepared={done} onFolder={()=>{}}/>))
  expect(host.querySelector('form')).toBeNull();expect(host.textContent).not.toContain('Manage access in chat')
 }finally{await act(async()=>root.unmount());host.remove()}
})
