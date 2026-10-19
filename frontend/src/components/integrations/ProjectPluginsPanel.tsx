import { useState, type ReactNode } from 'react'
import { usePersistentTab } from '../../hooks/usePersistentTab'
import { WorkspaceViewTabs } from '../workflow/WorkspaceViewTabs'
import type { ProductSurface } from '../../products/productSurfaceConfig'
import { useAuthStore } from '../../stores/useAuthStore'
import { integrationProductAvailable } from './integrationSections'

export type PluginTab = 'connected' | 'available' | 'secrets' | 'skills' | 'vault'
export const PROJECT_PLUGIN_TABS: Array<{ value: PluginTab; label: string; requiredProduct?: ProductSurface }> = [
  { value: 'connected', label: 'Connected MCPs' },
  { value: 'available', label: 'Available MCPs' },
  { value: 'secrets', label: 'Secrets' },
  { value: 'skills', label: 'Skills' },
  { value: 'vault', label: 'Vault', requiredProduct: 'mcp-gateway' },
]

export function projectPluginTabs(allowedProducts?: string[] | null) { return PROJECT_PLUGIN_TABS.filter(item => integrationProductAvailable(item.requiredProduct, allowedProducts)) }

export function useProjectPluginTab() {
  const allowedProducts = useAuthStore(state => state.user?.allowed_products)
  return usePersistentTab<PluginTab>('agentworks.tab.project-plugins', 'connected', projectPluginTabs(allowedProducts).map(item => item.value))
}

/** One plugin navigation and layout for Crew, Code, workflows and Relay. */
export function ProjectPluginsPanel({ connections, secrets, skills, vault, initialTab = 'connected', tab: controlledTab }: {
  connections?: (view: 'connected' | 'available') => ReactNode; secrets?: ReactNode; skills?: ReactNode; vault?: ReactNode; initialTab?: PluginTab; tab?: PluginTab
}) {
  const allowedProducts = useAuthStore(state => state.user?.allowed_products)
  const [localTab, setTab] = useState<PluginTab>(initialTab)
  const tab = controlledTab ?? localTab
  const content = { connected: connections?.('connected'), available: connections?.('available'), secrets, skills, vault }
  const visible = projectPluginTabs(allowedProducts).filter(item => content[item.value] !== undefined)
  const active = visible.some(item => item.value === tab) ? tab : visible[0]?.value
  if (!active) return null
  return <div className="space-y-4">
    {controlledTab === undefined && <WorkspaceViewTabs value={active} onChange={value => setTab(value as PluginTab)} options={[...visible]} ariaLabel="Integrations" />}
    <div role="tabpanel" data-ui-view-section={active} aria-label={visible.find(item => item.value === active)?.label}>{content[active]}</div>
  </div>
}
