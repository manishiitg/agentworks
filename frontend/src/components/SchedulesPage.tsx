import { useAuthStore } from '../stores/useAuthStore'
import { useWorkspaceConnectionStore } from '../stores/useWorkspaceConnectionStore'
import { WorkspacePageHeader } from './workspace/WorkspacePageHeader'
import { lazy, Suspense, useState } from 'react'
import { useLLMStore } from '../stores/useLLMStore'
import { useAppStore } from '../stores/useAppStore'
import { useProductSurfaceStore } from '../stores/useProductSurfaceStore'
import { useWorkflowStore } from '../stores/useWorkflowStore'
import { useGlobalPresetStore } from '../stores/useGlobalPresetStore'
import { selectWorkflowPreset } from '../utils/workflowNavigation'
import { openWorkflowPresetPage } from '../utils/workflowSessionRestore'
import { WORKSPACE_PAGE_GUTTER } from './workspace/WorkspacePageContent'
import type { TriggerOwner } from './scheduler/GlobalTriggersView'
import Schedules from './scheduler/SimpleSchedulesOverview'
import Workflows from './scheduler/GlobalWorkflowsView'

const Triggers = lazy(() => import('./scheduler/GlobalTriggersView'))
type OverviewTab = 'schedules' | 'triggers' | 'watchers' | 'workflows'
/** The Workflows page: workflows first, then the ways they start (owner, 26 Nov: "Automations" read as schedules only).
 * The owning project is where people manage an item. */
export default function SchedulesPage() {
  const user = useAuthStore(state => state.user?.id)
  const workspace = useWorkspaceConnectionStore(state => state.activeWorkspaceId)
  const initialSearch = useAppStore(state => state.schedulesSearchScope === `${workspace}:${user || ''}` ? state.schedulesSearchQuery : '')
  const showSchedules = useAppStore(state => state.showSchedulesOverview)
  const showProviders = useLLMStore(state => state.showLLMModal)
  const setShowSchedulesOverview = useAppStore(state => state.setShowSchedulesOverview)
  const productSurface = useProductSurfaceStore(state => state.productSurface)
  const [activeTab, setActiveTab] = useState<OverviewTab>('workflows')
  const scope = `${workspace}:${user || ''}:${productSurface}`
  const isCrew = productSurface === 'work'
  const tab = isCrew && activeTab === 'triggers' ? 'workflows' : activeTab
  const tabs: { id: OverviewTab; label: string }[] = [{ id: 'workflows', label: 'Workflows' }, { id: 'schedules', label: 'Schedules' }, { id: 'watchers', label: 'Watchers' }]
  if (!isCrew) tabs.push({ id: 'triggers', label: 'Webhooks' })

  const openTriggerOwner = (owner: TriggerOwner) => {
    setShowSchedulesOverview(false)
    if (owner.kind === 'crew') {
      const surface = useProductSurfaceStore.getState()
      surface.setSelectedWorkProjectId(owner.id)
      surface.setPendingWorkView(tab === 'watchers' ? 'conditions' : 'triggers')
      surface.setProductSurface('work')
    } else {
      useProductSurfaceStore.getState().setProductSurface('agentworks')
      const preset = useGlobalPresetStore.getState().workflowPresets.find(item => item.id === owner.id)
      if (preset) {
        void openWorkflowPresetPage(preset).catch(() => {
          selectWorkflowPreset(owner.id)
        }).finally(() => {
          if (useGlobalPresetStore.getState().activePresetIds.workflow === owner.id) {
            useWorkflowStore.getState().openWorkspaceView('workshop', tab === 'watchers' ? 'conditions' : 'triggers')
          }
        })
      } else {
        selectWorkflowPreset(owner.id)
        useWorkflowStore.getState().openWorkspaceView('workshop', tab === 'watchers' ? 'conditions' : 'triggers')
      }
    }
  }

  return (
    <section aria-label="Workflows" className="flex h-full min-h-0 flex-col bg-background">
      <WorkspacePageHeader page="schedules" title="Workflows" showBack={false} />
      <nav aria-label="Workflow pages" className={`flex shrink-0 flex-wrap items-center gap-x-5 border-b border-border ${WORKSPACE_PAGE_GUTTER}`}>
        {tabs.map(item => <button key={item.id} type="button" aria-current={tab === item.id ? 'page' : undefined}
          onClick={() => setActiveTab(item.id)}
          className={`border-b-2 px-1 py-2 text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring ${tab === item.id ? 'border-foreground font-semibold' : 'border-transparent text-muted-foreground hover:text-foreground'}`}>
          {item.label}
        </button>)}
      </nav>
      <div role="region" aria-label={tab === 'schedules' ? 'Schedule list' : tab === 'watchers' ? 'Watcher list' : tab === 'workflows' ? 'Workflow list' : 'Webhook list'} className="min-h-0 flex-1">
        {/* Workflows and Schedules start their data read with the page, not after
            a lazy import. A secondary tab loading must not suspend its list. */}
        {tab === 'workflows' && <Workflows key={scope} crew={isCrew} />}
        <div hidden={tab !== 'schedules'} className="h-full"><Schedules key={scope} initialSearch={initialSearch}
          active={tab === 'schedules' && showSchedules && !showProviders}
          onClose={() => setShowSchedulesOverview(false)} entityType={isCrew ? 'product' : 'workflow'} /></div>
        {(tab === 'watchers' || tab === 'triggers') && <Suspense fallback={<div className={`${WORKSPACE_PAGE_GUTTER} py-4 text-sm text-muted-foreground`}>Loading overview…</div>}>
          <Triggers key={`${scope}:${tab}`} mode={tab === 'watchers' ? 'watchers' : 'webhooks'} kind={isCrew ? 'crew' : 'workflow'}
            onOpen={openTriggerOwner} />
        </Suspense>}
      </div>
    </section>
  )
}
