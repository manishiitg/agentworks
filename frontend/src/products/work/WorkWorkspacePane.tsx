import { useAgentInboxEnabled } from '../../components/integrations/AgentInboxPanel'
import { crewMenuTarget, crewProjectMenuItems } from './crewProjectMenu'
import { projectMenuHistoryScope, useProjectMenuHistoryStore } from '../../stores/useProjectMenuHistoryStore'
import { openProductWorkspace } from '../../utils/productWorkspaceNavigation'
import { workWorkspacePanels } from './workWorkspacePanels'
import { WorkContextPanel } from './WorkContextPanel'
import { WorkspacePanelHeader } from '../../components/workspace/WorkspacePanelHeader'
import { WorkspacePanelNavigationProvider } from '../../components/workspace/WorkspacePanelNavigation'
import { Suspense, lazy, memo, useCallback, useEffect, useMemo, useState } from 'react'
import { useShallow } from 'zustand/react/shallow'
import { DollarSign, Laptop, Settings } from 'lucide-react'
import { DashboardCreateDraftButton } from '../../components/workflow/DashboardEmptyState'
import { WorkspaceViewHeader } from '../../components/workflow/WorkspaceViewHeader'
import { TooltipProvider } from '../../components/ui/tooltip'
import { GlobalActivityMonitor } from '../../components/GlobalActivityMonitor'
import { WorkspaceToolbarFrame } from '../../components/workspace/WorkspaceToolbarFrame'
import type { SavedFileSelection } from '../../components/workspace/chatFileNavigation'
import { setWorkspaceViewTarget, useWorkspaceViewTarget } from '../../hooks/useWorkspaceViewTarget'
import { useRegisterPanelSwitcher } from '../../stores/usePanelSwitcherStore'
import { WorkspaceToolbarButton } from '../../components/workspace/WorkspaceToolbarButton'
import api, { agentApi } from '../../services/api'
import type { PresetLLMConfig } from '../../services/api-types'
import { useChatStore } from '../../stores/useChatStore'
import { useWorkspaceStore } from '../../stores/useWorkspaceStore'
import { BrowserWorkspacePanel } from '../../components/workflow/BrowserWorkspacePanel'
import type { BrowserAutomationMode } from '../../components/BrowserAutomationSettings'
import { isBrowserCDPEnabled } from '../../utils/runtimeCapabilities'
import { openWorkspacePaneChatWithDraft, sendWorkspacePaneMessageToChat } from '../../utils/workspacePaneChat'
import type { WorkRuntimeSelection } from './workTabs'
import type { ProductIdentity, ProductIdentityPatch } from '../../platform/chat/productProjects'
import { PreviousChatHistoryPanel } from '../../components/PreviousChatHistoryPanel'
import { useResumePreviousChat } from '../../hooks/useResumePreviousChat'
import { WorkIdentityPanel } from './WorkIdentityPanel'
import { WorkIntegrationsPanel } from './WorkIntegrationsPanel'
import { isWorkWorkspaceViewEnabled } from './workViewGating'
import { useProjectMode } from './codeLocalFiles'
import { WorkSkillsPanel } from './WorkSkillsPanel'
import { WorkMemoryPanel } from './WorkMemoryPanel'
import { WorkPlanPanel } from './WorkPlanPanel'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'
import { useAuthStore } from '../../stores/useAuthStore'
import { SharedCrewFilesPanel } from './SharedCrewFilesPanel'
import { sharedCrewFileClient } from './sharedCrewFiles'
import { useProjectProduct } from './projectProduct'

const ReplyFileViewer = lazy(() => import('../../components/workspace/ReplyFileViewer').then(module => ({ default: module.ReplyFileViewer })))
const ChatFileReview = lazy(() => import('../../components/workspace/ChatFileReview.tsx').then(module => ({ default: module.ChatFileReview })))
const CostsPopup = lazy(() => import('../../components/workflow/CostsPopup'))
const AutomationHubPanel = lazy(() => import('../../components/automation/AutomationHubPanel').then(module => ({ default: module.AutomationHubPanel })))
const ReportView = lazy(() => import('../../components/workflow/ReportViewer').then(module => ({ default: module.ReportView })))
const DatabaseView = lazy(() => import('../../components/workflow/DatabaseView'))
const ReportHumanInputPanel = lazy(() => import('../../components/workflow/ReportHumanInputPanel'))
const CodeShellPanel = lazy(() => import('./CodeShellPanel').then(module => ({ default: module.CodeShellPanel })))
const FileWorkspacePane = lazy(() => import('../../components/FileWorkspacePane').then(module => ({ default: module.FileWorkspacePane })))
const CodeFilesPanel = lazy(() => import('./CodeFilesPanel').then(module => ({ default: module.CodeFilesPanel })))

export type WorkWorkspaceView = 'context' | 'dashboard' | 'plan' | 'memory' | 'database' | 'files' | 'browser' | 'costs' | 'schedules' | 'history' | 'suggestions' | 'identity' | 'mcp' | 'shell'

function sendWorkProjectPaneMessage(projectId: string, message: string, profileId = 'work') {
  return sendWorkspacePaneMessageToChat({ profileId, conversationKey: projectId, message })
}

// Panel lists live in products/productPanels.ts with every other product's panels.

export const WorkWorkspaceToolbar = memo(function WorkWorkspaceToolbar({ sessionId = '', projectId = '', workspacePath, onViewChange, enabledPanels, readOnly, showShell = false, showActivityMonitor = true }: { sessionId?: string; projectId?: string; tabId?: string; onOpenChat?: () => void; workspacePath: string; view: WorkWorkspaceView; onViewChange: (view: WorkWorkspaceView) => void; enabledPanels?: Set<string>; readOnly?: boolean; showShell?: boolean; showActivityMonitor?: boolean }) {
  // Both legacy origins use the merged Crew suggestion feature.
  const product = useProjectProduct()
  const representedSessionIds = useChatStore(useShallow(state => Object.values(state.chatTabs)
    .filter(tab => tab.metadata?.agentProfileWorkspace === workspacePath)
    .map(tab => tab.sessionId).filter((id): id is string => Boolean(id))))
  const projectMode = useProjectMode(sessionId)
  const isCowork = projectMode === 'cowork'
  const incomingEmail = useAgentInboxEnabled()
  const user = useAuthStore(state => state.user)
  const server = useWorkspaceConnectionStore(state => state.activeWorkspaceId)
  const inventory = workWorkspacePanels({ profileId: product.profileId, cowork: isCowork, readOnly: Boolean(readOnly), showShell, enabledPanels })
  const admitted = [...inventory.views, ...inventory.ops, ...inventory.setup]
  const menuItems = crewProjectMenuItems({ panels: admitted, allowedProducts: user?.allowed_products, enabledPanels, incomingEmail, projectId })
  useRegisterPanelSwitcher('work', [
    ...(!readOnly ? [{ id: 'context', label: 'Project menu' }] : []),
    ...menuItems.map(item => ({ id: item.id, label: item.label, group: item.group })),
  ], (key, section) => {
    if (key === 'context') { onViewChange('context'); return }
    const legacy = key === 'mcp:slack' ? 'mcp:slack-bot' : section ? key + ':' + section : key
    const current = useAuthStore.getState().user
    const item = crewProjectMenuItems({ panels: admitted, allowedProducts: current?.allowed_products, enabledPanels, incomingEmail, projectId }).find(candidate => candidate.id === legacy)
    if (!item || current?.id !== user?.id) return
    useProjectMenuHistoryStore.getState().visit(projectMenuHistoryScope(server || 'local', user?.id || 'pending', product.profileId, projectId || workspacePath), item.id)
    if (item.external) { openProductWorkspace(item.external); return }
    if (!item.view) return
    if (item.target) setWorkspaceViewTarget(crewMenuTarget(item.view), item.target)
    onViewChange(item.view)
  })

  return <div data-tour="work-tools" className="order-last ml-auto flex shrink-0 items-center gap-1">
    <TooltipProvider delayDuration={150}>
      {showActivityMonitor && <GlobalActivityMonitor representedSessionIds={representedSessionIds} />}
    </TooltipProvider>
  </div>
})

export function WorkBrowserPanel({ tabId, workspacePath }: { tabId: string; workspacePath: string }) {
  const product = useProjectProduct()
  const savedMode = useChatStore(state => state.chatTabs[tabId]?.config.browserMode)
  const savedPort = useChatStore(state => state.chatTabs[tabId]?.config.cdpPort ?? 9222)
  const [browserMode, setBrowserMode] = useState<BrowserAutomationMode>(savedMode)
  const [canonicalSettings, setCanonicalSettings] = useState<{ mode: BrowserAutomationMode; port: number }>({
    mode: savedMode, port: savedPort,
  })
  const [cdpPort, setCdpPort] = useState(savedPort)
  useEffect(() => {
    let live = true
    api.get<{ mode: BrowserAutomationMode; port: number }>('/api/browser/workspace', {
      params: { workspace_path: workspacePath, profile_id: product.profileId },
    }).then(({ data }) => {
      if (live) { setCanonicalSettings(data); setBrowserMode(data.mode); setCdpPort(data.port) }
    }).catch(() => {})
    return () => { live = false }
  }, [workspacePath, product.profileId])
  const [cdpConnected, setCdpConnected] = useState<boolean | null>(null)
  const [cdpError, setCdpError] = useState<string | null>(null)
  const [cdpChecking, setCdpChecking] = useState(false)
  const [saving, setSaving] = useState(false)
  const dirty = browserMode !== canonicalSettings.mode || cdpPort !== canonicalSettings.port

  useEffect(() => {
    setBrowserMode(canonicalSettings.mode)
    setCdpPort(canonicalSettings.port)
  }, [canonicalSettings, tabId])

  const checkCdpConnection = useCallback(async (port: number) => {
    if (!isBrowserCDPEnabled()) {
      setCdpConnected(false)
      setCdpError('CDP is disabled on this server deployment.')
      return
    }
    setCdpChecking(true)
    setCdpConnected(null)
    setCdpError(null)
    try {
      const result = await agentApi.checkCdpPort(port)
      setCdpConnected(result.connected)
      setCdpError(result.connected ? null : result.error || null)
    } catch {
      setCdpConnected(false)
      setCdpError('Unable to check the CDP port.')
    } finally {
      setCdpChecking(false)
    }
  }, [])

  const save = useCallback(async () => {
    const store = useChatStore.getState()
    const current = store.getTab(tabId)
    const projectId = current?.metadata?.agentProfileProjectId
    setSaving(true)
    try {
      await api.post('/api/browser/workspace', { action: 'save', mode: browserMode, port: cdpPort }, {
        params: { workspace_path: workspacePath, profile_id: product.profileId },
      })
      setCanonicalSettings({ mode: browserMode, port: cdpPort })
      for (const tab of Object.values(store.chatTabs)) {
        if (tab.tabId !== tabId && (!projectId || tab.metadata?.agentProfileProjectId !== projectId)) continue
        store.setTabConfig(tab.tabId, { browserMode, cdpPort, enableBrowserAccess: browserMode !== 'none', useCdp: browserMode === 'cdp' })
      }
    } catch {
      store.addToast('Unable to save project browser settings', 'error')
    } finally {
      setSaving(false)
    }
  }, [browserMode, cdpPort, tabId, workspacePath, product.profileId])

  return (
    <BrowserWorkspacePanel
      workspacePath={workspacePath}
      browserMode={browserMode}
      onBrowserModeChange={setBrowserMode}
      cdpPort={cdpPort}
      onCdpPortChange={setCdpPort}
      cdpConnected={cdpConnected}
      cdpError={cdpError}
      cdpChecking={cdpChecking}
      onCheckCdpConnection={checkCdpConnection}
      dirty={dirty}
      saving={saving}
      onSave={save}
      scopeNoun="project"
      profileId={product.profileId}
    />
  )
}

export const WorkWorkspacePane = memo(function WorkWorkspacePane({ workspacePath, filesPath, projectId, projectTitle, projectDescription, projectIdentity, tabId, view, enabledPanels, projectLLMConfig, selectedSecrets, selectedGlobalSecrets, workflowContextPaths, onViewChange, onRuntimeChange, nativeAgentTools, onNativeAgentToolsChange, onAppsSaved, onSelectedServersChange, onSelectedSkillsChange, onSelectedSecretsChange, onSelectedGlobalSecretsChange, onWorkflowContextPathsChange, onUpdateIdentity, onDeleteRequest, shared, onOpenChat, showShell = false }: { showShell?: boolean; workspacePath: string; filesPath?: string; projectId: string; projectTitle: string; projectDescription: string; projectIdentity?: ProductIdentity; tabId: string; view: WorkWorkspaceView; enabledPanels?: Set<string>; projectLLMConfig?: PresetLLMConfig; selectedSecrets: string[]; selectedGlobalSecrets: string[]; workflowContextPaths: string[]; onViewChange: (view: WorkWorkspaceView) => void; onRuntimeChange: (selection: WorkRuntimeSelection) => void | Promise<void>; nativeAgentTools?: boolean; onNativeAgentToolsChange?: (enabled: boolean) => Promise<unknown>; onAppsSaved?: () => void; onSelectedServersChange: (servers: string[]) => Promise<unknown>; onSelectedSkillsChange: (skills: string[]) => Promise<unknown>; onSelectedSecretsChange: (secrets: string[]) => Promise<unknown>; onSelectedGlobalSecretsChange: (secrets: string[]) => Promise<unknown>; onWorkflowContextPathsChange: (paths: string[]) => Promise<unknown>; onUpdateIdentity: (patch: ProductIdentityPatch) => Promise<unknown>; onDeleteRequest: () => void; onOpenChat?: () => void; shared?: { ownerId: string; ownerUsername?: string } }) {
  const readOnly = Boolean(shared)
  const accountKey = useAuthStore(state => `${state.user?.id || ''}:${state.user?.can_edit}`)
  const serverKey = useWorkspaceConnectionStore(state => state.activeWorkspaceId)
  const accessKey = `${serverKey}:${accountKey}:${workspacePath}:${Boolean(shared)}`
  const [projectAccess, setProjectAccess] = useState<{ key: string; canBuild: boolean } | null>(null)
  useEffect(() => {
    if (shared) return
    let cancelled = false
    void api.get<{ can_build: boolean }>('/api/workspace-access', { params: { workspace: workspacePath } })
      .then(({ data }) => {
        if (!cancelled) setProjectAccess({ key: accessKey, canBuild: data.can_build === true })
      })
      .catch(() => {
        if (!cancelled) setProjectAccess({ key: accessKey, canBuild: false })
      })
    return () => { cancelled = true }
  }, [accessKey, workspacePath, shared])
  const canEditConnections = !shared && projectAccess?.key === accessKey && projectAccess.canBuild
  const product = useProjectProduct()
  const noun = product.noun
  const ask = useCallback((message: string) => product.profileId === 'work'
    ? sendWorkspacePaneMessageToChat({ tabId, message })
    : sendWorkProjectPaneMessage(projectId, message, product.profileId), [product.profileId, projectId, tabId])
  const usesExistingFolder = Boolean(filesPath && filesPath !== workspacePath)

  const sharedFiles = useMemo(
    () => (readOnly ? sharedCrewFileClient(projectId, workspacePath, product.profileId) : null),
    [readOnly, projectId, workspacePath, product.profileId],
  )
  const [sharedFileRequest, setSharedFileRequest] = useState<{ path: string; nonce: number } | null>(null)
  const [openLocalFilesSettings, setOpenLocalFilesSettings] = useState(false)
  const openHistoryChat = useResumePreviousChat()
  const activeSessionId = useChatStore(state => state.chatTabs[tabId]?.sessionId ?? undefined)
  const contextMode = useProjectMode(activeSessionId || '')
  const [reviewOpen, setReviewOpen] = useState(false)
  const [reviewOutsideGit, setReviewOutsideGit] = useState(false)
  const [savedFile, setSavedFile] = useState<SavedFileSelection | null>(null)
  useEffect(() => { setReviewOpen(false); setSavedFile(null) }, [tabId, contextMode, accessKey])
  useWorkspaceViewTarget('files', target => {
    setSavedFile(null); setReviewOpen(target === 'review' || target === 'review-outside-git'); setReviewOutsideGit(target === 'review-outside-git')
    if (target.startsWith('saved:')) {
      try {
        const selection = JSON.parse(target.slice(6)) as SavedFileSelection
        if (selection.reference?.sessionId === activeSessionId && selection.reference.workspacePath === workspacePath && selection.change?.path) setSavedFile(selection)
      } catch { /* Ignore stale navigation targets. */ }
    }
  }, view === 'files')
  const serverFiles = <FileWorkspacePane showGit={contextMode === 'dev'} workspacePath={filesPath || workspacePath} onAsk={async message => { await ask(message) }} hiddenRootFolders={usesExistingFolder ? [] : ['.git', 'node_modules', 'product.json', 'workflow.json']} hideManagedEntriesByDefault={!usesExistingFolder} title="Workspace" hideAddToChat hideRootActions editable testId="work-files-panel" />
  const contextPanels = workWorkspacePanels({ profileId: product.profileId, cowork: product.profileId === 'work' && contextMode === 'cowork', readOnly: Boolean(readOnly), showShell, enabledPanels })
  // The dashboard's only link to the chat beside it: a stable callback, so
  // nothing on the chat side re-renders or re-runs the dashboard.
  const sendDashboardMessage = useCallback(async (message: string) => ({
    status: 'queued' as const,
    ...await ask(`From this project's dashboard:\n\n${message}`),
  }), [ask])
  const canonicalSessionId = useChatStore(state => Object.values(state.chatTabs).find(tab =>
    tab.metadata?.agentProfileId === product.profileId &&
    tab.metadata?.agentProfileProjectId === projectId &&
    tab.metadata?.agentProfileConversationKey === projectId &&
    tab.metadata?.isViewOnly !== true,
  )?.sessionId)

  const updateSecretSelection = async (secrets: string[]) => {
    try {
      await onSelectedSecretsChange(secrets)
    } catch (cause) {
      useChatStore.getState().addToast(cause instanceof Error ? cause.message : 'Could not save project secret selection.', 'error')
    }
  }

  const openProjectFile = async (filePath: string) => {
    if (readOnly) {
      // The proxy refuses cross-user reads, so shared files open in the
      // mediated browser instead of the workspace viewer.
      onViewChange('files')
      setSharedFileRequest(current => ({ path: filePath, nonce: (current?.nonce ?? 0) + 1 }))
      return
    }
    const fileName = filePath.split('/').filter(Boolean).pop() || filePath
    const workspace = useWorkspaceStore.getState()
    workspace.setSelectedFile({ name: fileName, path: filePath })
    workspace.setBinaryFileData(null)
    workspace.setLoadingFileContent(true)
    workspace.setShowFileContent(true)
    workspace.expandFoldersForFile(filePath)
    void workspace.highlightFile(filePath)
    onViewChange('files')
    try {
      const response = await agentApi.getPlannerFileContent(filePath)
      if (!response.success || !response.data) throw new Error(response.message || 'Could not open skill file.')
      workspace.setFileContent(String(response.data.content ?? ''))
    } catch (cause) {
      workspace.setShowFileContent(false)
      useChatStore.getState().addToast(cause instanceof Error ? cause.message : 'Could not open skill file.', 'error')
    } finally {
      workspace.setLoadingFileContent(false)
    }
  }

  if (!isWorkWorkspaceViewEnabled(view, enabledPanels)) {
    return <div className="grid h-full place-items-center bg-background text-sm text-muted-foreground">No workspace view is enabled for this product.</div>
  }

  // Belt and braces behind the toolbar filter and the surface's view
  // fallback: a stale saved view or an agent-driven view request must never
  // render an owner-only panel (identity editors, transcripts, usage)
  // for someone else's Crew.
  if (view === 'shell' && !showShell) {
    return <div className="grid h-full place-items-center bg-background p-6 text-center text-sm text-muted-foreground">The terminal is only available to the owner of this Code.</div>
  }
  if (readOnly && view !== 'dashboard') {
    return <div className="grid h-full place-items-center bg-background p-6 text-center text-sm text-muted-foreground">This workspace view is only available to the {noun} owner.</div>
  }

  return (
    <WorkspacePanelNavigationProvider workspacePath={workspacePath} view={view} chatTarget={{ tabId }} onOpenChat={onOpenChat}>
    <div className="relative flex h-full min-h-0 flex-col bg-background">
      <WorkspacePanelHeader workspacePath={workspacePath} view={view} title={view === 'context' ? 'Project menu' : [...contextPanels.views, ...contextPanels.ops, ...contextPanels.setup].find(panel => panel.id === view)?.label ?? view} contextAllowed={!readOnly} onBack={() => onViewChange('context')} />
      <div className="min-h-0 flex-1 overflow-hidden">
        {view==='context' && <WorkContextPanel mode={contextMode} reviewSessionId={contextMode === 'dev' ? activeSessionId : undefined} workspacePath={workspacePath} projectId={projectId} profileId={product.profileId} panels={[...contextPanels.views,...contextPanels.ops,...contextPanels.setup]} onOpen={onViewChange} enabledPanels={enabledPanels} dashboardChatTarget={product.profileId === 'work' ? { tabId } : { profileId: product.profileId, conversationKey: projectId }}/>}
        {view === 'files' && (readOnly ? <SharedCrewFilesPanel
          projectId={projectId}
          crewRoot={workspacePath}
          request={sharedFileRequest}
        /> : <Suspense fallback={<div className="grid h-full place-items-center text-sm text-muted-foreground">Loading…</div>}>{savedFile && contextMode === 'dev' ? <ReplyFileViewer key={JSON.stringify(savedFile)} selection={savedFile} tabId={tabId} onBack={() => { setSavedFile(null); setReviewOpen(true) }} /> : reviewOpen && contextMode === 'dev' && activeSessionId ? <ChatFileReview key={`${accessKey}:${activeSessionId}:${reviewOutsideGit}`} outsideGit={reviewOutsideGit} workspacePath={workspacePath} sessionId={activeSessionId} tabId={tabId} onBrowse={() => setReviewOpen(false)} /> : product.profileId === 'work' ? <CodeFilesPanel key={activeSessionId || workspacePath} sessionId={activeSessionId || ''} serverFiles={serverFiles} onAsk={async message => { await ask(message) }} /> : serverFiles}</Suspense>)}
        {view === 'shell' && showShell && <Suspense fallback={<div className="grid h-full place-items-center text-sm text-muted-foreground">Loading…</div>}><div className="flex h-full min-h-0 flex-col"><div className="min-h-0 flex-1"><CodeShellPanel projectId={projectId} /></div></div></Suspense>}
        {view === 'identity' && <WorkIdentityPanel

          workspacePath={workspacePath}
          shared={Boolean(shared)}
          projectId={projectId}
          projectTitle={projectTitle}
          projectDescription={projectDescription}
          projectIdentity={projectIdentity}
          tabId={tabId}
          openLocalFilesSettings={openLocalFilesSettings}
          onLocalFilesSettingsOpened={() => setOpenLocalFilesSettings(false)}
          selectedSecrets={selectedSecrets}
          selectedGlobalSecrets={selectedGlobalSecrets}
          projectLLMConfig={projectLLMConfig}
          enabledPanels={enabledPanels}
          onRuntimeChange={onRuntimeChange}
          nativeAgentTools={nativeAgentTools}
          onNativeAgentToolsChange={shared ? undefined : onNativeAgentToolsChange}
          onSelectedSecretsChange={updateSecretSelection}
          onSelectedGlobalSecretsChange={onSelectedGlobalSecretsChange}
          onUpdateIdentity={onUpdateIdentity}
          onDeleteRequest={onDeleteRequest}
        />}
        {view === 'plan' && <WorkPlanPanel
          workspacePath={workspacePath}
          onCreatePlan={() => { void openWorkspacePaneChatWithDraft({
            profileId: product.profileId,
            conversationKey: projectId,
            message: 'Help me create a plan for this project. Let us define the steps and save the plan in the project.',
          }).catch(error => {
            useChatStore.getState().addToast(error instanceof Error ? error.message : 'Could not open project chat.', 'error')
          }) }}
        />}
        {view === 'mcp' && <WorkIntegrationsPanel
          canEdit={canEditConnections}
          accessLoading={!shared && projectAccess?.key !== accessKey}
          selectedSecrets={selectedSecrets} onAppsSaved={onAppsSaved}
          onSelectedSecretsChange={onSelectedSecretsChange}
          key={workspacePath}
          workspacePath={workspacePath}
          projectId={projectId}
          projectTitle={projectTitle}
          tabId={tabId}
          enabledPanels={enabledPanels}
          onAsk={async message => { await ask(message) }}
          workflowContextPaths={workflowContextPaths}
          onWorkflowContextPathsChange={onWorkflowContextPathsChange}
        />}
        {view === 'memory' && <WorkMemoryPanel
          workspacePath={workspacePath}
          onOpenFile={filePath => { void openProjectFile(filePath) }}
          fileClient={sharedFiles ?? undefined}
          memoryEnabled={!enabledPanels || enabledPanels.has('memory')}
          skillsEnabled={!enabledPanels || enabledPanels.has('skills')}
          skillsManager={<WorkSkillsPanel workspacePath={workspacePath} projectId={projectId} tabId={tabId} onOpenFile={filePath => { void openProjectFile(filePath) }} onSelectedSkillsChange={onSelectedSkillsChange} />}
        />}
        <Suspense fallback={<div className="grid h-full place-items-center text-sm text-muted-foreground">Loading…</div>}>
          {view === 'dashboard' && <ReportView
            workspacePath={workspacePath}
            dashboardConversation
            crewConversationPane={readOnly}
            emptyDescription={readOnly ? "No dashboard is available right now. Check again, or ask the project owner for help." : 'Live data, forms, agent actions and visual stories. Describe what your team needs in chat.'}
            emptyAction={!readOnly && <DashboardCreateDraftButton target={product.profileId === 'work' ? { tabId } : { profileId: product.profileId, conversationKey: projectId }} />}
            sendChatMessage={sendDashboardMessage}
          />}
          {view === 'database' && <DatabaseView workspacePath={workspacePath} />}
          {view === 'suggestions' && <div className="flex h-full min-h-0 flex-col">
            <WorkspaceViewHeader title="Suggestions" />
            <div className="min-h-0 flex-1 space-y-4 overflow-auto p-4">
              <p className="text-sm leading-6 text-muted-foreground">Changes other people using this {noun} asked for. Accepting records your decision; make the change in chat.</p>
              <ReportHumanInputPanel workspacePath={workspacePath} chatTarget={{ profileId: product.profileId, conversationKey: projectId }} source="user_suggestion" showEmptyState />
            </div>
          </div>}
          {view === 'browser' && <WorkBrowserPanel tabId={tabId} workspacePath={workspacePath} />}
          {view === 'costs' && <CostsPopup projectMode workspacePath={workspacePath} runFolders={[]} selectedRunFolder={null} emptyHint="Send a message to see this project's usage here." />}
          {view === 'history' && <div className="flex h-full min-h-0 flex-col"><PreviousChatHistoryPanel
              workspacePath={workspacePath}
              activeSessionId={canonicalSessionId || activeSessionId}
              title="Chat history"
              emptyText={`No earlier chats for this ${product.itemNoun}.`}
              recentOnly
              includeAutomationChats
              allowOpen
              openOnRowClick
              runEntityType="product"
              productTriggerScope={{ profileId: product.profileId, projectId }}
              fill
              showAll
              actionLabel="Open"
              onSelectSession={openHistoryChat}
            /></div>}
          {view === 'schedules' && <AutomationHubPanel
            entityType="product"
            workspacePath={workspacePath}
            // A Code's schedules and triggers are its owner's: they run as the
            // owner, so someone the Code is shared with only sees them.
            canManage={!(product.profileId === 'work' && shared)}
            scopeNoun="project"
            productTriggerScope={enabledPanels?.has('triggers') === false ? undefined : { profileId: product.profileId, projectId }}
            workflowScope={{ workflowId: projectId, workspacePath, label: projectTitle }}
            onAskAI={async message => { await ask(message) }}
          />}
        </Suspense>
      </div>
    </div>
    </WorkspacePanelNavigationProvider>
  )
})
