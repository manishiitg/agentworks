import { useAuthStore } from '../stores/useAuthStore'
import { useWorkspaceConnectionStore } from '../stores/useWorkspaceConnectionStore'
import { WorkspacePageHeader } from './workspace/WorkspacePageHeader'
import { lazy, Suspense, useState } from 'react'
import { CalendarClock } from 'lucide-react'
import { useLLMStore } from '../stores/useLLMStore'
import { useAppStore } from '../stores/useAppStore'
import { useProductSurfaceStore } from '../stores/useProductSurfaceStore'
import { useWorkflowStore } from '../stores/useWorkflowStore'
import { useGlobalPresetStore } from '../stores/useGlobalPresetStore'
import { selectWorkflowPreset } from '../utils/workflowNavigation'
import { openWorkflowPresetPage } from '../utils/workflowSessionRestore'
import type { TriggerOwner } from './scheduler/GlobalTriggersView'

const Schedules = lazy(() => import('./scheduler/SimpleSchedulesOverview'))
const Triggers = lazy(() => import('./scheduler/GlobalTriggersView'))
type OverviewTab = 'schedules' | 'triggers'
/** Shared schedules and triggers overview, opened from the top-bar icon. */
export default function SchedulesPage() {
  const user = useAuthStore(state => state.user?.id)
  const workspace = useWorkspaceConnectionStore(state => state.activeWorkspaceId)
  const initialSearch = useAppStore(state => state.schedulesSearchScope === `${workspace}:${user || ''}` ? state.schedulesSearchQuery : '')
  const showSchedules = useAppStore(state => state.showSchedulesOverview)
  const showProviders = useLLMStore(state => state.showLLMModal)
  const setShowSchedulesOverview = useAppStore(state => state.setShowSchedulesOverview)
  const productSurface = useProductSurfaceStore(state => state.productSurface)
  const [activeTab, setActiveTab] = useState<OverviewTab>('schedules')
  const isCrew = productSurface === 'work'

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
      <WorkspacePageHeader page="schedules" title={activeTab === 'schedules' ? 'Schedules' : 'Triggers'} icon={CalendarClock}>
        {activeTab === 'triggers' && <button type="button" onClick={() => setActiveTab('schedules')} className="ml-auto rounded-lg border border-border px-3 py-1.5 text-xs text-muted-foreground">Schedules</button>}
      </WorkspacePageHeader>
      <div role="region" aria-label={activeTab === 'schedules' ? 'Schedule list' : 'Trigger list'} className="min-h-0 flex-1">
        <Suspense fallback={<div className="p-6 text-sm text-muted-foreground">Loading overview…</div>}>
          {activeTab === 'schedules' && <Schedules key={productSurface} initialSearch={initialSearch} active={showSchedules && !showProviders}
            onClose={() => setShowSchedulesOverview(false)} entityType={isCrew ? 'product' : 'workflow'}
            onTriggers={() => setActiveTab('triggers')} />}
          {activeTab === 'triggers' && <Triggers key={productSurface} kind={isCrew ? 'crew' : 'workflow'}
            onOpen={openTriggerOwner} />}
        </Suspense>
      </div>
    </section>
  )
}
