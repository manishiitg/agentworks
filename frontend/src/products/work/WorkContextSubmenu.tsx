import type { ReactNode } from 'react'
import { CalendarClock, Mail } from 'lucide-react'
import type { DashboardChatTarget } from '../../components/workflow/DashboardEmptyState'
import { WorkspaceBrowserRow } from '../../components/workspace/WorkspaceBrowserRow'
import { WorkspaceContextCard, WorkspaceContextGroup as Group, WorkspaceContextRow as Row } from '../../components/workspace/WorkspaceContextCard'
import { WorkspaceDashboardMenu } from '../../components/workspace/WorkspaceDashboardMenu'
import { crewFullMenuGroups, type CrewMenuItem } from './crewProjectMenu'
import type { CrewPanelRow } from './useCrewPanel'

export type WorkContextDocument = {
  id: string
  title: string
  path: string
  order: number
  motion?: boolean
  artifact?: boolean
}

export type WorkContextBrowserStatus = {
  connected: boolean
  name?: string
  label: string
}

export function WorkContextSubmenu({
  view,
  scope,
  workspacePath,
  profileId,
  inventory,
  integrations,
  apps,
  details,
  documents,
  browser,
  dashboardChatTarget,
  workflowMenuRow,
  hasInboxRow,
  onOpen,
  onOpenAutomation,
  onBack,
}: {
  view: 'full' | 'integrations'
  scope: string
  workspacePath: string
  profileId: 'work'
  inventory: CrewMenuItem[]
  integrations: CrewMenuItem[]
  apps: CrewPanelRow[]
  details?: Record<string, string>
  documents: WorkContextDocument[]
  browser: WorkContextBrowserStatus
  dashboardChatTarget?: DashboardChatTarget
  workflowMenuRow: ReactNode
  hasInboxRow: boolean
  onOpen: (item: CrewMenuItem) => void
  onOpenAutomation: (target: string) => void
  onBack: () => void
}) {
  const renderItem = (item: CrewMenuItem) => {
    if (view === 'full' && item.id === 'workflow') return <div key={item.id}>{workflowMenuRow}</div>
    if (view === 'full' && item.id === 'schedules') return <div key={item.id}>
      <Row icon={CalendarClock} detail={details?.['schedules:schedules']} onClick={() => onOpenAutomation('schedules')}>Schedules</Row>
      {hasInboxRow && <Row icon={Mail} detail={details?.['schedules:email']} onClick={() => onOpenAutomation('email')}>Inbox</Row>}
    </div>
    if (item.id === 'dashboard') return <WorkspaceDashboardMenu key={item.id} documents={documents} createTarget={dashboardChatTarget} onSelect={path => onOpen({ ...item, reportPath: path })}/>
    if (item.id === 'browser') return <WorkspaceBrowserRow key={item.id} workspacePath={workspacePath} profileId={profileId} status={browser} onClick={() => onOpen(item)}/>
    const connections = view === 'integrations' ? apps.filter(row => row.destination === item.id) : []
    const status = details?.[item.id]
    const color = connections.some(row => row.attention) ? 'text-amber-600 dark:text-amber-400' : connections.some(row => row.connected) ? 'text-emerald-600 dark:text-emerald-400' : undefined
    return <Row key={item.id} icon={item.icon} hint={connections.length && item.id !== 'mcp:connected' ? connections.map(row => row.detail).join('\n') : undefined} detail={view === 'integrations' && status ? <span className={color}>{status}</span> : undefined} onClick={() => onOpen(item)}>{item.label}</Row>
  }

  return <WorkspaceContextCard title={view === 'full' ? 'Full menu' : 'Integrations'} toolsAtTop={view === 'full'} onBack={onBack}>
    {view === 'integrations' ? <div className="space-y-0.5">{integrations.map(renderItem)}</div> : crewFullMenuGroups(inventory).map(({ title, items }, index) =>
      <Group key={`${scope}:${title}`} title={title} count={items.length + (hasInboxRow && items.some(item => item.id === 'schedules') ? 1 : 0)} collapsed defaultOpen={index === 0}>
        {items.map(renderItem)}
      </Group>
    )}
  </WorkspaceContextCard>
}
