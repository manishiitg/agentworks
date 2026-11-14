// @vitest-environment happy-dom
import React, { act, useState } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import { BookOpen } from 'lucide-react'
import { WorkContextPanel } from './WorkContextPanel'
import { WorkSkillsPanel } from './WorkSkillsPanel'
import { ProjectAgentTabs } from '../../components/chat/ProjectAgentTabs'
import DiscoverSkillsDialog from '../../components/skills/DiscoverSkillsDialog'
import { crewProjectMenuItems } from './crewProjectMenu'
import type { ProductPanel } from '../productPanels'
import type { WorkWorkspaceView } from './WorkWorkspacePane'

const f=vi.hoisted(()=>({ discover:vi.fn(), importDiscovered:vi.fn(), opened:vi.fn(), target:vi.fn(), user:{id:'owner',allowed_products:['work']}, server:'local', state:{activeTabId:null,chatTabs:{},addToast:vi.fn()} }))
vi.mock('../../api/skills',()=>({skillsApi:{discover:f.discover,importDiscovered:f.importDiscovered}}))
vi.mock('../../stores/useAuthStore',()=>({useAuthStore:Object.assign((select:(state:unknown)=>unknown)=>select({user:f.user}),{getState:()=>({user:f.user})})}))
vi.mock('../../stores/useWorkspaceConnectionStore',async importOriginal=>({...await importOriginal<typeof import('../../stores/useWorkspaceConnectionStore')>(),useWorkspaceConnectionStore:Object.assign((select:(state:unknown)=>unknown)=>select({activeWorkspaceId:f.server}),{getState:()=>({activeWorkspaceId:f.server})})}))
vi.mock('../../stores/useChatStore',async importOriginal=>({...await importOriginal<typeof import('../../stores/useChatStore')>(),useChatStore:Object.assign((select:(state:unknown)=>unknown)=>select(f.state),{getState:()=>f.state})}))
vi.mock('../../stores/useCapabilitiesStore',()=>({useCapabilitiesStore:Object.assign((select:(state:unknown)=>unknown)=>select({capabilities:{local_mode:true}}),{getState:()=>({capabilities:{local_mode:true}})})}))
vi.mock('../../stores/useProductNavigationToolbarStore',()=>({useProductNavigationToolbarStore:(select:(state:unknown)=>unknown)=>select({sidebarSettingsTarget:null})}))
vi.mock('../../hooks/useWorkspaceViewTarget',()=>({setWorkspaceViewTarget:f.target}))
vi.mock('./useCrewPanel',()=>({useCrewPanel:()=>({data:{sections:[{id:'connections',rows:[]}],details:{}},refresh:()=>{}})}))
vi.mock('./YourComputerPanel',()=>({useYourComputerSummary:()=>undefined,useCrewLivesOnMac:()=>false,useCrewAsking:()=>[],openCrewAccess:()=>{},openCrewAskThread:()=>{}}))
vi.mock('./teamApi',()=>({useTeamTab:()=>({view:undefined}),teamAgentForTab:()=>undefined,agentSetupForTab:()=>undefined}))
vi.mock('../../components/chat/AgentCard',()=>({AgentHeaderLine:()=>null,useCrewAgentsStore:{getState:()=>({set:()=>{}})},useOpenAgentStore:{getState:()=>({set:()=>{}})}}))
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
afterEach(()=>{act(()=>root.render(null));vi.clearAllMocks()})
const click=async(text:string)=>{const button=[...document.querySelectorAll('button')].find(item=>item.textContent?.trim()===text);expect(button,`reachable ${text}`).toBeTruthy();await act(async()=>button!.click())}
function Harness({entry,canDiscover=true}:{entry:'header'|'integrations';canDiscover?:boolean}) {
 const [discover,setDiscover]=useState(false);const [view,setView]=useState('')
 return <>{entry==='header'?<ProjectAgentTabs tabs={[]} listed={[]} canonicalTabId="" projectName="Example" projectId="example" allowSideChats={canDiscover} opening={false} surface="work" onOrder={async()=>{}} onPin={async()=>{}} onRename={async()=>{}} onOpen={f.opened} onCloseTab={()=>{}} onSelectTab={()=>{}} onImportLocalChat={()=>{}} onDiscoverSkills={canDiscover?()=>setDiscover(true):undefined}/>:view==='memory'?<WorkSkillsPanel workspacePath="Crew/example" projectId="example" tabId="" onSelectedSkillsChange={async()=>{}}/>:<WorkContextPanel workspacePath="Crew/example" projectId="example" panels={canDiscover?panels:[]} enabledPanels={new Set(canDiscover?['skills']:[])} onOpen={next=>setView(next)}/>}{discover&&<DiscoverSkillsDialog workspacePath="Crew/example" close={()=>setDiscover(false)}/>}</>
}

// PLAT-746-1008: exercise the real entry components, not just an already-open
// dialog. Skills navigation/import must neither create an agent nor copy files.
it.each(['header','integrations'] as const)('opens the existing project discovery picker from %s',async entry=>{
 f.discover.mockResolvedValue({skills:[],message:'Available source skills'})
 await act(async()=>root.render(<Harness entry={entry}/>))
 if(entry==='header') {
  await act(async()=>document.querySelector<HTMLButtonElement>('[data-testid="work-more-tabs"]')!.click())
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
it('retains the caller-provided own-only action and feature-filtered Skills destination',async()=>{
 await act(async()=>root.render(<Harness entry="header" canDiscover={false}/>))
 await act(async()=>document.querySelector<HTMLButtonElement>('[data-testid="work-more-tabs"]')!.click())
 expect(document.body.textContent).not.toContain('Discover & import skills')
 expect(crewProjectMenuItems({panels,enabledPanels:new Set()}).some(item=>item.id==='memory:skills')).toBe(false)
})
