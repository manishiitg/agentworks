// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import { TeamTab } from './TeamTab'
import { TeamActivity } from './TeamActivity'
import { useChatStore } from '../../stores/useChatStore'
import { useAuthStore } from '../../stores/useAuthStore'
import { openCrewAccess } from './MacSharedAgents'
import api from '../../services/api'
import { crewPeople, changeCrewPerson, teamRequest } from './teamApi'

const setupState=vi.hoisted(()=>({data:{setups:[] as any[],to_do:0},version:1,error:''}))
vi.mock('./useTeamSetupsList',()=>({useTeamSetupsList:()=>setupState}))
vi.mock('./teamApi', () => ({
  useTeamTab: (project: string) => ({ target: { project }, view: {
    team: { on: project !== 'off', folder: { folders: [] } }, people: [], intro: '',
  } }),
  crewPeople: vi.fn(() => new Promise(() => {})),
  changeCrewPerson: vi.fn(),
  teamRequest: vi.fn(),
  teamFailure: (error: Error) => ({ message: error.message }),
  teamKit: vi.fn(() => new Promise(() => {})),
  changeTeamWay: vi.fn(),
}))
vi.mock('./CrewWorksElsewhere', () => ({ CrewWhereRow: () => null }))
vi.mock('./TeamWay', () => ({ TeamRoutinesCard: () => null }))
vi.mock('../../services/liveFeed', () => ({ liveFeed: { subscribe: () => () => {}, getStatus:()=> 'live', onStatus:()=>()=>{} } }))
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

// PLAT-746-883: activity must be discoverable separately from setup, and a failed read must not disappear.
it('opens activity, shows lookup failure with retry, and separates People, Agents, Activity and Sync', async () => {
  vi.mocked(teamRequest).mockRejectedValueOnce(new Error('Unavailable')).mockResolvedValue({ feed: [], next_cursor: 'retained-item' })
  const host = document.createElement('div')
  document.body.append(host)
  const root = createRoot(host)
  const tab = (name: string) => [...host.querySelectorAll<HTMLButtonElement>('[role="tab"]')].find(button => button.textContent?.startsWith(name))!
  try {
    await act(async () => root.render(<TeamTab projectId="one" />))
    expect(tab('Tasks').getAttribute('aria-selected')).toBe('true')
    expect(host.querySelector('[role="alert"]')).not.toBeNull()
    expect(host.querySelector('section[aria-label="People"]')).toBeNull()
    await act(async () => host.querySelector<HTMLButtonElement>('[role="alert"] button')!.click())
    expect(host.querySelector('[role="alert"]')).toBeNull()
    expect(host.querySelector('section[aria-label="Tasks"]')).not.toBeNull()
    // A retained item can disappear before Show older is clicked: explain the terminal empty page.
    vi.mocked(teamRequest).mockResolvedValueOnce({ feed: [] })
    await act(async () => [...host.querySelectorAll<HTMLButtonElement>('button')].find(button => button.textContent === 'Show older')!.click())
    expect(host.textContent).toContain('No older activity')
    await act(async () => tab('Agents').click())
    expect(host.querySelector('section[aria-label="Agents"]')).not.toBeNull()
    await act(async () => tab('People').click())
    expect(host.querySelector('section[aria-label="Tasks"]')).toBeNull()
    expect(host.querySelector('section[aria-label="People"]')).not.toBeNull()
    await act(async () => openCrewAccess('one', '', 'team', 'activity'))
    expect(tab('Tasks').getAttribute('aria-selected')).toBe('true')
    await act(async () => openCrewAccess('one', '', 'team', 'sync'))
    expect(tab('Sync').getAttribute('aria-selected')).toBe('true')
    await act(async () => tab('Sync').dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowLeft', bubbles: true })))
    expect(document.activeElement).toBe(tab('Tasks'))
    await act(async () => root.render(<TeamTab projectId="off" />))
    expect(host.querySelector('[role="tablist"]')).toBeNull()
    expect(host.querySelector('section[aria-label="People"]')).not.toBeNull()
  } finally { await act(async () => root.unmount()); host.remove() }
})

// #2817: line-only readers get no thread request or native-chat navigation.
it('opens a bridge thread only when the feed admits an endpoint owner',async()=>{
 const host=document.createElement('div'),root=createRoot(host)
 const data=(can_open:boolean)=>({feed:[{id:'message:one',kind:'message',at:new Date().toISOString(),person:'Colleague',title:'Reviewer → Builder',verb:'',line:'A shared preview',bridge:{thread_id:'inbox-shared',can_open}}]})
 vi.mocked(teamRequest).mockImplementation(async(_target,suffix,method)=>method==='POST'?{seen:true}:suffix?.includes('thread=')?{messages:[{id:'one',at:new Date().toISOString(),agent:'Reviewer',text:'Owned bridge thread text'}]}:data(true))
 try {
  await act(async()=>root.render(<TeamActivity projectId="local-project" target={{project:'server-project'}}/>))
  await act(async()=>host.querySelector<HTMLButtonElement>('button[aria-expanded]')!.click())
  expect(host.textContent).toContain('Owned bridge thread text')
  expect(teamRequest).toHaveBeenCalledWith({project:'server-project'},'/activity?thread=inbox-shared')
  vi.mocked(teamRequest).mockImplementation(async(_target,_suffix,method)=>method==='POST'?{seen:true}:data(false))
  await act(async()=>root.render(<TeamActivity key="line-only" projectId="local-project" target={{project:'server-project'}}/>))
  expect(host.textContent).toContain('A shared preview')
  expect(host.textContent).not.toContain('Owned bridge thread text')
  expect(host.querySelector('button[aria-expanded]')).toBeNull()
 }finally {await act(async()=>root.unmount())}
})

// #2965: one server task list, shown by issue (sub-tasks under their parent) and by agent.
it('shows tasks under their issue and by agent',async()=>{
 const host=document.createElement('div'),root=createRoot(host),at=new Date().toISOString()
 const tasks=[{id:'msg-1',group:'#10',group_title:'Ship the release',title:'Ship the release',state:'in_progress',agent:'Builder',agent_key:'a1',person:'Colleague',at},{id:'msg-2',parent:'msg-1',group:'#10',group_title:'Ship the release',title:'Check the notes',state:'done',agent:'Reviewer',agent_key:'a2',person:'Colleague',proof:'PR 12',at}]
 vi.mocked(teamRequest).mockImplementation(async(_target,_suffix,method)=>method==='POST'?{seen:true}:{feed:[],tasks})
 try {
  await act(async()=>root.render(<TeamActivity projectId="local-project" target={{project:'server-project'}}/>))
  expect(host.textContent).toContain('1 of 2 done')
  expect(host.textContent).toContain('Check the notes')
  expect(host.textContent).toContain('PR 12')
  await act(async()=>[...host.querySelectorAll<HTMLButtonElement>('[role="radio"]')].find(b=>b.textContent==='By agent')!.click())
  expect(host.textContent).toContain("Colleague's Builder")
  expect(host.textContent).toContain('doing: Ship the release')
 }finally {await act(async()=>root.unmount())}
})

// Activity contains permission-filtered text: a different account must not see the previous answer while its own read waits.
it('clears permission-filtered activity immediately when the account changes', async () => {
  const previous = useAuthStore.getState().user
  useAuthStore.setState({ user: { id: 'first-person' } as never })
  const host = document.createElement('div'), root = createRoot(host)
  vi.mocked(teamRequest).mockResolvedValueOnce({ feed: [{ id: 'question:private', kind: 'question', at: new Date().toISOString(), person: 'Example', title: 'Research', verb: 'asked Main', line: 'Visible only to the first account', message: { id: 'private', question: 'Visible only to the first account', from: {}, to: {} } }] } as never).mockImplementation(() => new Promise(() => {}))
  try {
    await act(async () => root.render(<TeamActivity projectId="same-project" target={{ project: 'same-project' }} />))
    expect(host.textContent).toContain('Visible only to the first account')
    await act(async () => useAuthStore.setState({ user: { id: 'second-person' } as never }))
    expect(host.textContent).not.toContain('Visible only to the first account')
    expect(host.textContent).toContain('Loading Team tasks')
  } finally { await act(async () => root.unmount()); useAuthStore.setState({ user: previous }) }
})

// The compact link must select the one existing Team strip, not an in-card page.
// Owner, 2026-11-17/18: the Kit tab (formerly Todos) stays with no setup steps listed (#3141).
it('deep-links into the Kit with progress and keeps the segment when the server lists none',async()=>{
 setupState.data={to_do:1,setups:[{id:'setup',title:'Example setup',revision:1,instruction_revision:1,instructions_available:true,done:1,total:2,for_you:true,assigned_to:[],people:[{person_id:'peer',name:'Colleague',done:2,total:2}]}]}
 const host=document.createElement('div');document.body.append(host);const root=createRoot(host)
 try {
  await act(async()=>{openCrewAccess('setups-project','','team','setups');root.render(<TeamTab projectId="setups-project"/>)})
  const tabs=()=>[...host.querySelectorAll<HTMLButtonElement>('[role="tab"]')]
  expect(tabs().map(tab=>tab.textContent?.replace(/\s+/g,' ').trim())).toEqual(['People','Agents','Tasks','Sync','Kit1'])
  const setup=()=>tabs().find(tab=>tab.textContent?.startsWith('Kit'))!
  expect(setup().getAttribute('aria-selected')).toBe('true')
  expect(host.textContent).toContain('Example setup');expect(host.textContent).toContain('you: 1 of 2');expect(host.textContent).toContain('Colleague✓')
  await act(async()=>setup().dispatchEvent(new KeyboardEvent('keydown',{key:'ArrowRight',bubbles:true})))
  expect(document.activeElement).toBe(tabs()[0])
  setupState.data={setups:[],to_do:0}
  await act(async()=>root.render(<TeamTab projectId="setups-project"/>))
  expect(tabs().some(tab=>tab.textContent?.startsWith('Kit'))).toBe(true)
 } finally {setupState.data={setups:[],to_do:0};await act(async()=>root.unmount());host.remove()}
})

// #2812: exercise Add a person -> server preview -> the body-portal confirmation.
it('bounds the add-teammate confirmation independently of utility sizing and preserves Cancel and Add',async()=>{
 const host=document.createElement('div');document.body.append(host);const root=createRoot(host)
 const directory=vi.spyOn(api,'get').mockResolvedValue({data:{users:[{id:'colleague',username:'Colleague'}]}})
 vi.mocked(crewPeople).mockResolvedValue({kind:'server',team_on:true,intro:'',can_add:true,can_share:true,owner:{user_id:'owner',name:'Owner',level:'owner',level_label:'Owner',owner:true},people:[],dashboard_people:[],levels:[],gets:[]})
 const preview={person:'colleague',from:'none',to:'works',summary:'Colleague will be added to Works with me.',gains:['Shared files','Shared agents'],loses:[],notes:['They join when they accept.']}
 vi.mocked(changeCrewPerson).mockResolvedValue({confirm_needed:true,preview})
 const click=async(label:string)=>{await act(async()=>{const button=[...document.querySelectorAll<HTMLButtonElement>('button')].find(button=>button.textContent?.trim().endsWith(label));expect(button,`reachable ${label}`).toBeTruthy();button!.click();await Promise.resolve();await Promise.resolve()})}
 try {
  await act(async()=>{openCrewAccess('dialog-project','','team','people');root.render(<TeamTab projectId="dialog-project"/>);await Promise.resolve()})
  await click('+ Add a person');await click('Colleague')
  const dialog=document.querySelector<HTMLElement>('[role="dialog"]')!
  expect(dialog).toBeTruthy();expect(host.contains(dialog)).toBe(false)
  expect(dialog.style.width).toBe('28rem')
  expect(dialog.style.maxWidth).toBe('calc(100vw - 2rem)')
  expect(dialog.style.maxHeight).toBe('calc(100vh - 2rem)')
  expect(dialog.parentElement?.classList.contains('justify-center')).toBe(true)
  expect(dialog.textContent).toContain('Shared files');expect(dialog.textContent).toContain('They join when they accept.')
  expect(changeCrewPerson).toHaveBeenCalledTimes(1)
  await click('Cancel');expect(document.querySelector('[role="dialog"]')).toBeNull()
  await click('+ Add a person');await click('Colleague')
  vi.mocked(changeCrewPerson).mockResolvedValue({changed:true})
  await click('Add')
  expect(changeCrewPerson).toHaveBeenLastCalledWith({project:'dialog-project'}, {person:'colleague',level:'works',confirm:true,create_dashboard:false})
  expect(document.querySelector('[role="dialog"]')).toBeNull()
 } finally {await act(async()=>root.unmount());host.remove();directory.mockRestore();vi.mocked(crewPeople).mockImplementation(()=>new Promise(()=>{}));vi.mocked(changeCrewPerson).mockReset()}
})
