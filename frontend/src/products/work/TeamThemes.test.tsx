// @vitest-environment happy-dom
import React, {act} from 'react'
import {createRoot} from 'react-dom/client'
import {afterEach,beforeEach,expect,it,vi} from 'vitest'
import {TeamThemes} from './TeamThemes'
import {teamRequest} from './teamApi'
import api from '../../services/api'
import {useAppearanceStore} from '../../stores/useAppearanceStore'
import {defaultAppearance} from '../../stores/appearanceTheme'
vi.mock('./teamApi',()=>({teamRequest:vi.fn(),teamFailure:(e:Error)=>({message:e.message})}))
vi.mock('../../services/api',()=>({default:{put:vi.fn(),get:vi.fn()}}))
Object.assign(globalThis,{IS_REACT_ACT_ENVIRONMENT:true})
const host=document.createElement('div');document.body.append(host);const root=createRoot(host)
const button=(text:string)=>[...host.querySelectorAll('button')].find(b=>b.textContent?.includes(text)||b.getAttribute('aria-label')?.includes(text))!
beforeEach(()=>{useAppearanceStore.setState({saved:[{...defaultAppearance(),name:'Ocean'}],load:vi.fn(async()=>{})})})
afterEach(async()=>{await act(async()=>root.render(null));vi.clearAllMocks()})

// #3223 Sharing and using a theme is optional both ways: Share sends only the server's looks-only export to the Team
// folder, and Use hands a teammate's file to the server's looks-only import without applying it.
it('shares the looks-only export and uses a teammate theme without applying it',async()=>{
 vi.mocked(teamRequest).mockImplementation(async(_t,suffix,method)=>{
  if(suffix==='/files')return {files:[{path:'team/themes/Dusk.json',hash:'h1',author:'Colleague',modified_at:'2026-11-19T01:00:00Z'},{path:'team/notes.md',hash:'h2'}]}
  if(method==='GET')return JSON.stringify({name:'Dusk',mode:'dark'})
  return ''
 })
 vi.mocked(api.put).mockResolvedValue({data:{theme:{name:'Ocean',mode:'dark'}}})
 await act(async()=>{root.render(<TeamThemes target={{project:'crew',folderId:'f'}}/>);await Promise.resolve();await Promise.resolve()})
 expect(host.textContent).toContain('Dusk')
 expect(host.textContent).toContain('Shared by Colleague')
 expect(host.textContent).not.toContain('notes')
 await act(async()=>button('Share with the Team').click())
 expect(api.put).toHaveBeenCalledWith('/api/appearance',{operation:'export',name:'Ocean',looks_only:true},expect.anything())
 expect(teamRequest).toHaveBeenCalledWith(expect.anything(),expect.stringContaining('path=team%2Fthemes%2FOcean.json'),'PUT',expect.stringContaining('Ocean'),true)
 await act(async()=>button('Add Dusk to my themes').click())
 expect(api.put).toHaveBeenLastCalledWith('/api/appearance',{operation:'import',looks_only:true,theme:{name:'Dusk',mode:'dark'}},expect.anything())
})
