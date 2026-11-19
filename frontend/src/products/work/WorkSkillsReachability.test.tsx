// @vitest-environment happy-dom
import React, { act, useState } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import { BookOpen } from 'lucide-react'
import { WorkContextPanel } from './WorkContextPanel'
import { WorkSkillsPanel } from './WorkSkillsPanel'
import { PinnedCrewOrderMenu } from '../../components/workspace/PinnedCrewOrderMenu'
import { ProjectAgentTabs } from '../../components/chat/ProjectAgentTabs'
import { useCrewAgentsStore, useOpenAgentStore } from '../../components/chat/AgentCard'
import DiscoverSkillsDialog from '../../components/skills/DiscoverSkillsDialog'
import { crewProjectMenuItems } from './crewProjectMenu'
import type { ProductPanel } from '../productPanels'
import type { WorkWorkspaceView } from './WorkWorkspacePane'

const f=vi.hoisted(()=>({ discover:vi.fn(), importDiscovered:vi.fn(), opened:vi.fn(), importChat:vi.fn(), target:vi.fn(), user:{id:'owner',allowed_products:['work']}, server:'local', needsCount:0, sidebar:null as object|null, team:{view:undefined} as any, state:{activeTabId:null,chatTabs:{},addToast:vi.fn()} }))
vi.mock('../../api/skills',()=>({skillsApi:{discover:f.discover,importDiscovered:f.importDiscovered}}))
vi.mock('../../stores/useAuthStore',()=>({useAuthStore:Object.assign((select:(state:unknown)=>unknown)=>select({user:f.user}),{getState:()=>({user:f.user})})}))
vi.mock('../../stores/useWorkspaceConnectionStore',async importOriginal=>({...await importOriginal<typeof import('../../stores/useWorkspaceConnectionStore')>(),useWorkspaceConnectionStore:Object.assign((select:(state:unknown)=>unknown)=>select({activeWorkspaceId:f.server}),{getState:()=>({activeWorkspaceId:f.server})})}))
vi.mock('../../stores/useChatStore',async importOriginal=>({...await importOriginal<typeof import('../../stores/useChatStore')>(),useChatStore:Object.assign((select:(state:unknown)=>unknown)=>select(f.state),{getState:()=>f.state})}))
vi.mock('../../stores/useCapabilitiesStore',()=>({useCapabilitiesStore:Object.assign((select:(state:unknown)=>unknown)=>select({capabilities:{local_mode:true}}),{getState:()=>({capabilities:{local_mode:true}})})}))
vi.mock('../../stores/useProductNavigationToolbarStore',()=>({useProductNavigationToolbarStore:(select:(state:unknown)=>unknown)=>select({sidebarSettingsTarget:f.sidebar})}))
vi.mock('../../hooks/useWorkspaceViewTarget',()=>({setWorkspaceViewTarget:f.target}))
vi.mock('./useCrewPanel',()=>({useCrewPanel:()=>({data:{sections:[{id:'connections',rows:[]}],details:{}},refresh:()=>{}})}))
vi.mock('./ActivitySection',()=>({CrewActivitySection:({onNeedsCount}:any)=>{React.useEffect(()=>{onNeedsCount?.(f.needsCount)},[onNeedsCount,f.needsCount]);return null}}))
vi.mock('./CrewTeamPanel',()=>({CrewTeamPanel:({panelView='live',onPanelView,collapsed}:any)=>{return collapsed?null:<section aria-label="Team pane">{panelView==='live'?<button onClick={()=>onPanelView('agents')}>All agents</button>:<><button onClick={()=>onPanelView('live')}>Back to Team</button><h2>All Team agents</h2></>}</section>}}))
vi.mock('./YourComputerPanel',()=>({cloudStateIcon:()=>BookOpen,useYourComputerSummary:()=>undefined,useCrewLivesOnMac:()=>false,useCrewAsking:()=>[],openCrewAccess:()=>{},openCrewAskThread:()=>{}}))
vi.mock('./teamApi',()=>({useTeamTab:()=>f.team,teamAgentForTab:()=>undefined,agentSetupForTab:()=>undefined}))
vi.mock('../../components/chat/TerminalResume',()=>({continueInTerminal:()=>{}}))
vi.mock('../../components/chat/AgentWorksChatTabItem',()=>({AgentWorksChatTabItem:()=>null}))
vi.mock('../../components/workspace/SidebarCrewAgents',()=>({SidebarCrewAgents:()=>null}))
vi.mock('../../hooks/useMobileViewport',()=>({useMobileViewport:()=>false}))
vi.mock('../../components/workspace/ChatChangesRow',()=>({ChatChangesRow:()=>null}))
vi.mock('../../components/skills/SkillsManagerPanel',()=>({default:({headerAction}:{headerAction:React.ReactNode})=><section aria-label="Installed skills">{headerAction}</section>}))
vi.mock('../../components/skills/SkillImportDialog',()=>({default:()=>null}))
vi.mock('./projectProduct',()=>({useProjectProduct:()=>({profileId:'work',profileVersion:1,noun:'Crew'})}))
const panels:ProductPanel<WorkWorkspaceView>[]=[{id:'memory',label:'Memory',icon:BookOpen} as ProductPanel<WorkWorkspaceView>]
const host=document.createElement('div');document.body.append(host);const root=createRoot(host)
Object.assign(globalThis,{IS_REACT_ACT_ENVIRONMENT:true})
afterEach(()=>{act(()=>root.render(null));f.team={view:undefined};f.server='local';f.needsCount=0;f.sidebar=null;Object.assign(f.state,{activeTabId:null,chatTabs:{}});vi.clearAllMocks()})
const click=async(text:string)=>{const button=[...document.querySelectorAll('button')].find(item=>item.textContent?.trim()===text);expect(button,`reachable ${text}`).toBeTruthy();await act(async()=>button!.click())}
function CrewActions() {
 const crew=useCrewAgentsStore()
 const dispatch=(kind:'chat'|'skills')=>()=>window.dispatchEvent(new CustomEvent('crew-agent-import',{detail:{projectId:'example',kind}}))
 return <PinnedCrewOrderMenu name="Example" onNewAgent={crew.canAdd?()=>window.dispatchEvent(new CustomEvent('crew-new-agent',{detail:{projectId:'example'}})):undefined}
  onImportLocalChat={crew.canImportChat?dispatch('chat'):undefined} onDiscoverSkills={crew.canDiscoverSkills?dispatch('skills'):undefined}/>
}
const openCrewActions=async()=>await act(async()=>document.querySelector<HTMLButtonElement>('button[aria-label="Actions for Example"]')!.click())
function Harness({entry,canDiscover=true}:{entry:'header'|'integrations';canDiscover?:boolean}) {
 const [discover,setDiscover]=useState(false);const [view,setView]=useState('')
 return <>{entry==='header'?<><ProjectAgentTabs tabs={[]} listed={[]} canonicalTabId="" projectName="Example" projectId="example" allowSideChats={canDiscover} opening={false} surface="work" onOrder={async()=>{}} onRename={async()=>{}} onOpen={f.opened} onCloseTab={()=>{}} onSelectTab={()=>{}} onImportLocalChat={canDiscover?f.importChat:undefined} onDiscoverSkills={canDiscover?()=>setDiscover(true):undefined}/><CrewActions/></>:view==='memory'?<WorkSkillsPanel workspacePath="Crew/example" projectId="example" tabId="" onSelectedSkillsChange={async()=>{}}/>:<WorkContextPanel workspacePath="Crew/example" projectId="example" panels={canDiscover?panels:[]} enabledPanels={new Set(canDiscover?['skills']:[])} onOpen={next=>setView(next)}/>}{discover&&<DiscoverSkillsDialog workspacePath="Crew/example" close={()=>setDiscover(false)}/>}</>
}

// PLAT-746-1008: exercise the real entry components, not just an already-open
// dialog. Skills navigation/import must neither create an agent nor copy files.
it.each(['header','integrations'] as const)('opens the existing project discovery picker from %s',async entry=>{
 f.discover.mockResolvedValue({skills:[],message:'Available source skills'})
 await act(async()=>root.render(<Harness entry={entry}/>))
 if(entry==='header') {
  await openCrewActions()
  await click('Discover & import skills…')
 } else {
  await click('Connections');await click('Manage integrations');await click('Skills')
  expect(f.target).toHaveBeenCalledWith('memory','skills')
  await click('Discover & import…')
 }
 expect(document.querySelector('[role="dialog"]')).toBeTruthy()
 expect(f.discover).toHaveBeenCalledWith('Crew/example','claude-code')
 expect(f.importDiscovered).not.toHaveBeenCalled();expect(f.opened).not.toHaveBeenCalled()
})
it('opens local chat import from Crew actions only for this project', async()=>{
 await act(async()=>root.render(<Harness entry="header"/>))
 await act(async()=>window.dispatchEvent(new CustomEvent('crew-agent-import',{detail:{projectId:'other',kind:'chat'}})))
 expect(f.importChat).not.toHaveBeenCalled()
 await openCrewActions()
 await click('Import local chat…')
 expect(f.importChat).toHaveBeenCalledOnce()
 expect(f.opened).not.toHaveBeenCalled()
})

it('retains the caller-provided own-only action and feature-filtered Skills destination',async()=>{
 await act(async()=>root.render(<Harness entry="header" canDiscover={false}/>))
 await openCrewActions()
 expect(document.body.textContent).not.toContain('Discover & import skills')
 expect(crewProjectMenuItems({panels,enabledPanels:new Set()}).some(item=>item.id==='memory:skills')).toBe(false)
})

it('replaces the right-panel body for Team detail links and resets the view across account/server scopes',async()=>{
 f.team={view:{team:{on:true}}}
 const menu=[...panels,{id:'mcp',label:'Access',icon:BookOpen} as ProductPanel<WorkWorkspaceView>]
 const page=()=> <WorkContextPanel workspacePath="Crew/example" projectId="example" panels={menu} onOpen={f.opened}/>
 await act(async()=>root.render(page()))
 expect(host.textContent).toContain('Full menu')
 await click('All agents')
 expect(host.textContent).toContain('All Team agents')
 expect(host.textContent).not.toContain('Full menu')
 await click('Back to Team')
 expect(host.textContent).toContain('Full menu')
 await click('All agents');f.server='another-server'
 await act(async()=>root.render(page()))
 expect(host.textContent).not.toContain('All Team agents')
 expect(host.textContent).toContain('Full menu')
})

// #2798: closing the sidebar must not remove the active agent's details entry.
it('keeps the active agent name clickable in both sidebar states even after another Crew projects its stores',async()=>{
 const tab:any={tabId:'own-helper',name:'Helper',metadata:{agentProfileId:'work',agentProfileProjectId:'example',agentProfileConversationKey:'example:chat:helper'}}
 const listed:any={conversation_key:'example:chat:helper',name:'Helper',purpose:'Checks the project.',can_write:true,pinned:true}
 Object.assign(f.state,{activeTabId:tab.tabId,chatTabs:{[tab.tabId]:tab}})
 for(const sidebar of [null,{},null]) {
  f.sidebar=sidebar
  await act(async()=>root.render(<ProjectAgentTabs tabs={[tab]} listed={[listed]} canonicalTabId="main" projectName="Example" projectId="example" allowSideChats opening={false} surface="work" onOrder={async()=>{}} onRename={async()=>{}} onOpen={f.opened} onCloseTab={()=>{}} onSelectTab={()=>{}}/>))
  // #2912: an inactive mounted Crew can replace these shared projections.
  await act(async()=>{
   useCrewAgentsStore.getState().set('other','Other',[],false)
   useOpenAgentStore.getState().set(null,null)
  })
  const header=host.querySelector('[data-agent-header]')!
  expect(header.textContent).toContain('Example')
  expect(host.querySelector('[data-testid="work-chat-tab-strip"]')).toBeNull()
  expect(host.querySelector('[data-testid="work-more-tabs"]')).toBeNull()
  const details=header.querySelector<HTMLButtonElement>('button[aria-label="About Helper"]')
  expect(details).toBeTruthy()
  await act(async()=>details!.click())
  const dialog=document.querySelector('[role="dialog"]')!
  expect(dialog.textContent).toContain('Checks the project.')
  await act(async()=>document.dispatchEvent(new KeyboardEvent('keydown',{key:'Escape',bubbles:true})))
  expect(document.querySelector('[role="dialog"]')).toBeNull()
 }
 expect(f.opened).not.toHaveBeenCalled()
})

// #2812: New/Add/Fork share one body-portal shell; none may expand to the window.
it('bounds and centers the agent creation shell across its modes without changing create or cancel',async()=>{
 await act(async()=>root.render(<Harness entry="header"/>))
 const bounded=(name:string)=>{
  const dialog=document.querySelector<HTMLDivElement>(`[role="dialog"][aria-label="${name}"]`)!
  expect(dialog).toBeTruthy()
  expect(dialog.style.width).toBe('28rem')
  expect(dialog.style.maxWidth).toBe('calc(100vw - 2rem)')
  expect(dialog.style.maxHeight).toBe('calc(100vh - 2rem)')
  expect(dialog.classList.contains('overflow-y-auto')).toBe(true)
  expect(dialog.dataset.themeSurface).toBe('dialog')
  expect(dialog.parentElement?.classList.contains('items-center')).toBe(true)
  expect(dialog.parentElement?.classList.contains('justify-center')).toBe(true)
  expect(dialog.parentElement?.parentElement).toBe(document.body)
  return dialog
 }
 await openCrewActions()
 await click('New agent')
 bounded('Add agent')
 await click('Fork existing agent…')
 bounded('Fork existing agent')
 await act(async()=>document.querySelector<HTMLButtonElement>('[role="dialog"] [aria-label="Close"]')!.click())
 await act(async()=>window.dispatchEvent(new CustomEvent('crew-new-agent',{detail:{projectId:'example'}})))
 bounded('Add agent')
 await click('New agent')
 bounded('New agent')
 await click('Cancel')
 expect(document.querySelector('[role="dialog"]')).toBeNull()
 expect(f.opened).not.toHaveBeenCalled()
 await act(async()=>window.dispatchEvent(new CustomEvent('crew-new-agent',{detail:{projectId:'example'}})))
 await click('New agent')
 const input=bounded('New agent').querySelector<HTMLInputElement>('[aria-label="Agent name"]')!
 await act(async()=>{
  Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value')!.set!.call(input,'Research')
  input.dispatchEvent(new Event('input',{bubbles:true}))
 })
 await click('Create agent')
 expect(f.opened).toHaveBeenCalledOnce()
 expect(f.opened).toHaveBeenCalledWith('Research')
 expect(document.querySelector('[role="dialog"]')).toBeNull()
})

// #2893: collapse is a viewer/Team preference, not a pause of current Needs-you reads.
it('remembers the Team one-line view per person and Team while keeping the current counts',async()=>{
 const menu=[...panels,{id:'mcp',label:'Access',icon:BookOpen} as ProductPanel<WorkWorkspaceView>]
 f.team={view:{team:{on:true,name:'Design'},people:[],reachable:[{kind:'agent',activity:'running'}]}}
 f.needsCount=2
 const page=(id='collapse-team')=><WorkContextPanel workspacePath={`Crew/${id}`} projectId={id} panels={menu} onOpen={f.opened}/>
 const toggle=(label:string)=>host.querySelector<HTMLButtonElement>(`[aria-label="${label}"]`)!
 await act(async()=>root.render(page()))
 expect(host.querySelector('[aria-label="Team pane"]')).not.toBeNull()
 expect(toggle('Collapse Team').textContent).toContain('1 working')
 expect(toggle('Collapse Team').textContent).toContain('2 Needs you')
 await act(async()=>toggle('Collapse Team').click())
 expect(host.querySelector('[aria-label="Team pane"]')).toBeNull()
 expect(toggle('Expand Team').getAttribute('aria-expanded')).toBe('false')
 f.needsCount=3;await act(async()=>root.render(page()))
 expect(toggle('Expand Team').textContent).toContain('3 Needs you')
 await act(async()=>root.render(null));await act(async()=>root.render(page()))
 expect(host.querySelector('[aria-label="Team pane"]')).toBeNull()
 await act(async()=>root.render(page('another-team')))
 expect(host.querySelector('[aria-label="Team pane"]')).not.toBeNull()
 await act(async()=>root.render(page()))
 expect(host.querySelector('[aria-label="Team pane"]')).toBeNull()
 const previous=f.user.id;f.user.id='another-person'
 try{await act(async()=>root.render(page()));expect(host.querySelector('[aria-label="Team pane"]')).not.toBeNull()}
 finally{f.user.id=previous}
 await act(async()=>root.render(page()))
 await act(async()=>toggle('Expand Team').click())
 expect(host.querySelector('[aria-label="Team pane"]')).not.toBeNull()
})

// #3092: a saved hide preference must not filter or reorder the agent menu.
it('shows previously hidden agents in saved order regardless of selection', async()=>{
 const tabs=['visible','hidden-first','hidden-second'].map(name=>({tabId:name,name,metadata:{agentProfileId:'work',agentProfileProjectId:'example',agentProfileConversationKey:name}}))
 const listed=tabs.map(({tabId,name})=>({conversation_key:tabId,name,can_write:true,pinned:tabId==='visible'}))
 const page=()=> <ProjectAgentTabs tabs={tabs as React.ComponentProps<typeof ProjectAgentTabs>['tabs']} listed={listed as React.ComponentProps<typeof ProjectAgentTabs>['listed']} canonicalTabId="main" projectName="Example" projectId="example" allowSideChats opening={false} surface="work" onOrder={async()=>{}} onRename={async()=>{}} onOpen={f.opened} onCloseTab={()=>{}} onSelectTab={()=>{}}/>
 for(const activeTabId of ['visible','hidden-second']){
  Object.assign(f.state,{activeTabId,chatTabs:Object.fromEntries(tabs.map(tab=>[tab.tabId,tab]))})
  await act(async()=>root.render(page()))
  expect(useCrewAgentsStore.getState().agents.map(row=>row.tabId)).toEqual(['visible','hidden-first','hidden-second'])
  expect(host.querySelector('[data-testid="work-chat-tab-strip"]')).toBeNull()
  expect(host.querySelector('[data-testid="work-more-tabs"]')).toBeNull()
 }
})
