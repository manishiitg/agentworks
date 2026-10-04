import { WorkspaceBackButton } from './workspace/WorkspaceBackButton'
import { lazy, Suspense, useState } from 'react'
import { CalendarClock, Webhook } from 'lucide-react'
import { useLLMStore } from '../stores/useLLMStore'
import { useAppStore } from '../stores/useAppStore'
import { useProductSurfaceStore } from '../stores/useProductSurfaceStore'
import { useWorkflowStore } from '../stores/useWorkflowStore'
import { useGlobalPresetStore } from '../stores/useGlobalPresetStore'
import { selectWorkflowPreset } from '../utils/workflowNavigation'
import { openWorkflowPresetPage } from '../utils/workflowSessionRestore'
import type { TriggerOwner } from './scheduler/GlobalTriggersView'

const Schedules = lazy(() => import('./scheduler/WorkflowScheduleRunsPanel'))
const Triggers = lazy(() => import('./scheduler/GlobalTriggersView'))
type OverviewTab = 'schedules' | 'triggers'
const tabs = [
  { id: 'schedules' as const, label: 'Schedules', icon: CalendarClock },
  { id: 'triggers' as const, label: 'Triggers', icon: Webhook },
]

/** Shared schedules and triggers overview, opened from the top-bar icon. */
export default function SchedulesPage() {
  const showSchedules = useAppStore(state => state.showSchedulesOverview)
  const showProviders = useLLMStore(state => state.showLLMModal)
  const setShowSchedulesOverview = useAppStore(state => state.setShowSchedulesOverview)
  const productSurface = useProductSurfaceStore(state => state.productSurface)
  const [activeTab, setActiveTab] = useState<OverviewTab>('schedules')
  const isCrew = productSurface === 'work'
  const workflowKind = 'workflow'

  const openTriggerOwner = (owner: TriggerOwner) => {
    setShowSchedulesOverview(false)
    if (owner.kind === 'crew') {
      const surface = useProductSurfaceStore.getState()
      surface.setSelectedWorkProjectId(owner.id)
      surface.setPendingWorkView('triggers')
      surface.setProductSurface('work')
    } else {
      useProductSurfaceStore.getState().setProductSurface('agentworks')
      const preset = useGlobalPresetStore.getState().workflowPresets.find(item => item.id === owner.id)
      if (preset) {
        void openWorkflowPresetPage(preset).catch(() => {
          selectWorkflowPreset(owner.id)
        }).finally(() => {
          if (useGlobalPresetStore.getState().activePresetIds.workflow === owner.id) {
            useWorkflowStore.getState().openWorkspaceView('workshop', 'triggers')
          }
        })
      } else {
        selectWorkflowPreset(owner.id)
        useWorkflowStore.getState().openWorkspaceView('workshop', 'triggers')
      }
    }
  }

  return (
    <section aria-label="Schedules and triggers" className="flex h-full min-h-0 flex-col bg-background">
      <header className="shrink-0 border-b border-border px-4 sm:px-6">
        <div className="flex flex-wrap items-center gap-3 py-3">
          <WorkspaceBackButton />
          <span aria-hidden="true" className="h-4 w-px bg-border" />
          <CalendarClock className="h-4 w-4 text-primary" />
          <h1 className="text-base font-semibold text-foreground">Schedules and triggers</h1>
        </div>
        <div role="tablist" aria-label="Schedules and triggers" className="flex flex-wrap gap-1 pb-2">
          {tabs.map(tab => <button key={tab.id} type="button" role="tab" aria-selected={activeTab === tab.id}
            onClick={() => setActiveTab(tab.id)}
            className={`inline-flex items-center gap-1.5 rounded-md px-3 py-1.5 text-xs font-medium transition-colors ${activeTab === tab.id ? 'bg-primary/10 text-primary' : 'text-muted-foreground hover:bg-muted hover:text-foreground'}`}>
            <tab.icon className="h-3.5 w-3.5" />{tab.label}
          </button>)}
        </div>
      </header>
      <div role="tabpanel" className="min-h-0 flex-1">
        <Suspense fallback={<div className="p-6 text-sm text-muted-foreground">Loading overview…</div>}>
          {activeTab === 'schedules' && <Schedules key={productSurface} embedded active={showSchedules && !showProviders}
            onClose={() => setShowSchedulesOverview(false)} entityType={isCrew ? 'product' : 'workflow'}
            productProfileId={isCrew ? 'work' : undefined} workflowKind={isCrew ? undefined : workflowKind} />}
          {activeTab === 'triggers' && <Triggers key={productSurface} kind={isCrew ? 'crew' : 'workflow'}
            workflowKind={isCrew ? undefined : workflowKind} onOpen={openTriggerOwner} />}
        </Suspense>
      </div>
    </section>
  )
}
