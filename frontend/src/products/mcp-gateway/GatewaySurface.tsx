import { useCallback, useEffect, useRef, useState, type PointerEvent } from 'react'
import { Loader2, ShieldCheck } from 'lucide-react'
import { gatewayBaseUrl } from '../productSurfaceConfig'
import { ModePresetBar } from '../../components/ModePresetBar'
import ChatArea from '../../components/ChatArea'
import { AgentWorksChatTabItem } from '../../components/chat/AgentWorksChatTabItem'
import { ProductChatLandingCard } from '../../components/chat/ProductChatLandingCard'
import { ProductWorkspaceShell } from '../../components/workspace/ProductWorkspaceShell'
import { WorkspaceSplitRail } from '../../components/workspace/WorkspaceSplitDivider'
import { WorkspaceToolbarFrame } from '../../components/workspace/WorkspaceToolbarFrame'
import { WorkspaceToolbarButton } from '../../components/workspace/WorkspaceToolbarButton'
import { TooltipProvider } from '../../components/ui/tooltip'
import { GlobalHumanFeedbackPrompt } from '../../components/GlobalHumanFeedbackPrompt'
import { UpdateProgressToast } from '../../components/UpdateProgressToast'
import SchedulesPage from '../../components/SchedulesPage'
import AdminPages from '../../components/AdminPages'
import LlmModalHost from '../../components/topbar/LlmModalHost'
import { GatewayServersPanel } from './GatewayServersPanel'
import { GatewayWorkspacePane, gatewayPanels, type GatewayPanel } from './GatewayWorkspacePane'
import { GatewayModelSettings } from './GatewayModelSettings'
import { GATEWAY_AUTH_REQUIRED_EVENT } from './gatewayAdminApi'
import { agentApi } from '../../services/api'
import { useAuthStore } from '../../stores/useAuthStore'
import { useAppStore } from '../../stores/useAppStore'
import { useLLMStore } from '../../stores/useLLMStore'
import { useChatStore, waitForChatStoreHydration } from '../../stores/useChatStore'
import { usePointerDrag } from '../../hooks/usePointerDrag'
import { clampWorkSplitRatio } from '../work/workSurfaceLayoutResolver'
import { hydrateTabEvents } from '../../utils/sessionRestore'
import { activateTab } from '../../utils/activateTab'
import { openWorkspacePaneChatWithDraft } from '../../utils/workspacePaneChat'
import { readReportPreviewPreference, writeReportPreviewPreference, type ReportPreviewDevice } from '../../utils/reportPreviewPreference'

const PROFILE = 'caplayer'
const WORKSPACE = 'Chats/CapLayer'

/** Product adapter only: the server owns conversation identity and transport. */
function useCapLayerChat() {
  const [tabId, setTabId] = useState<string | null>(null)
  const [error, setError] = useState('')
  const generation = useRef(0)
  const prepare = useCallback(async (newConversation = false) => {
    const request = ++generation.current
    setError('')
    await waitForChatStoreHydration()
    const store = useChatStore.getState()
    const conversation = newConversation
      ? await agentApi.startNewAgentProfileConversation(PROFILE, { conversation_key: 'main' })
      : await agentApi.resolveAgentProfileConversation(PROFILE, { conversation_key: 'main' }, null)
    if (request !== generation.current) return
    if (newConversation) {
      for (const tab of Object.values(store.chatTabs)) {
        if (tab.metadata?.agentProfileId === PROFILE && tab.metadata.agentProfileConversationKey === 'main') {
          store.setTabMetadata(tab.tabId, { isViewOnly: true })
        }
      }
    }
    const created = await store.createChatTab('Chat', {
      mode: 'multi-agent', agentProfileId: PROFILE, agentProfileVersion: 1,
      agentProfileWorkspace: WORKSPACE, agentProfileProjectTitle: 'Vault',
      agentProfileChatContract: 'profile-v1', agentProfileConversationKey: conversation.conversation_key,
      agentProfileConversationId: conversation.conversation_id,
    }, conversation.session_id)
    if (request !== generation.current) return
    store.setTabConfig(created, { selectedServers: ['NO_SERVERS'], selectedSkills: [] })
    await hydrateTabEvents(conversation.session_id, { workspacePath: WORKSPACE, preferChatHistory: true, fallbackToChatHistory: true })
    if (request !== generation.current) return
    activateTab(created)
    setTabId(created)
  }, [])
  useEffect(() => {
    void prepare().catch(cause => setError(cause instanceof Error ? cause.message : 'Could not open Vault chat'))
    return () => { generation.current += 1 }
  }, [prepare])
  return { tabId, error, openNew: () => void prepare(true).catch(cause => setError(cause instanceof Error ? cause.message : 'Could not start conversation')) }
}

function GatewayChatTab({ tabId, chatOpen, openChat }: { tabId: string | null; chatOpen: boolean; openChat: () => void }) {
  const tab = useChatStore(state => tabId ? state.chatTabs[tabId] : undefined)
  return tab ? <AgentWorksChatTabItem tab={tab} isActive={chatOpen} canClose={false} isBlank={false}
    onTabClick={() => { openChat(); activateTab(tab.tabId) }} onCloseTab={() => {}} /> : null
}

function GatewayAdminWorkspace({ base, standalone }: { base: string; standalone: boolean }) {
  const { tabId, error, openNew } = useCapLayerChat()
  // Draft edits stay inside ChatArea instead of rerendering every admin panel.
  const sessionId = useChatStore(state => tabId ? state.chatTabs[tabId]?.sessionId : undefined)
  const chatBusy = useChatStore(state => Boolean(tabId && state.chatTabs[tabId]?.isStreaming))
  const revision = useChatStore(state => {
    const events = sessionId ? state.tabEvents[sessionId] : undefined
    return chatBusy ? undefined : events?.at(-1)?.id
  })
  const [panel, setPanel] = useState<GatewayPanel>(() => {
    try {
      const requested = sessionStorage.getItem('vault.requested-panel')
      sessionStorage.removeItem('vault.requested-panel')
      if (gatewayPanels.some(item => item.id === requested)) return requested as GatewayPanel
    } catch { /* Optional destination preference. */ }
    return 'access'
  })
  useEffect(() => {
    const open = (event: Event) => {
      const requested = (event as CustomEvent).detail
      if (gatewayPanels.some(item => item.id === requested)) setPanel(requested)
    }
    window.addEventListener('vault-open-panel', open)
    return () => window.removeEventListener('vault-open-panel', open)
  }, [])
  const [chatOpen, setChatOpen] = useState(true)
  const [panelOpen, setPanelOpen] = useState(true)
  const [previewDevice, setPreviewDevice] = useState<ReportPreviewDevice>(() => readReportPreviewPreference(WORKSPACE))
  const addCustomServer = useCallback(async () => {
    setChatOpen(true)
    await openWorkspacePaneChatWithDraft({
      profileId: PROFILE, conversationKey: 'main',
      message: 'Help me connect a custom MCP server. Ask me for its name and URL.',
    })
  }, [])
  const [ratio, setRatio] = useState(() => {
    try { const saved = Number(localStorage.getItem('caplayer_split_ratio')); return saved >= 0.15 && saved <= 0.85 ? saved : 0.43 } catch { return 0.43 }
  })
  const ratioRef = useRef(ratio)
  const splitRef = useRef<HTMLDivElement>(null)
  const { start } = usePointerDrag()
  const updateRatio = useCallback((value: number, persist = false) => {
    const next = clampWorkSplitRatio(value, splitRef.current?.getBoundingClientRect().width || window.innerWidth)
    ratioRef.current = next; setRatio(next)
    if (persist) { try { localStorage.setItem('caplayer_split_ratio', String(next)) } catch { /* Optional preference. */ } }
  }, [])
  const resize = useCallback((event: PointerEvent<HTMLButtonElement>) => {
    if (window.innerWidth < 768 || previewDevice === 'mobile') return
    const rect = splitRef.current?.getBoundingClientRect()
    if (!rect?.width) return
    start(event, { onMove: x => updateRatio((x - rect.left) / rect.width), onEnd: () => updateRatio(ratioRef.current, true) })
  }, [start, updateRatio, previewDevice])
  return <ProductWorkspaceShell
    testId="gateway-setup-panel" splitRef={splitRef} chatOpen={chatOpen} panelOpen={panelOpen} splitRatio={ratio}
    mobilePreview={previewDevice === 'mobile'}
    onOpenChat={() => setChatOpen(true)} onOpenWorkspace={() => setPanelOpen(true)}
    chatProps={{ 'aria-label': 'Vault chat' }} workspaceProps={{ 'aria-label': 'Vault workspace' }}
    tabs={<div className="flex min-w-0 flex-1 items-center gap-1 overflow-x-auto px-1" aria-label="Chat tabs">
      <GatewayChatTab tabId={tabId} chatOpen={chatOpen} openChat={() => setChatOpen(true)} />
    </div>}
    toolbar={<nav aria-label="Vault workspace toolbar" className="ml-auto flex shrink-0 items-center gap-1">
      <TooltipProvider delayDuration={150}><WorkspaceToolbarFrame aria-label="Vault sections">
        <div className="inline-flex items-center gap-0.5 px-0.5">{gatewayPanels.map(({ id, label, icon }) => <WorkspaceToolbarButton key={id} active={panel === id} icon={icon} label={label} onClick={() => setPanel(id)} />)}</div>
      </WorkspaceToolbarFrame></TooltipProvider>
    </nav>}
    chat={error ? <div role="alert" className="p-6 text-sm text-destructive">{error}</div> : tabId ? <div className="min-h-0 flex-1">
      <ChatArea tabId={tabId} compact showProductSteerAction showProductTerminalControl
        composerPlaceholder="Connect an MCP server or describe an access policy…" onNewChat={openNew}
        landingContent={<ProductChatLandingCard icon={ShieldCheck} title="Set up access with Vault"
          description="Ask the assistant to connect an MCP server, inspect its tools, or prepare an access policy for review."
          examples={['Connect a custom MCP server', 'Show connected MCP servers and their tools', 'Create a draft for a team with read access', 'Restrict tool calls to one organization or project']} />}
      />
    </div> : <div className="grid h-full place-items-center text-sm text-muted-foreground"><span><Loader2 className="mr-2 inline h-4 w-4 animate-spin" />Opening chat…</span></div>}
    divider={<WorkspaceSplitRail ratio={ratio} onPointerDown={resize} onStep={delta => { if (previewDevice !== 'mobile') updateRatio(ratioRef.current + delta, true) }} className="md:row-start-2"
      previewDevice={previewDevice} onPreviewDeviceChange={device => { setPreviewDevice(device); writeReportPreviewPreference(WORKSPACE, device) }}
      onCollapseChat={() => setChatOpen(false)} onCollapseWorkspace={() => setPanelOpen(false)} />}
    workspace={<GatewayWorkspacePane base={base} panel={panel} revision={revision} chatBusy={chatBusy}
      servers={<GatewayServersPanel base={base} view={panel === 'available-mcps' ? 'available' : 'connected'} onConnected={() => setPanel('servers')} standalone={standalone} onAddCustom={tabId ? addCustomServer : undefined} revision={revision} />} modelSettings={<GatewayModelSettings tabId={tabId} />} />}
  />
}

/** Same header, global pages, chat runtime and split shell as Crew and Code. */
export function GatewaySurface({ standalone = false }: { standalone?: boolean } = {}) {
  const base = gatewayBaseUrl() ?? (standalone ? window.location.origin : null)
  const user = useAuthStore(state => state.user)
  const checkAuth = useAuthStore(state => state.checkAuth)
  const showProviders = useLLMStore(state => state.showLLMModal)
  const showSchedules = useAppStore(state => state.showSchedulesOverview)
  const adminPage = useAppStore(state => state.adminPage)
  useEffect(() => {
    const onAuthRequired = () => { void checkAuth() }
    window.addEventListener(GATEWAY_AUTH_REQUIRED_EVENT, onAuthRequired)
    return () => window.removeEventListener(GATEWAY_AUTH_REQUIRED_EVENT, onAuthRequired)
  }, [checkAuth])
  return <div className="flex h-screen min-h-0 bg-background" data-testid="gateway-surface">
    <UpdateProgressToast /><GlobalHumanFeedbackPrompt />
    <ModePresetBar reduced walkthroughPaused />
    <div className="relative min-h-0 min-w-0 flex-1 overflow-hidden">
      <LlmModalHost />
      {showSchedules && !showProviders && <SchedulesPage />}
      {adminPage && !showProviders && <AdminPages />}
      <div className={showProviders || showSchedules || adminPage ? 'hidden' : 'h-full'}>
        {!base ? <div className="grid h-full place-items-center p-6 text-sm text-muted-foreground">Vault needs an MCP Gateway endpoint for this deployment.</div>
          : user?.is_admin === true ? <GatewayAdminWorkspace base={base} standalone={standalone} />
          : <main className="grid h-full place-items-center p-6 text-sm text-muted-foreground" role="status">Vault management requires an administrator account. Your product administrator manages this access in Users &amp; access.</main>}
      </div>
    </div>
  </div>
}
