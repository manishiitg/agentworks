// @vitest-environment happy-dom
import React, {act} from 'react'
import {createRoot} from 'react-dom/client'
import {afterEach,expect,it,vi} from 'vitest'
import {TeamPanel,type TeamPanelAgent,type TeamPanelEvent,type TeamPanelView} from './TeamPanel'
import {WorkspaceContextCard} from '../../components/workspace/WorkspaceContextCard'
Object.assign(globalThis,{IS_REACT_ACT_ENVIRONMENT:true})
const host=document.createElement('div');document.body.append(host);const root=createRoot(host)
const ask=vi.fn(),navigate=vi.fn(),artifact=vi.fn()
const agents:TeamPanelAgent[]=[
 {id:'own',name:'My private work',person:'You',mine:true,online:true,place:'mac',activity:'running'},
 {id:'peer',name:'Review',person:'Colleague',person_id:'person-1',mine:false,online:true,place:'mac',activity:'running',typing:true,last_message:'Checking the layout',cli:'CLI',model_id:'server-model'},
 {id:'wait',name:'Measurements',person:'Colleague',person_id:'person-1',mine:false,online:true,place:'mac',activity:'waiting',last_message:'Shared doing line'},
 {id:'idle',name:'Idle agent',person:'Idle person',mine:false,online:true,place:'mac'},
 {id:'offline',name:'Offline agent',person:'Away person',mine:false,online:false,place:'mac'},
 {id:'home',name:'Main',person:'Team',mine:false,team_agent:true,online:true,place:'server'},
]
const events:TeamPanelEvent[]=Array.from({length:6},(_,i)=>({id:`event-${i}`,kind:i===3?'join':i===2?'merge':'decision',at:`2026-11-15T12:0${i}:00Z`,title:`Member ${i}`,verb:i===3?'joined':'decided',line:`Event ${i}`}))
const shared=[{key:'own',name:'My shared helper',canChange:false}]
const artifacts=[{id:'team-copy',title:'Team proposal',kind:'artifact',addedBy:'Colleague'}]
const routines=[{id:'routine',name:'Morning summary',line:'Every morning',enabled:true,last_run_at:'2026-11-15T09:00:00Z',chat_id:'main'}]
async function render(view:TeamPanelView='live',rows=agents,feed=events,scope='server:person:crew') {
 await act(async()=>root.render(<WorkspaceContextCard><TeamPanel scope={scope} agents={rows} events={feed} view={view} onNavigate={navigate} onAsk={ask} shared={shared} artifacts={artifacts} routines={routines} onArtifact={artifact}/></WorkspaceContextCard>))
}
const click=async(text:string)=>{const button=[...host.querySelectorAll('button')].find(row=>row.textContent?.trim()===text||row.getAttribute('aria-label')?.includes(text)||row.textContent?.trim().startsWith(text));expect(button).toBeTruthy();await act(async()=>button!.click())}
afterEach(async()=>{await act(async()=>root.render(null));vi.clearAllMocks()})

it('shows admitted working people only, pins the Team agent, and replaces live lines without inventing targets',async()=>{
 await render()
 const panel=host.querySelector('[aria-label="Team live panel"]')!
 expect(panel.querySelector('button')?.textContent).toContain('Team agent')
 expect(panel.querySelector('button')?.title).toContain('Last ran Morning summary')
 expect(panel.querySelector('[aria-label="Working now"]')?.querySelectorAll('button')).toHaveLength(2)
 expect(panel.textContent).toContain('typing…')
 expect(panel.textContent).toContain('waiting for Colleague')
 expect(panel.textContent).toContain('Shared doing line')
 expect(panel.textContent).not.toContain('Idle person')
 expect(panel.textContent).not.toContain('Away person')
 expect(panel.textContent).not.toContain('My private work')
 await click('Review');expect(ask).toHaveBeenCalledWith(agents[1])
 await render('live',agents.map(row=>row.id==='peer'?{...row,typing:false,last_message:'Layout ready'}:row))
 expect(panel.textContent).not.toContain('Checking the layout')
 expect(panel.textContent).toContain('Layout ready')
})

it('collapses recent events by default, shows three single-line events and opens their full activity',async()=>{
 await render('live',agents,[...events,{id:'private',kind:'decision',private:true,at:'2026-11-15T13:00:00Z',title:'Only me',verb:'decided',line:'PRIVATE SENTINEL'},{id:'question',kind:'question',at:'2026-11-15T13:01:00Z',title:'Agent',verb:'asked',line:'QUESTION SENTINEL'}])
 const recent=host.querySelector('[aria-label="Just happened"]')!
 expect(recent.querySelector('button')?.getAttribute('aria-expanded')).toBe('false')
 expect(recent.querySelectorAll('li')).toHaveLength(0)
 expect(recent.textContent).toContain('3 new')
 await render('live',agents,[...events])
 expect(recent.querySelectorAll('li')).toHaveLength(0) // Updates never auto-expand.
 await click('Just happened')
 expect(recent.querySelectorAll('li')).toHaveLength(3)
 expect([...recent.querySelectorAll('li')].map(row=>row.textContent)).toEqual([5,4,3].map(i=>expect.stringContaining(`Event ${i}`)))
 const row=recent.querySelector('li button') as HTMLButtonElement
 expect(row.title).toContain('Event 5')
 expect(row.querySelector('.truncate')).toBeTruthy()
 await act(async()=>row.click());expect(navigate).toHaveBeenLastCalledWith('activity')
 await click('Just happened');expect(recent.querySelectorAll('li')).toHaveLength(0)
 await render('live',agents,events,'other-person');expect(recent.querySelector('button')?.getAttribute('aria-expanded')).toBe('false')
 expect(host.textContent).not.toContain('SENTINEL')
})

it('opens searchable agent details through right-panel navigation, with status and the same admitted ask row',async()=>{
 await render();await click('All agents');expect(navigate).toHaveBeenCalledWith('agents')
 await render('agents')
 expect(host.querySelector('[aria-label="Working now"]')).toBeNull()
 expect(host.textContent).toContain('Idle agent')
 expect(host.textContent).toContain('Offline agent')
 const input=host.querySelector('input')!
 await act(async()=>{Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value')!.set!.call(input,'colleague');input.dispatchEvent(new Event('input',{bubbles:true}))})
 expect(host.textContent).not.toContain('Idle agent')
 expect(host.textContent).toContain('server-model')
 await click('Measurements');expect(ask).toHaveBeenCalledWith(agents[2])
 await click('Team');expect(navigate).toHaveBeenLastCalledWith('live')
 await render('agents',agents,events,'server:other-person:crew');expect(input.value).toBe('')
})

it('opens items and full activity as separate right-panel views, never inline disclosures',async()=>{
 await render();await click('On the Team');expect(navigate).toHaveBeenCalledWith('items')
 expect(host.textContent).not.toContain('Team proposal')
 await render('items');expect(host.textContent).toContain('Morning summary');expect(host.textContent).toContain('Every morning')
 await click('Team proposal');expect(artifact).toHaveBeenCalledWith('team-copy')
 await render('live');await click('Shared by you');expect(navigate).toHaveBeenLastCalledWith('shared')
 await render('shared');expect(host.textContent).toContain('My shared helper');expect(host.textContent).toContain('Answers only')
 await render('live');await click('All activity');expect(navigate).toHaveBeenLastCalledWith('activity')
 await render('activity');expect(host.querySelectorAll('li')).toHaveLength(6)
})

it('gives empty live sections no space and keeps a 35-row detail list in the panel scroll body',async()=>{
 await act(async()=>root.render(<TeamPanel scope="empty" agents={agents.filter(row=>row.team_agent)} events={[]} onAsk={ask} onNavigate={navigate}/>))
 expect(host.querySelector('[aria-label="Working now"]')).toBeNull()
 expect(host.querySelector('[aria-label="Just happened"]')).toBeNull()
 expect(host.textContent).not.toContain('Shared by you')
 expect(host.textContent).not.toContain('On the Team')
 const rows:TeamPanelAgent[]=Array.from({length:35},(_,i)=>({...agents[1],id:`agent-${i}`,name:`Helper ${i}`}))
 await render('agents',rows)
 const list=host.querySelector('[aria-label="All Team agents"]')!
 expect(list.querySelectorAll('button')).toHaveLength(35)
 expect(host.querySelector('.workspace-menu-scroll')?.contains(list)).toBe(true)
 await act(async()=>(list.querySelectorAll('button')[34] as HTMLButtonElement).click())
 expect(ask).toHaveBeenCalledWith(rows[34])
})

// Owner: empty destinations disappear again when their live data is removed.
it('hides empty links and restores them only when admitted items arrive',async()=>{
 const empty=()=> <TeamPanel scope="empty" agents={[]} events={[]} onAsk={ask} onNavigate={navigate}/>
 await act(async()=>root.render(empty()))
 expect(host.querySelector('nav')).toBeNull()
 await render('live',agents,events,'empty')
 expect(host.textContent).toContain('All agents')
 expect(host.textContent).toContain('All activity')
 await act(async()=>root.render(empty()))
 expect(host.querySelector('nav')).toBeNull()
 expect(host.querySelector('[aria-label="Working now"]')).toBeNull()
})
