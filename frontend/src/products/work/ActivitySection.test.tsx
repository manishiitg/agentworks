// @vitest-environment happy-dom
import {act} from 'react'
import {createRoot} from 'react-dom/client'
import {afterEach,expect,it,vi} from 'vitest'
import {CrewActivitySection,NeedCard} from './ActivitySection'
const f=vi.hoisted(()=>({activity:{} as any}))
vi.mock('./TeamActivity',()=>({useTeamActivity:()=>f.activity}))
Object.assign(globalThis,{IS_REACT_ACT_ENVIRONMENT:true})
const host=document.createElement('div');document.body.append(host);const root=createRoot(host)
afterEach(async()=>{await act(async()=>root.render(null));vi.clearAllMocks()})

// #2893: the Team row's count is the person's own waiting cards; a Team that is off has none. The side panel lists
// no cards at all (owner, 19 Nov: approvals do not belong there); the Team monitor answers them.
it('counts the person\'s own Needs-you cards and lists none',async()=>{
 const count=vi.fn()
 const team:any={target:{project:'crew'},view:{team:{on:true},people:[],agents:[],reachable:[]}}
 const render=(t=team)=>act(async()=>root.render(<CrewActivitySection projectId="crew" team={t} onNeedsCount={count}/>))
 f.activity={view:{feed:[{id:'e',kind:'task',at:'2026-11-15T12:00:00Z',title:'Peer',verb:'finished',line:'Feed sentinel'}],needs:[{id:'a',line:'Pinned request',for_me:true,buttons:[]},{id:'b',for_me:false,buttons:[]}]}}
 await render();expect(count).toHaveBeenLastCalledWith(1)
 expect(host.querySelector('[aria-label="Needs you"]')).toBeNull()
 expect(host.textContent).toBe('')
 await render({...team,view:{...team.view,team:{on:false}}});expect(count).toHaveBeenLastCalledWith(0)
 expect(host.querySelector('[aria-label="Needs you"]')).toBeNull()
 f.activity={}
})

// #2965: a review request shows its PR and plan on the card, and the person's note goes back with their button.
it('opens a review card files and sends a note with the answer',async()=>{
 const answer=vi.fn().mockResolvedValue({note:'Sent'}), open=vi.fn().mockResolvedValue({name:'plan.md',content_base64:btoa('# The plan')})
 const need={id:'msg-1',line:'Review my plan',for:'You',for_me:true,buttons:['Approve','Changes needed'],agent:'Helper',person:'Peer',at:'2026-11-17T12:00:00Z',links:['https://github.com/example/repo/pull/12'],files:[{id:'file-1',name:'plan.md',size:200}]}
 await act(async()=>root.render(<section aria-label="Needs you"><NeedCard need={need} onAnswer={answer} openFile={open}/></section>))
 const card=host.querySelector('[aria-label="Needs you"]')!
 expect(card.querySelector('a[href="https://github.com/example/repo/pull/12"]')?.textContent).toContain('PR 12')
 await act(async()=>{[...card.querySelectorAll('button')].find(b=>b.textContent?.includes('plan.md'))!.click()})
 expect(open).toHaveBeenCalledWith('msg-1',need.files[0])
 expect(card.textContent).toContain('The plan')
 const note=card.querySelector('textarea')!
 await act(async()=>{Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype,'value')!.set!.call(note,'Split step 2');note.dispatchEvent(new Event('input',{bubbles:true}))})
 await act(async()=>{[...card.querySelectorAll('button')].find(b=>b.textContent==='Changes needed')!.click()})
 expect(answer).toHaveBeenCalledWith('msg-1','Changes needed',undefined,'Split step 2')
})
