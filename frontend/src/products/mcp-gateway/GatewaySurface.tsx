import { currentServiceChatRequest, useServiceChatNavigation } from '../../utils/serviceChatNavigation'
import { useProductUrlView } from '../../hooks/useProductUrlView'
import { useWorkspaceViewPreference } from '../../hooks/useWorkspaceViewPreference'
import { normalizeViewFrom } from '../../utils/workspaceViewPreference'
import { useCallback, useEffect, useRef, useState, type PointerEvent } from 'react'
import { ArrowLeft, Loader2, MessageCircle, ShieldCheck, ScrollText, PlugZap, BarChart3, SlidersHorizontal, Smartphone, Tablet, Laptop } from 'lucide-react'
import { gatewayBaseUrl } from '../productSurfaceConfig'
import { ModePresetBar } from '../../components/ModePresetBar'
import ChatArea from '../../components/ChatArea'
import { ProductChatLandingCard } from '../../components/chat/ProductChatLandingCard'
import { ProductWorkspaceShell } from '../../components/workspace/ProductWorkspaceShell'
import { WorkspaceSplitDivider } from '../../components/workspace/WorkspaceSplitDivider'
import { WorkspaceToolsGroup, WorkspaceToolsMenu } from '../../components/workspace/WorkspaceToolsMenu'
import { WorkspaceViewIconButton } from '../../components/workflow/WorkspaceViewIconButton'
import { useRegisterPanelSwitcher } from '../../stores/usePanelSwitcherStore'
import { VAULT_PANEL_SECTIONS } from '../productPanels'
import { Tooltip, TooltipContent, TooltipTrigger, TooltipProvider } from '../../components/ui/tooltip'
import { GlobalHumanFeedbackPrompt } from '../../components/GlobalHumanFeedbackPrompt'
import { UpdateProgressToast } from '../../components/UpdateProgressToast'
import SchedulesPage from '../../components/SchedulesPage'
import AdminPages from '../../components/AdminPages'
import LlmModalHost from '../../components/topbar/LlmModalHost'
import { GatewayServersPanel } from './GatewayServersPanel'
import { VaultReadOnlyContext, useVaultReadOnly } from './vaultReadOnly'
import { GatewayWorkspacePane, gatewayPanels, type GatewayPanel } from './GatewayWorkspacePane'
import { GatewayModelSettings } from './GatewayModelSettings'
import { GatewayAuditPanel } from './GatewayAuditPanel'
import { GatewayConnectPanel } from './GatewayConnectPanel'
import { WorkspaceViewTabs } from '../../components/workflow/WorkspaceViewTabs'
import { SettingsCardLayout } from '../../components/ui/SettingsCard'
import { GatewayFeedbackBoundary } from './gatewayConsoleShared'
import { GATEWAY_AUTH_REQUIRED_EVENT } from './gatewayAdminApi'
import { useAuthStore } from '../../stores/useAuthStore'
import { useAppStore } from '../../stores/useAppStore'
import { useLLMStore } from '../../stores/useLLMStore'
import { useChatStore } from '../../stores/useChatStore'
import { usePointerDrag } from '../../hooks/usePointerDrag'
import { clampWorkSplitRatio } from '../work/workSurfaceLayoutResolver'
import { openWorkspacePaneChatWithDraft } from '../../utils/workspacePaneChat'
import { readReportPreviewPreference, writeReportPreviewPreference, type ReportPreviewDevice } from '../../utils/reportPreviewPreference'
import { captureChatIdentity, isChatIdentityCurrent } from '../../utils/chatIdentity'
import { GatewayContextPanel, type VaultPage } from './GatewayContextPanel'
import { useVaultChat } from './useVaultChat'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'

const WORKSPACE = 'Chats/CapLayer'

const normalizeVaultPanel = normalizeViewFrom(gatewayPanels.map(panel => panel.id))

function GatewayAdminWorkspace({ base, standalone, page, onPage }: { base: string; standalone: boolean; page: VaultPage | null; onPage: (page: VaultPage | null) => void }) {
  const readOnly = useVaultReadOnly()
  const [chatOpen, setChatOpen] = useState(false)
  const [chatRequested, setChatRequested] = useState(false)
  const [mobilePane, setMobilePane] = useState<'chat' | 'workspace'>('chat')
  const [panel, setPanel] = useWorkspaceViewPreference<GatewayPanel>('mcp-gateway', base, 'servers', normalizeVaultPanel)
  const navigation = currentServiceChatRequest(useServiceChatNavigation(state => state.request), 'mcp-gateway')
  const { tabId, error, prepare, retry, openNew } = useVaultChat(!readOnly && (chatRequested || (!page && panel === 'models')), navigation?.tabId)
  useEffect(() => {
    if (readOnly) { setChatOpen(false); return }
    if (navigation) { setChatRequested(true); setChatOpen(true); setMobilePane('workspace') }
  }, [navigation, readOnly])
  const openChat = () => { setChatRequested(true); setChatOpen(true); setMobilePane('workspace') }
  const closeChat = () => { setChatOpen(false); setMobilePane('workspace') }
  const openPanel = (next: GatewayPanel) => { setPanel(next); onPage(null); setMobilePane('chat') }
  const openPage = (next: VaultPage) => { onPage(next); setMobilePane('chat') }
  const sessionId = useChatStore(state => tabId ? state.chatTabs[tabId]?.sessionId : undefined)
  const chatBusy = useChatStore(state => Boolean(tabId && state.chatTabs[tabId]?.isStreaming))
  const revision = useChatStore(state => {
    const events = sessionId ? state.tabEvents[sessionId] : undefined
    return chatBusy ? undefined : events?.at(-1)?.id
  })
  useProductUrlView('mcp-gateway', { view: page ?? panel })
  const [peopleTabRequest, setPeopleTabRequest] = useState<{ tab: 'users' | 'groups'; token: number }>()
  useRegisterPanelSwitcher('mcp-gateway', gatewayPanels.map(({ id, label }) => ({ id, label, sections: VAULT_PANEL_SECTIONS[id] })), (id, section) => {
    const next = normalizeVaultPanel(id)
    if (!next) return
    openPanel(next)
    if (id === 'people' && (section === 'users' || section === 'groups')) setPeopleTabRequest(current => ({ tab: section, token: (current?.token ?? 0) + 1 }))
  })
  useEffect(() => {
    try {
      const requested = normalizeVaultPanel(sessionStorage.getItem('vault.requested-panel'))
      if (requested) { sessionStorage.removeItem('vault.requested-panel'); setPanel(requested); onPage(null) }
    } catch { /* Optional navigation request. */ }
  }, [setPanel, onPage])
  useEffect(() => {
    const open = (event: Event) => {
      const requested = normalizeVaultPanel((event as CustomEvent).detail)
      if (requested) { setPanel(requested); setMobilePane('chat') }
    }
    window.addEventListener('vault-open-panel', open)
    return () => window.removeEventListener('vault-open-panel', open)
  }, [setPanel])
  const [previewDevice, setPreviewDevice] = useState<ReportPreviewDevice>(() => readReportPreviewPreference(WORKSPACE))
  const addCustomServer = useCallback(async () => {
    if (readOnly) return
    const identity = captureChatIdentity()
    setChatRequested(true); setChatOpen(true); setMobilePane('workspace')
    const target = await prepare()
    if (!target || !isChatIdentityCurrent(identity)) return
    await openWorkspacePaneChatWithDraft({ tabId: target,
      message: 'Help me connect a custom MCP server. Ask me for its name and URL.',
    })
  }, [prepare, readOnly])
  const [ratio, setRatio] = useState(() => {
    try { const saved = Number(localStorage.getItem('caplayer_split_ratio')); return saved >= 0.15 && saved <= 0.85 ? 1 - saved : 0.62 } catch { return 0.62 }
  })
  const ratioRef = useRef(ratio)
  const splitRef = useRef<HTMLDivElement>(null)
  const { start } = usePointerDrag()
  const updateRatio = useCallback((value: number, persist = false) => {
    const next = clampWorkSplitRatio(value, splitRef.current?.getBoundingClientRect().width || window.innerWidth)
    ratioRef.current = next; setRatio(next)
    if (persist) { try { localStorage.setItem('caplayer_split_ratio', String(1 - next)) } catch { /* Optional preference. */ } }
  }, [])
  const resize = useCallback((event: PointerEvent<HTMLButtonElement>) => {
    if (window.innerWidth < 768) return
    const rect = splitRef.current?.getBoundingClientRect()
    if (!rect?.width) return
    start(event, { onMove: x => updateRatio((x - rect.left) / rect.width), onEnd: () => updateRatio(ratioRef.current, true) })
  }, [start, updateRatio])
  const title = page === 'audit' ? 'Audit logs' : page === 'connect' ? 'Vault MCP endpoint' : gatewayPanels.find(item => item.id === panel)?.label ?? 'Vault'
  return <ProductWorkspaceShell
    testId="gateway-setup-panel" splitRef={splitRef} chatOpen panelOpen splitRatio={ratio} compactContext={!chatOpen} mobilePane={mobilePane}
    onOpenChat={() => setMobilePane('chat')} onOpenWorkspace={() => setMobilePane('workspace')}
    chatProps={{ 'aria-label': 'Vault management' }} workspaceProps={{ 'aria-label': chatOpen ? 'Vault assistant panel' : 'Vault overview' }}
    tabs={<div className="flex min-w-0 flex-1 items-center gap-2">{page && <WorkspaceViewIconButton icon={ArrowLeft} label="Back to Vault management" onClick={() => onPage(null)} />}<h1 className="truncate text-sm font-semibold">{title}</h1></div>}
    toolbar={<div className="ml-auto flex shrink-0 items-center gap-1">
      <button type="button" onClick={() => setMobilePane(value => value === 'chat' ? 'workspace' : 'chat')} aria-label="Vault options" className="grid h-8 w-8 place-items-center rounded-lg text-muted-foreground hover:bg-muted md:hidden"><SlidersHorizontal className="h-4 w-4" /></button>
      <WorkspaceToolsMenu>{close => <>
        <WorkspaceToolsGroup title="Vault">{gatewayPanels.map(item => <button type="button" role="menuitem" key={item.id} aria-current={!page && panel === item.id ? 'page' : undefined} onClick={() => { openPanel(item.id); close() }} className="flex w-full items-center gap-2 rounded-md px-3 py-2 text-left text-xs hover:bg-muted"><item.icon className="h-3.5 w-3.5 text-muted-foreground" />{item.label}</button>)}
          <button type="button" role="menuitem" aria-current={page === 'audit' ? 'page' : undefined} onClick={() => { openPage('audit'); close() }} className="flex w-full items-center gap-2 rounded-md px-3 py-2 text-left text-xs hover:bg-muted"><ScrollText className="h-3.5 w-3.5 text-muted-foreground" />Audit logs</button>
          <button type="button" role="menuitem" aria-current={page === 'connect' ? 'page' : undefined} onClick={() => { openPage('connect'); close() }} className="flex w-full items-center gap-2 rounded-md px-3 py-2 text-left text-xs hover:bg-muted"><PlugZap className="h-3.5 w-3.5 text-muted-foreground" />Vault MCP endpoint</button>
        </WorkspaceToolsGroup>
        <WorkspaceToolsGroup title="Assistant"><button type="button" role="menuitem" aria-label="Open Vault assistant" onClick={() => { openChat(); close() }} className="flex w-full items-center gap-2 rounded-md px-3 py-2 text-left text-xs hover:bg-muted"><MessageCircle className="h-3.5 w-3.5 text-muted-foreground" />Vault assistant</button></WorkspaceToolsGroup>
        <WorkspaceToolsGroup title="Preview">{([['mobile', Smartphone, 'Mobile preview'], ['tablet', Tablet, 'Tablet preview'], ['desktop', Laptop, 'Laptop preview']] as const).map(([device, Icon, label]) => <button key={device} type="button" role="menuitem" aria-label={label} aria-pressed={previewDevice === device} onClick={() => { setPreviewDevice(device); writeReportPreviewPreference(WORKSPACE, device); close() }} className="flex w-full items-center gap-2 rounded-md px-3 py-2 text-left text-xs hover:bg-muted"><Icon className="h-3.5 w-3.5 text-muted-foreground" />{label}</button>)}</WorkspaceToolsGroup>
      </>}</WorkspaceToolsMenu>
    </div>}
    chat={<div className={`mx-auto flex h-full min-h-0 w-full flex-1 flex-col ${previewDevice === 'mobile' ? 'max-w-[390px] border-x border-border' : previewDevice === 'tablet' ? 'max-w-[768px]' : ''}`} data-vault-preview={previewDevice}>
      {page ? <GatewayManagementPage base={base} page={page} /> : <GatewayWorkspacePane base={base} panel={panel} revision={revision} chatBusy={chatBusy} peopleTabRequest={peopleTabRequest}
        servers={<GatewayServersPanel chatSessionId={sessionId ?? undefined} base={base} view={panel === 'available-mcps' ? 'available' : 'connected'} onConnected={() => openPanel('servers')} standalone={standalone} onAddCustom={addCustomServer} revision={revision} />} modelSettings={error ? <div className="p-4 text-sm"><p role="alert" className="text-destructive">{error}</p><button type="button" onClick={retry} className="mt-2 underline">Retry assistant settings</button></div> : tabId ? <GatewayModelSettings tabId={tabId} /> : <p className="p-4 text-sm text-muted-foreground">Opening model settings…</p>} />}
    </div>}
    divider={<WorkspaceSplitDivider ratio={ratio} onPointerDown={resize} onStep={delta => updateRatio(ratioRef.current + delta, true)} className="md:row-start-2" />}
    workspace={<div className="h-full min-h-0 overflow-y-auto">
      <div hidden={chatOpen}><button type="button" onClick={() => setMobilePane('chat')} className="mx-4 mt-3 inline-flex items-center gap-2 text-xs text-muted-foreground md:hidden"><ArrowLeft className="h-3.5 w-3.5" />Back to management</button><GatewayContextPanel panel={panel} page={page} onOpen={openPanel} onPage={openPage} onChat={openChat} readOnly={readOnly} /></div>
      {chatRequested && !readOnly && <section hidden={!chatOpen} aria-label="Vault chat" className="h-full min-h-0 flex-col" style={{ display: chatOpen ? 'flex' : 'none' }}>
        <div className="flex shrink-0 items-center gap-2 border-b border-border px-3 py-2"><WorkspaceViewIconButton label="Back to Vault overview" icon={ArrowLeft} onClick={closeChat} /><MessageCircle className="h-4 w-4 text-muted-foreground" /><h2 className="text-sm font-medium">Vault assistant</h2></div>
        <div className="min-h-0 flex-1">{error ? <div className="p-6 text-sm"><p role="alert" className="text-destructive">{error}</p><button type="button" onClick={retry} className="mt-2 underline">Retry assistant</button></div> : tabId ? <ChatArea tabId={tabId} compact showProductSteerAction showProductTerminalControl
          composerPlaceholder="Connect an MCP server or describe an access policy…" onNewChat={openNew}
          landingContent={<ProductChatLandingCard icon={ShieldCheck} title="Set up access with Vault"
            description="Ask the assistant to connect an MCP server, inspect its tools, or apply tool permissions or regex conditions."
            examples={['Connect a custom MCP server', 'Show connected MCP servers and their tools', 'Give a team read access', 'Restrict tool calls to one organization or project']} />}
        /> : <div className="grid h-full place-items-center text-sm text-muted-foreground"><span><Loader2 className="mr-2 inline h-4 w-4 animate-spin" />Opening chat…</span></div>}</div>
      </section>}
    </div>}
  />
}

function GatewayManagementPage({ base, page }: { base: string; page: VaultPage }) {
  const [auditTab, setAuditTab] = useState<'logs' | 'analysis'>('logs')
  return <section aria-label={page === 'audit' ? 'Vault audit page' : 'Vault MCP endpoint page'} className="flex h-full min-h-0 flex-col">
    {page === 'audit' && <WorkspaceViewTabs value={auditTab} onChange={value => setAuditTab(value === 'analysis' ? 'analysis' : 'logs')}
      options={[{ value: 'logs', label: 'Logs', icon: ScrollText }, { value: 'analysis', label: 'Analysis', icon: BarChart3 }]} ariaLabel="Audit tabs" />}
    <div className="min-h-0 flex-1 overflow-y-auto p-4 sm:p-6"><SettingsCardLayout unboxed><GatewayFeedbackBoundary>
      {page === 'audit' ? <GatewayAuditPanel base={base} tab={auditTab} /> : <GatewayConnectPanel base={base} />}
    </GatewayFeedbackBoundary></SettingsCardLayout></div>
  </section>
}

/** Same header, global pages, chat runtime and split shell as Crew and Code. */
const normalizeVaultPage = normalizeViewFrom(['workspace', 'audit', 'connect'] as const)

export function GatewaySurface({ standalone = false }: { standalone?: boolean } = {}) {
  const base = gatewayBaseUrl() ?? (standalone ? window.location.origin : null)
  const user = useAuthStore(state => state.user)
  const workspace = useWorkspaceConnectionStore(state => state.activeWorkspaceId)
  const checkAuth = useAuthStore(state => state.checkAuth)
  const showProviders = useLLMStore(state => state.showLLMModal)
  const showSchedules = useAppStore(state => state.showSchedulesOverview)
  const adminPage = useAppStore(state => state.adminPage)
  const [savedPage, selectPage] = useWorkspaceViewPreference('mcp-gateway', `${base ?? 'main'}:page`, 'workspace', normalizeVaultPage)
  const page = savedPage === 'workspace' ? null : savedPage
  const setPage = useCallback((next: VaultPage | null) => selectPage(next ?? 'workspace'), [selectPage])
  useEffect(() => {
    try {
      const requested = sessionStorage.getItem('vault.requested-panel')
      if (requested === 'audit' || requested === 'connect') {
        sessionStorage.removeItem('vault.requested-panel')
        setPage(requested)
      } else if (requested) setPage(null)
    } catch { /* Optional navigation request. */ }
  }, [setPage])
  const openPage = (next: VaultPage) => {
    useLLMStore.getState().setShowLLMModal(false)
    useAppStore.getState().setAdminPage(null)
    useAppStore.getState().setShowSchedulesOverview(false)
    setPage(next)
  }
  useEffect(() => {
    const open = (event: Event) => {
      const requested = (event as CustomEvent).detail
      setPage(requested === 'audit' || requested === 'connect' ? requested : null)
    }
    window.addEventListener('vault-open-panel', open)
    return () => window.removeEventListener('vault-open-panel', open)
  }, [setPage])
  useEffect(() => {
    const onAuthRequired = () => { void checkAuth() }
    window.addEventListener(GATEWAY_AUTH_REQUIRED_EVENT, onAuthRequired)
    return () => window.removeEventListener(GATEWAY_AUTH_REQUIRED_EVENT, onAuthRequired)
  }, [checkAuth])
  // Vault readers see every Vault screen with nothing they can change.
  const canViewVault = user?.is_admin === true || user?.is_vault_reader === true
  return <VaultReadOnlyContext.Provider value={user?.is_admin !== true}><div className="flex h-screen min-h-0 bg-background" data-testid="gateway-surface">
    <UpdateProgressToast /><GlobalHumanFeedbackPrompt />
    <ModePresetBar reduced walkthroughPaused productActions={base && canViewVault ? <TooltipProvider delayDuration={150}>
      <div aria-label="Vault pages" data-product-navigation-section="product-actions" className="flex flex-col items-stretch gap-1">
        {([{ id: 'audit', label: 'Audit logs', icon: ScrollText }, { id: 'connect', label: 'Vault MCP endpoint', icon: PlugZap }] as const).map(item => <Tooltip key={item.id}>
          <TooltipTrigger asChild><button type="button" role="menuitem" data-product-navigation-action aria-label={item.label}
            aria-pressed={page === item.id && !showProviders && !showSchedules && !adminPage} onClick={() => openPage(item.id)}
            className={`relative rounded-md p-1.5 transition-colors ${page === item.id && !showProviders && !showSchedules && !adminPage ? 'bg-primary/10 text-primary' : 'text-muted-foreground hover:bg-muted hover:text-foreground'}`}>
            <item.icon className="h-4 w-4" aria-hidden="true" /><span className="sr-only">{item.label}</span>
          </button></TooltipTrigger><TooltipContent side="bottom">{item.label}</TooltipContent>
        </Tooltip>)}
      </div>
    </TooltipProvider> : undefined} />
    <div className="relative min-h-0 min-w-0 flex-1 overflow-hidden">
      <LlmModalHost />
      {showSchedules && !showProviders && <SchedulesPage />}
      {adminPage && !showProviders && <AdminPages />}
      <div className={showProviders || showSchedules || adminPage ? 'hidden' : 'h-full'}>
        {!base ? <div className="grid h-full place-items-center p-6 text-sm text-muted-foreground">Vault needs an MCP Gateway endpoint for this deployment.</div>
          : canViewVault ? <GatewayAdminWorkspace key={`${workspace}:${base}:${user?.id ?? ''}:${user?.is_admin === true ? 'admin' : 'reader'}`} base={base} standalone={standalone} page={page} onPage={setPage} />
          : <main className="grid h-full place-items-center p-6 text-sm text-muted-foreground" role="status">Vault needs an administrator or a Vault reader account. Your product administrator manages this access in Users &amp; access.</main>}
      </div>
    </div>
  </div></VaultReadOnlyContext.Provider>
}
