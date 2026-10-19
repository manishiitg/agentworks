import { groupWorkspacePanels } from '../../components/workspace/workspacePanelGroups'
import { WorkspaceToolsMenu, WorkspaceToolsGroup } from '../../components/workspace/WorkspaceToolsMenu'
import { workWorkspacePanels } from './workWorkspacePanels'
import type { ProductPanel } from '../productPanels'
import { WorkspaceContextBrowser } from '../../components/workspace/WorkspaceContextBrowser'
import { WorkspaceAutomationLinks } from '../../components/workspace/WorkspaceAutomationLinks'
import { setWorkspaceViewTarget } from '../../hooks/useWorkspaceViewTarget'
import { useEffect, useState } from 'react'
import {
  BookOpen,
  ChevronRight,
  Clock3,
  Files,
  Hand,
  LayoutDashboard,
  MessageSquareText,
  Plug,
  Users,
} from 'lucide-react'
import {
  WorkspaceContextCard,
  WorkspaceContextRow as Row,
  WorkspaceContextSection as Section,
  WorkspaceContextShortcut as Shortcut,
} from '../../components/workspace/WorkspaceContextCard'
import { schedulerApi } from '../../api/scheduler'
import { agentApi } from '../../services/api'
import type { ScheduledJob } from '../../services/api-types'
import { useLiveRefetch } from '../../hooks/useLiveRefetch'
import { useReportDocuments, selectReportDocument } from '../../components/workflow/reportDocuments'
import { reportDocumentLabel } from '../../components/workflow/reportDocumentLabels'
import { WORK_PANELS } from '../productPanels'
import { isWorkWorkspaceViewEnabled } from './workViewGating'
import type { WorkWorkspaceView } from './WorkWorkspacePane'
import { projectProductConfig } from './projectProduct'
import { WorkspaceMoreTools, isProjectSettingsPanel } from '../../components/workspace/WorkspaceMoreTools'

export function WorkContextPanel({
  workspacePath,
  projectId = '',
  profileId = 'work',
  panels,
  onOpen,
  enabledPanels,
}: {
  workspacePath: string
  projectId?: string
  profileId?: 'work' | 'code'
  panels?: ProductPanel<WorkWorkspaceView>[]
  onOpen: (view: WorkWorkspaceView) => void
  enabledPanels?: Set<string>
}) {
  const allowed = (view: WorkWorkspaceView) =>
    panels ? panels.some((item) => item.id === view) : isWorkWorkspaceViewEnabled(view, enabledPanels)
  const showSchedules = allowed('schedules')
  const showSuggestions = allowed('suggestions')
  const [snapshot, setSnapshot] = useState<{
    path: string
    profile: string
    jobs: ScheduledJob[]
    count: number | null
    error: boolean
    partial: boolean
  } | null>(null)
  const [tick, setTick] = useState(0)
  const { documents, defaultPath } = useReportDocuments(allowed('dashboard') ? workspacePath : '')
  useLiveRefetch(() => setTick((value) => value + 1), {
    kinds: ['schedules', 'human_inputs'],
    workflow: workspacePath,
    fallbackMs: 30000,
    enabled: showSchedules || showSuggestions,
  })
  useEffect(() => {
    let disposed = false
    void Promise.allSettled([
      showSchedules ? schedulerApi.listJobs({ entity_type: 'product', limit: 1000 }) : Promise.resolve({ jobs: [], total: 0 }),
      showSuggestions
        ? agentApi.listReportHumanInputs(workspacePath, 'pending', 'user_suggestion')
        : Promise.resolve(null),
    ]).then(([jobs, decisions]) => {
      if (disposed) return
      setSnapshot({
        path: workspacePath,
        profile: profileId,
        jobs:
          jobs.status === 'fulfilled'
            ? (jobs.value.jobs || []).filter((job) => job.workspace_path === workspacePath)
            : [],
        count:
          decisions.status === 'fulfilled' && decisions.value?.success
            ? (decisions.value.inputs || []).filter(
                (item) => item.workspace_path === workspacePath && item.status === 'pending'
              ).length
            : null,
        error: jobs.status === 'rejected',
        partial: jobs.status === 'fulfilled' && jobs.value.total > (jobs.value.jobs || []).length,
      })
    })
    return () => {
      disposed = true
    }
  }, [workspacePath, profileId, tick, showSchedules, showSuggestions])
  const data = snapshot?.path === workspacePath && snapshot.profile === profileId ? snapshot : null
  const policy = projectProductConfig(profileId)
  const fallback = workWorkspacePanels({ profileId, enabledPanels })
  const available = (panels ?? [...fallback.views, ...fallback.ops, ...fallback.setup]).filter(item => allowed(item.id))
  return (
    <WorkspaceContextCard product={policy.shortcutPreferenceProduct ?? profileId} legacyProduct={policy.shortcutPreferenceLegacyProduct} consolidateConnections availableShortcuts={available.filter(item => !isProjectSettingsPanel(item.id)).map(item => ({ ...item, onClick: () => { if (item.id === 'mcp') setWorkspaceViewTarget('mcp', 'overview'); onOpen(item.id) } }))} moreTools={<WorkspaceMoreTools items={available.filter(item => item.id !== 'mcp' && !isProjectSettingsPanel(item.id)).map(item => ({ ...item, onClick: () => onOpen(item.id) }))} />}>
      {allowed('dashboard') && (
        <Shortcut id="dashboard" label="Dashboard" icon={LayoutDashboard}>
        <Section title="Results">
          <Row
            icon={LayoutDashboard}
            onClick={() => {
              selectReportDocument(workspacePath, defaultPath)
              onOpen('dashboard')
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
                  onOpen('dashboard')
                }}
              >
                {reportDocumentLabel(document, documents)}
              </Row>
            ))}
          {documents.filter(document => document.path !== defaultPath).length > 3 && (
            <button type="button" className="mt-1 text-[11px] text-muted-foreground hover:text-foreground" onClick={() => onOpen('dashboard')}>View all results</button>
          )}
        </Section>
        </Shortcut>
      )}
      {allowed('schedules') && (
        <Shortcut id="schedules" label="Schedules & triggers" icon={Clock3}>
        <Section
          title="Scheduled work"
          action={
            <button
              type="button"
              aria-label="Manage schedules and triggers"
              title="Manage schedules and triggers"
              onClick={() => onOpen('schedules')}
              className="grid h-6 w-6 place-items-center rounded-md text-muted-foreground transition-colors hover:bg-background/50 hover:text-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
            >
              <ChevronRight aria-hidden="true" className="h-3.5 w-3.5" />
            </button>
          }
        >
          {!data ? (
            <p className="py-1 text-xs text-muted-foreground">Loading schedules…</p>
          ) : data.error ? (
            <button
              className="text-xs text-muted-foreground underline"
              onClick={() => setTick((value) => value + 1)}
            >
              Could not load schedules · Retry
            </button>
          ) : data.jobs.length ? (
            <>
              {data.jobs.slice(0, 2).map((job) => (
                <Row
                  key={job.id}
                  icon={Clock3}
                  detail={job.enabled ? 'Active' : 'Paused'}
                  onClick={() => onOpen('schedules')}
                >
                  {job.name}
                </Row>
              ))}
              {data.jobs.length > 2 && (
                <button
                  className="mt-1 text-[11px] text-muted-foreground hover:text-foreground"
                  onClick={() => onOpen('schedules')}
                >
                  View all {data.jobs.length} schedules
                </button>
              )}
            </>
          ) : null}
          {data?.partial && (
            <button className="mt-1 text-[11px] text-muted-foreground" onClick={() => onOpen('schedules')}>
              Open schedules to see the complete list
            </button>
          )}
          <WorkspaceAutomationLinks
            workspacePath={workspacePath}
            profileId={profileId}
            projectId={projectId}
            scheduleCount={data && !data.error ? data.jobs.length : null}
            triggersEnabled={enabledPanels?.has('triggers') !== false}
            onOpen={(section) => {
              setWorkspaceViewTarget('workshop', section)
              onOpen('schedules')
            }}
          />
        </Section>
        </Shortcut>
      )}
      {allowed('memory') && (
        <Section title="Knowledge">
          <Shortcut id="memory" label="Memory & skills" icon={BookOpen}><Row icon={BookOpen} hint="Saved facts and reusable instructions" onClick={() => onOpen('memory')}>
            Memory &amp; skills
          </Row></Shortcut>
        </Section>
      )}
      {showSuggestions && (
        <Section title="Reviews">
          <Shortcut id="suggestions" label="Suggestions" icon={Hand}><Row
            icon={Hand}
            attention={Boolean(data?.count)}
            detail={data?.count == null ? undefined : data.count ? `${data.count} waiting` : 'Up to date'}
            onClick={() => onOpen('suggestions')}
          >
            Suggestions
          </Row></Shortcut>
        </Section>
      )}
      {(allowed('browser') || (profileId === 'code' && allowed('files')) || allowed('schedules')) && (
        <Section title="Project tools">
          {profileId === 'code' && allowed('files') && (
            <Shortcut id="files" label="Files" icon={Files}><Row icon={Files} onClick={() => onOpen('files')}>
              Files
            </Row></Shortcut>
          )}
          {allowed('browser') && (
            <Shortcut id="browser" label="Browser" icon={WORK_PANELS.views.find(item => item.id === 'browser')!.icon}><WorkspaceContextBrowser
              workspacePath={workspacePath}
              profileId={profileId}
              onOpen={() => onOpen('browser')}
            /></Shortcut>
          )}
          {allowed('schedules') && (
            <Shortcut id="schedules:chats" label="Chat history" icon={MessageSquareText}><Row
              icon={MessageSquareText}
              onClick={() => {
                setWorkspaceViewTarget('workshop', 'chats')
                onOpen('schedules')
              }}
            >
              Chat history
            </Row></Shortcut>
          )}
        </Section>
      )}
      {allowed('mcp') && <Shortcut id="mcp" label="Connections & sharing" icon={Plug}>
        <div className="border-t border-border pt-3">
          <Row icon={Plug} hint="What this agent can use" onClick={() => { setWorkspaceViewTarget('mcp', 'overview'); onOpen('mcp') }}>Connections</Row>
          <Row icon={Users} hint="People, dashboard and ways to use this project" onClick={() => { setWorkspaceViewTarget('mcp', 'use-share'); onOpen('mcp') }}>Use &amp; share</Row>
        </div>
      </Shortcut>}
    </WorkspaceContextCard>
  )
}

export function WorkContextTools({
  onOpen,
  view,
  enabledPanels,
  panels,
  profileId = 'work',
}: {
  panels?: ProductPanel<WorkWorkspaceView>[]
  profileId?: 'work' | 'code'
  view: WorkWorkspaceView
  onOpen: (view: WorkWorkspaceView) => void
  enabledPanels?: Set<string>
}) {
  const fallback = workWorkspacePanels({ profileId, enabledPanels })
  const secondary = panels ?? [...fallback.views, ...fallback.ops, ...fallback.setup]

  return (
    <WorkspaceToolsMenu label="Project settings">
      {(close) => <>
            {groupWorkspacePanels(secondary.filter(item => isProjectSettingsPanel(item.id))).map(group => <WorkspaceToolsGroup key={group.title} title={group.title}>
            {group.items.map((item) => (
              <button
                type="button"
                role="menuitem"
                aria-current={view === item.id ? 'page' : undefined}
                key={item.id}
                className={`flex w-full items-center gap-2 rounded-md px-3 py-2 text-left text-xs hover:bg-muted ${view === item.id ? 'bg-primary/10 text-primary' : ''}`}
                onClick={() => {
                  onOpen(item.id)
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
