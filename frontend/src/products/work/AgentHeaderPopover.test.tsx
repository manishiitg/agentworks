// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import { ShareAgentCard, ShareCrew } from './ShareCrew'
import { AgentHoverCard } from '../../components/chat/AgentCard'
import { fillOwnTabDraft } from '../../utils/agentPurposeDraft'
import { crewSharePage, teamRequest } from './teamApi'

vi.mock('../../utils/agentPurposeDraft',()=>({fillMainDraft:vi.fn(),fillOwnTabDraft:vi.fn(()=>true)}))
vi.mock('./AgentSharingRow',()=>({teamAgentChatTab:()=>({tabId:'exact-own-agent'})}))
const fixture = vi.hoisted(() => ({ row: { key: 'agent-1', mine: true, audience: 'whole_team', chosen_people: [], when_others_ask: 'Answer theme questions.', uses: [{ name: 'Project files', kind: 'files' }], can_edit_rule: true } }))
vi.mock('./teamApi', () => ({ useTeamTab: () => ({ target: { project: 'crew' }, view: { agents: [fixture.row], people: [] } }), teamRequest: vi.fn(async () => ({})), teamFailure: (e: Error) => ({ message: e.message }), crewSharePage: vi.fn() }))
vi.mock('./TeamTab', () => ({ Saving: ({ children }: any) => children, AgentDetails: () => <div>Project files</div>, TeamTab: ({agents}:any) => <div>{agents}</div>, LeaveTeam: () => null }))
vi.mock('./TeamAgentAsk', () => ({ AgentAskThread: () => <div>Online conversation</div> }))
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
afterEach(() => vi.clearAllMocks())

const agent: any = { key: 'agent-1', name: 'Reviewer', ask: { on: true, can_change: true, can_edit: true, can_make_changes: false, title: 'Teammates can ask this agent', summary:'SERVER CURRENT CHOICES', setup_ask:'Explain these saved choices, and offer changes.', uses_line:'Example app (1 connection)', changes_label: 'When they ask', changes_options: [{ value: false, label: 'Only answer',example:'Reads, checks and replies.',line:'A longer explanation.' }, { value: true, label: 'Answer and do the work' }], chat_share: 'agent', chat_share_label: 'Your chat', chat_share_options: [{ value: 'agent', label: 'Hidden' }, { value: '', label: 'Visible' }], line: 'Only you', hidden_line: 'Chat stays private.' } }

it('uses the same admitted agent-rule payloads in the header and right panel', async () => {
 const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
 try {
  for (const compact of [true, false]) {
   await act(async () => root.render(<ShareAgentCard key={String(compact)} agent={agent} target={{project:'crew'}} projectId="crew" compact={compact} />))
   expect(host.querySelector('[role="note"]')?.textContent).toBe('SERVER CURRENT CHOICES')
   expect(host.textContent).toContain('Example app (1 connection)')
   // #2792: each setting owns its label, control and optional example; no grid holes.
   for(const [section,label] of [['Work permission','When they ask'],['Asking audience','Who can ask'],['Chat visibility','Your chat']]) {
    const setting=host.querySelector(`section[aria-label="${section}"]`)!
    expect(setting).toBeTruthy()
    expect(setting.querySelector(`[role="radiogroup"][aria-label="${label}"]`)).toBeTruthy()
    expect(setting.querySelector('[role="radiogroup"]')?.classList.contains('flex-col')).toBe(true)
    for(const radio of setting.querySelectorAll('[role="radio"]')) expect(radio.classList.contains('whitespace-nowrap')).toBe(true)
   }
   expect(host.querySelector('section[aria-label="Teammate instructions"]')?.textContent).toContain('Answer theme questions.')
   await act(async()=>[...host.querySelectorAll('button')].find(button=>button.textContent==='✦ Ask AI to explain')!.click())
   expect(fillOwnTabDraft).toHaveBeenLastCalledWith('exact-own-agent','Explain these saved choices, and offer changes.')
   await act(async()=>{host.querySelector('summary')!.click()})
   expect(host.textContent).toContain('A longer explanation.')
   await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === 'Answer and do the work')!.click())
   expect(teamRequest).toHaveBeenLastCalledWith({ project: 'crew', connection: undefined }, '/agent-rule', 'POST', { agent_key: 'agent-1', can_make_changes: true })
   await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === 'Edit')!.click())
   expect(host.querySelector('textarea[aria-label="Your instructions for teammates"]')).not.toBeNull()
   await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === 'Save note')!.click())
   expect(teamRequest).toHaveBeenLastCalledWith({ project:'crew', connection:undefined }, '/agent-rule', 'POST', { agent_key:'agent-1', when_others_ask:'Answer theme questions.' })
  }
  await act(async () => root.render(<ShareAgentCard agent={{...agent,ask:{...agent.ask,can_change:false,fixed:'Keep one shared'}}} target={{project:'crew'}} projectId="crew" compact />))
  expect((host.querySelector('[role="switch"]') as HTMLButtonElement).disabled).toBe(true)
 } finally { await act(async () => root.unmount());host.remove() }
})

it('shows compact metadata and opens automation details without sending a turn', async () => {
 const host=document.createElement('div');document.body.append(host);const root=createRoot(host)
 const purpose=vi.fn(), publish=vi.fn(), add=vi.fn()
 try {
  await act(async () => root.render(<AgentHoverCard clickToOpen info={{name:'Reviewer',purpose:'Checks the UI.',onSetPurpose:purpose,onRunOnline:publish,onAddAutomation:add,setup:{reach:'',starts_on_own:true,online_copy:{state:'ready',synced_at:new Date().toISOString()},published:{state:'not_published'},next_automation:'Morning check in 1 h',automations:[{name:'Morning check',kind:'Schedule',line:'next in 1 h',on:true}]}}}><button>Agent details</button></AgentHoverCard>))
  await act(async () => host.querySelector('button')!.click())
  const dialog=document.querySelector('[role="dialog"]')!
  expect(dialog.textContent).not.toMatch(/Coding agent|Not available yet/)
  expect(dialog.textContent).toContain('Online copy answers')
  expect(dialog.textContent).toContain('Not published')
  await act(async () => [...dialog.querySelectorAll('button')].find(button => button.textContent === 'Open ›')!.click())
  expect(dialog.textContent).toContain('Morning check · Schedule')
  expect(add).not.toHaveBeenCalled()
  await act(async () => [...dialog.querySelectorAll('button')].find(button => button.textContent === 'Publish…')!.click())
  expect(publish).toHaveBeenCalledOnce()
 } finally { await act(async () => root.unmount());host.remove() }
})

// #2792: teammate discovery stays above the potentially long own sharing-card list.
it('lists teammates before own agents in the actual sharing page',async()=>{
 const host=document.createElement('div'),root=createRoot(host)
 vi.mocked(crewSharePage).mockResolvedValue({options:[],agents:[agent],shared_with_you:[{name:'Colleague',items:[{name:'Shared helper',key:'peer',line:'Shared',can:'Answers'}]}],who_can_use:'Team members'} as any)
 try {
  await act(async()=>{root.render(<ShareCrew projectId="crew"/>);await Promise.resolve();await Promise.resolve()})
  expect(host.textContent).toContain("Teammates' agents")
  expect(host.textContent!.indexOf("Teammates' agents")).toBeLessThan(host.textContent!.indexOf('Your agents'))
 } finally {await act(async()=>root.unmount())}
})
