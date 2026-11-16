// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import { ShareAgentCard } from './ShareCrew'
import { AgentHoverCard } from '../../components/chat/AgentCard'
import { teamRequest } from './teamApi'

const fixture = vi.hoisted(() => ({ row: { key: 'agent-1', mine: true, audience: 'whole_team', chosen_people: [], when_others_ask: 'Answer theme questions.', uses: [{ name: 'Project files', kind: 'files' }], can_edit_rule: true } }))
vi.mock('./teamApi', () => ({ useTeamTab: () => ({ target: { project: 'crew' }, view: { agents: [fixture.row], people: [] } }), teamRequest: vi.fn(async () => ({})), teamFailure: (e: Error) => ({ message: e.message }), crewSharePage: vi.fn() }))
vi.mock('./TeamTab', () => ({ Saving: ({ children }: any) => children, AgentDetails: () => <div>Project files</div>, TeamTab: () => null, LeaveTeam: () => null }))
vi.mock('./TeamAgentAsk', () => ({ AgentAskThread: () => <div>Online conversation</div> }))
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
afterEach(() => vi.clearAllMocks())

const agent: any = { key: 'agent-1', name: 'Reviewer', ask: { on: true, can_change: true, can_edit: true, can_make_changes: false, title: 'Shared with team', changes_label: 'When they ask', changes_options: [{ value: false, label: 'Answers only' }, { value: true, label: 'Make changes' }], chat_share: 'agent', chat_share_label: 'Your chat', chat_share_options: [{ value: 'agent', label: 'Hidden' }, { value: '', label: 'Visible' }], line: 'Only you', hidden_line: 'Chat stays private.' } }

it('uses the same admitted agent-rule payloads in the header and right panel', async () => {
 const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
 try {
  for (const compact of [true, false]) {
   await act(async () => root.render(<ShareAgentCard key={String(compact)} agent={agent} target={{project:'crew'}} projectId="crew" compact={compact} />))
   await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === 'Make changes')!.click())
   expect(teamRequest).toHaveBeenLastCalledWith({ project: 'crew', connection: undefined }, '/agent-rule', 'POST', { agent_key: 'agent-1', can_make_changes: true })
   await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === 'Edit')!.click())
   expect(host.querySelector('textarea[aria-label="Your note"]')).not.toBeNull()
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
