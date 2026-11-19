// @vitest-environment happy-dom
import {act} from 'react'
import {createRoot} from 'react-dom/client'
import {afterEach,expect,it,vi} from 'vitest'
import {CrewActivitySection,ActivityList} from './ActivitySection'
import type {TeamPanelEvent} from './TeamPanel'
const f=vi.hoisted(()=>({activity:{} as any,open:vi.fn()}))
vi.mock('./TeamActivity',()=>({useTeamActivity:()=>f.activity,openTeamActivity:f.open,loadTeamThread:()=>Promise.resolve([])}))
Object.assign(globalThis,{IS_REACT_ACT_ENVIRONMENT:true})
const host=document.createElement('div');document.body.append(host);const root=createRoot(host)
const events:TeamPanelEvent[]=Array.from({length:6},(_,i)=>({id:`event-${i}`,kind:i===3?'join':i===2?'merge':'decision',at:`2026-11-15T12:0${i}:00Z`,title:`Member ${i}`,verb:i===3?'joined':'decided',line:`Event ${i}`}))
const click=async(text:string)=>{const button=[...host.querySelectorAll('button')].find(row=>row.textContent?.trim()===text||row.getAttribute('aria-label')?.includes(text)||row.textContent?.trim().startsWith(text));expect(button).toBeTruthy();await act(async()=>button!.click())}
const recent=()=>host.querySelector('[aria-label="Tasks"]')!
afterEach(async()=>{await act(async()=>root.render(null));vi.clearAllMocks()})

it('remembers open or minimized, grows in place and opens the complete list elsewhere',async()=>{
 localStorage.clear()
 const all=vi.fn()
 const show=(feed:TeamPanelEvent[],scope='server:person:crew')=>act(async()=>root.render(<ActivityList scope={scope} events={feed} collapsedItem={feed.find(item=>item.id==='event-5')??null} onAll={all}/>))
 await show([...events,{id:'private',kind:'decision',private:true,at:'2026-11-15T13:00:00Z',title:'Only me',verb:'decided',line:'PRIVATE SENTINEL'}])
 expect(recent().querySelector('[aria-label="Toggle Tasks"]')?.getAttribute('aria-expanded')).toBe('true')
 expect(recent().querySelectorAll('li')).toHaveLength(4)
 expect(recent().textContent).not.toContain('new') // The server, not visible row count, supplies unseen.
 await click('▸ Show more');expect(recent().querySelectorAll('li')).toHaveLength(6)
 await click('All ›');expect(all).toHaveBeenCalled()
 expect(host.textContent).not.toContain('PRIVATE SENTINEL')
 await click('Toggle Tasks')
 expect(recent().querySelectorAll('li')).toHaveLength(0)
 expect(recent().textContent).toContain('Event 5')
 await show(events,'another-account')
 expect(recent().querySelector('[aria-label="Toggle Tasks"]')?.getAttribute('aria-expanded')).toBe('true')
})

// Team off: the person's own feed, with no full-list link and nothing to open.
it('shows a personal feed without the Team-only links',async()=>{
 await act(async()=>root.render(<ActivityList scope="personal" events={events.slice(0,1)}/>))
 expect(recent().textContent).toContain('Event 0')
 expect(host.textContent).not.toContain('All ›')
 expect(recent().querySelector('li button')).toBeNull()
 // Owner, 18 Nov: nothing to show with the Team off hides the section; with the Team on, one line and no toggle.
 await act(async()=>root.render(<ActivityList scope="empty" events={[]}/>))
 expect(host.querySelector('[aria-label="Tasks"]')).toBeNull()
 await act(async()=>root.render(<ActivityList scope="empty" events={[]} onAll={()=>{}}/>))
 expect(recent().textContent).toContain('No tasks yet')
 expect(recent().querySelector('[aria-label="Toggle Tasks"]')).toBeNull()
})

// Owner, 2026-11-17: no "N new" pill; unseen items keep only their dot.
it('marks the seen boundary only while the section is open, with no new-count pill',async()=>{
 localStorage.setItem('agentworks.team-activity:unseen-scope','minimized')
 const seen=vi.fn(),open=vi.fn()
 await act(async()=>root.render(<ActivityList scope="unseen-scope" events={[{...events[5],unseen:true,kind:'message',bridge:{thread_id:'inbox',can_open:true}}]} onActivitySeen={seen} onEvent={open}/>))
 expect(host.textContent).not.toMatch(/\d+ new/);expect(seen).not.toHaveBeenCalled()
 await click('Toggle Tasks');expect(seen).toHaveBeenCalledWith('event-5')
 const event=host.querySelector<HTMLButtonElement>('li button')!
 await act(async()=>event.click());expect(open).toHaveBeenCalledWith(expect.objectContaining({id:'event-5'}))
 expect(host.querySelector('li [aria-label="Unseen"]')).toBeTruthy()
})

// #2902: minimized preview is a server answer, even if own history is newer.
it('uses only the server collapsed preview and keeps personal needs separate',async()=>{
 localStorage.clear()
 const own={id:'own',kind:'task',at:'2026-11-16T12:00:00Z',title:'You',verb:'answered',line:'Own sentinel'}
 const peer={id:'peer',kind:'decision',at:'2026-11-15T12:00:00Z',title:'Peer',verb:'decided',line:'Peer sentinel'}
 const need={id:'need',line:'Pinned request',for:'You',for_me:true,buttons:['Approve'],agent:'Helper',person:'Peer',at:peer.at}
 const show=async(collapsedItem:TeamPanelEvent|null)=>act(async()=>root.render(<ActivityList scope="others-only" events={[own]} collapsedItem={collapsedItem} needs={[need]}/>))
 await show(peer)
 await click('Toggle Tasks')
 expect(recent().textContent).toContain('Peer sentinel')
 // #3210: an agent in a feature has its ◈ block as the one work list; cards waiting on a person still show.
 await act(async()=>root.render(<ActivityList scope="others-only" events={[own,peer]} needs={[need]} needsOnly/>))
 expect(host.textContent).toContain('Pinned request')
 expect(host.querySelector('[aria-label="Tasks"]')).toBeNull()
 await show(peer)
 expect(recent().textContent).not.toContain('Own sentinel')
 expect(recent().textContent).not.toContain('Pinned request')
 expect(host.querySelector('[aria-label="Needs you"]')?.textContent).toContain('Pinned request')
 await show(null)
 expect(recent().textContent).not.toContain('Peer sentinel')
 expect(recent().textContent).not.toContain('Own sentinel')
})

// #2893: the Team row's count is the person's own waiting cards; a Team that is off has none.
it('reports only current personal Needs-you cards and opens the Team list in the main pane',async()=>{
 const count=vi.fn()
 const team:any={target:{project:'crew'},view:{team:{on:true},people:[],agents:[],reachable:[]}}
 const render=(t=team)=>act(async()=>root.render(<CrewActivitySection scope="compact" projectId="crew" team={t} onNeedsCount={count}/>))
 f.activity={view:{feed:[],needs:[{id:'a',for_me:true,buttons:[]},{id:'b',for_me:false,buttons:[]}]}}
 await render();expect(count).toHaveBeenLastCalledWith(1)
 await click('All ›');expect(f.open).toHaveBeenCalledWith('crew')
 await render({...team,view:{...team.view,team:{on:false}}});expect(count).toHaveBeenLastCalledWith(0)
 expect(host.textContent).not.toContain('All ›')
 f.activity={}
})

// #2965: a review request shows its PR and plan on the card, and the person's note goes back with their button.
it('opens a review card files and sends a note with the answer',async()=>{
 const answer=vi.fn().mockResolvedValue({note:'Sent'}), open=vi.fn().mockResolvedValue({name:'plan.md',content_base64:btoa('# The plan')})
 const need={id:'msg-1',line:'Review my plan',for:'You',for_me:true,buttons:['Approve','Changes needed'],agent:'Helper',person:'Peer',at:'2026-11-17T12:00:00Z',links:['https://github.com/example/repo/pull/12'],files:[{id:'file-1',name:'plan.md',size:200}]}
 await act(async()=>root.render(<ActivityList scope="review" events={[]} needs={[need]} onAnswer={answer} openFile={open}/>))
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
