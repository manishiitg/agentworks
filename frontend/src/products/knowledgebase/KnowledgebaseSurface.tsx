import { lazy, Suspense, useCallback, useEffect, useRef, useState, type PointerEvent } from 'react'
import { BookOpen, MessageCircle, Plug, RefreshCw, ShieldCheck } from 'lucide-react'
import ChatArea, { type ChatAreaRef } from '../../components/ChatArea'
import { ModePresetBar } from '../../components/ModePresetBar'
import LlmModalHost from '../../components/topbar/LlmModalHost'
import { ProductWorkspaceShell } from '../../components/workspace/ProductWorkspaceShell'
import { WorkspaceToolbarFrame } from '../../components/workspace/WorkspaceToolbarFrame'
import { WorkspaceToolbarButton } from '../../components/workspace/WorkspaceToolbarButton'
import { WorkspaceSplitDivider, WorkspaceSplitCollapseControls } from '../../components/workspace/WorkspaceSplitDivider'
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
import { KnowledgebaseModelControl } from './KnowledgebaseModelControl'

const AdminPages = lazy(() => import('../../components/AdminPages'))
const SchedulesPage = lazy(() => import('../../components/SchedulesPage'))

const views = [{ id: 'library', label: 'Library', icon: BookOpen }, { id: 'access', label: 'Access', icon: ShieldCheck }, { id: 'connect', label: 'Connect', icon: Plug }] as const
function readRatio(): number { try { const ratio = Number(localStorage.getItem('knowledgebase:split')); return ratio >= .15 && ratio <= .85 ? ratio : .38 } catch { return .38 } }

export function KnowledgebaseSurface() {
  const [proposals, setProposals] = useState<KnowledgeAccessProposal[]>([])
  const [approvalError, setApprovalError] = useState('')
  const [approving, setApproving] = useState(false)
  useEffect(() => {
    let active = true
    const refresh = () => { knowledgebaseApi.proposals().then(data => { if (active) setProposals(data.proposals) }).catch(() => {}) }
    refresh(); const timer = window.setInterval(refresh, 3000)
    return () => { active = false; window.clearInterval(timer) }
  }, [])
  async function confirmAccess(id: string, approve: boolean) {
    setApproving(true); setApprovalError('')
    try { await knowledgebaseApi.confirmAccess(id, approve); setProposals(items => items.filter(item => item.id !== id)); setRevision(value => value + 1) }
    catch (error) { setApprovalError(knowledgebaseError(error)) }
    finally { setApproving(false) }
  }
  const [bootstrap, setBootstrap] = useState<KnowledgeBootstrap | null>(null)
  const [tabId, setTabId] = useState<string | null>(null)
  const [error, setError] = useState('')
  const [attempt, setAttempt] = useState(0)
  const [view, setView] = useState<KnowledgebaseView>('library')
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
  const drag = usePointerDrag()
  useEffect(() => {
    let cancelled = false
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
      const conversation = await agentApi.resolveAgentProfileConversation('knowledgebase', { conversation_key: 'main' })
      if (cancelled) return
      const id = await store.createChatTab('Access chat', {
        mode: 'multi-agent', agentProfileId: 'knowledgebase', agentProfileVersion: 1,
        agentProfileWorkspace: state.chat_workspace, agentProfileProjectTitle: 'Knowledge Base',
        agentProfileChatContract: 'profile-v1', agentProfileConversationKey: conversation.conversation_key,
        agentProfileConversationId: conversation.conversation_id, agentProfileMCPSelectionInitialized: true,
      }, conversation.session_id)
      store.setTabConfig(id, { selectedServers: ['NO_SERVERS'], selectedSkills: [] })
      if ((store.tabEvents[conversation.session_id]?.length ?? 0) === 0) await hydrateTabEvents(conversation.session_id, { workspacePath: state.chat_workspace, preferChatHistory: true, fallbackToChatHistory: true })
      if (cancelled) return
      store.switchTab(id); setTabId(id)
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
  const changeRatio = useCallback((next: number) => { const bounded = clampWorkSplitRatio(next, containerRef.current?.getBoundingClientRect().width || window.innerWidth); setRatio(bounded); try { localStorage.setItem('knowledgebase:split', String(bounded)) } catch { /* Preference only. */ } }, [])
  function startResize(event: PointerEvent<HTMLButtonElement>) {
    const bounds = containerRef.current?.getBoundingClientRect()
    if (!bounds) return
    drag.start(event, { onMove: clientX => changeRatio((clientX - bounds.left) / bounds.width), onEnd: () => {} })
  }
  function askAccess() {
    setCollapsed(null); setMobilePane('chat')
    if (tabId) useChatStore.getState().switchTab(tabId)
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
        {error ? <div className="grid h-full place-items-center p-6"><div className="max-w-md text-center"><p role="alert" className="text-sm text-destructive">{error}</p><button type="button" onClick={() => setAttempt(value => value + 1)} className="mt-4 rounded-md border border-border px-4 py-2 text-sm">Retry</button></div></div> : !bootstrap ? <div className="grid h-full place-items-center text-sm text-muted-foreground">Opening Knowledge Base…</div> : <div className="min-h-0 flex-1"><ProductWorkspaceShell
          splitRef={containerRef} chatOpen={collapsed !== 'chat'} panelOpen={collapsed !== 'workspace'} splitRatio={ratio} mobilePane={mobilePane}
          onOpenChat={() => setCollapsed(null)} onOpenWorkspace={() => setCollapsed(null)}
          chatProps={{ 'aria-label': 'Access management chat' }} workspaceProps={{ 'aria-label': 'Knowledge Base workspace' }}
          tabs={<div className="flex min-w-0 flex-1 items-center gap-2 text-xs">
            <MessageCircle className="h-3.5 w-3.5 shrink-0 text-primary" /><strong className="truncate">Access chat</strong>
            <span className="hidden min-w-0 truncate text-[10px] text-muted-foreground lg:block" title={folder || 'Organization root'}>{folder || 'Organization root'}</span>
            <button type="button" className="ml-auto grid h-7 w-7 shrink-0 place-items-center rounded-md border border-border text-muted-foreground md:hidden" aria-label={mobilePane === 'chat' ? 'Show knowledge library' : 'Show access chat'} title={mobilePane === 'chat' ? 'Library' : 'Access chat'} onClick={() => { setCollapsed(null); setMobilePane(value => value === 'chat' ? 'workspace' : 'chat') }}>
              {mobilePane === 'chat' ? <BookOpen className="h-3.5 w-3.5" /> : <MessageCircle className="h-3.5 w-3.5" />}
            </button>
          </div>}
          toolbar={<nav aria-label="Knowledge Base workspace toolbar" className="ml-auto flex shrink-0 items-center gap-1">
            <WorkspaceToolbarFrame aria-label="Knowledge Base views"><div className="inline-flex items-center gap-0.5 px-0.5">{views.map(({ id, label, icon }) => <WorkspaceToolbarButton key={id} active={view === id} icon={icon} label={label} onClick={() => { setView(id); setMobilePane('workspace'); if (collapsed === 'workspace') setCollapsed(null) }} />)}</div></WorkspaceToolbarFrame>
            <WorkspaceViewIconButton label="Refresh Knowledge Base" icon={RefreshCw} onClick={() => setRevision(value => value + 1)} />
          </nav>}
          chat={<>
            <div className="min-h-0 flex-1">{tabId ? <ChatArea ref={chatRef} tabId={tabId} inputVariant="product" hideRuntimeStatus fullTurnStreaming composerPlaceholder="Grant, change, or revoke folder access…" knowledgebaseFolderPath={folder} landingContent={<div className="mx-auto max-w-xs p-6 text-center"><ShieldCheck className="mx-auto mb-3 h-8 w-8 text-primary" /><h2 className="text-base font-semibold">Manage folder access</h2><p className="mt-2 text-sm leading-6 text-muted-foreground">Ask who can read a folder, grant Priya access to Payments, or update an existing grant.</p><p className="mt-3 text-xs leading-5 text-muted-foreground">Content is written through your agents’ MCP connections.</p></div>} /> : <p className="p-5 text-xs text-muted-foreground">Connecting access chat…</p>}</div>
            {tabId && <KnowledgebaseModelControl tabId={tabId} />}
          </>}
          divider={<WorkspaceSplitDivider ratio={ratio} onPointerDown={startResize} onStep={delta => changeRatio(ratio + delta)} className="md:row-start-2"><WorkspaceSplitCollapseControls onCollapseChat={() => setCollapsed('chat')} onCollapseWorkspace={() => setCollapsed('workspace')} /></WorkspaceSplitDivider>}
          workspace={<KnowledgebaseWorkspacePane view={view} folder={folder} onFolder={setFolder} onAsk={askAccess} revision={revision} isAdmin={bootstrap.is_admin} />}
        /></div>}
      </div>
    </div>
  </div>
}
