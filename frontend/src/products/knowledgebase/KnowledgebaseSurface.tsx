import { clearServiceChatRequest, currentServiceChatRequest, useServiceChatNavigation } from '../../utils/serviceChatNavigation'
import { currentProductRoute } from '../../utils/productRoutes'
import { useProductUrlView } from '../../hooks/useProductUrlView'
import { useWorkspaceViewPreference } from '../../hooks/useWorkspaceViewPreference'
import { normalizeViewFrom } from '../../utils/workspaceViewPreference'
import { lazy, Suspense, useCallback, useEffect, useRef, useState, type PointerEvent } from 'react'
import { ArrowLeft, MessageCircle, RefreshCw, ShieldCheck, SlidersHorizontal } from 'lucide-react'
import ChatArea from '../../components/ChatArea'
import { ModePresetBar } from '../../components/ModePresetBar'
import LlmModalHost from '../../components/topbar/LlmModalHost'
import { ProductWorkspaceShell } from '../../components/workspace/ProductWorkspaceShell'
import { WorkspaceSplitDivider } from '../../components/workspace/WorkspaceSplitDivider'
import { WorkspaceToolsGroup, WorkspaceToolsMenu } from '../../components/workspace/WorkspaceToolsMenu'
import { WorkspaceViewIconButton } from '../../components/workflow/WorkspaceViewIconButton'
import { usePointerDrag } from '../../hooks/usePointerDrag'
import { knowledgebaseApi, knowledgebaseError, type KnowledgeAccessProposal, type KnowledgeBootstrap } from '../../services/knowledgebaseApi'
import { useAppStore } from '../../stores/useAppStore'
import { useAuthStore } from '../../stores/useAuthStore'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'
import { useModeStore } from '../../stores/useModeStore'
import { useLLMStore } from '../../stores/useLLMStore'
import { clampWorkSplitRatio } from '../work/workSurfaceLayoutResolver'
import { KnowledgebaseWorkspacePane, type KnowledgebaseView } from './KnowledgebaseWorkspacePane'
import { KnowledgebaseAccessConfirmation } from './KnowledgebaseAccessConfirmation'
import { KnowledgebaseModelSettings } from './KnowledgebaseModelSettings'
import { KnowledgebaseContextPanel } from './KnowledgebaseContextPanel'
import { useKnowledgebaseChat } from './useKnowledgebaseChat'
import { ProductChatLandingCard } from '../../components/chat/ProductChatLandingCard'
import { TooltipProvider } from '../../components/ui/tooltip'
import { AskAIButton } from '../../components/workflow/AskAIButton'
import { activateTab } from '../../utils/activateTab'
import { setProductCommands } from '../../commands/registry'
import { BRAIN_PANELS } from '../productPanels'
import { useRegisterPanelSwitcher } from '../../stores/usePanelSwitcherStore'
import { loadWorkProductCommands } from '../work/workData'
import { toProductCommandDefinitions } from '../work/productCommands'

const AdminPages = lazy(() => import('../../components/AdminPages'))
const SchedulesPage = lazy(() => import('../../components/SchedulesPage'))
const normalizeBrainView = normalizeViewFrom(BRAIN_PANELS.map(view => view.id))
function readRatio(): number { try { const ratio = Number(localStorage.getItem('knowledgebase:split')); return ratio >= .15 && ratio <= .85 ? 1 - ratio : .62 } catch { return .62 } }

export function KnowledgebaseSurface() {
  const account = useAuthStore(state => state.user?.id)
  const server = useWorkspaceConnectionStore(state => state.activeWorkspaceId)
  const scope = JSON.stringify([account, server])
  const [snapshot, setSnapshot] = useState<{ scope: string; data: KnowledgeBootstrap } | null>(null)
  const bootstrap = snapshot?.scope === scope ? snapshot.data : null
  const canManage = !!bootstrap && bootstrap.can_write !== false
  const [error, setError] = useState('')
  const [attempt, setAttempt] = useState(0)
  const [revision, setRevision] = useState(0)
  const [view, setView] = useWorkspaceViewPreference<KnowledgebaseView>('knowledgebase', 'main', 'library', normalizeBrainView)
  const effectiveView = canManage ? view : 'library'
  const [folder, setFolder] = useState(() => currentProductRoute()?.surface === 'knowledgebase' ? currentProductRoute()?.folder ?? '' : '')
  useProductUrlView('knowledgebase', { view: effectiveView, folder })
  const [openScope, setOpenScope] = useState<string | null>(null)
  const chatOpen = canManage && openScope === scope
  const navigation = currentServiceChatRequest(useServiceChatNavigation(state => state.request), 'knowledgebase')
  const chat = useKnowledgebaseChat(bootstrap, scope, canManage && view === 'models', navigation?.tabId, navigation?.token)
  const [ratio, setRatio] = useState(readRatio)
  const [mobilePane, setMobilePane] = useState<'chat' | 'workspace'>('chat')
  const containerRef = useRef<HTMLDivElement>(null)
  const drag = usePointerDrag()
  const showProviders = useLLMStore(state => state.showLLMModal)
  const showSchedules = useAppStore(state => state.showSchedulesOverview)
  const adminPage = useAppStore(state => state.adminPage)
  const globalPage = !!(showProviders || showSchedules || adminPage)
  const views = bootstrap ? BRAIN_PANELS.filter(item => canManage || item.id === 'library') : []
  const openView = (next: KnowledgebaseView) => {
    if (!views.some(item => item.id === next)) return
    setView(next); setMobilePane('chat')
  }
  useRegisterPanelSwitcher('knowledgebase', views, id => { const next = normalizeBrainView(id); if (next) openView(next) })
  const openChat = () => {
    if (!canManage) return
    chat.request(); setOpenScope(scope); setMobilePane('workspace')
  }
  const closeChat = () => { setOpenScope(null); setMobilePane('workspace') }
  const askChat = async (message: string) => {
    if (!canManage) throw new Error('Brain chat is unavailable.')
    openChat()
    await chat.ask(message)
  }
  const askAccess = () => askChat(`Inspect access for folder ${JSON.stringify(folder || 'organization root')}.`)
  const connect = () => { useLLMStore.getState().setShowLLMModal(false); useAppStore.getState().setAdminPage('mcp') }
  useEffect(() => {
    let cancelled = false
    void loadWorkProductCommands('knowledgebase', 1, 'Brain')
      .then(commands => { if (!cancelled) setProductCommands(toProductCommandDefinitions(commands)) })
      .catch(() => { if (!cancelled) setProductCommands([]) })
    return () => { cancelled = true; setProductCommands([]) }
  }, [scope])
  useEffect(() => {
    const controller = new AbortController()
    setError('')
    void knowledgebaseApi.bootstrap(controller.signal).then(data => {
      if (controller.signal.aborted) return
      setSnapshot({ scope, data })
      if (data.can_write === false) setOpenScope(null)
      // Brain uses the shared agent configuration when its assistant is requested.
      useModeStore.getState().setModeCategory('multi-agent')
      useAppStore.getState().setAgentMode('multi-agent')
    }).catch(failure => { if (!controller.signal.aborted) setError(knowledgebaseError(failure)) })
    return () => controller.abort()
  }, [scope, attempt, revision])
  useEffect(() => {
    setOpenScope(null); setMobilePane('chat'); drag.stop()
    setFolder(currentProductRoute()?.surface === 'knowledgebase' ? currentProductRoute()?.folder ?? '' : '')
  }, [scope, drag.stop])
  useEffect(() => {
    if (navigation && canManage) {
      chat.request(); setOpenScope(scope); setMobilePane('workspace')
      return () => clearServiceChatRequest(navigation)
    }
  }, [navigation, canManage, scope])
  useEffect(() => {
    if (chatOpen && chat.tabId && !globalPage) activateTab(chat.tabId)
  }, [chatOpen, chat.tabId, globalPage])
  useEffect(() => {
    const timer = window.setInterval(() => { if (!document.hidden) setRevision(value => value + 1) }, 15000)
    const refresh = () => { if (!document.hidden) setRevision(value => value + 1) }
    document.addEventListener('visibilitychange', refresh)
    return () => { window.clearInterval(timer); document.removeEventListener('visibilitychange', refresh) }
  }, [])
  const [proposals, setProposals] = useState<KnowledgeAccessProposal[]>([])
  const [approvalError, setApprovalError] = useState('')
  const [approving, setApproving] = useState(false)
  useEffect(() => {
    let active = true
    setProposals([]); setApprovalError('')
    if (!canManage) return
    const refresh = () => { knowledgebaseApi.proposals().then(data => { if (active) setProposals(data.proposals) }).catch(() => {}) }
    refresh(); const timer = window.setInterval(refresh, 3000)
    return () => { active = false; window.clearInterval(timer) }
  }, [scope, canManage])
  async function confirmAccess(id: string, approve: boolean, pat?: string) {
    if (!canManage) return
    setApproving(true); setApprovalError('')
    try { await knowledgebaseApi.confirmAccess(id, approve, pat); setProposals(items => items.filter(item => item.id !== id)); setRevision(value => value + 1) }
    catch (failure) { setApprovalError(knowledgebaseError(failure)) }
    finally { setApproving(false) }
  }
  const changeRatio = useCallback((next: number) => {
    const bounded = clampWorkSplitRatio(next, containerRef.current?.getBoundingClientRect().width || window.innerWidth)
    setRatio(bounded)
    try { localStorage.setItem('knowledgebase:split', String(1 - bounded)) } catch { /* Preference only. */ }
  }, [])
  function startResize(event: PointerEvent<HTMLButtonElement>) {
    const bounds = containerRef.current?.getBoundingClientRect()
    if (bounds) drag.start(event, { onMove: clientX => changeRatio((clientX - bounds.left) / bounds.width), onEnd: () => {} })
  }
  const title = BRAIN_PANELS.find(item => item.id === effectiveView)?.label ?? 'Library'
  return <div className="flex h-dvh min-h-0 overflow-hidden bg-background text-foreground" data-product="knowledgebase">
    <ModePresetBar reduced walkthroughPaused />
    <div className="relative min-h-0 min-w-0 flex-1 overflow-hidden">
      <LlmModalHost />
      <Suspense fallback={<div className="grid h-full place-items-center text-sm text-muted-foreground">Opening page…</div>}>
        {showSchedules && !showProviders && <SchedulesPage />}
        {adminPage && !showProviders && <AdminPages />}
      </Suspense>
      <div className={globalPage ? 'hidden' : 'flex h-full min-h-0 flex-col'}>
        {canManage && <KnowledgebaseAccessConfirmation proposals={proposals} error={approvalError} busy={approving} onConfirm={confirmAccess} />}
        {!bootstrap ? <div className="grid h-full place-items-center p-6"><div className="max-w-md text-center">{error ? <><p role="alert" className="text-sm text-destructive">{error}</p><button type="button" onClick={() => setAttempt(value => value + 1)} className="mt-4 rounded-md border border-border px-4 py-2 text-sm">Retry</button></> : <p className="text-sm text-muted-foreground">Opening Brain…</p>}</div></div> : <div className="min-h-0 flex-1" data-testid={!canManage ? 'knowledgebase-reader' : undefined}><ProductWorkspaceShell
          splitRef={containerRef} chatOpen panelOpen splitRatio={ratio} compactContext={!chatOpen} mobilePane={mobilePane} navigationActive={!globalPage}
          onOpenChat={() => setMobilePane('chat')} onOpenWorkspace={() => setMobilePane('workspace')}
          chatProps={{ 'aria-label': 'Brain library and management' }} workspaceProps={{ 'aria-label': chatOpen ? 'Access management chat' : 'Brain overview' }}
          tabs={<div className="flex min-w-0 flex-1 items-center gap-2">{effectiveView !== 'library' && <WorkspaceViewIconButton icon={ArrowLeft} label="Back to library" onClick={() => openView('library')} />}<h1 className="truncate text-sm font-semibold">{title}</h1>{folder && <span className="truncate text-xs text-muted-foreground" title={folder}>{folder}</span>}</div>}
          toolbar={<div className="ml-auto flex shrink-0 items-center gap-1">
            <button type="button" onClick={() => setMobilePane(value => value === 'chat' ? 'workspace' : 'chat')} aria-label="Brain options" className="grid h-8 w-8 place-items-center rounded-lg text-muted-foreground hover:bg-muted md:hidden"><SlidersHorizontal className="h-4 w-4" /></button>
            <WorkspaceToolsMenu>{close => <>
              <WorkspaceToolsGroup title="Brain">{views.map(item => <button type="button" role="menuitem" key={item.id} aria-current={effectiveView === item.id ? 'page' : undefined} onClick={() => { openView(item.id); close() }} className="flex w-full items-center gap-2 rounded-md px-3 py-2 text-left text-xs hover:bg-muted"><item.icon className="h-3.5 w-3.5 text-muted-foreground" />{item.label}</button>)}</WorkspaceToolsGroup>
              {canManage && <WorkspaceToolsGroup title="Assistant"><button type="button" role="menuitem" onClick={() => { openChat(); close() }} className="flex w-full items-center gap-2 rounded-md px-3 py-2 text-left text-xs hover:bg-muted"><MessageCircle className="h-3.5 w-3.5 text-muted-foreground" />Access chat</button></WorkspaceToolsGroup>}
            </>}</WorkspaceToolsMenu>
            <WorkspaceViewIconButton label="Refresh Brain" icon={RefreshCw} onClick={() => setRevision(value => value + 1)} />
          </div>}
          chat={<div className="flex min-h-0 flex-1 flex-col">
            {canManage && bootstrap.backup_configured === false && <div role="status" data-testid="knowledgebase-backup-banner" className="flex shrink-0 flex-wrap items-center justify-between gap-2 border-b border-border bg-muted/60 px-4 py-2 text-xs text-muted-foreground"><span>Backup not configured.{!bootstrap.is_admin && ' Ask an administrator to configure backup.'}</span>{bootstrap.is_admin && <TooltipProvider><AskAIButton workspacePath={null} onAsk={askChat} label="Configure backup" message="Help me configure Brain Git backup. Ask for my repository HTTPS URL and username, then use brain_access action=configure_backup with those details. The optional PAT for a private repository is entered in the secure confirmation field, not in chat. Do not create a commit or push content." /></TooltipProvider>}</div>}
            {error && <p role="status" className="px-4 py-2 text-xs text-destructive">{error}</p>}
            <div className="min-h-0 flex-1"><KnowledgebaseWorkspacePane view={effectiveView} folder={folder} onFolder={setFolder} onAsk={canManage ? askAccess : undefined} onGitAsk={canManage ? askChat : undefined} onConnect={canManage ? connect : undefined} revision={revision} modelSettings={chat.tabId ? <KnowledgebaseModelSettings tabId={chat.tabId} /> : <div className="p-4 text-sm text-muted-foreground">{chat.error ? <><p role="alert">{chat.error}</p><button type="button" onClick={chat.retry} className="mt-2 underline">Retry assistant settings</button></> : 'Opening model settings…'}</div>} /></div>
          </div>}
          divider={<WorkspaceSplitDivider ratio={ratio} onPointerDown={startResize} onStep={delta => changeRatio(ratio + delta)} className="md:row-start-2" />}
          workspace={<div className="h-full min-h-0 overflow-y-auto">
            <div hidden={chatOpen}>
              <button type="button" onClick={() => setMobilePane('chat')} className="mx-4 mt-3 inline-flex items-center gap-2 text-xs text-muted-foreground md:hidden"><ArrowLeft className="h-3.5 w-3.5" />Back to library</button>
              <KnowledgebaseContextPanel bootstrap={bootstrap} folder={folder} view={effectiveView} onOpen={openView} onChat={openChat} onConnect={connect} />
            </div>
            {canManage && <div hidden={!chatOpen} className="h-full min-h-0 flex-col" style={{ display: chatOpen ? 'flex' : 'none' }}>
              <div className="flex shrink-0 items-center gap-2 border-b border-border px-3 py-2"><WorkspaceViewIconButton label="Back to Brain overview" icon={ArrowLeft} onClick={closeChat} /><MessageCircle className="h-4 w-4 text-muted-foreground" /><h2 className="text-sm font-medium">Access chat</h2></div>
              <div className="min-h-0 flex-1">{chat.error ? <div className="p-4 text-sm"><p role="alert" className="text-destructive">{chat.error}</p><button type="button" onClick={chat.retry} className="mt-2 underline">Retry access chat</button></div> : chat.tabId ? <ChatArea ref={chat.chatRef} tabId={chat.tabId} compact showProductSteerAction composerPlaceholder="Grant, change, or inspect folder access…" knowledgebaseFolderPath={folder} onNewChat={chat.newConversation}
                landingContent={<ProductChatLandingCard icon={ShieldCheck} title="Manage Brain access" description="Folder access and backup. Your AI tools add and reuse knowledge through MCP." examples={['Who can read this folder?', 'Change a folder grant']} />} /> : chatOpen ? <p className="p-5 text-xs text-muted-foreground">Opening access chat…</p> : null}</div>
            </div>}
          </div>}
        /></div>}
      </div>
    </div>
  </div>
}
