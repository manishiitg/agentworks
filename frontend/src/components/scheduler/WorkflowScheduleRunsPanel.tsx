import React, { lazy, useEffect, useState } from 'react'
import { Bot, CalendarClock, ChevronLeft, Clock, Search, MessageSquare, Webhook, Loader, Pause, Play } from 'lucide-react'
import ModalPortal from '../ui/ModalPortal'
import { Button } from '../ui/Button'
import type { ScheduledJob } from '../../services/api-types'
import { TooltipProvider } from '../ui/tooltip'
import { useScheduleRunsData } from './scheduleRuns/useScheduleRunsData'
import type { WorkflowScope } from './scheduleRuns/helpers'
import { ScheduleRunsHeader } from './scheduleRuns/ScheduleRunsHeader'
import type { ScheduleStatusSnapshot } from './scheduleRuns/ScheduleStatusPills'
import { ScheduleOverviewView } from './scheduleRuns/ScheduleOverviewView'
import { ScheduleCalendarView } from './scheduleRuns/ScheduleCalendarView'
import { ScheduleGroupsView } from './scheduleRuns/ScheduleGroupsView'
import { ScheduleListView } from './scheduleRuns/ScheduleListView'
import { ScheduleTableView } from './scheduleRuns/ScheduleTableView'
import WorkflowAPITriggersView from '../workflow/WorkflowAPITriggersView'
import type { ProductTriggerScope } from '../../api/productWebhooks'
import { useWorkflowStore } from '../../stores/useWorkflowStore'
import { TriggerDeliveryHistoryPanel } from '../automation/TriggerDeliveryHistoryPanel'
import { WorkspaceViewHeader } from '../workflow/WorkspaceViewHeader'

const ProductAPITriggersView = lazy(() => import('../workflow/ProductAPITriggersView'))

interface WorkflowScheduleRunsPanelProps {
  onClose: () => void
  embedded?: boolean
  active?: boolean
  onJobsLoaded?: (jobs: ScheduledJob[]) => void
  workflowScope?: WorkflowScope
  headerAction?: React.ReactNode
  entityType?: 'workflow' | 'product'
  productProfileId?: string
  canManage?: boolean
  scopeNoun?: 'automation' | 'project'
  productTriggerScope?: ProductTriggerScope
  botContent?: React.ReactNode
  showAutomationTabs?: boolean
  /** Embedded hosts with their own header (the Automation hub) hide this panel's header row. */
  hideHeader?: boolean
  /** Hub-owned refresh: bumping this token reloads jobs. Starts at 0 (no reload). */
  refreshToken?: number
  /** Reports status snapshots so an owning header can show pills and spinners. */
  onStatus?: (status: ScheduleStatusSnapshot) => void
}

const WorkflowScheduleRunsPanel: React.FC<WorkflowScheduleRunsPanelProps> = ({ onClose, onJobsLoaded, workflowScope, embedded = false, active = true, headerAction, entityType = 'workflow', productProfileId, canManage, scopeNoun = 'automation', productTriggerScope, botContent, showAutomationTabs = true, hideHeader = false, refreshToken = 0, onStatus }) => {
  const panel = useScheduleRunsData({ onClose, onJobsLoaded, workflowScope, entityType, productProfileId, canManage, active })
  const { loadJobs, summary, workflowScheduleSummary, isLoading, isSchedulerPaused, isWorkflowScoped } = panel
  useEffect(() => {
    if (refreshToken) void loadJobs(true)
  }, [refreshToken, loadJobs])
  useEffect(() => {
    onStatus?.({ summary, workflowScheduleSummary, isLoading, isSchedulerPaused, isWorkflowScoped })
  }, [onStatus, summary, workflowScheduleSummary, isLoading, isSchedulerPaused, isWorkflowScoped])
  const workspaceViewTarget = useWorkflowStore(state => state.workspaceViewTarget)
  const hasWorkflowWebhooks = entityType === 'workflow' && Boolean(workflowScope?.workspacePath)
  const hasProductWebhooks = entityType === 'product' && Boolean(productTriggerScope)

  const hasWebhooks = hasWorkflowWebhooks || hasProductWebhooks
  const [automationSection, setAutomationSection] = useState<'schedules' | 'webhooks' | 'bots'>(() =>
    hasWebhooks && workspaceViewTarget?.view === 'schedules' && workspaceViewTarget.target === 'webhooks'
      ? 'webhooks'
      : 'schedules',
  )
  useEffect(() => {
    if (workspaceViewTarget?.view !== 'schedules') return
    setAutomationSection(hasWebhooks && workspaceViewTarget.target === 'webhooks' ? 'webhooks' : 'schedules')
  }, [hasWebhooks, workspaceViewTarget])
  const {
    error,
    activeView,
    setActiveView,
    activeFilter,
    setActiveFilter,
    searchQuery,
    setSearchQuery,
    selectedWorkflowFilter,
    setSelectedWorkflowFilter,
    panelJobs,
    workflowOptions,
    filteredJobs,
    workflowGroups,
    filterPills,
    isReadOnlyUser,
    isUpdatingSchedulerPause,
    handleToggleGlobalPause,
  } = panel

  const compact = embedded && !isWorkflowScoped

  const views = [
    { key: 'by-workflow' as const, label: entityType === 'product' ? 'Crews' : 'Workflows' },
    { key: 'schedules' as const, label: 'List' },
    { key: 'calendar' as const, label: 'Calendar' },
  ]
  const viewControls = (
    <div className="flex items-center gap-1 rounded-lg bg-muted/40 p-0.5" role="group" aria-label="Schedule views">
      {views.map(view => <button key={view.key} type="button" aria-pressed={activeView === view.key}
        onClick={() => setActiveView(view.key)}
        className={`rounded-md px-3 py-1.5 text-xs font-medium transition-colors ${activeView === view.key
          ? 'bg-background text-foreground shadow-sm ring-1 ring-border'
          : 'text-muted-foreground hover:bg-background/50 hover:text-foreground'}`}>
        {view.label}
      </button>)}
    </div>
  )

  const panelElement = (
    <TooltipProvider delayDuration={300}>
    <div
      className={embedded
        ? 'flex h-full min-h-0 min-w-0 w-full max-w-none flex-1 bg-background'
        : 'fixed inset-0 z-[9999] flex items-center justify-center bg-black/50'}
      onClick={embedded ? undefined : (e) => { if (e.target === e.currentTarget) onClose() }}
    >
      <div className={embedded
        ? 'flex h-full min-h-0 min-w-0 w-full max-w-none flex-1 flex-col bg-card text-card-foreground'
        : 'mx-4 flex max-h-[85vh] w-full max-w-6xl flex-col rounded-xl border border-border bg-card text-card-foreground shadow-2xl'}>

        {showAutomationTabs && (hasWebhooks || botContent) && (
          <div className="flex shrink-0 items-center gap-1 border-b border-border px-4 py-2 sm:px-6" role="tablist" aria-label="Automation channels">
            <button type="button" role="tab" aria-selected={automationSection === 'schedules'}
              onClick={() => setAutomationSection('schedules')}
              className={`inline-flex items-center gap-2 rounded-md px-3 py-1.5 text-xs font-medium transition-colors ${automationSection === 'schedules' ? 'bg-muted text-foreground' : 'text-muted-foreground hover:bg-muted/60 hover:text-foreground'}`}>
              <CalendarClock className="h-3.5 w-3.5" />Schedules
            </button>
            {hasWebhooks && <button type="button" role="tab" aria-selected={automationSection === 'webhooks'}
              onClick={() => setAutomationSection('webhooks')}
              className={`inline-flex items-center gap-2 rounded-md px-3 py-1.5 text-xs font-medium transition-colors ${automationSection === 'webhooks' ? 'bg-muted text-foreground' : 'text-muted-foreground hover:bg-muted/60 hover:text-foreground'}`}>
              <Webhook className="h-3.5 w-3.5" />{hasProductWebhooks ? 'Triggers' : 'Webhooks'}
            </button>}
            {botContent && <button type="button" role="tab" aria-selected={automationSection === 'bots'}
              onClick={() => setAutomationSection('bots')}
              className={`inline-flex items-center gap-2 rounded-md px-3 py-1.5 text-xs font-medium transition-colors ${automationSection === 'bots' ? 'bg-muted text-foreground' : 'text-muted-foreground hover:bg-muted/60 hover:text-foreground'}`}>
              <Bot className="h-3.5 w-3.5" />Bots
            </button>}
          </div>
        )}

        {automationSection === 'bots' && botContent ? (
          <div className="flex min-h-0 flex-1 flex-col">
            {!hideHeader && <WorkspaceViewHeader icon={Bot} title="Bots" helpTopic="Automation · Bots" actions={headerAction} />}
            <div className="min-h-0 flex-1 overflow-y-auto">{botContent}</div>
          </div>
        ) : automationSection === 'webhooks' && hasProductWebhooks && productTriggerScope ? (
          <div className="min-h-0 flex-1">
            <ProductAPITriggersView
              scope={productTriggerScope}
              deliveryHistory={workflowScope?.workspacePath ? <TriggerDeliveryHistoryPanel workspacePath={workflowScope.workspacePath} entityType="product" /> : undefined}
              headerAction={headerAction}
            />
          </div>
        ) : automationSection === 'webhooks' && workflowScope?.workspacePath ? (
          <div className="min-h-0 flex-1">
            <WorkflowAPITriggersView
              workspacePath={workflowScope.workspacePath}
              deliveryHistory={<TriggerDeliveryHistoryPanel workspacePath={workflowScope.workspacePath} entityType="workflow" />}
              headerAction={headerAction}
            />
          </div>
        ) : <>

        {/* Header */}
        {!hideHeader && (
          <ScheduleRunsHeader panel={panel} onClose={onClose} showClose={!embedded} headerAction={headerAction}
            compact={compact} navigation={compact ? viewControls : undefined}
            helpTopic={showAutomationTabs && (hasWebhooks || botContent) ? 'Automation · Schedules' : undefined} />
        )}

        {/* Body */}
        <div className="flex-1 overflow-y-auto">
          {isSchedulerPaused && !isLoading && !error && (
            <div role="status" aria-label="All schedules paused" className="border-b border-warning/30 bg-warning/10 px-5 py-2.5">
              <div className="flex flex-wrap items-center justify-between gap-2">
                <div className="flex min-w-0 flex-1 flex-wrap items-center gap-x-2 gap-y-0.5 text-xs">
                  <span className="inline-flex items-center gap-1.5 font-medium text-warning">
                    <Pause className="h-3.5 w-3.5 shrink-0" />
                    All schedules are paused.
                  </span>
                  <span className="text-muted-foreground">Timed runs won't start until you resume them.</span>
                </div>
                {!isReadOnlyUser && (
                  <Button variant="outline" size="sm" onClick={() => void handleToggleGlobalPause()} disabled={isUpdatingSchedulerPause}>
                    {isUpdatingSchedulerPause ? <Loader className="h-3.5 w-3.5 animate-spin" /> : <Play className="h-3.5 w-3.5" />}
                    Resume schedules
                  </Button>
                )}
              </div>
            </div>
          )}
          {!isWorkflowScoped && panelJobs.length > 0 && !isLoading && !error && (
            <div className="flex flex-wrap items-center gap-2 border-b border-border bg-card px-5 py-2 text-xs" aria-label="Schedule health summary">
              <span className="mr-1 font-medium text-foreground">Needs attention</span>
              {summary.missed > 0 && <button type="button" onClick={() => { setSelectedWorkflowFilter('all'); setSearchQuery(''); setActiveFilter('missed'); setActiveView('schedules') }} className="rounded-full border border-warning/30 bg-warning/10 px-2.5 py-1 text-warning hover:bg-warning/20">{summary.missed} with missed runs</button>}
              {summary.issues > 0 && <button type="button" onClick={() => { setSelectedWorkflowFilter('all'); setSearchQuery(''); setActiveFilter('issues'); setActiveView('schedules') }} className="rounded-full border border-destructive/30 bg-destructive/10 px-2.5 py-1 text-destructive hover:bg-destructive/20">{summary.issues} with run issues</button>}
              {summary.waiting > 0 && <button type="button" onClick={() => { setSelectedWorkflowFilter('all'); setSearchQuery(''); setActiveFilter('waiting'); setActiveView('schedules') }} className="rounded-full border border-info/30 bg-info/10 px-2.5 py-1 text-info hover:bg-info/20">{summary.waiting} queued or waiting</button>}
              {summary.overlap > 0 && <button type="button" onClick={() => { setSelectedWorkflowFilter('all'); setSearchQuery(''); setActiveFilter('overlap'); setActiveView('schedules') }} className="rounded-full border border-warning/30 bg-warning/10 px-2.5 py-1 text-warning hover:bg-warning/20">{summary.overlap} at overlap risk</button>}
              {summary.missed === 0 && summary.issues === 0 && summary.waiting === 0 && summary.overlap === 0 && <span className="text-muted-foreground">No current issues reported</span>}
            </div>
          )}
          {(!compact || hideHeader) && panelJobs.length > 0 && (
            <div className="sticky top-0 z-10 border-b border-border bg-card/95 backdrop-blur px-5 py-3">
              <div className="space-y-2">
                <div className="flex flex-wrap items-center gap-2">
                  {/* The view switcher and search earn their place only across
                      automations. A workflow-scoped view already identifies one
                      automation and always shows its schedule list, so it keeps
                      just the state filter. */}
                  {!isWorkflowScoped && viewControls}
                  {(activeView === 'schedules' || activeView === 'by-workflow') && (
                    <div className="flex min-w-0 flex-1 flex-wrap items-center gap-2">
                      {!isWorkflowScoped && (
                        <div className="relative min-w-48 flex-1">
                          <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-muted-foreground" />
                          <input
                            type="text"
                            value={searchQuery}
                            onChange={(e) => setSearchQuery(e.target.value)}
                            aria-label="Search schedules"
                            placeholder="Search automations or schedules…"
                            className="w-full rounded-lg border border-border bg-background pl-9 pr-3 py-1.5 text-sm text-foreground placeholder:text-muted-foreground focus:outline-none focus:ring-2 focus:ring-primary/50"
                          />
                        </div>
                      )}
                      {!isWorkflowScoped && (activeView !== 'by-workflow' || selectedWorkflowFilter !== 'all') && (
                        <select
                          aria-label={entityType === 'product' ? 'Filter by Crew' : 'Filter by automation'}
                          value={selectedWorkflowFilter}
                          onChange={(event) => setSelectedWorkflowFilter(event.target.value)}
                          className="min-w-40 max-w-full rounded-lg border border-border bg-background px-3 py-1.5 text-sm text-foreground focus:outline-none focus:ring-2 focus:ring-primary/50"
                        >
                          <option value="all">{entityType === 'product' ? 'All Crews' : 'All automations'}</option>
                          {workflowOptions.map((option) => (
                            <option key={option.value} value={option.value}>{option.label}</option>
                          ))}
                        </select>
                      )}
                      <div className="flex items-center gap-2">
                        <select aria-label="Filter schedules by state" value={activeFilter}
                          onChange={event => setActiveFilter(event.target.value as typeof activeFilter)}
                          className="rounded-lg border border-border bg-background px-3 py-1.5 text-sm text-foreground focus:outline-none focus:ring-2 focus:ring-primary/50">
                          {filterPills.map(pill => <option key={pill.key} value={pill.key}>{pill.key === 'all' ? 'All states' : pill.label} ({pill.count})</option>)}
                        </select>
                      </div>
                    </div>
                  )}
                </div>
                {activeView === 'schedules' && (
                  <div className="flex items-center gap-1.5 text-xs text-muted-foreground">
                    <MessageSquare className="h-3.5 w-3.5 shrink-0" />
                    <span>To change a schedule, ask the automation agent in Chat.</span>
                  </div>
                )}
              </div>
            </div>
          )}

          {isLoading && panelJobs.length === 0 ? (
            <div className="flex items-center justify-center h-40 text-sm text-muted-foreground">Loading...</div>
          ) : error ? (
            <div className="flex items-center justify-center px-6 py-10">
              <p role="alert" className="rounded-lg border border-destructive/30 bg-destructive/10 px-4 py-3 text-sm text-destructive">{error}</p>
            </div>
          ) : panelJobs.length === 0 ? (
            <div className="flex flex-col items-center justify-center h-48 gap-3 px-6 text-center text-sm text-muted-foreground">
              <Clock className="w-8 h-8 opacity-30" />
              <div>
                <p>{isWorkflowScoped ? `No schedules for this ${scopeNoun} yet.` : 'No automation schedules yet.'}</p>
                <p className="mt-1 text-xs">
                  {isWorkflowScoped
                    ? `Ask chat to schedule this ${scopeNoun} when you are ready.`
                    : 'Ask chat to schedule an automation when you are ready.'}
                </p>
              </div>
            </div>
          ) : activeView === 'overview' ? (
            <ScheduleOverviewView panel={panel} />
          ) : activeView === 'calendar' ? (
            <ScheduleCalendarView panel={panel} />
          ) : (
            <>
              {!isWorkflowScoped && activeView === 'schedules' && selectedWorkflowFilter !== 'all' && (
                <div className="flex items-center gap-2 px-4 pt-3 sm:px-6">
                  <button
                    type="button"
                    onClick={() => {
                      setSelectedWorkflowFilter('all')
                      setActiveView('by-workflow')
                    }}
                    className="inline-flex items-center gap-1 text-xs font-medium text-primary hover:underline"
                  >
                    <ChevronLeft className="h-3.5 w-3.5" /> {entityType === 'product' ? 'All Crews' : 'All workflows'}
                  </button>
                  <span className="truncate text-xs text-muted-foreground">
                    {workflowOptions.find(option => option.value === selectedWorkflowFilter)?.label}
                  </span>
                </div>
              )}
              {compact && !hideHeader && (
              <div className="border-b border-border px-4 sm:px-6 py-2.5">
                <div className="flex flex-wrap items-center gap-2">
                    <div className="flex min-w-0 flex-1 flex-wrap gap-2">
                      <div className="relative min-w-48 flex-1">
                        <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-muted-foreground" />
                        <input
                          type="text"
                          value={searchQuery}
                          onChange={(e) => setSearchQuery(e.target.value)}
                          aria-label="Search schedules"
                          placeholder={isWorkflowScoped ? 'Search schedules…' : 'Search automations or schedules…'}
                          className="w-full rounded-lg border border-border bg-background pl-9 pr-3 py-1.5 text-sm text-foreground placeholder:text-muted-foreground focus:outline-none focus:ring-2 focus:ring-primary/50"
                        />
                      </div>
                      {!isWorkflowScoped && (activeView !== 'by-workflow' || selectedWorkflowFilter !== 'all') && (
                        <select
                          aria-label={entityType === 'product' ? 'Filter by Crew' : 'Filter by automation'}
                          value={selectedWorkflowFilter}
                          onChange={(event) => setSelectedWorkflowFilter(event.target.value)}
                          className="min-w-40 max-w-full rounded-lg border border-border bg-background px-3 py-1.5 text-sm text-foreground focus:outline-none focus:ring-2 focus:ring-primary/50"
                        >
                          <option value="all">{entityType === 'product' ? 'All Crews' : 'All automations'}</option>
                          {workflowOptions.map((option) => (
                            <option key={option.value} value={option.value}>{option.label}</option>
                          ))}
                        </select>
                      )}
                    </div>

                  <div className="flex items-center gap-2">
                    <select aria-label="Filter schedules by state" value={activeFilter}
                      onChange={event => setActiveFilter(event.target.value as typeof activeFilter)}
                      className="rounded-lg border border-border bg-background px-3 py-1.5 text-sm text-foreground focus:outline-none focus:ring-2 focus:ring-primary/50">
                      {filterPills.map(pill => <option key={pill.key} value={pill.key}>{pill.key === 'all' ? 'All states' : pill.label} ({pill.count})</option>)}
                    </select>
                  </div>
                </div>
              </div>
              )}
            </>
          )}
          {panelJobs.length > 0 && activeView === 'by-workflow' && workflowGroups.length === 0 && (
            <div className="flex flex-col items-center justify-center h-40 gap-2 text-sm text-muted-foreground px-6 text-center">
              <Search className="w-8 h-8 opacity-30" />
              <p>No automations match the current filter.</p>
              <button
                onClick={() => {
                  setSearchQuery('')
                  setActiveFilter('all')
                  setSelectedWorkflowFilter('all')
                }}
                className="text-xs text-primary hover:underline"
              >
                Clear search and show all schedules
              </button>
            </div>
          )}
          {panelJobs.length > 0 && activeView === 'by-workflow' && workflowGroups.length > 0 && (
            <ScheduleGroupsView panel={panel} entityLabel={entityType === 'product' ? 'Crew' : 'Automation'} />
          )}
          {panelJobs.length > 0 && activeView === 'schedules' && filteredJobs.length === 0 && (
            <div className="flex flex-col items-center justify-center h-40 gap-2 text-sm text-muted-foreground px-6 text-center">
              <Search className="w-8 h-8 opacity-30" />
              <p>No schedules match the current filter.</p>
              <button
                onClick={() => {
                  setSearchQuery('')
                  setActiveFilter('all')
                  setSelectedWorkflowFilter('all')
                }}
                className="text-xs text-primary hover:underline"
              >
                Clear search and show all schedules
              </button>
            </div>
          )}
          {panelJobs.length > 0 && activeView === 'schedules' && filteredJobs.length > 0 && (
            compact ? <ScheduleTableView panel={panel} entityType={entityType} /> : <ScheduleListView panel={panel} />
          )}
        </div>
        </>}
      </div>

    </div>
    </TooltipProvider>
  )

  return embedded ? panelElement : <ModalPortal>{panelElement}</ModalPortal>
}

export default WorkflowScheduleRunsPanel
