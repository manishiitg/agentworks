import { groupWorkspacePanels } from '../workspace/workspacePanelGroups'
import { WorkspaceToolsMenu, WorkspaceToolsGroup } from '../workspace/WorkspaceToolsMenu'
import { useCanWriteWorkflow } from '../../hooks/useCanWriteWorkflow'
import { WorkspaceContextBrowser } from '../workspace/WorkspaceContextBrowser'
import { WorkspaceAutomationLinks } from '../workspace/WorkspaceAutomationLinks'
import { WorkspaceMoreTools, isProjectSettingsPanel } from '../workspace/WorkspaceMoreTools'
import { useAppStore } from '../../stores/useAppStore'
import { useLLMStore } from '../../stores/useLLMStore'
import {
  WorkspaceContextCard,
  WorkspaceContextRow as Row,
  WorkspaceContextSection as Section,
  WorkspaceContextShortcut as Shortcut,
} from '../workspace/WorkspaceContextCard'
import { useEffect, useState } from 'react'
import {
  Activity,
  BookOpen,
  ChevronRight,
  Clock3,
  Hand,
  LayoutDashboard,
  MessageSquareText,
  Plug,
  Users,
} from 'lucide-react'
import { schedulerApi } from '../../api/scheduler'
import type { ScheduledJob } from '../../services/api-types'
import { useWorkflowStore } from '../../stores/useWorkflowStore'
import { useWorkflowManifestStore } from '../../stores/useWorkflowManifestStore'
import { useAuthStore } from '../../stores/useAuthStore'
import { useLiveRefetch } from '../../hooks/useLiveRefetch'
import { usePendingDecisionState } from './hooks/usePendingDecisionCount'
import { selectReportDocument, useReportDocuments } from './reportDocuments'
import { reportDocumentLabel } from './reportDocumentLabels'
import { WORKSPACE_VIEWS, workflowContextViews, type WorkspaceViewId } from './workspaceViews'

export function jobsForWorkspace(jobs: ScheduledJob[], workspacePath: string) {
  return jobs.filter((job) => job.workspace_path?.replace(/\/+$/, '') === workspacePath.replace(/\/+$/, ''))
}
/** Read-only summary; every action routes through the existing workspace views. */
export function WorkflowContextPanel({ workspacePath }: { workspacePath: string | null }) {
  const open = useWorkflowStore((state) => state.openWorkspaceView)
  const { documents, defaultPath } = useReportDocuments(workspacePath || '')
  const decisions = usePendingDecisionState(workspacePath)
  const canWrite = useCanWriteWorkflow(workspacePath)
  const multiUser = useAuthStore(state => state.isMultiUserMode)
  const manifest = useWorkflowManifestStore(
    (state) => state.workflows.find((item) => item.workspace_path === workspacePath)?.manifest
  )
  const [schedules, setSchedules] = useState<{
    workspace: string
    jobs: ScheduledJob[]
    error: boolean
    partial?: boolean
  } | null>(null)
  const [tick, setTick] = useState(0)
  useLiveRefetch(() => setTick((value) => value + 1), {
    kinds: ['schedules'],
    workflow: workspacePath,
    fallbackMs: 30000,
    enabled: !!workspacePath,
  })
  useEffect(() => {
    if (!workspacePath) return
    let disposed = false
    void schedulerApi
      .listJobs({ entity_type: 'workflow', limit: 1000 })
      .then((result) => {
        if (!disposed)
          setSchedules({
            workspace: workspacePath,
            jobs: jobsForWorkspace(result.jobs || [], workspacePath),
            error: false,
            partial: result.total > (result.jobs || []).length,
          })
      })
      .catch(() => {
        if (!disposed) setSchedules({ workspace: workspacePath, jobs: [], error: true })
      })
    return () => {
      disposed = true
    }
  }, [workspacePath, tick])
  if (!workspacePath) return null
  const jobs = schedules?.workspace === workspacePath ? schedules : null
  const pulseEnabled =
    manifest?.pulse?.enabled ??
    manifest?.schedules?.some((schedule) => schedule.pulse_review_only && schedule.enabled) ??
    false
  return (
    <WorkspaceContextCard product="agentworks" consolidateConnections summary="Results, scheduled work and reviews for this project." availableShortcuts={workflowContextViews(false, multiUser).filter(item => !isProjectSettingsPanel(item.id)).map(item => ({ ...item, onClick: () => open(item.id, item.id === 'mcp' ? 'overview' : undefined) }))} moreTools={<WorkspaceMoreTools items={[
      ...workflowContextViews(false, multiUser).filter(item => item.id !== 'mcp' && !isProjectSettingsPanel(item.id)).map(item => ({ ...item, onClick: () => open(item.id) })),
      { id: 'global-activity', label: 'Activity', icon: Activity, onClick: () => openProjectActivity(workspacePath) },
    ]} />}>
      <Shortcut id="report" label="Dashboard" icon={LayoutDashboard}>
      <Section title="Results">
        <Row
          icon={LayoutDashboard}
          onClick={() => {
            selectReportDocument(workspacePath, defaultPath)
            open('report')
          }}
        >
          Dashboard
        </Row>
        {documents
          .filter((document) => document.path !== defaultPath)
          .slice(0, 3)
          .map((document) => (
            <Row
              key={document.path}
              hint={document.path}
              icon={LayoutDashboard}
              onClick={() => {
                selectReportDocument(workspacePath, document.path)
                open('report')
              }}
            >
              {reportDocumentLabel(document, documents)}
            </Row>
          ))}
        {documents.filter(document => document.path !== defaultPath).length > 3 && (
          <button
            className="mt-1 text-[11px] text-muted-foreground hover:text-foreground"
            onClick={() => open('report')}
          >
            View all results
          </button>
        )}
      </Section>
      </Shortcut>
      <Shortcut id="workshop:schedules" label="Schedules & triggers" icon={Clock3}>
      <Section
        title="Scheduled work"
        action={
          <button
            type="button"
            aria-label="Manage schedules and triggers"
            title="Manage schedules and triggers"
            onClick={() => open('workshop', 'schedules')}
            className="grid h-6 w-6 place-items-center rounded-md text-muted-foreground transition-colors hover:bg-background/50 hover:text-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
          >
            <ChevronRight aria-hidden="true" className="h-3.5 w-3.5" />
          </button>
        }
      >
        {!jobs ? (
          <p className="py-1 text-xs text-muted-foreground">Loading schedules…</p>
        ) : jobs.error ? (
          <button
            className="text-xs text-muted-foreground underline"
            onClick={() => setTick((value) => value + 1)}
          >
            Could not load schedules · Retry
          </button>
        ) : jobs.jobs.length ? (
          <>
            {jobs.jobs.slice(0, 2).map((job) => (
              <Row
                key={job.id}
                icon={Clock3}
                detail={job.enabled ? 'Active' : 'Paused'}
                onClick={() => open('workshop', 'schedules')}
              >
                {job.name}
              </Row>
            ))}
            {jobs.jobs.length > 2 && (
              <button
                className="mt-1 text-[11px] text-muted-foreground hover:text-foreground"
                onClick={() => open('workshop', 'schedules')}
              >
                View all {jobs.jobs.length} schedules
              </button>
            )}
          </>
        ) : null}
        <WorkspaceAutomationLinks
          workspacePath={workspacePath}
          profileId="agentworks"
          projectId={workspacePath}
          scheduleCount={jobs && !jobs.error ? jobs.jobs.length : null}
          onOpen={(section) => open('workshop', section)}
        />
      </Section>
      {jobs?.partial && (
        <p role="status" className="text-[11px] text-muted-foreground">
          Some schedules are outside this summary. Open schedules to see all.
        </p>
      )}
      </Shortcut>
      <Section title="Knowledge">
        <Shortcut id="knowledge" label="Knowledge & learnings" icon={BookOpen}><Row icon={BookOpen} onClick={() => open('knowledge')}>
          Knowledge & learnings
        </Row></Shortcut>
      </Section>
      <Section title="Reviews & decisions">
        <Shortcut id="pulse" label="Pulse reviews" icon={Activity}><Row
          icon={Activity}
          hint="Review and improve this workflow (Pulse)"
          detail={manifest ? (pulseEnabled ? 'Enabled' : 'Off') : undefined}
          onClick={() => open('pulse')}
        >
          Pulse reviews
        </Row></Shortcut>
        <Shortcut id="human-actions" label="Decisions" icon={Hand}><Row
          icon={Hand}
          attention={decisions.loaded && decisions.count > 0}
          detail={
            decisions.loaded ? (decisions.count ? `${decisions.count} waiting` : 'Up to date') : undefined
          }
          onClick={() => open('human-actions')}
        >
          Decisions
        </Row></Shortcut>
      </Section>
      <Section title="Project tools">
        <Shortcut id="browser" label="Browser" icon={WORKSPACE_VIEWS.find(item => item.id === 'browser')!.icon}><WorkspaceContextBrowser
          workspacePath={workspacePath}
          profileId="workflow"
          readOnly={!canWrite}
          onOpen={() => open('browser')}
        /></Shortcut>
        <Shortcut id="workshop:chats" label="Chat history" icon={MessageSquareText}><Row icon={MessageSquareText} onClick={() => open('workshop', 'chats')}>
          Chat history
        </Row></Shortcut>
      </Section>
      <Shortcut id="mcp" label="Connections & sharing" icon={Plug}><div className="border-t border-border pt-3"><Row icon={Plug} hint="What this agent can use" onClick={() => open('mcp', 'overview')}>Connections</Row><Row icon={Users} hint="People, dashboard and ways to use this project" onClick={() => open('mcp', 'use-share')}>Use &amp; share</Row></div></Shortcut>
    </WorkspaceContextCard>
  )
}

/** Kept outside the pane so tools remain reachable when the pane is closed. */
export function WorkflowContextTools({ relayMode = false }: { relayMode?: boolean }) {
  const open = useWorkflowStore((state) => state.openWorkspaceView)
  const activeView = useWorkflowStore((state) => state.workflowWorkspaceView ?? state.lastCanvasView)
  const isMultiUser = useAuthStore((state) => state.isMultiUserMode)
  const views = workflowContextViews(relayMode, isMultiUser)
  return (
    <WorkspaceToolsMenu label="Project settings">
      {(close) => <>
            {groupWorkspacePanels(views.filter(item => isProjectSettingsPanel(item.id)).sort((a, b) => a.id === 'identity' ? -1 : b.id === 'identity' ? 1 : 0)).map(group => <WorkspaceToolsGroup key={group.title} title={group.title}>
            {group.items.map((item) => (
              <button
                type="button"
                role="menuitem"
                aria-current={activeView === item.id ? 'page' : undefined}
                key={item.id}
                className={`flex w-full items-center gap-2 rounded-md px-3 py-2 text-left text-xs hover:bg-muted ${activeView === item.id ? 'bg-primary/10 text-primary' : ''}`}
                onClick={() => {
                  open(item.id as WorkspaceViewId)
                  close()
                }}
              >
                <item.icon className="h-3.5 w-3.5 text-muted-foreground" />
                {item.id === 'identity' ? 'Project settings' : item.label}
                <ChevronRight className="ml-auto h-3 w-3 text-muted-foreground" />
              </button>
            ))}
            </WorkspaceToolsGroup>)}
      </>}
    </WorkspaceToolsMenu>
  )
}

function openProjectActivity(workspacePath: string) {
  useLLMStore.getState().setShowLLMModal(false)
  const app = useAppStore.getState()
  app.setShowSchedulesOverview(false)
  app.setActivityWorkflowPath(workspacePath)
  app.setShowWorkflowsOverview(true)
}
