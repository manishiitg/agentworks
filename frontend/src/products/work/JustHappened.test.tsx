// @vitest-environment happy-dom
import {act} from 'react'
import {createRoot} from 'react-dom/client'
import {afterEach,expect,it,vi} from 'vitest'
import {CrewJustHappened,JustHappenedList} from './JustHappened'
import type {TeamPanelEvent} from './TeamPanel'
const f=vi.hoisted(()=>({activity:{} as any,open:vi.fn()}))
vi.mock('./TeamActivity',()=>({useTeamActivity:()=>f.activity,openTeamActivity:f.open}))
Object.assign(globalThis,{IS_REACT_ACT_ENVIRONMENT:true})
const host=document.createElement('div');document.body.append(host);const root=createRoot(host)
const events:TeamPanelEvent[]=Array.from({length:6},(_,i)=>({id:`event-${i}`,kind:i===3?'join':i===2?'merge':'decision',at:`2026-11-15T12:0${i}:00Z`,title:`Member ${i}`,verb:i===3?'joined':'decided',line:`Event ${i}`}))
const click=async(text:string)=>{const button=[...host.querySelectorAll('button')].find(row=>row.textContent?.trim()===text||row.getAttribute('aria-label')?.includes(text)||row.textContent?.trim().startsWith(text));expect(button).toBeTruthy();await act(async()=>button!.click())}
const recent=()=>host.querySelector('[aria-label="Just happened"]')!
afterEach(async()=>{await act(async()=>root.render(null));vi.clearAllMocks()})

it('remembers open or minimized, grows in place and opens the complete list elsewhere',async()=>{
 localStorage.clear()
 const all=vi.fn()
 const show=(feed:TeamPanelEvent[],scope='server:person:crew')=>act(async()=>root.render(<JustHappenedList scope={scope} events={feed} collapsedItem={feed.find(item=>item.id==='event-5')??null} onAll={all}/>))
 await show([...events,{id:'private',kind:'decision',private:true,at:'2026-11-15T13:00:00Z',title:'Only me',verb:'decided',line:'PRIVATE SENTINEL'}])
 expect(recent().querySelector('[aria-label="Toggle Just happened"]')?.getAttribute('aria-expanded')).toBe('true')
 expect(recent().querySelectorAll('li')).toHaveLength(4)
 expect(recent().textContent).not.toContain('new') // The server, not visible row count, supplies unseen.
 await click('▸ Show more');expect(recent().querySelectorAll('li')).toHaveLength(6)
 await click('All ›');expect(all).toHaveBeenCalled()
 expect(host.textContent).not.toContain('PRIVATE SENTINEL')
 await click('Toggle Just happened')
 expect(recent().querySelectorAll('li')).toHaveLength(0)
 expect(recent().textContent).toContain('Event 5')
 await show(events,'another-account')
 expect(recent().querySelector('[aria-label="Toggle Just happened"]')?.getAttribute('aria-expanded')).toBe('true')
})

// Team off: the person's own feed, with no full-list link and nothing to open.
it('shows a personal feed without the Team-only links',async()=>{
 await act(async()=>root.render(<JustHappenedList scope="personal" events={events.slice(0,1)}/>))
 expect(recent().textContent).toContain('Event 0')
 expect(host.textContent).not.toContain('All ›')
 expect(recent().querySelector('li button')).toBeNull()
 await act(async()=>root.render(<JustHappenedList scope="empty" events={[]}/>))
 expect(recent().textContent).toContain('Nothing new')
})

it('uses the server unseen count and seen boundary only while the section is open',async()=>{
 localStorage.setItem('agentworks.team-activity:unseen-scope','minimized')
 const seen=vi.fn(),open=vi.fn()
 await act(async()=>root.render(<JustHappenedList scope="unseen-scope" events={[{...events[5],unseen:true,kind:'message',bridge:{thread_id:'inbox',can_open:true}}]} unseen={7} onActivitySeen={seen} onEvent={open}/>))
 expect(host.textContent).toContain('7 new');expect(seen).not.toHaveBeenCalled()
 await click('Toggle Just happened');expect(seen).toHaveBeenCalledWith('event-5')
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
 const show=async(collapsedItem:TeamPanelEvent|null)=>act(async()=>root.render(<JustHappenedList scope="others-only" events={[own]} collapsedItem={collapsedItem} unseen={1} needs={[need]}/>))
 await show(peer)
 await click('Toggle Just happened')
 expect(recent().textContent).toContain('Peer sentinel')
 expect(recent().textContent).not.toContain('Own sentinel')
 expect(recent().textContent).not.toContain('Pinned request')
 expect(recent().textContent).toContain('1 new')
 expect(host.querySelector('[aria-label="Needs you"]')?.textContent).toContain('Pinned request')
 await show(null)
 expect(recent().textContent).not.toContain('Peer sentinel')
 expect(recent().textContent).not.toContain('Own sentinel')
})

// #2893: the Team row's count is the person's own waiting cards; a Team that is off has none.
it('reports only current personal Needs-you cards and opens the Team list in the main pane',async()=>{
 const count=vi.fn()
 const team:any={target:{project:'crew'},view:{team:{on:true},people:[],agents:[],reachable:[]}}
 const render=(t=team)=>act(async()=>root.render(<CrewJustHappened scope="compact" projectId="crew" team={t} onNeedsCount={count}/>))
 f.activity={view:{feed:[],needs:[{id:'a',for_me:true,buttons:[]},{id:'b',for_me:false,buttons:[]}]}}
 await render();expect(count).toHaveBeenLastCalledWith(1)
 await click('All ›');expect(f.open).toHaveBeenCalledWith('crew')
 await render({...team,view:{...team.view,team:{on:false}}});expect(count).toHaveBeenLastCalledWith(0)
 expect(host.textContent).not.toContain('All ›')
 f.activity={}
})
