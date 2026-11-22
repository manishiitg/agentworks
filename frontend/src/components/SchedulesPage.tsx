import { useAuthStore } from '../stores/useAuthStore'
import { useWorkspaceConnectionStore } from '../stores/useWorkspaceConnectionStore'
import { WorkspacePageHeader } from './workspace/WorkspacePageHeader'
import { lazy, Suspense, useState } from 'react'
import { Zap } from 'lucide-react'
import { useLLMStore } from '../stores/useLLMStore'
import { useAppStore } from '../stores/useAppStore'
import { useProductSurfaceStore } from '../stores/useProductSurfaceStore'
import { useWorkflowStore } from '../stores/useWorkflowStore'
import { useGlobalPresetStore } from '../stores/useGlobalPresetStore'
import { selectWorkflowPreset } from '../utils/workflowNavigation'
import { openWorkflowPresetPage } from '../utils/workflowSessionRestore'
import { isLocalDeployment } from '../products/productSurfaceConfig'
import { useCapabilitiesStore } from '../stores/useCapabilitiesStore'
import type { TriggerOwner } from './scheduler/GlobalTriggersView'

const Schedules = lazy(() => import('./scheduler/SimpleSchedulesOverview'))
const Triggers = lazy(() => import('./scheduler/GlobalTriggersView'))
type OverviewTab = 'schedules' | 'triggers' | 'watchers'
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
  const localMode = useCapabilitiesStore(state => state.capabilities?.local_mode) ?? isLocalDeployment()
  const scope = `${workspace}:${user || ''}:${productSurface}`
  const isCrew = productSurface === 'work'

  const openTriggerOwner = (owner: TriggerOwner) => {
    setShowSchedulesOverview(false)
    if (owner.kind === 'crew') {
      const surface = useProductSurfaceStore.getState()
      surface.setSelectedWorkProjectId(owner.id)
      surface.setPendingWorkView(activeTab === 'watchers' ? 'conditions' : 'triggers')
      surface.setProductSurface('work')
    } else {
      useProductSurfaceStore.getState().setProductSurface('agentworks')
      const preset = useGlobalPresetStore.getState().workflowPresets.find(item => item.id === owner.id)
      if (preset) {
        void openWorkflowPresetPage(preset).catch(() => {
          selectWorkflowPreset(owner.id)
        }).finally(() => {
          if (useGlobalPresetStore.getState().activePresetIds.workflow === owner.id) {
            useWorkflowStore.getState().openWorkspaceView('workshop', activeTab === 'watchers' ? 'conditions' : 'triggers')
          }
        })
      } else {
        selectWorkflowPreset(owner.id)
        useWorkflowStore.getState().openWorkspaceView('workshop', activeTab === 'watchers' ? 'conditions' : 'triggers')
      }
    }
  }

  return (
    <section aria-label="Automations" className="flex h-full min-h-0 flex-col bg-background">
      <WorkspacePageHeader page="schedules" title="Automations" icon={Zap} />
      <div className="border-b border-border px-5 py-3">
        <div className="mx-auto flex max-w-5xl flex-wrap items-center justify-between gap-3">
          <nav aria-label="Automation types" className="flex items-center gap-1">
            {([{ id: 'schedules', label: 'Schedules' }, { id: 'watchers', label: 'Watchers' }, ...(!localMode ? [{ id: 'triggers', label: 'Webhooks' }] : [])] as const).map(item => <button key={item.id} type="button" aria-current={activeTab === item.id ? 'page' : undefined} onClick={() => setActiveTab(item.id as OverviewTab)} className={`rounded-md px-3 py-1.5 text-sm ${activeTab === item.id ? 'bg-muted font-medium' : 'text-muted-foreground hover:bg-muted'}`}>{item.label}</button>)}
            {localMode && <details className="relative"><summary className="cursor-pointer list-none rounded-md px-3 py-1.5 text-sm text-muted-foreground">More</summary><button type="button" onClick={event => { setActiveTab('triggers'); event.currentTarget.closest('details')?.removeAttribute('open') }} className="absolute left-0 top-full z-20 mt-1 w-64 rounded-lg border border-border bg-popover p-3 text-left text-sm shadow-lg">Webhooks<span className="block text-xs text-muted-foreground">Requires a reachable address for this Mac</span></button></details>}
          </nav>
          <p className="text-xs text-muted-foreground">{localMode ? 'On this Mac · requires your Mac to be on' : 'On this server · runs while your Mac is off'}</p>
        </div>
      </div>
      <div role="region" aria-label={activeTab === 'schedules' ? 'Schedule list' : activeTab === 'watchers' ? 'Watcher list' : 'Webhook list'} className="min-h-0 flex-1">
        <Suspense fallback={<div className="p-6 text-sm text-muted-foreground">Loading overview…</div>}>
          <div hidden={activeTab !== 'schedules'} className="h-full"><Schedules key={scope} initialSearch={initialSearch} active={activeTab === 'schedules' && showSchedules && !showProviders}
            onClose={() => setShowSchedulesOverview(false)} entityType={isCrew ? 'product' : 'workflow'} localMode={localMode}
            /></div>
          {activeTab !== 'schedules' && <Triggers key={`${scope}:${activeTab}`} mode={activeTab === 'watchers' ? 'watchers' : 'webhooks'} kind={isCrew ? 'crew' : 'workflow'}
            onOpen={openTriggerOwner} />}
        </Suspense>
      </div>
    </section>
  )
}
