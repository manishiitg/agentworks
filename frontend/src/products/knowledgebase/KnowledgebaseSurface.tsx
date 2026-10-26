import { clearServiceChatRequest, currentServiceChatRequest, useServiceChatNavigation } from '../../utils/serviceChatNavigation'
import { currentProductRoute } from '../../utils/productRoutes'
import { useProductUrlView } from '../../hooks/useProductUrlView'
import { useWorkspaceViewPreference } from '../../hooks/useWorkspaceViewPreference'
import { normalizeViewFrom } from '../../utils/workspaceViewPreference'
import { lazy, Suspense, useCallback, useEffect, useRef, useState, type PointerEvent } from 'react'
import { MessageCircle, SlidersHorizontal, X } from 'lucide-react'
import ChatArea from '../../components/ChatArea'
import { ModePresetBar } from '../../components/ModePresetBar'
import { ProductBreadcrumb } from '../../components/workspace/ProductBreadcrumb'
import LlmModalHost from '../../components/topbar/LlmModalHost'
import { ProductWorkspaceShell } from '../../components/workspace/ProductWorkspaceShell'
import { WorkspaceSplitDivider } from '../../components/workspace/WorkspaceSplitDivider'
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
import { KnowledgebaseBackupPanel } from './KnowledgebaseBackupPanel'
import { useKnowledgebaseChat } from './useKnowledgebaseChat'
import { ProductChatLandingCard } from '../../components/chat/ProductChatLandingCard'
import { activateTab } from '../../utils/activateTab'
import { setProductCommands } from '../../commands/registry'
import { BRAIN_PANELS } from '../productPanels'
import { useRegisterPanelSwitcher } from '../../stores/usePanelSwitcherStore'

const KnowledgebaseUseCrew = lazy(() => import('./KnowledgebaseUseCrew').then(module => ({ default: module.KnowledgebaseUseCrew })))

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
  useRegisterPanelSwitcher('knowledgebase', views.filter(item => ['library', 'access', 'backup', 'crew'].includes(item.id)), id => { const next = normalizeBrainView(id); if (next) openView(next) })
  const closeChat = () => { setOpenScope(null); setMobilePane('chat') }
  useEffect(() => {
    const home = (event: Event) => { if ((event as CustomEvent).detail === 'knowledgebase') { setView('library'); setFolder(''); setOpenScope(null); setMobilePane('chat') } }
    window.addEventListener('open-product-home', home)
    return () => window.removeEventListener('open-product-home', home)
  }, [setView])
  const connect = () => { useLLMStore.getState().setShowLLMModal(false); useAppStore.getState().setAdminPage('mcp') }
  useEffect(() => { setProductCommands([]); return () => setProductCommands([]) }, [scope])
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
    refresh(); const timer = window.setInterval(refresh, 15000)
    return () => { active = false; window.clearInterval(timer) }
  }, [scope, canManage, revision])
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
  return <div className="flex h-full min-h-0 overflow-hidden bg-background text-foreground" data-product="knowledgebase">
    <ModePresetBar reduced />
    <div className="relative min-h-0 min-w-0 flex-1 overflow-hidden">
      <LlmModalHost />
      <Suspense fallback={<div className="grid h-full place-items-center text-sm text-muted-foreground">Opening page…</div>}>
        {showSchedules && !showProviders && <SchedulesPage />}
        {adminPage && !showProviders && <AdminPages />}
      </Suspense>
      <div className={globalPage ? 'hidden' : 'flex h-full min-h-0 flex-col'}>
        {canManage && <KnowledgebaseAccessConfirmation proposals={proposals} error={approvalError} busy={approving} onConfirm={confirmAccess} />}
        {!bootstrap ? <div className="grid h-full place-items-center p-6"><div className="max-w-md text-center">{error ? <><p role="alert" className="text-sm text-destructive">{error}</p><button type="button" onClick={() => setAttempt(value => value + 1)} className="mt-4 rounded-md border border-border px-4 py-2 text-sm">Retry</button></> : <p className="text-sm text-muted-foreground">Opening Brain…</p>}</div></div> : <div className="min-h-0 flex-1" data-testid={!canManage ? 'knowledgebase-reader' : undefined}><ProductWorkspaceShell
          splitRef={containerRef} chatOpen panelOpen={chatOpen} retainWorkspace={canManage && Boolean(chat.tabId)} toolbarWhenPanelClosed splitRatio={ratio} mobilePane={mobilePane} navigationActive={!globalPage}
          onOpenChat={() => setMobilePane('chat')} onOpenWorkspace={() => setMobilePane('workspace')}
          chatProps={{ 'aria-label': 'Brain library and management' }} workspaceProps={{ 'aria-label': 'Previous Brain conversation' }}
          tabs={<div className="flex min-w-0 flex-1 items-center gap-2"><ProductBreadcrumb surface="knowledgebase" menuItems={[
            ...views.filter(item => ['library', 'access', 'crew', 'backup'].includes(item.id)).map(item => ({ ...item, active: effectiveView === item.id, detail: item.id === 'backup' ? bootstrap.backup_configured ? 'Configured' : 'Not set up' : undefined, attention: item.id === 'backup' && !bootstrap.backup_configured && bootstrap.is_admin, onSelect: () => openView(item.id) })),
          ]} section={effectiveView === 'library' ? undefined : title} onHome={() => { openView('library'); setFolder(''); closeChat() }} />{folder && <span className="truncate text-xs text-muted-foreground" title={folder}>{folder}</span>}</div>}
          toolbar={<div className="ml-auto flex shrink-0 items-center gap-1">
            {chatOpen && <button type="button" onClick={() => setMobilePane(value => value === 'chat' ? 'workspace' : 'chat')} aria-label={mobilePane === 'chat' ? 'Show previous Brain conversation' : 'Show Brain library'} className="grid h-8 w-8 place-items-center rounded-lg text-muted-foreground hover:bg-muted md:hidden"><SlidersHorizontal className="h-4 w-4" /></button>}
          </div>}
          chat={<div className="flex min-h-0 flex-1 flex-col">
            {error && <p role="status" className="px-4 py-2 text-xs text-destructive">{error}</p>}
            <div className="min-h-0 flex-1"><KnowledgebaseWorkspacePane view={effectiveView} folder={folder} onFolder={setFolder} onRefresh={() => setRevision(value => value + 1)} onAsk={undefined} onGitAsk={undefined} onConnect={canManage ? connect : undefined} revision={revision} onOpenAccess={canManage ? () => openView('access') : undefined} onUseCrew={canManage ? () => openView('crew') : undefined} onPrepared={() => setRevision(value => value + 1)} isAdmin={bootstrap.is_admin} crew={<Suspense fallback={<p className="p-4 text-sm text-muted-foreground">Loading Crews…</p>}><KnowledgebaseUseCrew key={scope} /></Suspense>} backup={<KnowledgebaseBackupPanel bootstrap={bootstrap} onPrepared={() => setRevision(value => value + 1)} onSecrets={() => openView('secrets')} />} modelSettings={chat.tabId ? <KnowledgebaseModelSettings tabId={chat.tabId} /> : <div className="p-4 text-sm text-muted-foreground">{chat.error ? <><p role="alert">{chat.error}</p><button type="button" onClick={chat.retry} className="mt-2 underline">Retry assistant settings</button></> : 'Opening model settings…'}</div>} /></div>
          </div>}
          divider={<WorkspaceSplitDivider ratio={ratio} onPointerDown={startResize} onStep={delta => changeRatio(ratio + delta)} className="md:row-start-2" />}
          workspace={<div className="h-full min-h-0 overflow-y-auto">
            {canManage && <div hidden={!chatOpen} className="h-full min-h-0 flex-col" style={{ display: chatOpen ? 'flex' : 'none' }}>
              <div className="flex shrink-0 items-center gap-2 border-b border-border px-3 py-2"><WorkspaceViewIconButton label="Close previous Brain conversation" icon={X} onClick={closeChat} /><MessageCircle className="h-4 w-4 text-muted-foreground" /><h2 className="text-sm font-medium">Previous Brain conversation</h2></div>
              <div className="min-h-0 flex-1">{chat.error ? <div className="p-4 text-sm"><p role="alert" className="text-destructive">{chat.error}</p><button type="button" onClick={chat.retry} className="mt-2 underline">Retry history</button></div> : chat.tabId ? <ChatArea ref={chat.chatRef} tabId={chat.tabId} compact hideInput previousChatsWorkspacePath={bootstrap.chat_workspace} knowledgebaseFolderPath={folder}
                landingContent={<ProductChatLandingCard icon={MessageCircle} title="Previous Brain conversation" examples={[]} description="Revisit previous conversations here. Use a Crew for new knowledge work." />} /> : chatOpen ? <p className="p-5 text-xs text-muted-foreground">Opening previous conversation…</p> : null}</div>
            </div>}
          </div>}
        /></div>}
      </div>
    </div>
  </div>
}
