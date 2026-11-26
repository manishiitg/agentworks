import { afterEach, expect, it, vi } from 'vitest'
import { fillAskFromMyAgentDraft, fillOwnAgentDraft, fillOwnTabDraft } from './agentPurposeDraft'
const f=vi.hoisted(()=>({tabs:{} as Record<string,unknown>,configs:{} as Record<string,{inputText:string;autoRun?:boolean}>,set:vi.fn(),activate:vi.fn(),queue:vi.fn()}))
vi.mock('../stores/useChatStore',()=>({useChatStore:{getState:()=>({chatTabs:f.tabs,getTabConfig:(id:string)=>f.configs[id],setTabConfig:f.set})}}))
vi.mock('../stores/useAuthStore',()=>({useAuthStore:{getState:()=>({user:{id:'owner'}})}}))
vi.mock('./workspacePaneChat',()=>({CHAT_FOCUS_COMPOSER_EVENT:'chat-focus-composer',sendWorkspacePaneMessageToChat:f.queue}))
vi.mock('./activateTab',()=>({activateTab:f.activate}))
const tab=(id:string,project='crew',extra={})=>({tabId:id,createdAt:1,lastAccessedAt:10,metadata:{agentProfileProjectId:project,agentProfileTabOwnerID:'owner',agentProfileTabCanWrite:true,...extra}})
afterEach(()=>{f.tabs={};f.configs={};vi.clearAllMocks();vi.unstubAllGlobals()})
// Owner regression: Main is optional/hidden. A Team row must still open an
// own writable chat, never choose a newer foreign/watched/other-Crew tab.
it('drafts the exact recipient in the most recent own writable agent when Main is absent',()=>{
 vi.stubGlobal('window',{setTimeout:vi.fn()})
 f.tabs={old:{...tab('old'),lastAccessedAt:2},recent:tab('recent','crew',{agentProfileBuilder:false}),other:{...tab('other','elsewhere'),lastAccessedAt:100},watched:{...tab('watched','crew',{isViewOnly:true}),lastAccessedAt:101},peer:{...tab('peer','crew',{agentProfileTabOwnerID:'peer'}),lastAccessedAt:102}}
 f.configs.recent={inputText:'Keep my unsent question'}
 expect(fillAskFromMyAgentDraft('crew','Colleague','Review','workspace:work:crew/agent/selected','check the release status.')).toBe(true)
 expect(f.activate).toHaveBeenCalledWith('recent')
 expect(f.set).toHaveBeenCalledWith('recent',{inputText:expect.stringContaining('workspace:work:crew/agent/selected'),autoRun:false})
 expect(f.set.mock.calls[0][1].inputText).toContain('Keep my unsent question')
 expect(f.set.mock.calls[0][1].inputText).toContain('check the release status.')
 expect(f.set.mock.calls[0][1].autoRun).toBe(false)
})
it('does not draft in a foreign, watched, setup or scheduled chat when no own writable tab exists',()=>{
 f.tabs={peer:tab('peer','crew',{agentProfileTabOwnerID:'peer'}),setup:tab('setup','crew',{agentProfileBuilder:true}),scheduled:tab('scheduled','crew',{isScheduledRun:true}),watch:tab('watch','crew',{agentProfileTabCanWrite:false})}
 expect(fillAskFromMyAgentDraft('crew','Colleague','Review','exact-target')).toBe(false)
 expect(f.set).not.toHaveBeenCalled();expect(f.activate).not.toHaveBeenCalled()
})

// Appearance is global but must only draft in this person's own interactive Crew
// tab, preserving unsent text and never triggering Send or any configuration op.
it('drafts personal appearance in an own work agent and never a teammate or run tab',()=>{
 vi.stubGlobal('window',{setTimeout:vi.fn()})
 f.tabs={own:tab('own','crew',{agentProfileId:'work'}),peer:{...tab('peer','other',{agentProfileId:'work',agentProfileTabOwnerID:'peer'}),lastAccessedAt:99},watch:{...tab('watch','other',{agentProfileId:'work',isViewOnly:true}),lastAccessedAt:100},run:{...tab('run','other',{agentProfileId:'work',isExecutionRun:true}),lastAccessedAt:101},other:tab('other','other',{agentProfileId:'another-product'})}
 f.configs.own={inputText:'Keep this unsent message',autoRun:true}
 expect(fillOwnAgentDraft('Customize my appearance: ')).toBe(true)
 expect(f.activate).toHaveBeenCalledWith('own')
 expect(f.set).toHaveBeenCalledWith('own',{inputText:'Customize my appearance: \n\nKeep this unsent message',autoRun:false})
 expect(f.set.mock.calls[0][1].autoRun).toBe(false)
 // Returning to an already filled starter must also clear a stale auto-send.
 f.set.mockClear()
 f.configs.own={inputText:'Customize my appearance: \nMy existing style request',autoRun:true}
 expect(fillOwnAgentDraft('Customize my appearance: ')).toBe(true)
 expect(f.set).toHaveBeenCalledWith('own',{inputText:'Customize my appearance: \nMy existing style request',autoRun:false})
 f.set.mockClear();f.activate.mockClear();delete f.tabs.own
 expect(fillOwnAgentDraft('Customize my appearance: ')).toBe(false)
 expect(f.set).not.toHaveBeenCalled();expect(f.activate).not.toHaveBeenCalled()
})

// Sharing explanations must not silently switch to Main or a foreign watched tab.
it('drafts settings in the exact own agent only, without sending',()=>{
 vi.stubGlobal('window',{setTimeout:vi.fn()})
 f.tabs={own:tab('own','crew',{agentProfileId:'work'}),peer:tab('peer','crew',{agentProfileId:'work',agentProfileTabOwnerID:'peer'}),watched:tab('watched','crew',{agentProfileId:'work',agentProfileTabCanWrite:false})}
 expect(fillOwnTabDraft('peer','Explain')).toBe(false)
 expect(fillOwnTabDraft('watched','Explain')).toBe(false)
 expect(f.set).not.toHaveBeenCalled()
 expect(fillOwnTabDraft('own','Explain')).toBe(true)
 expect(f.set).toHaveBeenCalledWith('own',{inputText:'Explain',autoRun:false})
 expect(f.activate).toHaveBeenCalledWith('own')
})
