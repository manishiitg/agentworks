// @vitest-environment happy-dom
import React, {act} from 'react'
import {createRoot} from 'react-dom/client'
import {afterEach,expect,it,vi} from 'vitest'
import {TeamPanel,type TeamPanelAgent,type TeamPanelView,type TeamPanelRoutine} from './TeamPanel'
import {WorkspaceContextCard} from '../../components/workspace/WorkspaceContextCard'
Object.assign(globalThis,{IS_REACT_ACT_ENVIRONMENT:true})
const host=document.createElement('div');document.body.append(host);const root=createRoot(host)
const ask=vi.fn(),navigate=vi.fn(),artifact=vi.fn()
const agents:TeamPanelAgent[]=[
 {id:'own',name:'My private work',person:'You',mine:true,online:true,place:'mac',activity:'running'},
 {id:'peer',name:'Review',purpose:'Review shared changes',examples:['Check a patch','Explain a failure'],person:'Colleague',person_id:'person-1',mine:false,online:true,place:'mac',activity:'running',typing:true,last_message:'Checking the layout',cli:'Claude',client_build:'helper-build',update_needed:true,model_id:'server-model'},
 {id:'wait',name:'Measurements',person:'Colleague',person_id:'person-1',mine:false,online:true,place:'mac',activity:'waiting',engine:'codex',last_message:'Shared doing line'},
 {id:'idle',name:'Idle agent',person:'Idle person',mine:false,online:true,place:'mac'},
 {id:'offline',name:'Offline agent',person:'Away person',mine:false,online:false,place:'mac'},
 {id:'home',name:'Main',person:'Team',mine:false,team_agent:true,online:true,place:'server'},
]
const shared=[{key:'own',name:'My shared helper',canChange:false}]
const artifacts=[{id:'team-copy',title:'Team proposal',kind:'artifact',addedBy:'Colleague'}]
const routines=[{id:'routine',name:'Morning summary',line:'Every morning',enabled:true,last_run_at:'2026-11-15T09:00:00Z',chat_id:'main'}]
async function render(view:TeamPanelView='live',rows=agents,scope='server:person:crew',routineRows:TeamPanelRoutine[]=routines) {
 await act(async()=>root.render(<WorkspaceContextCard><TeamPanel scope={scope} agents={rows} view={view} onNavigate={navigate} onAsk={ask} shared={shared} artifacts={artifacts} routines={routineRows} onArtifact={artifact}/></WorkspaceContextCard>))
}
const click=async(text:string)=>{const button=[...host.querySelectorAll('button')].find(row=>row.textContent?.trim()===text||row.getAttribute('aria-label')?.includes(text)||row.textContent?.trim().startsWith(text));expect(button).toBeTruthy();await act(async()=>button!.click())}
afterEach(async()=>{await act(async()=>root.render(null));vi.clearAllMocks()})

it('shows admitted working people only, does not pin idle agents, and replaces live lines without inventing targets',async()=>{
 await render()
 const panel=host.querySelector('[aria-label="Team live panel"]')!
 expect(panel.querySelector('[aria-label="Open Team agent"]')).toBeNull()
 expect(panel.querySelector('[aria-label="Working now"]')?.querySelectorAll('button')).toHaveLength(2)
 expect(panel.querySelector('[role="group"][aria-label="Colleague"]')).toBeNull()
 const group=panel.querySelector('[aria-label="Working now"]')!
 expect(group.querySelectorAll('button')).toHaveLength(2)
 expect(group.querySelector('[data-theme-agent-state]')).toBeNull()
 expect(group.querySelector('[role="img"][aria-label="Claude Code"] img')?.getAttribute('src')).toBe('/provider-icons/claude.ico')
 expect(group.querySelector('[role="img"][aria-label="Codex"] img')?.getAttribute('src')).toBe('/provider-icons/codex.png')
 expect(group.querySelector('[role="img"][aria-label="running"]')).toBeTruthy()
 expect(group.querySelector('[role="img"][aria-label="waiting"]')).toBeTruthy()
 expect(group.textContent).not.toMatch(/Claude|codex|running|waiting/)
 for(const row of group.querySelectorAll('button')) {
  expect(row.classList.contains('w-full')).toBe(true)
  expect(row.classList.contains('pl-6')).toBe(false)
  expect(row.querySelector('.animate-spin')).toBeNull()
 }
 expect(group.textContent).not.toContain('typing…')
 expect(panel.querySelector('[aria-label="Open Colleague\'s Review"]')?.getAttribute('title')).toContain('Update needed')
 expect(panel.textContent).not.toContain('helper-build')
 expect(panel.querySelector('[aria-label="Open Colleague\'s Review"]')?.getAttribute('title')).toContain('AgentWorks helper-build')
 const hover=panel.querySelector('[aria-label="Open Colleague\'s Review"]')?.getAttribute('title')
 expect(hover).toContain('Review shared changes');expect(hover).toContain('Check a patch');expect(hover).toContain('Explain a failure')
 expect(panel.textContent).toContain('Shared doing line')
 expect(panel.textContent).not.toContain('Idle person')
 expect(panel.textContent).not.toContain('Away person')
 expect(panel.textContent).not.toContain('My private work')
 expect(panel.textContent).not.toContain('always online')
 await click('Review');expect(ask).toHaveBeenCalledWith(agents[1])
 await render('live',agents.map(row=>row.id==='peer'?{...row,typing:false,last_message:'Layout ready'}:row))
 expect(panel.textContent).not.toContain('Checking the layout')
 expect(panel.textContent).toContain('Layout ready')
 // Both short server CLI labels and engine IDs use the existing logo assets.
 for(const [cli,icon] of [['cursor-cli','cursor.svg'],['Muse','muse.ico'],['agy','antigravity.svg']]) {
  await render('live',[{...agents[1],cli}])
  expect(host.querySelector('[aria-label="Working now"] button img')?.getAttribute('src')).toBe(`/provider-icons/${icon}`)
 }
 await render('live',[{...agents[1],cli:'Other program'}])
 expect(host.querySelector('[role="img"][aria-label="Other program"] svg')).toBeTruthy()
 expect(host.textContent).not.toContain('Other program')
 await act(async()=>root.render(<TeamPanel scope="person" agents={agents} people={[{user_id:'person-1',name:'Colleague',active:true,can_remove:false}]} selectedPerson="person-1" onNavigate={navigate} onAsk={ask}/>))
 expect(host.querySelector('[aria-label="Colleague\'s agents"] button[title]')?.getAttribute('title')).toContain('Explain a failure')
})

it('opens searchable agent details through right-panel navigation, with status and the same admitted ask row',async()=>{
 await render('agents')
 expect(host.querySelector('[aria-label="Working now"]')).toBeNull()
 expect(host.textContent).toContain('Idle agent')
 expect(host.textContent).toContain('Offline agent')
 const input=host.querySelector('input')!
 await act(async()=>{Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value')!.set!.call(input,'colleague');input.dispatchEvent(new Event('input',{bubbles:true}))})
 expect(host.textContent).not.toContain('Idle agent')
 expect(host.textContent).toContain('server-model')
 expect(host.querySelector('[aria-label="Open Colleague\'s Review"]')?.getAttribute('title')).toContain('Review shared changes')
 await click('Measurements');expect(ask).toHaveBeenCalledWith(agents[2])
 await click('Team');expect(navigate).toHaveBeenLastCalledWith('live')
 await render('agents',agents,'server:other-person:crew');expect(input.value).toBe('')
})

it('opens items as separate right-panel views, never inline disclosures',async()=>{
 await render()
 expect(host.textContent).not.toContain('Team proposal')
 await render('items');expect(host.textContent).toContain('Morning summary');expect(host.textContent).toContain('Every morning')
 await click('Team proposal');expect(artifact).toHaveBeenCalledWith('team-copy')
 await render('shared');expect(host.textContent).toContain('My shared helper');expect(host.textContent).toContain('Answers only')
})

it('gives empty live sections no space and keeps a 35-row detail list in the panel scroll body',async()=>{
 await act(async()=>root.render(<TeamPanel scope="empty" agents={agents.filter(row=>row.team_agent)} onAsk={ask} onNavigate={navigate}/>))
 expect(host.querySelector('[aria-label="Working now"]')).toBeNull()
 expect(host.textContent).not.toContain('No teammates working now')
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

// Owner: Team agent is ordinary, not pinned; only its running work enters the live list.
it('shows the Team agent only while running and keeps its normal cloud row in Agents',async()=>{
 const home={...agents[5],place:'server'}
 await render('live',[home])
 expect(host.querySelector('[aria-label="Open Team agent"]')).toBeNull()
 await render('live',[agents[1],{...home,activity:'running',last_message:'Updating the Team'}])
 const list=host.querySelector('[aria-label="Working now"]')!
 const rows=list.querySelectorAll('button')
 expect(rows).toHaveLength(2)
 expect(rows[0].getAttribute('aria-label')).toContain('Review')
 expect(rows[1].textContent).toContain('Team agent');expect(rows[1].querySelector('[role="img"][aria-label="running"]')).toBeTruthy();expect(rows[1].textContent).toContain('Updating the Team')
 expect(rows[1].querySelector('[aria-label="Runs on the server, always available"]')).toBeTruthy()
 await act(async()=>rows[1].click());expect(ask).toHaveBeenLastCalledWith(expect.objectContaining({id:home.id,team_agent:true}))
 await render('live',[{...home,activity:'waiting'}])
 expect(host.querySelector('[aria-label="Open Team agent"]')).toBeNull()
 await render('agents',[home])
 const detail=host.querySelector('[aria-label="Open Team agent"]')!
 expect(detail.textContent).toContain('idle')
 expect(detail.querySelector('[aria-label="Runs on the server, always available"]')).toBeTruthy()
})

// The compact panel must not duplicate own working agents or clip its navigation.
it('keeps teammate identities and marks without a replacement chips row',async()=>{
 await render('live',[agents[0],{...agents[1],person:'Alex'},{...agents[2],person:'Blair',person_id:'person-2'},agents[5]])
 const working=host.querySelector('[aria-label="Working now"]')!
 const rows=working.querySelectorAll('button')
 expect(rows).toHaveLength(2)
 expect(rows[0].querySelector('[title="Alex"] [aria-hidden="true"]')?.textContent).toBe('A')
 expect(rows[1].querySelector('[title="Blair"] [aria-hidden="true"]')?.textContent).toBe('B')
 expect(rows[0].querySelector('[role="img"][aria-label="running"]')).toBeTruthy();expect(rows[1].querySelector('[role="img"][aria-label="waiting"]')).toBeTruthy()
 expect(working.querySelector('[data-theme-agent-state]')).toBeNull()
 expect(host.querySelector('nav[aria-label="Team details"]')).toBeNull()
 await render('live',[agents[0],agents[3],agents[5]])
 expect(host.querySelector('[aria-label="Working now"]')).toBeNull()
 expect(host.textContent).not.toContain('No teammates working now')
 expect(host.textContent).not.toContain('My private work')
})

it('uses admitted readiness for offline copy wording without changing the ask identity',async()=>{
 const peer={...agents[4],offline_copy_ready:true}
 await render('agents',[peer,agents[5]])
 expect(host.textContent).toContain('Its copy answers')
 await click('Offline agent');expect(ask).toHaveBeenLastCalledWith(peer)
 await render('agents',[{...peer,offline_copy_ready:false},agents[5]])
 expect(host.textContent).toContain('New asks go to the Team agent')
 await render('agents',[{...peer,offline_copy_ready:false}])
 expect(host.textContent).toContain('No Team agent available')
})

// #2801: selecting a current person is a local view, never a new audience or direct Send.
it('shows a selected person in place, working first and idle dimmed, and clears across scope or removal',async()=>{
 const selected={user_id:'person-1',name:'Colleague',active:true,can_remove:false}
 const rows=[{...agents[3],person:'Colleague',person_id:'person-1'}, {...agents[1],name:'worktracker'}]
 const select=vi.fn()
 await act(async()=>root.render(<TeamPanel scope="one" people={[selected]} selectedPerson="person-1" onPersonSelect={select} agents={rows} onAsk={ask} onNavigate={navigate} personAction={<button onClick={()=>ask(selected)}>✦ Ask my agent to ask Colleague's agents…</button>}/>))
 const list=host.querySelector('[aria-label="Colleague\'s agents"]')!
 const buttons=list.querySelectorAll('button[aria-label^="Open"]')
 expect(buttons[0].textContent).toContain('worktracker')
 expect(buttons[0].querySelector('.font-medium')?.classList.contains('shrink-0')).toBe(true)
 expect(buttons[1].classList.contains('opacity-50')).toBe(true)
 expect(list.querySelectorAll('button')).toHaveLength(4) // Back, two compact rows, one footer.
 expect(buttons[0].querySelector('[role="img"][aria-label="Claude Code"]')).not.toBeNull()
 expect(buttons[1].querySelector('[role="img"][aria-label="terminal"]')).not.toBeNull()
 expect(list.textContent).not.toContain('To talk to')
 await click("✦ Ask my agent to ask Colleague's agents…");expect(ask).toHaveBeenLastCalledWith(selected)
 await act(async()=>root.render(<TeamPanel scope="one" people={[selected]} selectedPerson="person-1" onPersonSelect={select} agents={[]} onAsk={ask} onNavigate={navigate}/>))
 expect(host.textContent).toContain("Colleague hasn't added an agent to this Team yet.")
 expect(host.querySelector('button[aria-label^="Open"]')).toBeNull()
 await click('Back to working agents');expect(select).toHaveBeenCalledWith(undefined)
 await act(async()=>root.render(<TeamPanel scope="new-account" people={[]} selectedPerson="person-1" agents={[]} onAsk={ask} onNavigate={navigate}/>))
 expect(host.textContent).not.toContain('Colleague')
 expect(host.querySelector('[aria-label="Working now"]')).toBeNull()
})

