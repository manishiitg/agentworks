// @vitest-environment happy-dom
import React, { act, useState } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import { ShareAgentCard, ShareCrew } from './ShareCrew'
import { AgentHoverCard } from '../../components/chat/AgentCard'
import { ForkToTeammate } from '../../components/chat/ForkToTeammate'
import { fillOwnTabDraft } from '../../utils/agentPurposeDraft'
import { crewSharePage, teamRequest } from './teamApi'

vi.mock('../../utils/agentPurposeDraft',()=>({fillMainDraft:vi.fn(),fillOwnTabDraft:vi.fn(()=>true)}))
vi.mock('./AgentSharingRow',()=>({teamAgentChatTab:()=>({tabId:'exact-own-agent'})}))
const fixture = vi.hoisted(() => ({ row: { key: 'agent-1', mine: true, audience: 'whole_team', chosen_people: [], when_others_ask: 'Answer theme questions.', uses: [{ name: 'Project files', kind: 'files' }], can_edit_rule: true } }))
vi.mock('./teamApi', () => ({ useTeamTab: () => ({ target: { project: 'crew' }, view: { team:{on:true}, agents: [fixture.row], people: [] } }), teamRequest: vi.fn(async () => ({})), teamFailure: (e: Error) => ({ message: e.message }), crewSharePage: vi.fn() }))
vi.mock('./TeamTab', () => ({ Saving: ({ children }: any) => children, AgentDetails: () => <div>Project files</div>, TeamTab: ({agents}:any) => <div>{agents}</div>, LeaveTeam: () => null }))
vi.mock('./TeamAgentAsk', () => ({ AgentAskThread: () => <div>Online conversation</div> }))
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
afterEach(() => vi.clearAllMocks())

const agent: any = { key: 'agent-1', name: 'Reviewer', ask: { on: true, can_change: true, can_edit: true, title: 'Teammates can ask this agent', summary:'SERVER CURRENT CHOICES', setup_ask:'Explain these saved choices, and offer changes.', uses_line:'Example app (1 connection)', chat_share: 'agent', chat_share_label: 'Your chat', chat_share_options: [{ value: 'agent', label: 'Hidden' }, { value: '', label: 'Visible' }], line: 'Only you', hidden_line: 'Chat stays private.' } }

it('uses the same admitted agent-rule payloads in the header and right panel', async () => {
 const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
 const openSharing = () => [...host.querySelectorAll<HTMLButtonElement>('button[aria-expanded]')].find(button => button.textContent?.startsWith('Sharing'))!
 try {
  for (const compact of [true, false]) {
   await act(async () => root.render(<ShareAgentCard key={String(compact)} agent={agent} target={{project:'crew'}} projectId="crew" compact={compact} />))
   // #3084: on the Team page the agent is one line; its settings open from the row.
   if(!compact) await act(async()=>host.querySelector<HTMLButtonElement>('#team-agent-card-agent-1 > div > button[aria-expanded]')!.click())
   expect(host.querySelector('[role="note"]')?.textContent).toBe('SERVER CURRENT CHOICES')
   if (compact) {
    expect(host.querySelector('[role="note"]')?.closest('[hidden]')).toBeNull()
    expect(host.querySelector('[role="switch"]')?.closest('[hidden]')).not.toBeNull()
    await act(async()=>openSharing().click())
    expect(host.querySelector('[role="switch"]')?.closest('[hidden]')).toBeNull()
   }
   expect(host.textContent).toContain('Example app (1 connection)')
   // #2792: each setting owns its label, control and optional example; no grid holes.
   for(const [section,label] of [['Asking audience','Who can ask'],['Chat visibility','Your chat']]) {
    const setting=host.querySelector(`section[aria-label="${section}"]`)!
    expect(setting).toBeTruthy()
    expect(setting.querySelector('[role="radiogroup"]')).toBeNull()
    await act(async()=>setting.querySelector<HTMLButtonElement>('button[aria-expanded]')!.click())
    expect(setting.querySelector(`[role="radiogroup"][aria-label="${label}"]`)).toBeTruthy()
   }
   expect(host.querySelector('section[aria-label="Teammate instructions"]')?.textContent).toContain('Answer theme questions.')
   await act(async()=>[...host.querySelectorAll('button')].find(button=>button.textContent==='✦ Ask AI to explain')!.click())
   expect(fillOwnTabDraft).toHaveBeenLastCalledWith('exact-own-agent','Explain these saved choices, and offer changes.')
   await act(async () => host.querySelector<HTMLButtonElement>('[role="radiogroup"][aria-label="Who can ask"] [role="radio"]:last-child')!.click())
   expect(teamRequest).toHaveBeenLastCalledWith({project:'crew',connection:undefined},'/agent-rule','POST',{agent_key:'agent-1',audience:'chosen',chosen_people:[]})
   await act(async () => host.querySelector<HTMLButtonElement>('[role="radiogroup"][aria-label="Your chat"] [role="radio"]:last-child')!.click())
   expect(teamRequest).toHaveBeenLastCalledWith({project:'crew',connection:undefined},'/agent-rule','POST',{agent_key:'agent-1',chat_share:''})
   await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === 'Edit')!.click())
   expect(host.querySelector('textarea[aria-label="Your instructions for teammates"]')).not.toBeNull()
   await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === 'Save note')!.click())
   expect(teamRequest).toHaveBeenLastCalledWith({ project:'crew', connection:undefined }, '/agent-rule', 'POST', { agent_key:'agent-1', when_others_ask:'Answer theme questions.' })
  }
  await act(async () => root.render(<ShareAgentCard agent={{...agent,ask:{...agent.ask,can_change:false,fixed:'Keep one shared'}}} target={{project:'crew'}} projectId="crew" compact />))
  expect((host.querySelector('[role="switch"]') as HTMLButtonElement).disabled).toBe(true)
  expect(host.querySelector('[role="note"]')?.textContent).toBe('SERVER CURRENT CHOICES')
  await act(async () => root.render(<ShareAgentCard key="off" agent={{...agent,ask:{...agent.ask,on:false}}} target={{project:'crew'}} projectId="crew" compact />))
  await act(async () => openSharing().click())
  await act(async () => host.querySelector<HTMLButtonElement>('section[aria-label="Asking audience"] button[aria-expanded]')!.click())
  for(const button of host.querySelectorAll<HTMLButtonElement>('[role="radio"]')) expect(button.disabled).toBe(true)
 } finally { await act(async () => root.unmount());host.remove() }
})

it('shows compact metadata and opens automation details without sending a turn', async () => {
 const host=document.createElement('div');document.body.append(host);const root=createRoot(host)
 const purpose=vi.fn(), add=vi.fn()
 try {
  await act(async () => root.render(<AgentHoverCard clickToOpen info={{name:'Reviewer',purpose:'Checks the UI.',examples:['Review a layout','Check keyboard access','Find a regression'],onSetPurpose:purpose,onAddAutomation:add,setup:{reach:'',starts_on_own:true,when_offline:{line:'Teammates can wait or read this chat · synced 2m ago'},next_automation:'Morning check in 1 h',automations:[{name:'Morning check',kind:'Schedule',line:'next in 1 h',on:true}]}}}><button>Agent details</button></AgentHoverCard>))
  await act(async () => host.querySelector('button')!.click())
  const dialog=document.querySelector('[role="dialog"]')!
  expect(dialog.textContent).not.toMatch(/Coding agent|Not available yet/)
  expect(dialog.textContent).toContain('Ask me to: Review a layout · Check keyboard access · Find a regression')
  expect(dialog.textContent).toContain('Teammates can wait or read this chat')
  expect(dialog.textContent).not.toMatch(/Publish|Run online/)
  const controls = [...dialog.querySelectorAll('button[aria-controls]')]
  const setup = controls.find(button => button.textContent?.startsWith('Setup'))!
  const automation = controls.find(button => button.textContent?.startsWith('Automations'))!
  expect(setup.getAttribute('aria-expanded')).toBe('false')
  expect(automation.getAttribute('aria-expanded')).toBe('false')
  expect(dialog.querySelectorAll('[hidden]')).toHaveLength(2)
  await act(async () => (automation as HTMLButtonElement).click())
  expect(dialog.textContent).toContain('Morning check · Schedule')
  expect(add).not.toHaveBeenCalled()
 } finally { await act(async () => root.unmount());host.remove() }
})

// #2792: teammate discovery stays above the potentially long own sharing-card list.
it('lists teammates before own agents in the actual sharing page',async()=>{
 const host=document.createElement('div'),root=createRoot(host)
 vi.mocked(crewSharePage).mockResolvedValue({options:[{key:'team',title:'Team',on:true}],actions_group:{hidden:true,rows:[]},agents:[agent],shared_with_you:[{name:'Colleague',items:[{name:'Shared helper',key:'peer',line:'Shared',can:'Answers'}]}],who_can_use:'Team members'} as any)
 try {
  await act(async()=>{root.render(<ShareCrew projectId="crew"/>);await Promise.resolve();await Promise.resolve()})
  expect(host.textContent).toContain("Teammates' agents")
  expect(host.textContent!.indexOf("Teammates' agents")).toBeLessThan(host.textContent!.indexOf('Your agents'))
 } finally {await act(async()=>root.unmount())}
})

// #2925: opening the copy entry never sends; explicit confirmation uses the existing local copy path.
it('opens an owned agent copy confirmation and sends only to the selected teammate',async()=>{
 const host=document.createElement('div');document.body.append(host);const root=createRoot(host)
 const state:any={target:{project:'server-crew',localProject:'local-crew'},view:{people:[{user_id:'self',name:'Owner',you:true},{user_id:'peer',name:'Teammate'},{user_id:'pending',name:'Invited',note:'invited'}]}}
 function Harness(){
  const [copy,setCopy]=useState(false)
  return <><AgentHoverCard clickToOpen info={{name:'Helper',purpose:'Checks the project.'}} actions={{forkTo:()=>setCopy(true)}}><button>Agent details</button></AgentHoverCard>
   {copy&&<ForkToTeammate state={state} agentId="own-agent" name="Helper" close={()=>setCopy(false)}/>}</>
 }
 try {
  await act(async()=>root.render(<Harness/>))
  await act(async()=>host.querySelector('button')!.click())
  const popover=document.querySelector('[role="dialog"]')!
  await act(async()=>[...popover.querySelectorAll('button')].find(button=>button.textContent==='Send a copy to a teammate…')!.click())
  const dialog=document.querySelector('[role="dialog"]')!
  expect(dialog.textContent).toContain('Send a copy of Helper')
  expect(teamRequest).not.toHaveBeenCalled()
  const select=dialog.querySelector('select')!
  expect([...select.options].map(option=>option.value)).toEqual(['','peer'])
  expect([...dialog.querySelectorAll('button')].find(button=>button.textContent==='Send copy')!.disabled).toBe(true)
  await act(async()=>{select.value='peer';select.dispatchEvent(new Event('change',{bubbles:true}))})
  await act(async()=>[...dialog.querySelectorAll('button')].find(button=>button.textContent==='Send copy')!.click())
  expect(teamRequest).toHaveBeenCalledOnce()
  expect(teamRequest).toHaveBeenCalledWith({project:'local-crew'},'/agent-fork-local','POST',{operation:'send',agent_id:'own-agent',recipient_id:'peer'})
  expect(dialog.textContent).toContain('Your agent keeps running unchanged.')
  await act(async()=>[...dialog.querySelectorAll('button')].find(button=>button.textContent==='Done')!.click())
  expect(document.querySelector('[role="dialog"]')).toBeNull()
 } finally {await act(async()=>root.unmount());host.remove()}
})
