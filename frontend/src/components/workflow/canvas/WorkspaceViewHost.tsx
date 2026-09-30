import { usePulseToggle } from '../hooks/usePulseToggle'
import React, {
  Suspense,
  forwardRef,
  lazy,
  useCallback,
  useEffect,
  useImperativeHandle,
  useMemo,
  useRef,
  useState,
} from 'react'
import { useShallow } from 'zustand/react/shallow'
import { ReactFlowProvider } from '@xyflow/react'
import { useChatStore } from '../../../stores/useChatStore'
import { WorkflowToolbar } from './WorkflowToolbar'
import { AskAIButton } from '../AskAIButton'
import { getWorkspaceAskAIMessage } from '../workspaceAskAI'
import { ReportView } from '../ReportViewer'
import { usePlanData } from '../hooks/usePlanData'
import { useWorkflowExecution } from '../hooks/useWorkflowExecution'
import { useWorkspaceState } from '../hooks/useWorkspaceState'
import { useWorkflowStore } from '../../../stores/useWorkflowStore'
import { useWorkflowManifestStore } from '../../../stores/useWorkflowManifestStore'
import { useWorkspaceStore } from '../../../stores/useWorkspaceStore'
import { agentApi, workspaceApi } from '../../../services/api'
import type {
  ExecutionOptions,
  PulseFinalCommandState,
  PulseModuleState,
  PulsePlanDriftDueItem,
  PulseReviewFocus,
  PulseReviewerModule,
  PulseShadowSignalObservation,
  PulseNextRun,
  PulseGoalWorkItem,
  PulseAutonomy,
  VariablesManifest,
} from '../../../services/api-types'
import { DEFAULT_PULSE_AUTONOMY } from '../../../services/api-types'
import { PULSE_FIXED_COMMANDS, PULSE_MODULE_COMMANDS } from './pulseSections'
import { assertNeverView, getWorkspaceView, isInspectorView, type InspectorViewId, type WorkspaceViewId, type WorkspaceViewKind } from '../workspaceViews'
import {
  WorkspaceViewDataContext,
  useWorkspaceViewData,
  type FlowShell,
  type PulseData,
  type WorkflowImageExportFormat,
  type WorkspaceViewData,
} from './workspaceViewData'
import {
  WORKFLOW_REPORT_EXPORT_EVENT,
  WorkflowCanvasInner,
  usePreviewDevice,
  type WorkflowCanvasProps,
  type WorkflowCanvasRef,
} from './WorkflowCanvas'

const NO_DISABLED_PULSE_REVIEWERS: PulseReviewerModule[] = []

// Every inspector view is lazy: only the one the user opens is fetched, so the
// eager bundle carries the flow canvas and nothing else from this list. The
// single <Suspense> around the inspector slot below shows the same muted
// "Loading…" line the views use for their own data loading.
const CostsPopup = lazy(() => import('../CostsPopup'))
const ExecutionLogsPopup = lazy(() => import('../ExecutionLogsPopup'))
const KnowledgeView = lazy(() => import('../KnowledgeView'))
const WorkflowScheduleRunsPanel = lazy(() => import('../../scheduler/WorkflowScheduleRunsPanel'))
const WorkflowAPITriggersView = lazy(() => import('../WorkflowAPITriggersView'))
const WorkflowCapabilitiesPanel = lazy(() => import('../WorkflowCapabilitiesPanel'))
const PulseView = lazy(() => import('../PulseView'))
const HumanActionsView = lazy(() => import('../HumanActionsView'))
const WorkflowBackupView = lazy(() => import('../WorkflowBackupView'))
const WorkflowPublishView = lazy(() => import('../WorkflowPublishView'))
const WorkflowNotificationView = lazy(() => import('../WorkflowNotificationView'))
const WorkflowAccessView = lazy(() => import('../WorkflowAccessView'))
const FileWorkspacePane = lazy(() => import('../../FileWorkspacePane').then(module => ({ default: module.FileWorkspacePane })))

function formatPulseTimestamp(value?: string): string {
  if (!value) return ''
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return ''
  return date.toLocaleString(undefined, {
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  })
}

const noop = () => {}
const dispatchReportExport = () => window.dispatchEvent(new CustomEvent(WORKFLOW_REPORT_EXPORT_EVENT))

// ---------------------------------------------------------------------------
// View bodies: what goes under the toolbar for each kind. Each renders only
// the pane content; the host owns the root, the toolbar, and the pane wrapper.
// ---------------------------------------------------------------------------

function ReportBody({ workspacePath }: { workspacePath: string | null }) {
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
        focusTier={reportFocusTier}
        headerAction={<AskAIButton workspacePath={workspacePath} message={getWorkspaceAskAIMessage('report')} iconOnly />}
      />}
    </div>
  )
}

function FilesBody() {
  return <FileWorkspacePane hideManagedEntriesByDefault />
}

function InspectorBody({ workspacePath, presetQueryId }: { workspacePath: string | null; presetQueryId: string | null }) {
  const workflowWorkspaceView = useWorkflowStore(state => state.workflowWorkspaceView)
  const refreshToken = useWorkflowStore(state => state.workspaceViewRefreshToken)
  const historyLogsTarget = useWorkflowStore(state => state.workspaceViewTarget?.view === 'execution-logs' && state.workspaceViewTarget.target.startsWith('history:') ? state.workspaceViewTarget : null)
  const { planData, selectedRunFolder, runFolderNames, workspace, pulse } = useWorkspaceViewData()
  const plan = planData.plan
  const refreshWorkspaceState = workspace.refresh

  const closeInspector = useCallback(() => {
    useWorkflowStore.getState().setShowWorkspacePane(false)
  }, [])
  const askAI = (view: InspectorViewId) => (
    <AskAIButton workspacePath={workspacePath} message={getWorkspaceAskAIMessage(view)} iconOnly />
  )

  // One explicit branch per inspector view. The `default` is a compile-time
  // exhaustiveness check: a view added to the registry without a branch here
  // is a type error, not a silent fallthrough into some other view.
  const renderInspector = (view: InspectorViewId) => {
    switch (view) {
      case 'costs':
        return (
          <CostsPopup
            workspacePath={workspacePath}
            runFolders={runFolderNames}
            selectedRunFolder={selectedRunFolder}
            headerAction={askAI('costs')}
          />
        )
      case 'execution-logs':
        return (
          <ExecutionLogsPopup
            workspacePath={workspacePath}
            runFolder={selectedRunFolder}
            allowDefaultRunFolder={!historyLogsTarget}
            key={historyLogsTarget?.token ?? 'execution-logs'}
            runFolders={runFolderNames}
            runFolderInfos={workspace.state?.run_folders || []}
            onRefreshRunFolders={refreshWorkspaceState}
            headerAction={askAI('execution-logs')}
          />
        )
      case 'knowledge':
        return <KnowledgeView workspacePath={workspacePath} plan={plan} />
      case 'schedules':
        return (
          <WorkflowScheduleRunsPanel
            embedded
            workflowScope={{ presetQueryId: presetQueryId || undefined, workspacePath: workspacePath || undefined }}
            onClose={closeInspector}
            headerAction={askAI('schedules')}
          />
        )
      case 'webhooks':
        return (
          <WorkflowAPITriggersView
            workspacePath={workspacePath}
            onViewRuns={() => useWorkflowStore.getState().openWorkspaceView('schedules')}
            headerAction={askAI('webhooks')}
          />
        )
      case 'workshop':
        // WorkspaceViewHost renders the caller-supplied Workshop panel before
        // reaching InspectorBody. Keep this branch for registry exhaustiveness.
        return null
      case 'pulse':
        return (
          <PulseView
            workspacePath={workspacePath}
            monitorOn={pulse.monitorOn}
            monitorSaving={pulse.monitorSaving}
            onToggleMonitor={pulse.toggleMonitor}
            disabledReviewModules={pulse.disabledReviewModules}
            reviewModuleSaving={pulse.reviewModuleSaving}
            onToggleReviewModule={pulse.toggleReviewModule}
            moduleStates={pulse.moduleStates}
            planDriftDue={pulse.planDriftDue}
            planDriftDueItems={pulse.planDriftDueItems}
            planDriftDueError={pulse.planDriftDueError}
            finalCommandStates={pulse.finalCommandStates}
            nextRun={pulse.nextRun}
            goalWork={pulse.goalWork}
            autonomy={pulse.autonomy}
            autonomySaving={pulse.autonomySaving}
            onChangeAutonomy={pulse.setAutonomy}
            focusAreas={pulse.focusAreas}
            focusSaving={pulse.focusSaving}
            onSaveFocusAreas={pulse.saveFocusAreas}
            reviewFocuses={pulse.reviewFocuses}
            reviewFocusSelections={pulse.reviewFocusSelections}
            statusError={pulse.statusError}
            statusLoading={pulse.statusLoading}
            overview={pulse.overview}
            onRefresh={() => { void pulse.refresh() }}
            headerAction={askAI('pulse')}
          />
        )
      case 'human-actions':
        return <HumanActionsView workspacePath={workspacePath} />
      case 'backup':
        return <WorkflowBackupView workspacePath={workspacePath} headerAction={askAI('backup')} />
      case 'publish':
        return <WorkflowPublishView workspacePath={workspacePath} headerAction={askAI('publish')} />
      case 'notify':
        return <WorkflowNotificationView workspacePath={workspacePath} headerAction={askAI('notify')} />
      case 'access':
        return <WorkflowAccessView workspacePath={workspacePath} />
      case 'identity':
      case 'playbooks':
      case 'mcp':
      case 'browser':
        return <WorkflowCapabilitiesPanel section={view} workspacePath={workspacePath} />
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
export const WorkspaceViewHost = React.memo(forwardRef<WorkflowCanvasRef, WorkflowCanvasProps>((props, ref) => {
  const {
    workspacePath,
    presetQueryId,
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
    embeddedPlanOnly = false,
    openHumanActionsOnMount = false,
  } = props

  const selectedRunFolder = useWorkflowStore(state => state.selectedRunFolder)
  const workflowWorkspaceView = useWorkflowStore(state => state.workflowWorkspaceView)
  const lastCanvasView = useWorkflowStore(state => state.lastCanvasView)

  // The registry decides what renders: no explicit view means the last canvas
  // view (Plan or Report). An embedded plan-only canvas (Video Studio) is
  // always the flow body regardless of the shared store.
  const effectiveView: WorkspaceViewId = workflowWorkspaceView ?? lastCanvasView
  const kind: WorkspaceViewKind = embeddedPlanOnly ? 'canvas' : getWorkspaceView(effectiveView).kind

  // --- Toolbar data, loaded once for every view ---------------------------
  const planData = usePlanData(workspacePath)
  const { status } = useWorkflowExecution()
  const workspace = useWorkspaceState(workspacePath, selectedRunFolder)
  const plan = planData.plan
  const workspaceState = workspace.state
  const isLoadingWorkspaceState = workspace.loading

  // --- Pulse: the toolbar's badge and the pane's PulseView are siblings, so
  // this lives here and reaches both through props / the WorkspaceViewData
  // context rather than either fetching its own copy.
  // useShallow is load-bearing: this selector builds a new object per call,
  // and without shallow comparison zustand's useSyncExternalStore sees a fresh
  // snapshot every render and loops ("Maximum update depth exceeded").
  const pulseConfig = useWorkflowManifestStore(useShallow(state => {
    const wf = state.workflows.find(w => w.workspace_path === workspacePath)
    return {
      enabled: wf?.manifest.pulse?.enabled,
      disabledReviewModules: wf?.manifest.pulse?.disabled_review_modules,
      legacyEnabled: wf?.manifest.schedules?.some(schedule => schedule.pulse_review_only && schedule.enabled),
    }
  }))
  const disabledReviewModules = pulseConfig.disabledReviewModules ?? NO_DISABLED_PULSE_REVIEWERS
  const monitorOn = !!(pulseConfig.enabled || pulseConfig.legacyEnabled)
  const updateWorkflowManifest = useWorkflowManifestStore(state => state.updateWorkflow)
  const { monitorSaving, toggleMonitor } = usePulseToggle(
    workspacePath, monitorOn, updateWorkflowManifest, useChatStore.getState().addToast,
  )
  const [reviewModuleSaving, setReviewModuleSaving] = useState<PulseReviewerModule | null>(null)
  const toggleReviewModule = useCallback((module: PulseReviewerModule) => {
    if (!workspacePath || reviewModuleSaving) return
    const disabled = new Set(disabledReviewModules)
    const enabling = disabled.delete(module)
    if (!enabling) disabled.add(module)
    setReviewModuleSaving(module)
    void updateWorkflowManifest(workspacePath, { pulse_disabled_review_modules: [...disabled] })
      .then(() => useChatStore.getState().addToast(`${module === 'technical_review' ? 'Health' : module === 'architecture_review' ? 'Architecture' : 'Strategy'} reviewer turned ${enabling ? 'on' : 'off'}`, 'success'))
      .catch(error => useChatStore.getState().addToast(error instanceof Error ? error.message : 'Could not update Pulse reviewers', 'error'))
      .finally(() => setReviewModuleSaving(null))
  }, [workspacePath, reviewModuleSaving, disabledReviewModules, updateWorkflowManifest])

  const [pulseModuleStates, setPulseModuleStates] = useState<PulseModuleState[]>([])
  const [planDriftDue, setPlanDriftDue] = useState(false)
  const [planDriftDueItems, setPlanDriftDueItems] = useState<PulsePlanDriftDueItem[]>([])
  const [planDriftDueError, setPlanDriftDueError] = useState<string | null>(null)
  const [pulseFinalCommandStates, setPulseFinalCommandStates] = useState<PulseFinalCommandState[]>([])
  const [pulseReviewFocuses, setPulseReviewFocuses] = useState<PulseReviewFocus[]>([])
  const [pulseReviewFocusSelections, setPulseReviewFocusSelections] = useState<PulseReviewFocus[]>([])
  const [pulseLoopClosureObservation, setPulseLoopClosureObservation] = useState<PulseShadowSignalObservation | null>(null)
  const [pulseNextRun, setPulseNextRun] = useState<PulseNextRun | null>(null)
  const [pulseGoalWork, setPulseGoalWork] = useState<PulseGoalWorkItem[]>([])
  const [pulseAutonomy, setPulseAutonomyState] = useState<PulseAutonomy>(DEFAULT_PULSE_AUTONOMY)
  const [pulseAutonomySaving, setPulseAutonomySaving] = useState(false)
  const [pulseFocusAreas, setPulseFocusAreas] = useState<string[]>([])
  const [pulseFocusSaving, setPulseFocusSaving] = useState(false)
  const [pulseStatusLoading, setPulseStatusLoading] = useState(false)
  const [pulseStatusError, setPulseStatusError] = useState<string | null>(null)

  const setPulseAutonomy = useCallback((next: PulseAutonomy) => {
    if (!workspacePath || pulseAutonomySaving) return
    if (next.run === pulseAutonomy.run && next.outward === pulseAutonomy.outward && next.change === pulseAutonomy.change) return
    const previous = pulseAutonomy
    setPulseAutonomyState(next)
    setPulseAutonomySaving(true)
    void updateWorkflowManifest(workspacePath, { pulse_autonomy_run: next.run, pulse_autonomy_outward: next.outward, pulse_autonomy_change: next.change })
      .then(() => useChatStore.getState().addToast('Pulse autonomy updated', 'success'))
      .catch(error => {
        setPulseAutonomyState(previous)
        useChatStore.getState().addToast(error instanceof Error ? error.message : 'Could not update Pulse autonomy', 'error')
      })
      .finally(() => setPulseAutonomySaving(false))
  }, [workspacePath, pulseAutonomySaving, pulseAutonomy, updateWorkflowManifest])

  const savePulseFocusAreas = useCallback(async (areas: string[]) => {
    if (!workspacePath || pulseFocusSaving) return false
    setPulseFocusSaving(true)
    try {
      await updateWorkflowManifest(workspacePath, { pulse_focus_areas: areas })
      setPulseFocusAreas(areas)
      useChatStore.getState().addToast('Goal Work focus areas saved', 'success')
      return true
    } catch (error) {
      useChatStore.getState().addToast(error instanceof Error ? error.message : 'Could not save focus areas', 'error')
      return false
    } finally {
      setPulseFocusSaving(false)
    }
  }, [workspacePath, pulseFocusSaving, updateWorkflowManifest])

  const refreshPulseModuleStates = useCallback(async (showLoading = true) => {
    if (!workspacePath) {
      setPulseModuleStates([])
      setPlanDriftDue(false)
      setPlanDriftDueItems([])
      setPlanDriftDueError(null)
      setPulseFinalCommandStates([])
      setPulseReviewFocuses([])
      setPulseReviewFocusSelections([])
      setPulseLoopClosureObservation(null)
      setPulseStatusError(null)
      return
    }
    if (showLoading) setPulseStatusLoading(true)
    setPulseStatusError(null)
    try {
      const resp = await agentApi.getPulseModuleState(workspacePath)
      if (!resp.success) {
        throw new Error(resp.error || 'Failed to load Pulse status')
      }
      setPulseModuleStates(resp.modules || [])
      setPlanDriftDue(!!resp.plan_drift_due)
      setPlanDriftDueItems(resp.plan_drift_due_items || [])
      setPlanDriftDueError(resp.plan_drift_due_error || null)
      setPulseNextRun(resp.next_pulse || null)
      setPulseGoalWork(resp.goal_work || [])
      setPulseAutonomyState(resp.autonomy ?? { ...DEFAULT_PULSE_AUTONOMY, run: resp.autonomy_run === 'ask' ? 'ask' : 'auto' })
      setPulseFocusAreas(resp.focus_areas || [])
      setPulseFinalCommandStates(resp.commands || [])
      setPulseReviewFocuses(resp.review_focus_history || [])
      setPulseReviewFocusSelections(resp.review_focus_selections || [])
      setPulseLoopClosureObservation(
        (resp.shadow_signal_observations || []).find(observation => observation.detector === 'loop_closure') || null
      )
    } catch (err) {
      const message = err instanceof Error ? err.message : 'Failed to load Pulse status'
      setPulseStatusError(message)
    } finally {
      if (showLoading) setPulseStatusLoading(false)
    }
  }, [workspacePath])

  // Used by cross-workflow decision links: opening a decision must surface
  // Human actions, but re-renders after that must not keep reopening a view the user
  // deliberately navigated away from -- one-shot, gated the same as before
  // (only from the flow view, so a user already on e.g. Costs isn't yanked
  // into Human actions).
  const openedInitialActionsRef = useRef(false)
  useEffect(() => {
    if (!openHumanActionsOnMount || openedInitialActionsRef.current || kind !== 'canvas') return
    openedInitialActionsRef.current = true
    useWorkflowStore.getState().openWorkspaceView('human-actions')
  }, [openHumanActionsOnMount, kind])

  // The module state is a ~250KB payload: poll slowly, never while the tab is
  // hidden, and never start a poll while the previous one is still in flight
  // (on a slow link a fixed 5s interval piled requests up and starved chat).
  const pulsePollInFlightRef = useRef(false)
  useEffect(() => {
    if (workflowWorkspaceView !== 'pulse') return
    const poll = async (showLoading: boolean) => {
      if (pulsePollInFlightRef.current) return
      if (!showLoading && document.hidden) return
      pulsePollInFlightRef.current = true
      try {
        await refreshPulseModuleStates(showLoading)
      } finally {
        pulsePollInFlightRef.current = false
      }
    }
    void poll(true)
    const timer = window.setInterval(() => { void poll(false) }, 30_000)
    const onVisible = () => { if (!document.hidden) void poll(false) }
    document.addEventListener('visibilitychange', onVisible)
    return () => {
      window.clearInterval(timer)
      document.removeEventListener('visibilitychange', onVisible)
    }
  }, [workflowWorkspaceView, refreshPulseModuleStates])

  const pulseModuleStateByModule = useMemo(
    () => new Map(pulseModuleStates.map(state => [state.module, state])),
    [pulseModuleStates],
  )
  const pulseFinalCommandStateByCommand = useMemo(
    () => new Map(pulseFinalCommandStates.map(state => [state.command, state])),
    [pulseFinalCommandStates],
  )
  const pulseOverview = useMemo(() => {
    const timestamps = [
      ...pulseModuleStates.map(state => state.updated_at || state.last_ran_at || state.last_checked_at),
      ...pulseFinalCommandStates.map(state => state.updated_at || state.finished_at || state.started_at),
      pulseLoopClosureObservation?.observed_at,
    ].filter((value): value is string => !!value)
    const latestTimestamp = timestamps.reduce((latest, value) => {
      const time = new Date(value).getTime()
      return Number.isNaN(time) || time <= latest ? latest : time
    }, 0)
    const recordedModuleStates = PULSE_MODULE_COMMANDS
      .map(command => pulseModuleStateByModule.get(command.id))
      .filter((state): state is PulseModuleState => !!state)
    const recordedFinalStates = PULSE_FIXED_COMMANDS
      .map(command => pulseFinalCommandStateByCommand.get(command.id))
      .filter((state): state is PulseFinalCommandState => !!state)
    return {
      recorded: recordedModuleStates.length + recordedFinalStates.length,
      total: PULSE_MODULE_COMMANDS.length + PULSE_FIXED_COMMANDS.length,
      latest: latestTimestamp > 0 ? formatPulseTimestamp(new Date(latestTimestamp).toISOString()) : '',
    }
  }, [pulseFinalCommandStateByCommand, pulseFinalCommandStates, pulseLoopClosureObservation, pulseModuleStateByModule, pulseModuleStates])

  const pulse = useMemo<PulseData>(() => ({
    monitorOn,
    monitorSaving,
    toggleMonitor,
    disabledReviewModules,
    reviewModuleSaving,
    toggleReviewModule,
    moduleStates: pulseModuleStates,
    planDriftDue,
    planDriftDueItems,
    planDriftDueError,
    finalCommandStates: pulseFinalCommandStates,
    nextRun: pulseNextRun,
    goalWork: pulseGoalWork,
    autonomy: pulseAutonomy,
    autonomySaving: pulseAutonomySaving,
    setAutonomy: setPulseAutonomy,
    focusAreas: pulseFocusAreas,
    focusSaving: pulseFocusSaving,
    saveFocusAreas: savePulseFocusAreas,
    reviewFocuses: pulseReviewFocuses,
    reviewFocusSelections: pulseReviewFocusSelections,
    statusError: pulseStatusError,
    statusLoading: pulseStatusLoading,
    overview: pulseOverview,
    refresh: refreshPulseModuleStates,
  }), [
    monitorOn, monitorSaving, toggleMonitor, disabledReviewModules, reviewModuleSaving, toggleReviewModule, pulseModuleStates, planDriftDue, planDriftDueItems, planDriftDueError, pulseFinalCommandStates, pulseNextRun, pulseGoalWork, pulseAutonomy, pulseAutonomySaving, setPulseAutonomy, pulseFocusAreas, pulseFocusSaving, savePulseFocusAreas,
    pulseReviewFocuses, pulseReviewFocusSelections, pulseStatusError, pulseStatusLoading,
    pulseOverview, refreshPulseModuleStates,
  ])

  // The flow canvas's VariablesSidebar edits the manifest in place; keep the
  // toolbar showing that edit until the next workspace refresh replaces it.
  const [variablesManifest, setVariablesManifest] = useState<VariablesManifest | null>(null)
  const [isLoadingVariables, setIsLoadingVariables] = useState(false)
  useEffect(() => {
    if (workspaceState) {
      setVariablesManifest(workspaceState.variables_manifest || null)
      setIsLoadingVariables(false)
    } else if (!isLoadingWorkspaceState) {
      setVariablesManifest(null)
      setIsLoadingVariables(false)
    } else {
      setIsLoadingVariables(isLoadingWorkspaceState)
    }
  }, [workspaceState, isLoadingWorkspaceState])

  const runFoldersForToolbar = useMemo(() => {
    if (!workspaceState?.run_folders) return []
    return workspaceState.run_folders.map(f => ({ name: f.name }))
  }, [workspaceState?.run_folders])
  const runFolderNames = useMemo(
    () => runFoldersForToolbar.map(folder => folder.name),
    [runFoldersForToolbar],
  )

  // --- Flow shell: the flow canvas replaces the whole pane (toolbar included)
  // while its data is loading or errored. Computed here so the toolbar and
  // the body agree.
  const [isRefreshingPlan, setIsRefreshingPlan] = useState(false)
  const flowLoading = planData.loading && !isRefreshingPlan
  const isPlanNotFoundError = planData.error && /not found|does not exist|planning must be run first/i.test(planData.error)
  const flowError = isPlanNotFoundError ? null : planData.error
  const flowShell: FlowShell = (flowLoading || isLoadingWorkspaceState)
    ? 'loading'
    : (flowError || workspace.error)
      ? 'error'
      : 'ready'

  // --- Image export lives in the flow canvas (it needs the React Flow
  // instance); the toolbar lives here. Bridge with a registration.
  const exportHandlerRef = useRef<((format: WorkflowImageExportFormat) => Promise<void>) | null>(null)
  const registerExportHandler = useCallback((handler: ((format: WorkflowImageExportFormat) => Promise<void>) | null) => {
    exportHandlerRef.current = handler
  }, [])
  const exportFlowImage = useCallback(() => {
    void exportHandlerRef.current?.('png')
  }, [])

  const hasPlan = Boolean(plan?.steps?.length)
  const onExport = kind === 'preview'
    ? dispatchReportExport
    : kind === 'canvas' && flowShell === 'ready' && hasPlan
      ? exportFlowImage
      : undefined

  // Stable identity: the toolbar is 1,100 lines with a dozen subscriptions,
  // and an inline arrow here re-rendered it on every host render.
  const handleStartPhase = useCallback((phaseId: string, executionOptions?: ExecutionOptions) => {
    onStartPhase?.(phaseId, executionOptions)
  }, [onStartPhase])

  // --- Imperative API: shared refresh for every view; the flow canvas keeps
  // its own granular refresh and node focus and is delegated to when active.
  const flowRef = useRef<WorkflowCanvasRef>(null)
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
    if (!workspaceViewTarget) return
    const { view, target } = workspaceViewTarget
    if (view === 'flow') {
      // The canvas mounts and lays out its nodes after this effect; give it a
      // frame before asking it to focus one.
      const frame = window.requestAnimationFrame(() => flowRef.current?.focusStep(target))
      return () => window.cancelAnimationFrame(frame)
    }
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
          if (!response.data.is_image && !fullPath.toLowerCase().endsWith('.json')) {
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
  }, [workspacePath, workspaceViewTarget, setBinaryFileData, setFileContent, setLoadingFileContent, setSelectedFile, setShowFileContent])
  const loadPlanRefresh = planData.refresh
  const refreshWorkspaceState = workspace.refresh
  const sharedRefresh = useCallback(async () => {
    await Promise.all([loadPlanRefresh(), refreshWorkspaceState()])
  }, [loadPlanRefresh, refreshWorkspaceState])
  useImperativeHandle(ref, () => ({
    refresh: async (changedStepIDs?: string[], deletedStepIDs?: string[]) => {
      if (kind === 'canvas' && flowRef.current) {
        return flowRef.current.refresh(changedStepIDs, deletedStepIDs)
      }
      await sharedRefresh()
      return null
    },
    getStepCount: () => plan?.steps?.length ?? 0,
    focusStep: (stepId: string) => {
      flowRef.current?.focusStep(stepId)
    },
    resyncViewport: () => {
      flowRef.current?.resyncViewport()
    },
  }), [kind, sharedRefresh, plan])

  const data = useMemo<WorkspaceViewData>(() => ({
    planData,
    status,
    workspace,
    selectedRunFolder,
    runFolderNames,
    variablesManifest,
    isLoadingVariables,
    setVariablesManifest,
    isRefreshingPlan,
    setIsRefreshingPlan,
    flowShell,
    flowError,
    registerExportHandler,
    pulse,
  }), [
    planData, status, workspace, selectedRunFolder, runFolderNames,
    variablesManifest, isLoadingVariables, isRefreshingPlan, flowShell, flowError, registerExportHandler,
    pulse,
  ])

  const showToolbar = !hideToolbar && !(kind === 'canvas' && flowShell !== 'ready')
  const gridToolbar = sharedToolbar && showChatArea
  const isInspectorKind = kind === 'inspector' || kind === 'capability'

  let body: React.ReactNode
  if (kind === 'canvas') {
    // The flow canvas handles `toolbarOnly` itself: its loading and error
    // screens still show in that mode, only the plan is skipped.
    body = (
      <ReactFlowProvider>
        <WorkflowCanvasInner
          {...props}
          ref={flowRef}
          assistantControl={workspacePath ? (
            <AskAIButton
              workspacePath={workspacePath}
              message={getWorkspaceAskAIMessage('flow')}
              iconOnly
              className="inline-flex h-8 w-8 items-center justify-center rounded-md border border-border bg-background/95 text-muted-foreground shadow-sm backdrop-blur-sm transition-colors hover:bg-muted hover:text-foreground"
            />
          ) : undefined}
        />
      </ReactFlowProvider>
    )
  } else if (toolbarOnly) {
    body = null
  } else if (kind === 'preview') {
    body = <ReportBody workspacePath={workspacePath} />
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
    body = <InspectorBody workspacePath={workspacePath} presetQueryId={presetQueryId} />
  }

  return (
    <WorkspaceViewDataContext.Provider value={data}>
      <div className={`flex flex-col h-full ${className} ${gridToolbar ? 'contents' : ''}`}>
        {showToolbar && (
          <div className={gridToolbar ? 'col-start-1 row-start-1 md:col-span-2' : ''}>
            <WorkflowToolbar
              status={status}
              plan={plan || undefined}
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
              monitorOn={monitorOn}
            />
          </div>
        )}
        <div
          data-tour="workflow-canvas-pane"
          data-ui-workspace={embeddedPlanOnly ? undefined : workspacePath ?? undefined}
          data-ui-view={effectiveView}
          data-testid="tour-workflow-canvas-pane"
          className={`${gridToolbar ? 'flex-1 col-start-1 row-start-2 md:col-start-2' : 'flex-1'} ${paneClassName} flex min-h-0 flex-col ${isInspectorKind ? 'overflow-hidden border-l border-border' : ''}`}
        >
          <div className={`min-h-0 flex-1 ${effectiveView === 'workshop' ? 'flex overflow-hidden' : ''}`}>{body}</div>
          {!isInspectorKind && !toolbarOnly && <span hidden data-ui-view-mounted />}
        </div>
      </div>
    </WorkspaceViewDataContext.Provider>
  )
}))

WorkspaceViewHost.displayName = 'WorkspaceViewHost'

export default WorkspaceViewHost
