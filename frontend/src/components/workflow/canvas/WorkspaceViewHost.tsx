import GoalOverview from '../GoalOverview'
import { inspectionKey } from '../../../utils/stepInspection'
import { WorkspacePanelNavigationProvider } from '../../workspace/WorkspacePanelNavigation'
import { WorkspacePanelHeader } from '../../workspace/WorkspacePanelHeader'
import { useCanWriteWorkflow } from '../../../hooks/useCanWriteWorkflow'
import React, {
  Suspense,
  lazy,
  useCallback,
  useEffect,
  useMemo,
  useRef,
} from 'react'
import { useChatStore } from '../../../stores/useChatStore'
import { WorkflowToolbar } from './WorkflowToolbar'
import { RelayContextPanel } from '../RelayContextPanel'
import { ReportView } from '../ReportViewer'
import { DashboardCreateDraftButton } from '../DashboardEmptyState'
import { useWorkflowExecution } from '../hooks/useWorkflowExecution'
import { useWorkspaceState } from '../hooks/useWorkspaceState'
import { useWorkflowStore } from '../../../stores/useWorkflowStore'
import { useWorkflowManifestStore } from '../../../stores/useWorkflowManifestStore'
import { useWorkspaceStore } from '../../../stores/useWorkspaceStore'
import { agentApi, workspaceApi } from '../../../services/api'
import type { ExecutionOptions } from '../../../services/api-types'
import { assertNeverView, getWorkspaceView, isInspectorView, type InspectorViewId, type WorkspaceViewId, type WorkspaceViewKind } from '../workspaceViews'
import { WorkspaceViewDataContext, useWorkspaceViewData, type WorkspaceViewData } from './workspaceViewData'
import { WORKFLOW_REPORT_EXPORT_EVENT, usePreviewDevice } from './reportPreview'
import type { WorkflowCanvasProps } from './workspaceViewTypes'

// Every inspector view is lazy: only the one the user opens is fetched, so the
// eager bundle carries none of these inspectors. The
// single <Suspense> around the inspector slot below shows the same muted
// "Loading…" line the views use for their own data loading.
const WorkflowContextPanel = lazy(() => import('../WorkflowContextPanel').then(module => ({ default: module.WorkflowContextPanel })))
const CostsPopup = lazy(() => import('../CostsPopup'))
const ExecutionLogsPopup = lazy(() => import('../ExecutionLogsPopup'))
const RelayExecutionLogsView = lazy(() => import('../RelayExecutionLogsView'))
const RelayPythonView = lazy(() => import('../RelayPythonView'))
const KnowledgeView = lazy(() => import('../KnowledgeView'))
const WorkflowScheduleRunsPanel = lazy(() => import('../../scheduler/WorkflowScheduleRunsPanel'))
const WorkflowAPITriggersView = lazy(() => import('../WorkflowAPITriggersView'))
const WorkflowCapabilitiesPanel = lazy(() => import('../WorkflowCapabilitiesPanel'))
const HumanActionsView = lazy(() => import('../HumanActionsView'))
const ShareDashboardButton = lazy(() => import('../ShareDashboardButton').then(module => ({ default: module.ShareDashboardButton })))
const FileWorkspacePane = lazy(() => import('../../FileWorkspacePane').then(module => ({ default: module.FileWorkspacePane })))

const noop = () => {}
const dispatchReportExport = () => window.dispatchEvent(new CustomEvent(WORKFLOW_REPORT_EXPORT_EVENT))

function ReportBody({ workspacePath, relayMode }: { workspacePath: string | null; relayMode: boolean }) {
  const access = useWorkflowManifestStore(state => state.workflows.find(item => item.workspace_path === workspacePath)?.my_access)
  const canCreate = access === 'owner' || access === 'write'
  // In Mobile the report sits in a narrow 480px column; when chat is focused we
  // keep the report mobile-framed so it fits. Laptop keeps its desktop report
  // beside a separate compact chat column, so it needs no report-width cap.
  const reportPreviewDevice = usePreviewDevice(workspacePath)
  const reportFocusTier: 'mobile' | undefined =
    useWorkflowStore(state => state.focusedPane === 'chat' && reportPreviewDevice === 'mobile')
      ? 'mobile'
      : undefined
  return (
    <div className="h-full min-h-0 relative">
      {workspacePath && <ReportView
        workspacePath={workspacePath}
        dashboardConversation
        defaultRelayDashboard={relayMode}
        emptyAction={canCreate ? <DashboardCreateDraftButton target={{ workspacePath }} /> : undefined}
        focusTier={reportFocusTier}
      />}
    </div>
  )
}

function FilesBody() {
  return <FileWorkspacePane hideManagedEntriesByDefault />
}

function InspectorBody({ workspacePath, presetQueryId, relayMode }: { workspacePath: string | null; presetQueryId: string | null; relayMode: boolean }) {
  const workflowWorkspaceView = useWorkflowStore(state => state.workflowWorkspaceView)
  const refreshToken = useWorkflowStore(state => state.workspaceViewRefreshToken)
  const historyLogsTarget = useWorkflowStore(state => state.workspaceViewTarget?.view === 'execution-logs' && state.workspaceViewTarget.target.startsWith('history:') ? state.workspaceViewTarget : null)
  const inspectedRun = useChatStore(state => {
    const tab = state.getActiveTab()
    const inspections = tab?.metadata?.stepInspections
    return inspections?.items.find(item => item.workspace_path === workspacePath && item.session_id === tab?.sessionId && inspectionKey(item) === inspections.activeId)
  })
  const { selectedRunFolder, runFolderNames, workspace } = useWorkspaceViewData()
  const relayID = useWorkflowManifestStore(state => state.workflows.find(item => item.workspace_path === workspacePath)?.manifest.id)
  const refreshWorkspaceState = workspace.refresh

  const closeInspector = useCallback(() => {
    useWorkflowStore.getState().setShowWorkspacePane(false)
  }, [])
  // One explicit branch per inspector view. The `default` is a compile-time
  // exhaustiveness check: a view added to the registry without a branch here
  // is a type error, not a silent fallthrough into some other view.
  const renderInspector = (view: InspectorViewId) => {
    switch (view) {
      case 'context':
        return relayMode ? <RelayContextPanel workspacePath={workspacePath}/> : <WorkflowContextPanel workspacePath={workspacePath}/>
      case 'costs':
        return (
          <CostsPopup
            workspacePath={workspacePath}
            runFolders={runFolderNames}
            selectedRunFolder={selectedRunFolder}
          />
        )
      case 'execution-logs':
        if (relayMode && workspacePath && relayID) {
          return <RelayExecutionLogsView
            relayID={relayID}
            draftWorkspacePath={workspacePath}
            draftSelectedRunFolder={selectedRunFolder}
            draftRunFolders={runFolderNames}
            draftRunFolderInfos={workspace.state?.run_folders || []}
            onRefreshDraftRuns={refreshWorkspaceState}
          />
        }
        return (
          <ExecutionLogsPopup
            workspacePath={workspacePath}
            runFolder={historyLogsTarget ? selectedRunFolder : inspectedRun?.run_folder || null}
            defaultRunFolder={workspace.state?.default_run_folder}
            allowDefaultRunFolder={!historyLogsTarget}
            key={historyLogsTarget?.token ?? 'execution-logs'}
            runFolders={runFolderNames}
            runFolderInfos={workspace.state?.run_folders || []}
            onRefreshRunFolders={refreshWorkspaceState}
          />
        )
      case 'knowledge':
        return <KnowledgeView workspacePath={workspacePath} />
      case 'schedules':
        return (
          <WorkflowScheduleRunsPanel
            embedded
            workflowScope={{ presetQueryId: presetQueryId || undefined, workspacePath: workspacePath || undefined }}
            onClose={closeInspector}
          />
        )
      case 'webhooks':
        return (
          <WorkflowAPITriggersView
            workspacePath={workspacePath}
            onViewRuns={() => useWorkflowStore.getState().openWorkspaceView('schedules')}
          />
        )
      case 'workshop':
        // WorkspaceViewHost renders the caller-supplied Workshop panel before
        // reaching InspectorBody. Keep this branch for registry exhaustiveness.
        return null
      case 'goals':
        return workspacePath ? <GoalOverview key={workspacePath} workspacePath={workspacePath} /> : null
      case 'human-actions':
        return <HumanActionsView workspacePath={workspacePath} />
      case 'access':
        return workspacePath ? <div className="p-4"><ShareDashboardButton workspacePath={workspacePath} variant="panel" /></div> : null
      case 'identity':
      case 'mcp':
      case 'browser':
        return <WorkflowCapabilitiesPanel key={view === 'mcp' ? workspacePath : undefined} section={view} workspacePath={workspacePath} presetQueryId={presetQueryId} relayMode={relayMode} />
      default:
        return assertNeverView(view)
    }
  }

  if (!isInspectorView(workflowWorkspaceView)) return null
  return (
    <div className="h-full min-h-0">
      <Suspense
        // A new token remounts the view, so it refetches whatever it shows.
        key={`${workflowWorkspaceView}:${refreshToken}`}
        fallback={
          <div className="flex items-center justify-center gap-2 py-12 text-sm text-muted-foreground">
            Loading…
          </div>
        }
      >
        {renderInspector(workflowWorkspaceView)}
        <span hidden data-ui-view-mounted />
      </Suspense>
    </div>
  )
}

// ---------------------------------------------------------------------------
// Host
// ---------------------------------------------------------------------------

/**
 * The one component that owns the right-side workspace pane: it loads the
 * toolbar data once, renders the toolbar once, and mounts the selected view
 * below it. Its root element and the toolbar keep their identity across view
 * switches; only the body swaps. Before this, four sibling components each
 * re-declared the same hooks and toolbar, and switching between them
 * unmounted and rebuilt the whole pane.
 *
 * Report and Pulse (log) are lightweight preview-pane views with no React
 * Flow tree; the flow canvas is wrapped in its own ReactFlowProvider so
 * report mode never repaints on flow-only store traffic.
 */
export const WorkspaceViewHost = React.memo((props: WorkflowCanvasProps) => {
  const {
    workspacePath,
    presetQueryId,
    relayMode = false,
    currentPhase,
    onStartPhase,
    onCreatePlan,
    showChatArea = false,
    onToggleChatArea,
    toolbarOnly = false,
    sharedToolbar = false,
    chatTabsSlot,
    workshopPanel,
    paneClassName = '',
    className = '',
    hideToolbar = false,
    openHumanActionsOnMount = false,
    scope,
  } = props

  const panelChatTabId = useChatStore(state => {
    const tab = state.activeTabId ? state.chatTabs[state.activeTabId] : undefined
    return tab?.metadata?.presetQueryId === presetQueryId && tab?.metadata?.phaseId === 'workflow-builder' && !tab.metadata.isViewOnly && tab.metadata.agentProfileTabCanWrite !== false ? tab.tabId : undefined
  })
  const globalSelectedRunFolder = useWorkflowStore(state => state.selectedRunFolder)
  const selectedRunFolder = scope ? scope.selectedRunFolder : globalSelectedRunFolder
  const pythonRelay = useWorkflowManifestStore(state => relayMode && state.workflows.some(item => item.workspace_path === workspacePath && item.manifest.relay_runtime === 'python'))
  const relayID = useWorkflowManifestStore(state => state.workflows.find(item => item.workspace_path === workspacePath)?.manifest.id)
  const workflowWorkspaceView = useWorkflowStore(state => state.workflowWorkspaceView)
  const lastCanvasView = useWorkflowStore(state => state.lastCanvasView)

  // The graph destination belongs only to the Python Relay source view.
  const selectedView = scope?.view ?? workflowWorkspaceView ?? lastCanvasView
  const effectiveView: WorkspaceViewId = selectedView === 'flow' && !pythonRelay ? 'report' : selectedView
  const canWrite = useCanWriteWorkflow(workspacePath)
  const kind: WorkspaceViewKind = getWorkspaceView(effectiveView).kind

  // --- Toolbar data, loaded once for every view ---------------------------
  const { status: chatStatus } = useWorkflowExecution()
  const workspace = useWorkspaceState(workspacePath, selectedRunFolder, !scope)
  const status = scope ? (workspace.state?.active_executions?.length ? 'running' : 'idle') : chatStatus
  const workspaceState = workspace.state
  const isLoadingWorkspaceState = workspace.loading

  // Used by cross-workflow decision links: opening a decision must surface
  // Human actions, but re-renders after that must not keep reopening a view the user
  // deliberately navigated away from -- one-shot, gated the same as before
  const openedInitialActionsRef = useRef(false)
  useEffect(() => {
    if (!openHumanActionsOnMount || openedInitialActionsRef.current) return
    openedInitialActionsRef.current = true
    useWorkflowStore.getState().openWorkspaceView('human-actions')
  }, [openHumanActionsOnMount])

  const variablesManifest = workspaceState?.variables_manifest || null

  const runFoldersForToolbar = useMemo(() => {
    if (!workspaceState?.run_folders) return []
    return workspaceState.run_folders.map(f => ({ name: f.name }))
  }, [workspaceState?.run_folders])
  const runFolderNames = useMemo(
    () => runFoldersForToolbar.map(folder => folder.name),
    [runFoldersForToolbar],
  )

  const onExport = kind === 'preview' ? dispatchReportExport : undefined

  // Stable identity: the toolbar is 1,100 lines with a dozen subscriptions,
  // and an inline arrow here re-rendered it on every host render.
  const handleStartPhase = useCallback((phaseId: string, executionOptions?: ExecutionOptions) => {
    onStartPhase?.(phaseId, executionOptions)
  }, [onStartPhase])

  // open_workspace_view(view, target) — focus something inside the view the
  // agent just opened. Only the views that have something to focus act on it;
  // the rest ignore the target, so the agent can always pass one.
  const workspaceViewTarget = useWorkflowStore(state => state.workspaceViewTarget)
  const setSelectedFile = useWorkspaceStore(state => state.setSelectedFile)
  const setShowFileContent = useWorkspaceStore(state => state.setShowFileContent)
  const setFileContent = useWorkspaceStore(state => state.setFileContent)
  const setLoadingFileContent = useWorkspaceStore(state => state.setLoadingFileContent)
  const setBinaryFileData = useWorkspaceStore(state => state.setBinaryFileData)
  useEffect(() => {
    if (scope || !workspaceViewTarget) return
    const { view, target } = workspaceViewTarget
    if (view === 'files') {
      const relativePath = target.replace(/^\/+|\/+$/g, '')
      const normalizedWorkspace = (workspacePath ?? '').replace(/^\/+|\/+$/g, '')
      const fullPath = normalizedWorkspace && relativePath !== normalizedWorkspace && !relativePath.startsWith(`${normalizedWorkspace}/`)
        ? `${normalizedWorkspace}/${relativePath}`
        : relativePath
      const fileName = fullPath.split('/').filter(Boolean).pop() ?? fullPath
      const ext = fileName.split('.').pop()?.toLowerCase() ?? ''
      const isViewableBinary = ['xls', 'xlsx', 'docx', 'pdf', 'webm', 'mp4', 'mov', 'mp3', 'wav', 'm4a', 'aac', 'ogg', 'oga', 'flac', 'opus'].includes(ext)

      setSelectedFile({ name: fileName, path: fullPath })
      setLoadingFileContent(true)
      setShowFileContent(true)
      void (async () => {
        try {
          if (isViewableBinary) {
            const response = await workspaceApi.get(
              `/api/documents/${encodeURIComponent(fullPath)}`,
              { params: { download: 'true' }, responseType: 'arraybuffer' },
            )
            setBinaryFileData(response.data as ArrayBuffer)
            setFileContent('')
            return
          }
          setBinaryFileData(null)
          const response = await agentApi.getPlannerFileContent(fullPath)
          if (!response.success || !response.data) {
            setShowFileContent(false)
            return
          }
          let content = response.data.content == null ? '' : String(response.data.content)
          if (!response.data.is_image && !/\.(json|py)$/i.test(fullPath)) {
            content = content.replace(/\\n/g, '\n').replace(/\\t/g, '\t').replace(/\\r/g, '\r')
          }
          if (fullPath.toLowerCase().endsWith('.json')) {
            try { content = JSON.stringify(JSON.parse(content), null, 2) } catch { /* show source unchanged */ }
          }
          setFileContent(content)
        } catch {
          // Keeping the viewer hidden prevents the UI broker from acknowledging
          // a file it could not retrieve.
          setShowFileContent(false)
        } finally {
          setLoadingFileContent(false)
        }
      })()
    }
  }, [scope, workspacePath, workspaceViewTarget, setBinaryFileData, setFileContent, setLoadingFileContent, setSelectedFile, setShowFileContent])
  const data = useMemo<WorkspaceViewData>(() => ({
    workspace, selectedRunFolder, runFolderNames,
  }), [workspace, selectedRunFolder, runFolderNames])

  const showToolbar = !hideToolbar
  const gridToolbar = sharedToolbar && showChatArea
  const isInspectorKind = kind === 'inspector' || kind === 'capability'

  let body: React.ReactNode
  if (kind === 'canvas' && pythonRelay) {
    body = toolbarOnly ? null : <Suspense fallback={<div className="p-6 text-sm text-muted-foreground">Loading Flow…</div>}>
      <RelayPythonView workspacePath={workspacePath} relayID={relayID} onBuild={onCreatePlan || noop} scope={scope} />
    </Suspense>
  } else if (toolbarOnly) {
    body = null
  } else if (kind === 'preview') {
    body = <ReportBody workspacePath={workspacePath} relayMode={relayMode} />
  } else if (effectiveView === 'workshop') {
    body = workshopPanel ?? null
  } else if (kind === 'files') {
    body = (
      <Suspense
        fallback={
          <div className="flex items-center justify-center gap-2 py-12 text-sm text-muted-foreground">
            Loading…
          </div>
        }
      >
        <FilesBody />
      </Suspense>
    )
  } else {
    body = <InspectorBody workspacePath={workspacePath} presetQueryId={presetQueryId} relayMode={relayMode} />
  }

  return (
    <WorkspaceViewDataContext.Provider value={data}>
      <div className={`flex flex-col h-full ${className} ${gridToolbar ? 'contents' : ''}`}>
        {showToolbar && (
          <div className={gridToolbar ? 'col-start-1 row-start-1 md:col-span-2' : ''}>
            <WorkflowToolbar
              relayMode={relayMode}
              pythonRelay={pythonRelay}
              status={status}
              currentPhase={currentPhase}
              workspacePath={workspacePath}
              presetQueryId={presetQueryId}
              runFolders={runFoldersForToolbar}
              variablesManifest={variablesManifest}
              isLoadingWorkspaceState={isLoadingWorkspaceState}
              onStartPhase={handleStartPhase}
              onCreatePlan={onCreatePlan || noop}
              showChatArea={showChatArea}
              onToggleChatArea={onToggleChatArea}
              onExport={onExport}
              chatTabsSlot={chatTabsSlot}
              chatTabId={panelChatTabId}
            />
          </div>
        )}
        <div
          data-tour="workflow-canvas-pane"
          data-ui-workspace={workspacePath ?? undefined}
          data-ui-view={effectiveView}
          data-testid="tour-workflow-canvas-pane"
          className={`${gridToolbar ? 'flex-1 col-start-1 row-start-2 md:col-start-2' : 'flex-1'} ${paneClassName} relative flex min-h-0 flex-col ${isInspectorKind && effectiveView !== 'context' ? 'overflow-hidden border-l border-border' : ''}`}
        >
          <WorkspacePanelNavigationProvider workspacePath={workspacePath} view={effectiveView} chatTarget={panelChatTabId ? { tabId: panelChatTabId } : undefined}>
            {!toolbarOnly && <WorkspacePanelHeader workspacePath={workspacePath} view={effectiveView} title={getWorkspaceView(effectiveView).label} contextAllowed={canWrite} onBack={() => useWorkflowStore.getState().openWorkspaceView('context')} />}
            <div className={`min-h-0 flex-1 ${effectiveView === 'workshop' ? 'flex overflow-hidden' : ''}`}>{body}</div>
          </WorkspacePanelNavigationProvider>
          {!isInspectorKind && !toolbarOnly && <span hidden data-ui-view-mounted />}
        </div>
      </div>
    </WorkspaceViewDataContext.Provider>
  )
})

WorkspaceViewHost.displayName = 'WorkspaceViewHost'

export default WorkspaceViewHost
