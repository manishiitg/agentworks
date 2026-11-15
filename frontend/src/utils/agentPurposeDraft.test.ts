import { afterEach, expect, it, vi } from 'vitest'
import { fillAskFromMyAgentDraft } from './agentPurposeDraft'
import { startTeamDashboardHandoff } from './teamDashboardHandoff'
const f=vi.hoisted(()=>({tabs:{} as Record<string,unknown>,configs:{} as Record<string,{inputText:string}>,set:vi.fn(),activate:vi.fn()}))
vi.mock('../stores/useChatStore',()=>({useChatStore:{getState:()=>({chatTabs:f.tabs,getTabConfig:(id:string)=>f.configs[id],setTabConfig:f.set})}}))
vi.mock('../stores/useAuthStore',()=>({useAuthStore:{getState:()=>({user:{id:'owner'}})}}))
vi.mock('./activateTab',()=>({activateTab:f.activate}))
const tab=(id:string,project='crew',extra={})=>({tabId:id,createdAt:1,lastAccessedAt:10,metadata:{agentProfileProjectId:project,agentProfileTabOwnerID:'owner',agentProfileTabCanWrite:true,...extra}})
afterEach(()=>{f.tabs={};f.configs={};vi.clearAllMocks();vi.unstubAllGlobals()})
// Owner regression: Main is optional/hidden. A Team row must still open an
// own writable chat, never choose a newer foreign/watched/other-Crew tab.
it('drafts the exact recipient in the most recent own writable agent when Main is absent',()=>{
 vi.stubGlobal('window',{setTimeout:vi.fn()})
 f.tabs={old:{...tab('old'),lastAccessedAt:2},recent:tab('recent'),other:{...tab('other','elsewhere'),lastAccessedAt:100},watched:{...tab('watched','crew',{isViewOnly:true}),lastAccessedAt:101},peer:{...tab('peer','crew',{agentProfileTabOwnerID:'peer'}),lastAccessedAt:102}}
 f.configs.recent={inputText:'Keep my unsent question'}
 expect(fillAskFromMyAgentDraft('crew','Colleague','Review','workspace:work:crew/agent/selected')).toBe(true)
 expect(f.activate).toHaveBeenCalledWith('recent')
 expect(f.set).toHaveBeenCalledWith('recent',{inputText:expect.stringContaining('workspace:work:crew/agent/selected')})
 expect(f.set.mock.calls[0][1].inputText).toContain('Keep my unsent question')
 expect(f.set.mock.calls[0][1]).not.toHaveProperty('autoRun')
})
it('does not draft in a foreign or Run chat when no own writable tab exists',()=>{
 f.tabs={peer:tab('peer','crew',{agentProfileTabOwnerID:'peer'}),run:tab('run','crew',{agentProfileBuilder:false}),watch:tab('watch','crew',{agentProfileTabCanWrite:false})}
 expect(fillAskFromMyAgentDraft('crew','Colleague','Review','exact-target')).toBe(false)
 expect(f.set).not.toHaveBeenCalled();expect(f.activate).not.toHaveBeenCalled()
})

// The Add to Team click starts a real own turn, but cannot auto-send an
// unrelated human draft or select a watched/foreign/Run agent when Main is off.
it('starts the own source agent for Add to Team without overwriting a human draft',()=>{
 f.tabs={own:tab('own'),peer:{...tab('peer','crew',{agentProfileTabOwnerID:'peer'}),lastAccessedAt:99},watch:tab('watch','crew',{isViewOnly:true}),run:tab('run','crew',{agentProfileBuilder:false})}
 startTeamDashboardHandoff('crew','Add this dashboard')
 expect(f.set).toHaveBeenCalledWith('own',{inputText:'Add this dashboard',autoRun:true})
 f.set.mockClear();f.configs.own={inputText:'My unsent request'}
 expect(()=>startTeamDashboardHandoff('crew','Add this dashboard')).toThrow('draft')
 expect(f.set).not.toHaveBeenCalled()
 delete f.tabs.own
 expect(()=>startTeamDashboardHandoff('crew','Add this dashboard')).toThrow('own agent chats')
 expect(f.set).not.toHaveBeenCalled()
})
