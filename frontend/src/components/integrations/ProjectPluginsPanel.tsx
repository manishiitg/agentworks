import { useState, type ReactNode } from 'react'
import { usePersistentTab } from '../../hooks/usePersistentTab'
import { WorkspaceViewTabs } from '../workflow/WorkspaceViewTabs'

export type PluginTab = 'connected' | 'available' | 'secrets' | 'skills' | 'vault'
export const PROJECT_PLUGIN_TABS = [
  { value: 'connected', label: 'Connected' },
  { value: 'available', label: 'Available' },
  { value: 'secrets', label: 'Secrets' },
  { value: 'skills', label: 'Skills' },
  { value: 'vault', label: 'Vault' },
] as const

export function useProjectPluginTab() { return usePersistentTab<PluginTab>('agentworks.tab.project-plugins', 'connected', PROJECT_PLUGIN_TABS.map(item => item.value)) }

/** One plugin navigation and layout for Crew, Code, workflows and Relay. */
export function ProjectPluginsPanel({ connections, secrets, skills, vault, initialTab = 'connected', tab: controlledTab }: {
  connections?: (view: 'connected' | 'available') => ReactNode; secrets?: ReactNode; skills?: ReactNode; vault?: ReactNode; initialTab?: PluginTab; tab?: PluginTab
}) {
  const [localTab, setTab] = useState<PluginTab>(initialTab)
  const tab = controlledTab ?? localTab
  const content = { connected: connections?.('connected'), available: connections?.('available'), secrets, skills, vault }
  const visible = PROJECT_PLUGIN_TABS.filter(item => content[item.value] !== undefined)
  const active = visible.some(item => item.value === tab) ? tab : visible[0]?.value
  if (!active) return null
  return <div className="space-y-4">
    {controlledTab === undefined && <WorkspaceViewTabs value={active} onChange={value => setTab(value as PluginTab)} options={[...visible]} ariaLabel="Plugins" />}
    <div role="tabpanel" aria-label={visible.find(item => item.value === active)?.label}>{content[active]}</div>
  </div>
}
