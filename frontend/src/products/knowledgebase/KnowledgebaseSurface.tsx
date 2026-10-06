import { useWorkspaceViewPreference } from '../../hooks/useWorkspaceViewPreference'
import { normalizeViewFrom } from '../../utils/workspaceViewPreference'
import { lazy, Suspense, useCallback, useEffect, useRef, useState, type PointerEvent } from 'react'
import { BookOpen, BrainCircuit, KeyRound, RefreshCw, ShieldCheck } from 'lucide-react'
import ChatArea, { type ChatAreaRef } from '../../components/ChatArea'
import { ModePresetBar } from '../../components/ModePresetBar'
import LlmModalHost from '../../components/topbar/LlmModalHost'
import { ProductWorkspaceShell } from '../../components/workspace/ProductWorkspaceShell'
import { WorkspaceToolbarFrame } from '../../components/workspace/WorkspaceToolbarFrame'
import { WorkspaceToolbarButton } from '../../components/workspace/WorkspaceToolbarButton'
import { WorkspaceSplitRail } from '../../components/workspace/WorkspaceSplitDivider'
import { WorkspaceViewIconButton } from '../../components/workflow/WorkspaceViewIconButton'
import { usePointerDrag } from '../../hooks/usePointerDrag'
import { agentApi } from '../../services/api'
import { knowledgebaseApi, knowledgebaseError, type KnowledgeAccessProposal, type KnowledgeBootstrap } from '../../services/knowledgebaseApi'
import { useChatStore, waitForChatStoreHydration } from '../../stores/useChatStore'
import { useAppStore } from '../../stores/useAppStore'
import { useModeStore } from '../../stores/useModeStore'
import { useLLMStore } from '../../stores/useLLMStore'
import { clampWorkSplitRatio } from '../work/workSurfaceLayoutResolver'
import { hydrateTabEvents } from '../../utils/sessionRestore'
import { KnowledgebaseWorkspacePane, type KnowledgebaseView } from './KnowledgebaseWorkspacePane'
import { KnowledgebaseAccessConfirmation } from './KnowledgebaseAccessConfirmation'
import { KnowledgebaseModelSettings } from './KnowledgebaseModelSettings'
import { AgentWorksChatTabItem } from '../../components/chat/AgentWorksChatTabItem'
import { ProductChatLandingCard } from '../../components/chat/ProductChatLandingCard'
import { TooltipProvider } from '../../components/ui/tooltip'
import { AskAIButton } from '../../components/workflow/AskAIButton'
import { activateTab } from '../../utils/activateTab'
import { setProductCommands } from '../../commands/registry'
import { loadWorkProductCommands } from '../work/workData'
import { toProductCommandDefinitions } from '../work/productCommands'

const AdminPages = lazy(() => import('../../components/AdminPages'))
const SchedulesPage = lazy(() => import('../../components/SchedulesPage'))

const views = [{ id: 'library', label: 'Files', icon: BookOpen }, { id: 'access', label: 'Access', icon: ShieldCheck }, { id: 'models', label: 'Models', icon: BrainCircuit }, { id: 'secrets', label: 'Secrets', icon: KeyRound }] as const
const normalizeBrainView = normalizeViewFrom(views.map(view => view.id))
function readRatio(): number { try { const ratio = Number(localStorage.getItem('knowledgebase:split')); return ratio >= .15 && ratio <= .85 ? ratio : .38 } catch { return .38 } }

function KnowledgebaseChatTab({ tabId, chatOpen, openChat }: { tabId: string | null; chatOpen: boolean; openChat: () => void }) {
  const tab = useChatStore(state => tabId ? state.chatTabs[tabId] : undefined)
  return tab ? <AgentWorksChatTabItem tab={tab} isActive={chatOpen} canClose={false} isBlank={false}
    productSurface="knowledgebase" onTabClick={() => { openChat(); activateTab(tab.tabId) }} onCloseTab={() => {}} /> : null
}

export function KnowledgebaseSurface() {
  const [proposals, setProposals] = useState<KnowledgeAccessProposal[]>([])
  const [approvalError, setApprovalError] = useState('')
  const [approving, setApproving] = useState(false)
  // Brain's own slash commands (/organize), declared in its product.yaml; cleared on unmount so they never show in
  // another product's chat (PLAT-618).
  useEffect(() => {
    let cancelled = false
    void loadWorkProductCommands('knowledgebase', 1, 'Brain')
      .then(commands => { if (!cancelled) setProductCommands(toProductCommandDefinitions(commands)) })
      .catch(() => { if (!cancelled) setProductCommands([]) })
    return () => { cancelled = true; setProductCommands([]) }
  }, [])
  useEffect(() => {
    let active = true
    const refresh = () => { knowledgebaseApi.proposals().then(data => { if (active) setProposals(data.proposals) }).catch(() => {}) }
    refresh(); const timer = window.setInterval(refresh, 3000)
    return () => { active = false; window.clearInterval(timer) }
  }, [])
  async function confirmAccess(id: string, approve: boolean, pat?: string) {
    setApproving(true); setApprovalError('')
    try { await knowledgebaseApi.confirmAccess(id, approve, pat); setProposals(items => items.filter(item => item.id !== id)); setRevision(value => value + 1) }
    catch (error) { setApprovalError(knowledgebaseError(error)) }
    finally { setApproving(false) }
  }
  const [bootstrap, setBootstrap] = useState<KnowledgeBootstrap | null>(null)
  const [tabId, setTabId] = useState<string | null>(null)
  const [error, setError] = useState('')
  const [attempt, setAttempt] = useState(0)
  const [view, setView] = useWorkspaceViewPreference<KnowledgebaseView>('knowledgebase', 'main', 'library', normalizeBrainView)
  const [folder, setFolder] = useState('')
  const [revision, setRevision] = useState(0)
  const [ratio, setRatio] = useState(readRatio)
  const [collapsed, setCollapsed] = useState<'chat' | 'workspace' | null>(null)
  const [mobilePane, setMobilePane] = useState<'chat' | 'workspace'>('workspace')
  const showProviders = useLLMStore(state => state.showLLMModal)
  const showSchedules = useAppStore(state => state.showSchedulesOverview)
  const adminPage = useAppStore(state => state.adminPage)
  const containerRef = useRef<HTMLDivElement>(null)
  const chatRef = useRef<ChatAreaRef>(null)
  const newConversation = useRef(false)
  const drag = usePointerDrag()
  useEffect(() => {
    let cancelled = false
    const startNew = newConversation.current
    newConversation.current = false
    const controller = new AbortController()
    setError(''); setTabId(null)
    async function prepare() {
      const state = await knowledgebaseApi.bootstrap(controller.signal)
      if (cancelled) return
      setBootstrap(state)
      useModeStore.getState().setModeCategory('multi-agent')
      useAppStore.getState().setAgentMode('multi-agent')
      await waitForChatStoreHydration()
      if (cancelled) return
      const store = useChatStore.getState()
      const conversation = await (startNew ? agentApi.startNewAgentProfileConversation('knowledgebase', { conversation_key: 'main' }) : agentApi.resolveAgentProfileConversation('knowledgebase', { conversation_key: 'main' }))
      if (cancelled) return
      if (startNew) {
        for (const tab of Object.values(store.chatTabs)) {
          if (tab.metadata?.agentProfileId === 'knowledgebase' && tab.metadata.agentProfileConversationKey === 'main') store.setTabMetadata(tab.tabId, { isViewOnly: true })
        }
      }
      const id = await store.createChatTab('Chat', {
        mode: 'multi-agent', agentProfileId: 'knowledgebase', agentProfileVersion: 1,
        agentProfileWorkspace: state.chat_workspace, agentProfileProjectTitle: 'Brain',
        agentProfileChatContract: 'profile-v1', agentProfileConversationKey: conversation.conversation_key,
        agentProfileConversationId: conversation.conversation_id, agentProfileMCPSelectionInitialized: true,
      }, conversation.session_id)
      store.setTabConfig(id, { selectedServers: ['NO_SERVERS'], selectedSkills: [] })
      if ((store.tabEvents[conversation.session_id]?.length ?? 0) === 0) await hydrateTabEvents(conversation.session_id, { workspacePath: state.chat_workspace, preferChatHistory: true, fallbackToChatHistory: true })
      if (cancelled) return
      const app = useAppStore.getState()
      if (!app.adminPage && !app.showSchedulesOverview && !useLLMStore.getState().showLLMModal) activateTab(id)
      setTabId(id)
    }
    void prepare().catch(error => { if (!cancelled) setError(knowledgebaseError(error)) })
    return () => { cancelled = true; controller.abort() }
  }, [attempt])
  useEffect(() => {
    const timer = window.setInterval(() => { if (!document.hidden) setRevision(value => value + 1) }, 15000)
    const refresh = () => { if (!document.hidden) setRevision(value => value + 1) }
    document.addEventListener('visibilitychange', refresh)
    return () => { window.clearInterval(timer); document.removeEventListener('visibilitychange', refresh) }
  }, [])
  useEffect(() => {
    if (revision === 0) return
    const controller = new AbortController()
    void knowledgebaseApi.bootstrap(controller.signal).then(state => { if (!controller.signal.aborted) setBootstrap(state) }).catch(() => {})
    return () => controller.abort()
  }, [revision])
  const changeRatio = useCallback((next: number) => { const bounded = clampWorkSplitRatio(next, containerRef.current?.getBoundingClientRect().width || window.innerWidth); setRatio(bounded); try { localStorage.setItem('knowledgebase:split', String(bounded)) } catch { /* Preference only. */ } }, [])
  function startResize(event: PointerEvent<HTMLButtonElement>) {
    const bounds = containerRef.current?.getBoundingClientRect()
    if (!bounds) return
    drag.start(event, { onMove: clientX => changeRatio((clientX - bounds.left) / bounds.width), onEnd: () => {} })
  }
  async function askChat(message: string) {
    if (!tabId || !chatRef.current) return
    setCollapsed(null); setMobilePane('chat')
    activateTab(tabId)
    await chatRef.current.submitQuery(message)
  }
  function askAccess() {
    setCollapsed(null); setMobilePane('chat')
    if (tabId) activateTab(tabId)
    void chatRef.current?.submitQuery(`Inspect access for folder ${JSON.stringify(folder || 'organization root')}.`)
  }
  return <div className="flex h-dvh min-h-0 overflow-hidden bg-background text-foreground" data-product="knowledgebase">
    <ModePresetBar reduced walkthroughPaused />
    <div className="relative min-h-0 min-w-0 flex-1 overflow-hidden">
      <LlmModalHost />
      <Suspense fallback={<div className="grid h-full place-items-center text-sm text-muted-foreground">Opening page…</div>}>
        {showSchedules && !showProviders && <SchedulesPage />}
        {adminPage && !showProviders && <AdminPages />}
      </Suspense>
      <div className={showProviders || showSchedules || adminPage ? 'hidden' : 'flex h-full min-h-0 flex-col'}>
        <KnowledgebaseAccessConfirmation proposals={proposals} error={approvalError} busy={approving} onConfirm={confirmAccess} />
        {error ? <div className="grid h-full place-items-center p-6"><div className="max-w-md text-center"><p role="alert" className="text-sm text-destructive">{error}</p><button type="button" onClick={() => setAttempt(value => value + 1)} className="mt-4 rounded-md border border-border px-4 py-2 text-sm">Retry</button></div></div> : !bootstrap ? <div className="grid h-full place-items-center text-sm text-muted-foreground">Opening Brain…</div> : <div className="min-h-0 flex-1"><ProductWorkspaceShell
          splitRef={containerRef} chatOpen={collapsed !== 'chat'} panelOpen={collapsed !== 'workspace'} splitRatio={ratio} mobilePane={mobilePane}
          onOpenChat={() => setCollapsed(null)} onOpenWorkspace={() => setCollapsed(null)}
          chatProps={{ 'aria-label': 'Access management chat' }} workspaceProps={{ 'aria-label': 'Brain workspace' }}
          tabs={<div className="flex min-w-0 flex-1 items-center gap-1 overflow-x-auto px-1" aria-label="Chat tabs">
            <KnowledgebaseChatTab tabId={tabId} chatOpen={collapsed !== 'chat'} openChat={() => { setCollapsed(null); setMobilePane('chat') }} />
          </div>}
          toolbar={<nav aria-label="Brain workspace toolbar" className="ml-auto flex shrink-0 items-center gap-1">
            <TooltipProvider delayDuration={150}><WorkspaceToolbarFrame aria-label="Brain views"><div className="inline-flex items-center gap-0.5 px-0.5">{views.map(({ id, label, icon }) => <WorkspaceToolbarButton key={id} active={view === id} icon={icon} label={label} onClick={() => { setView(id); setMobilePane('workspace'); if (collapsed === 'workspace') setCollapsed(null) }} />)}</div></WorkspaceToolbarFrame></TooltipProvider>
            <WorkspaceViewIconButton label="Refresh Brain" icon={RefreshCw} onClick={() => setRevision(value => value + 1)} />
          </nav>}
          chat={<div className="flex min-h-0 flex-1 flex-col">
            {bootstrap.backup_configured === false && <div role="status" data-testid="knowledgebase-backup-banner" className="flex shrink-0 flex-wrap items-center justify-between gap-2 border-b border-border bg-muted/60 px-4 py-2 text-sm text-muted-foreground"><span><strong className="font-medium">Backup not configured.</strong> Content changes are saved and available immediately.{!bootstrap.is_admin && ' Ask an administrator to configure backup.'}</span>{bootstrap.is_admin && tabId && <TooltipProvider><AskAIButton workspacePath={null} onAsk={askChat} label="Configure backup" message="Help me configure Brain Git backup. Ask for my repository HTTPS URL and username, then use brain_access action=configure_backup with those details. The optional PAT for a private repository is entered in the secure confirmation field, not in chat. Do not create a commit or push content." /></TooltipProvider>}</div>}
            <div className="min-h-0 flex-1">{tabId ? <ChatArea ref={chatRef} tabId={tabId} compact showProductSteerAction
            composerPlaceholder="Grant, change, or revoke folder access…" knowledgebaseFolderPath={folder}
            onNewChat={() => { newConversation.current = true; setAttempt(value => value + 1) }}
            landingContent={<ProductChatLandingCard icon={ShieldCheck} title="Manage Brain access"
              description="Ask the assistant to inspect folder permissions or manage access. Content is written through your agents’ MCP connections."
              examples={['Who can read this folder?', 'Give Priya read access to Payments', 'Change a folder grant', 'Revoke access to a service']} />}
          /> : <p className="p-5 text-xs text-muted-foreground">Connecting access chat…</p>}</div></div>}
          divider={<WorkspaceSplitRail ratio={ratio} onPointerDown={startResize} onStep={delta => changeRatio(ratio + delta)} className="md:row-start-2" onCollapseChat={() => setCollapsed('chat')} onCollapseWorkspace={() => setCollapsed('workspace')} />}
          workspace={<KnowledgebaseWorkspacePane view={view} folder={folder} onFolder={setFolder} onAsk={askAccess} onGitAsk={askChat} revision={revision} modelSettings={<KnowledgebaseModelSettings tabId={tabId} />} />}
        /></div>}
      </div>
    </div>
  </div>
}
