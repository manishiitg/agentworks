import { Suspense, lazy, memo, useCallback, useEffect, useMemo, useState } from 'react'
import { Settings } from 'lucide-react'
import { AskAIButton } from '../../components/workflow/AskAIButton'
import { WorkspaceViewActions } from '../../components/workflow/WorkspaceViewActions'
import { WorkspacePanelGuideContext } from '../../components/workflow/WorkspacePanelGuideContext'
import { TooltipProvider } from '../../components/ui/tooltip'
import { GlobalActivityMonitor } from '../../components/GlobalActivityMonitor'
import { WorkspaceToolbarFrame } from '../../components/workspace/WorkspaceToolbarFrame'
import { WORK_PANELS, WORK_PANEL_SECTIONS } from '../productPanels'
import { setWorkspaceViewTarget } from '../../hooks/useWorkspaceViewTarget'
import { useRegisterPanelSwitcher } from '../../stores/usePanelSwitcherStore'
import { WorkspaceToolbarGroup } from '../../components/workspace/WorkspaceToolbarGroup'
import { WorkspaceToolbarButton } from '../../components/workspace/WorkspaceToolbarButton'
import { ReportDocumentSwitcher } from '../../components/workflow/ReportDocumentSwitcher'
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
import type { CrewTemplateId } from './crewTemplates'
import { isWorkWorkspaceViewEnabled } from './workViewGating'
import { useCodeFilesPreference } from './codeLocalFiles'
import { WorkMemoryPanel } from './WorkMemoryPanel'
import { WorkPlanPanel } from './WorkPlanPanel'
import { SharedCrewFilesPanel } from './SharedCrewFilesPanel'
import { sharedCrewFileClient } from './sharedCrewFiles'
import { useProjectProduct } from './projectProduct'
import { useBrowserToolbarConnection } from '../../hooks/useBrowserToolbarConnection'

const CostsPopup = lazy(() => import('../../components/workflow/CostsPopup'))
const AutomationHubPanel = lazy(() => import('../../components/automation/AutomationHubPanel').then(module => ({ default: module.AutomationHubPanel })))
const ReportView = lazy(() => import('../../components/workflow/ReportViewer').then(module => ({ default: module.ReportView })))
const DatabaseView = lazy(() => import('../../components/workflow/DatabaseView'))
const ReportHumanInputPanel = lazy(() => import('../../components/workflow/ReportHumanInputPanel'))
const CodeShellPanel = lazy(() => import('./CodeShellPanel').then(module => ({ default: module.CodeShellPanel })))
const FileWorkspacePane = lazy(() => import('../../components/FileWorkspacePane').then(module => ({ default: module.FileWorkspacePane })))
const CodeFilesPanel = lazy(() => import('./CodeFilesPanel').then(module => ({ default: module.CodeFilesPanel })))
const CodeServerTerminalNotice = lazy(() => import('./CodeFilesPanel').then(module => ({ default: module.CodeServerTerminalNotice })))

export type WorkWorkspaceView = 'dashboard' | 'plan' | 'memory' | 'database' | 'files' | 'browser' | 'costs' | 'schedules' | 'suggestions' | 'identity' | 'mcp' | 'shell'

function sendWorkProjectPaneMessage(projectId: string, message: string, profileId = 'work') {
  return sendWorkspacePaneMessageToChat({ profileId, conversationKey: projectId, message })
}

// Panel lists live in products/productPanels.ts with every other product's panels.
const VIEW_BUTTONS = WORK_PANELS.views
const OPS_BUTTONS = WORK_PANELS.ops
const SETUP_BUTTONS = WORK_PANELS.setup

// usePendingCrewSuggestions counts suggestions waiting for the owner, for
// the toolbar badge. Refreshed on view changes and every minute.
function usePendingCrewSuggestions(workspacePath: string, enabled: boolean, view: WorkWorkspaceView): number {
  const [count, setCount] = useState(0)
  useEffect(() => {
    if (!enabled || !workspacePath) {
      setCount(0)
      return
    }
    let cancelled = false
    const load = () => {
      agentApi.listReportHumanInputs(workspacePath, 'pending', 'user_suggestion')
        .then(response => { if (!cancelled) setCount(response.inputs?.length ?? 0) })
        .catch(() => { if (!cancelled) setCount(0) })
    }
    load()
    const timer = window.setInterval(load, 60_000)
    return () => { cancelled = true; window.clearInterval(timer) }
  }, [workspacePath, enabled, view])
  return count
}

export const WorkWorkspaceToolbar = memo(function WorkWorkspaceToolbar({ sessionId = '', workspacePath, view, onViewChange, enabledPanels, readOnly, showShell = false, showActivityMonitor = true }: { sessionId?: string; workspacePath: string; view: WorkWorkspaceView; onViewChange: (view: WorkWorkspaceView) => void; enabledPanels?: Set<string>; readOnly?: boolean; showShell?: boolean; showActivityMonitor?: boolean }) {
  // Suggestions are Crew's: people who use a Crew suggest changes to its
  // owner. A Code has no such audience, so it never shows them.
  const product = useProjectProduct()
  const isCode = product.profileId === 'code'
  const filePreference = useCodeFilesPreference(sessionId)
  const localCodeSession = isCode && filePreference.location === 'computer' && Boolean(filePreference.target)
  const hasSuggestions = !isCode
  // A Code's toolbar reads: Dashboard | Files, Terminal, Browser | Automation, Costs | Setup (owner 2026-10-03). Its working tools
  // are the Ops group; Automation and Costs share the next one; the Database view is not offered.
  const viewButtons = isCode
    ? [...VIEW_BUTTONS.filter(item => item.id !== 'browser'), ...OPS_BUTTONS.filter(item => item.id === 'costs')]
    : VIEW_BUTTONS
  const opsButtons = isCode
    ? [...OPS_BUTTONS.filter(item => item.id === 'files' || item.id === 'shell'), ...VIEW_BUTTONS.filter(item => item.id === 'browser')]
    : OPS_BUTTONS
  const visibleViews = (readOnly
    ? viewButtons.filter(item => item.id === 'memory' && (!enabledPanels || enabledPanels.has('memory')))
    : enabledPanels ? viewButtons.filter(item => enabledPanels.has(item.id) || item.id === 'suggestions') : viewButtons)
    .filter(item => isWorkWorkspaceViewEnabled(item.id, undefined, localCodeSession))
    .filter(item => item.id !== 'suggestions' || hasSuggestions)
  const pendingSuggestions = usePendingCrewSuggestions(workspacePath, !readOnly && hasSuggestions, view)
  const visibleOps = (readOnly
    ? opsButtons.filter(item => item.id === 'files' || item.id === 'shell')
    : enabledPanels ? opsButtons.filter(item => item.id === 'shell' || enabledPanels.has(item.id)) : opsButtons)
    .filter(item => item.id !== 'shell' || showShell)
  const browserConnected = useBrowserToolbarConnection(workspacePath, product.profileId, [...visibleViews, ...visibleOps].some(item => item.id === 'browser'), !readOnly)
  // Setup (identity, integrations) edits owner state, so someone else's
  // Crew offers no setup views at all — not even the always-on identity.
  // A Code's Share view is open to everyone in it (co-owners edit it, the
  // rest see who has access); Crews have no Share view.
  const visibleSetup = (readOnly
    ? []
    : SETUP_BUTTONS.filter(item => isWorkWorkspaceViewEnabled(item.id, enabledPanels, localCodeSession)))
  // Ops and Setup are always open and show icons only.
  // No empty frame when every view moved elsewhere.
  // A Code hides the "Use in AI apps" integration tab (CODE_HIDDEN_INTEGRATION_TABS).
  const sectionsFor = (id: string) => (WORK_PANEL_SECTIONS[id] ?? []).filter(section => !(isCode && id === 'mcp' && section.id === 'cli'))
  useRegisterPanelSwitcher(isCode ? 'code' : 'work', [
    ...visibleViews.map(item => ({ id: item.id, label: item.label, group: 'Views', sections: sectionsFor(item.id) })),
    ...visibleOps.map(item => ({ id: item.id, label: item.label, group: 'Ops', sections: sectionsFor(item.id) })),
    ...visibleSetup.map(item => ({ id: item.id, label: item.label, group: 'Setup', sections: sectionsFor(item.id) })),
  ], (id, section) => {
    // The Integrations, Identity and Automation panels pick up this target (useWorkspaceViewTarget).
    if (section) setWorkspaceViewTarget(id === 'schedules' ? 'workshop' : id as 'mcp' | 'identity', section)
    onViewChange(id as WorkWorkspaceView)
  })
  const viewsGroup = visibleViews.some(item => item.id !== 'dashboard') && <div className="inline-flex items-center gap-0.5 px-0.5">
      {visibleViews.filter(item => item.id !== 'dashboard').map((item) => <WorkspaceToolbarButton key={item.id} {...item} connected={item.id === 'browser' ? browserConnected : undefined} badge={item.id === 'suggestions' ? pendingSuggestions : undefined} active={view === item.id} onClick={() => onViewChange(item.id)} />)}
    </div>

  return (
    <div data-tour="work-tools" className="ml-auto flex shrink-0 items-center gap-1">
      <TooltipProvider delayDuration={150}>
        {showActivityMonitor && <GlobalActivityMonitor />}
        {visibleViews.some(item => item.id === 'dashboard') && <ReportDocumentSwitcher workspacePath={workspacePath} active={view === 'dashboard'} onOpen={() => onViewChange('dashboard')} />}
        <WorkspaceToolbarFrame>
          {/* A Crew shows its views first; a Code shows them (Automation, Costs) after its working tools. */}
          {!isCode && viewsGroup}
          {/* Ops and Setup show their icons only: always open, no label. */}
          {visibleOps.length > 0 && <WorkspaceToolbarGroup label="Ops" open hideToggleWhenOpen title={isCode ? 'Files, terminal and browser' : 'Operations: project files, database and costs'}>
            <div className="inline-flex items-center gap-0.5">{visibleOps.map((item) => <WorkspaceToolbarButton key={item.id} {...item} connected={item.id === 'browser' ? browserConnected : undefined} active={view === item.id} onClick={() => onViewChange(item.id)} />)}</div>
          </WorkspaceToolbarGroup>}
          {isCode && viewsGroup}
          {visibleSetup.length > 0 && <WorkspaceToolbarGroup label="Setup" open hideToggleWhenOpen title={isCode ? 'Setup: name and integrations' : 'Setup: identity and integrations'}>
            <div className="inline-flex items-center gap-0.5">{visibleSetup.map((item) => <WorkspaceToolbarButton key={item.id} {...item} label={isCode && item.id === 'identity' ? 'Settings' : item.label} icon={isCode && item.id === 'identity' ? Settings : item.icon} connected={item.id === 'browser' ? browserConnected : undefined} active={view === item.id} onClick={() => onViewChange(item.id)} />)}</div>
          </WorkspaceToolbarGroup>}
        </WorkspaceToolbarFrame>
      </TooltipProvider>
    </div>
  )
})

function WorkBrowserPanel({ tabId, projectId, workspacePath }: { tabId: string; projectId: string; workspacePath: string }) {
  const product = useProjectProduct()
  const savedMode = useChatStore(state => state.chatTabs[tabId]?.config.browserMode ?? 'auto')
  const savedPort = useChatStore(state => state.chatTabs[tabId]?.config.cdpPort ?? 9222)
  const [browserMode, setBrowserMode] = useState<BrowserAutomationMode>(savedMode === 'none' ? 'auto' : savedMode)
  const [canonicalSettings, setCanonicalSettings] = useState<{ mode: BrowserAutomationMode; port: number }>({
    mode: savedMode === 'none' ? 'auto' : savedMode, port: savedPort,
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
  // Crew has no workspace refresh token: remount the panel to reload sessions.
  const [refreshNonce, setRefreshNonce] = useState(0)
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
        store.setTabConfig(tab.tabId, { browserMode, cdpPort, enableBrowserAccess: true, useCdp: browserMode === 'cdp' })
      }
    } catch {
      store.addToast('Unable to save project browser settings', 'error')
    } finally {
      setSaving(false)
    }
  }, [browserMode, cdpPort, tabId, workspacePath, product.profileId])

  return (
    <BrowserWorkspacePanel
      key={refreshNonce}
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
      assistantControl={
        <WorkspaceViewActions
          workspacePath={workspacePath}
          message="Help me configure browser access for this project. Ask what site or task it is for before changing anything."
          onAsk={async message => { await sendWorkProjectPaneMessage(projectId, message, product.profileId) }}
          onRefresh={() => setRefreshNonce(nonce => nonce + 1)}
          refreshLabel="Refresh Browser"
        />
      }
    />
  )
}

export const WorkWorkspacePane = memo(function WorkWorkspacePane({ workspacePath, projectId, projectTitle, projectDescription, projectIdentity, projectTemplates, onInstallTemplate, tabId, view, enabledPanels, projectLLMConfig, selectedSecrets, selectedGlobalSecrets, workflowContextPaths, onViewChange, onRuntimeChange, nativeAgentTools, onNativeAgentToolsChange, onSelectedServersChange, onSelectedSkillsChange, onSelectedSecretsChange, onSelectedGlobalSecretsChange, onWorkflowContextPathsChange, onUpdateIdentity, onDeleteRequest, shared, showShell = false }: { showShell?: boolean; workspacePath: string; projectId: string; projectTitle: string; projectDescription: string; projectIdentity?: ProductIdentity; projectTemplates: Array<{ id: string; version: number }>; onInstallTemplate: (id: CrewTemplateId) => Promise<void>; tabId: string; view: WorkWorkspaceView; enabledPanels?: Set<string>; projectLLMConfig?: PresetLLMConfig; selectedSecrets: string[]; selectedGlobalSecrets: string[]; workflowContextPaths: string[]; onViewChange: (view: WorkWorkspaceView) => void; onRuntimeChange: (selection: WorkRuntimeSelection) => void | Promise<void>; nativeAgentTools?: boolean; onNativeAgentToolsChange?: (enabled: boolean) => Promise<unknown>; onSelectedServersChange: (servers: string[]) => Promise<unknown>; onSelectedSkillsChange: (skills: string[]) => Promise<unknown>; onSelectedSecretsChange: (secrets: string[]) => Promise<unknown>; onSelectedGlobalSecretsChange: (secrets: string[]) => Promise<unknown>; onWorkflowContextPathsChange: (paths: string[]) => Promise<unknown>; onUpdateIdentity: (patch: ProductIdentityPatch) => Promise<unknown>; onDeleteRequest: () => void; shared?: { ownerId: string; ownerUsername?: string } }) {
  const readOnly = Boolean(shared)
  const product = useProjectProduct()
  const noun = product.noun
  const ask = useCallback((message: string) => product.profileId === 'code'
    ? sendWorkspacePaneMessageToChat({ tabId, message })
    : sendWorkProjectPaneMessage(projectId, message, product.profileId), [product.profileId, projectId, tabId])
  const serverFiles = <FileWorkspacePane workspacePath={workspacePath} onAsk={async message => { await ask(message) }} hiddenRootFolders={['.git', 'node_modules', 'product.json', 'workflow.json']} hideManagedEntriesByDefault title="Workspace" hideAddToChat hideRootActions testId="work-files-panel" />
  const sharedFiles = useMemo(
    () => (readOnly ? sharedCrewFileClient(projectId, workspacePath, product.profileId) : null),
    [readOnly, projectId, workspacePath, product.profileId],
  )
  const [sharedFileRequest, setSharedFileRequest] = useState<{ path: string; nonce: number } | null>(null)
  const [openLocalFilesSettings, setOpenLocalFilesSettings] = useState(false)
  const openHistoryChat = useResumePreviousChat()
  const activeSessionId = useChatStore(state => state.chatTabs[tabId]?.sessionId ?? undefined)
  const filePreference = useCodeFilesPreference(activeSessionId || '')
  const localCodeSession = product.profileId === 'code' && filePreference.location === 'computer' && Boolean(filePreference.target)
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
      workspace.setFileContent(String(response.data.content ?? '').replace(/\\n/g, '\n').replace(/\\t/g, '\t').replace(/\\r/g, '\r'))
    } catch (cause) {
      workspace.setShowFileContent(false)
      useChatStore.getState().addToast(cause instanceof Error ? cause.message : 'Could not open skill file.', 'error')
    } finally {
      workspace.setLoadingFileContent(false)
    }
  }

  if (!isWorkWorkspaceViewEnabled(view, undefined, localCodeSession)) {
    return <div className="grid h-full place-items-center bg-background p-6 text-center text-sm text-muted-foreground">Dashboard and Automation are unavailable while this Code chat uses local files.</div>
  }
  if (!isWorkWorkspaceViewEnabled(view, enabledPanels, localCodeSession)) {
    return <div className="grid h-full place-items-center bg-background text-sm text-muted-foreground">No workspace view is enabled for this product.</div>
  }

  // Belt and braces behind the toolbar filter and the surface's view
  // fallback: a stale saved view or an agent-driven view request must never
  // render an owner-only panel (identity editors, transcripts, usage)
  // for someone else's Crew.
  if (view === 'shell' && !showShell) {
    return <div className="grid h-full place-items-center bg-background p-6 text-center text-sm text-muted-foreground">The terminal is only available to the owner of this Code.</div>
  }
  if (readOnly && view !== 'memory' && view !== 'files' && view !== 'shell') {
    return <div className="grid h-full place-items-center bg-background p-6 text-center text-sm text-muted-foreground">This workspace view is only available to the {noun} owner.</div>
  }

  return (
    <WorkspacePanelGuideContext.Provider value={product.profileId === 'code' ? 'code' : 'crew'}>
    <div className="flex h-full min-h-0 flex-col bg-background">
      <div className="min-h-0 flex-1 overflow-hidden">
        {view === 'files' && (readOnly ? <SharedCrewFilesPanel
          projectId={projectId}
          crewRoot={workspacePath}
          request={sharedFileRequest}
        /> : <Suspense fallback={<div className="grid h-full place-items-center text-sm text-muted-foreground">Loading…</div>}>{product.profileId === 'code' ? <CodeFilesPanel key={activeSessionId || workspacePath} sessionId={activeSessionId || ''} serverFiles={serverFiles} onAsk={async message => { await ask(message) }} onManageConnection={() => { setOpenLocalFilesSettings(true); onViewChange('identity') }} /> : serverFiles}</Suspense>)}
        {view === 'shell' && showShell && <Suspense fallback={<div className="grid h-full place-items-center text-sm text-muted-foreground">Loading…</div>}><div className="flex h-full min-h-0 flex-col"><CodeServerTerminalNotice sessionId={activeSessionId || ''} /><div className="min-h-0 flex-1"><CodeShellPanel projectId={projectId} /></div></div></Suspense>}
        {view === 'identity' && <WorkIdentityPanel
          workspacePath={workspacePath}
          shared={Boolean(shared)}
          projectId={projectId}
          projectTitle={projectTitle}
          projectDescription={projectDescription}
          projectIdentity={projectIdentity}
          projectTemplates={projectTemplates}
          onInstallTemplate={onInstallTemplate}
          tabId={tabId}
          openLocalFilesSettings={openLocalFilesSettings}
          onLocalFilesSettingsOpened={() => setOpenLocalFilesSettings(false)}
          selectedSecrets={selectedSecrets}
          selectedGlobalSecrets={selectedGlobalSecrets}
          projectLLMConfig={projectLLMConfig}
          enabledPanels={enabledPanels}
          onAsk={async message => { await ask(message) }}
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
          onAsk={message => ask(message)}
          onCreatePlan={() => { void openWorkspacePaneChatWithDraft({
            profileId: product.profileId,
            conversationKey: projectId,
            message: 'Help me create a plan for this project. Let us define the steps and save the plan in the project.',
          }).catch(error => {
            useChatStore.getState().addToast(error instanceof Error ? error.message : 'Could not open project chat.', 'error')
          }) }}
        />}
        {view === 'mcp' && <WorkIntegrationsPanel
          key={workspacePath}
          selectedSecrets={selectedSecrets}
          selectedGlobalSecrets={selectedGlobalSecrets}
          onSelectedSecretsChange={updateSecretSelection}
          onSelectedGlobalSecretsChange={onSelectedGlobalSecretsChange}
          workspacePath={workspacePath}
          projectId={projectId}
          projectTitle={projectTitle}
          projectTemplates={projectTemplates}
          tabId={tabId}
          enabledPanels={enabledPanels}
          onAsk={async message => { await ask(message) }}
          onSelectedServersChange={onSelectedServersChange}
          onSelectedSkillsChange={onSelectedSkillsChange}
          workflowContextPaths={workflowContextPaths}
          onWorkflowContextPathsChange={onWorkflowContextPathsChange}
        />}
        {view === 'memory' && <WorkMemoryPanel
          workspacePath={workspacePath}
          onAsk={async message => { await ask(message) }}
          onOpenFile={filePath => { void openProjectFile(filePath) }}
          fileClient={sharedFiles ?? undefined}
          readOnly={readOnly}
        />}
        <Suspense fallback={<div className="grid h-full place-items-center text-sm text-muted-foreground">Loading…</div>}>
          {view === 'dashboard' && <ReportView
            workspacePath={workspacePath}
            emptyIdentity={{ icon: projectIdentity?.icon, name: projectIdentity?.name || projectTitle, projectName: projectTitle }}
            emptyDescription={`Ask ${noun} to create a visual dashboard for this project. It can organize tasks, notes, plans, status, research, or anything else you want to manage visually.`}
            sendChatMessage={sendDashboardMessage}
            headerAction={<AskAIButton
              workspacePath={workspacePath}
              message={`Help me with this ${noun} project's results page. Explain what it shows in plain words and ask what I want to change.`}
              onAsk={async message => { await ask(message) }}
              iconOnly
            />}
          />}
          {view === 'database' && <DatabaseView workspacePath={workspacePath} headerAction={<AskAIButton
            workspacePath={workspacePath}
            message={`Help me with this ${noun} project's stored records. Explain what's kept and ask what I want to look at or change.`}
            onAsk={async message => { await ask(message) }}
            iconOnly
          />} />}
          {view === 'suggestions' && <div className="flex h-full min-h-0 flex-col">
            <div className="flex items-center justify-between gap-2 border-b border-border px-3 py-2">
              <p className="text-xs text-muted-foreground">Changes other people using this {noun} asked for. Accepting records your decision; make the change in chat.</p>
              <AskAIButton
                workspacePath={workspacePath}
                message={`Look at the accepted suggestions in this ${noun}'s Suggestions view and help me make those changes to the ${noun}.`}
                onAsk={async message => { await ask(message) }}
                iconOnly
              />
            </div>
            <ReportHumanInputPanel workspacePath={workspacePath} source="user_suggestion" showEmptyState className="min-h-0 flex-1 overflow-auto p-3" />
          </div>}
          {view === 'browser' && <WorkBrowserPanel tabId={tabId} projectId={projectId} workspacePath={workspacePath} />}
          {view === 'costs' && <CostsPopup projectMode workspacePath={workspacePath} runFolders={[]} selectedRunFolder={null} emptyHint="Send a message to see this project's usage here." headerAction={<AskAIButton
            workspacePath={workspacePath}
            message={`Help me understand what this ${noun} project costs to run. Explain in plain words where the money goes, then ask what I want to change.`}
            onAsk={async message => { await ask(message) }}
            iconOnly
          />} />}
          {view === 'schedules' && <AutomationHubPanel
            entityType="product"
            workspacePath={workspacePath}
            // A Code's schedules and triggers are its owner's: they run as the
            // owner, so someone the Code is shared with only sees them.
            canManage={!(product.profileId === 'code' && shared)}
            scopeNoun="project"
            productTriggerScope={enabledPanels?.has('triggers') === false ? undefined : { profileId: product.profileId, projectId }}
            chatContent={<PreviousChatHistoryPanel
              workspacePath={workspacePath}
              activeSessionId={canonicalSessionId || activeSessionId}
              title=""
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
            />}
            workflowScope={{ workflowId: projectId, workspacePath, label: projectTitle }}
            askAIMessages={{
              chats: `Help me with this ${noun} project's automation: past chats, schedules, and triggers. Explain what's here and ask what I want to review or change.`,
              schedules: `Help me manage this project's schedules or authenticated webhook triggers. Each sends exactly one saved instruction to this ${noun} project; do not create workflow routes or workflow executions.`,
              triggers: `Help me manage this project's authenticated triggers. Each trigger sends one saved instruction to this ${noun} project.`,
              functions: `Help me with this ${noun}'s functions: typed actions other Crews and workflows can call. List them, explain what each does, and ask what I want to add or change.`,
            }}
            onAskAI={async message => { await sendWorkProjectPaneMessage(projectId, message) }}
          />}
        </Suspense>
      </div>
    </div>
    </WorkspacePanelGuideContext.Provider>
  )
})
