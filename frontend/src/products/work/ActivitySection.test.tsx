// @vitest-environment happy-dom
import {act} from 'react'
import {createRoot} from 'react-dom/client'
import {afterEach,expect,it,vi} from 'vitest'
import {CrewActivitySection,NeedCard} from './ActivitySection'
const f=vi.hoisted(()=>({activity:{} as any,teamCalls:[] as any[]}))
vi.mock('./TeamActivity',()=>({useTeamActivity:(...args:any[])=>{f.teamCalls.push(args);return f.activity}}))
Object.assign(globalThis,{IS_REACT_ACT_ENVIRONMENT:true})
const host=document.createElement('div');document.body.append(host);const root=createRoot(host)
afterEach(async()=>{await act(async()=>root.render(null));f.teamCalls=[];vi.clearAllMocks()})

// #2893: the count is the person's own waiting cards; a Team that is off has none. The side panel lists only the
// person's own cards, newest open and the rest folded (owner, 23 Nov).
it('lists only the person\'s own Needs-you cards, the newest open',async()=>{
 const count=vi.fn()
 const team:any={target:{project:'crew'},view:{team:{on:true},people:[],agents:[],reachable:[]}}
 const render=(t=team)=>act(async()=>root.render(<CrewActivitySection projectId="crew" team={t} onNeedsCount={count}/>))
 f.activity={view:{needs:[{id:'a',line:'Newest request',for_me:true,buttons:[]},{id:'c',line:'Older request',for_me:true,buttons:[]},{id:'b',line:'Someone else',for_me:false,buttons:[]}]}}
 await render();expect(count).toHaveBeenLastCalledWith(2)
 const box=()=>host.querySelector('[aria-label="Needs you"]')
 expect(box()?.textContent).toContain('Newest request');expect(box()?.textContent).not.toContain('Older request');expect(box()?.textContent).not.toContain('Someone else')
 await act(async()=>{[...host.querySelectorAll('button')].find(b=>b.textContent==='Show 1 more')!.click()})
 expect(box()?.textContent).toContain('Older request')
 await render({...team,view:{...team.view,team:{on:false}}});expect(count).toHaveBeenLastCalledWith(0)
 expect(box()).toBeNull()
 f.activity={}
})

// #2965: a review request shows its PR and plan on the card, and the person's note goes back with their button.
// The card shows the request's own words, not only its title: a card with just the title left people guessing.
it('opens a review card files and sends a note with the answer',async()=>{
 const answer=vi.fn().mockResolvedValue({note:'Sent'}), open=vi.fn().mockResolvedValue({name:'plan.md',content_base64:btoa('# The plan')})
 const need={id:'msg-1',line:'Review my plan',message:'Step 2 changes the schedule format.',for:'You',for_me:true,buttons:['Approve','Changes needed'],agent:'Helper',person:'Peer',at:'2026-11-17T12:00:00Z',links:['https://github.com/example/repo/pull/12'],files:[{id:'file-1',name:'plan.md',size:200}]}
 await act(async()=>root.render(<section aria-label="Needs you"><NeedCard need={need} onAnswer={answer} openFile={open}/></section>))
 const card=host.querySelector('[aria-label="Needs you"]')!
 expect(card.querySelector('a[href="https://github.com/example/repo/pull/12"]')?.textContent).toContain('PR 12')
 await act(async()=>{[...card.querySelectorAll('button')].find(b=>b.textContent?.includes('plan.md'))!.click()})
 expect(open).toHaveBeenCalledWith('msg-1',need.files[0])
 expect(card.textContent).toContain('The plan')
 expect(card.textContent).toContain('Step 2 changes the schedule format.')
 await act(async()=>{[...card.querySelectorAll('button')].find(b=>b.textContent==='Add a note')!.click()})
 const note=card.querySelector('textarea')!
 await act(async()=>{Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype,'value')!.set!.call(note,'Split step 2');note.dispatchEvent(new Event('input',{bubbles:true}))})
 await act(async()=>{[...card.querySelectorAll('button')].find(b=>b.textContent==='Changes needed')!.click()})
 expect(answer).toHaveBeenCalledWith('msg-1','Changes needed',undefined,'Split step 2')
})

// #4181: closing the right panel pauses Team activity reads instead of polling while hidden.
it('disables team activity reads when the right panel is hidden',async()=>{
 const team:any={target:{project:'crew'},view:{team:{on:true},people:[],agents:[],reachable:[]}}
 await act(async()=>root.render(<CrewActivitySection projectId="crew" team={team} enabled={false}/>))
 expect(f.teamCalls.at(-1)).toEqual([undefined,'crew',false])
})
