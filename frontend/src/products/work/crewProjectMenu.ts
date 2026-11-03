import { BookOpen, Cable, Cpu, Database, DollarSign, Files, Folder, Github, Hand, Laptop, LayoutDashboard, MessageSquareText, MonitorCog, Route, Settings, Sparkles, Terminal, UsersRound, Vault, type LucideIcon } from 'lucide-react'
import type { ProductPanel } from '../productPanels'
import type { WorkWorkspaceView } from './WorkWorkspacePane'
import { projectAutomationSections, projectChannelSections, projectConnectionSections } from '../../components/workspace/WorkspaceProjectNavigation'
import { isLocalDeployment, visibleProductSurfaceIDs } from '../productSurfaceConfig'
import { isWorkIdentityTabEnabled, isWorkIntegrationTabEnabled } from './workViewGating'
import { PROJECT_MENU_GROUPS } from '../../components/workspace/projectMenuGroups'
import { hasDesktopConnections, useConnectedWorkStore } from '../../stores/useConnectedWorkStore'

export const CREW_MENU_GROUPS = PROJECT_MENU_GROUPS
export type CrewMenuGroup = typeof CREW_MENU_GROUPS[number]
export type CrewMenuItem = {
  id: string; label: string; icon: LucideIcon; group: CrewMenuGroup; subgroup?: string
  view?: WorkWorkspaceView; target?: string; reportPath?: string; external?: 'bullpen'
}

/** One inventory for the Crew menu and panel search. All old feature gates apply. */
export function crewProjectMenuItems({ panels, allowedProducts, enabledPanels, incomingEmail = false, canShare = true, mode = 'cowork' }: {
  panels: ProductPanel<WorkWorkspaceView>[]; allowedProducts?: string[] | null; enabledPanels?: Set<string>
  incomingEmail?: boolean; canShare?: boolean; projectId?: string; mode?: 'dev' | 'cowork'
}): CrewMenuItem[] {
  const has = (view: WorkWorkspaceView) => panels.some(item => item.id === view)
  const bots = !enabledPanels || enabledPanels.has('bots')
  const items: CrewMenuItem[] = []
  const add = (view: WorkWorkspaceView, group: CrewMenuGroup, label: string, icon: LucideIcon, target?: string, subgroup?: string) => {
    if (has(view)) items.push({ id: target ? `${view}:${target}` : view, view, group, label, icon, target, subgroup })
  }
  add('dashboard', 'Build', 'Dashboards', LayoutDashboard)
  if (has('schedules')) for (const item of projectAutomationSections({ incomingEmail, triggers: enabledPanels?.has('triggers') !== false })) {
    add('schedules', item.id === 'functions' ? 'Build' : 'Use', item.id === 'email' ? 'Email inbox' : item.label, item.icon, item.id, item.id === 'functions' ? undefined : item.id === 'email' ? 'Use from other apps' : 'Run automatically')
  }
  if (!enabledPanels || enabledPanels.has('skills')) add('memory', 'Build', 'Skills', Sparkles, 'skills')
  add('files', 'Build', 'Files', Files)
  add('database', 'Build', 'Database', Database)
  add('plan', 'Build', 'Plan', Route)
  add('browser', 'Use', 'Browser', panels.find(item => item.id === 'browser')?.icon || Laptop)
  add('history', 'Use', 'Chat history', MessageSquareText)
  if (has('mcp')) {
    if (has('dashboard') && canShare) {
      add('mcp', 'Share', 'Sharing', UsersRound, 'share')
    }
    for (const item of projectChannelSections(bots, bots)) {
      add('mcp', item.id === 'slack' ? 'Integrations' : 'Use', item.id === 'slack' ? 'Slack bot' : item.label, item.icon, item.id === 'slack' ? 'slack-bot' : item.id, item.id === 'slack' ? undefined : 'Use from other apps')
    }
    for (const item of projectConnectionSections({ allowedProducts, workMode: true, enabledPanels, onOpen: () => {} })) {
      // Linked projects sit with the Crew's connections; the main menu keeps Sharing, Where it lives and Connected agents (owner, 3 Nov).
      add('mcp', 'Integrations', item.id === 'mcp:folders' ? 'Linked projects' : item.label, item.icon, item.id.slice(4), item.subgroup)
    }
    if (hasDesktopConnections()) add('mcp', 'Integrations', 'Folders on this Mac', Folder, 'mac-folders')
    if (mode === 'dev' && isWorkIntegrationTabEnabled('secrets', enabledPanels)) add('mcp', 'Integrations', 'GitHub', Github, 'github')
    // In the Mac app a Crew lives on this Mac or online (shown once a network is connected); on a server it is Laptop access.
    if (!hasDesktopConnections()) add('mcp', 'Share', 'Laptop access', Laptop, 'computer', 'Access')
    else if (useConnectedWorkStore.getState().connections.some(item => item.id !== 'local')) add('mcp', 'Share', 'Where it lives', Laptop, 'computer', 'Access')
    if (isLocalDeployment()) add('mcp', 'Integrations', 'Computer use', MonitorCog, 'computer-use')
    if (!enabledPanels || enabledPanels.has('bots')) add('mcp', 'Integrations', 'My Slack account', MessageSquareText, 'slack-account')
    if (visibleProductSurfaceIDs(allowedProducts).includes('bullpen')) add('mcp', 'Share', 'Connected agents', Cable, 'agentbridge', 'Access')
  }
  add('suggestions', 'Share', 'Suggestions', Hand)
  add('identity', 'Setup', 'Instructions & identity', Settings, 'general')
  if (isWorkIdentityTabEnabled('models', enabledPanels)) add('identity', 'Setup', 'Models', Cpu, 'models')
  if (!enabledPanels || enabledPanels.has('memory')) add('memory', 'Setup', 'Memory', BookOpen, 'memory')
  add('costs', 'Setup', 'Costs & usage', DollarSign)
  add('shell', 'Setup', 'Terminal', Terminal, undefined, 'Advanced')
  const integrationOrder = ['mcp:connected', 'mcp:available', 'mcp:secrets', 'mcp:github', 'mcp:gmail', 'mcp:slack-account', 'mcp:computer-use', 'mcp:brain', 'mcp:folders', 'mcp:mac-folders']
  return items.sort((a, b) => CREW_MENU_GROUPS.indexOf(a.group) - CREW_MENU_GROUPS.indexOf(b.group) || (a.group === 'Integrations' ? integrationOrder.indexOf(a.id) - integrationOrder.indexOf(b.id) : 0))
}

export function crewMenuTarget(view: WorkWorkspaceView) {
  if (view === 'schedules') return 'workshop' as const
  if (view === 'memory' || view === 'identity' || view === 'mcp') return view
  return 'context' as const
}
