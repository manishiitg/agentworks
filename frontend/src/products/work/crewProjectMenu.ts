import { BookOpen, Cable, Database, DollarSign, Files, Folder, Github, Globe, Laptop, LayoutDashboard, MessageSquareText, MonitorCog, Route, Zap, Settings, Sparkles, Terminal, UsersRound, Vault, type LucideIcon } from 'lucide-react'
import type { ProductPanel } from '../productPanels'
import type { WorkWorkspaceView } from './WorkWorkspacePane'
import { projectChannelSections, projectConnectionSections } from '../../components/workspace/WorkspaceProjectNavigation'
import { isLocalDeployment } from '../productSurfaceConfig'
import { isWorkIntegrationTabEnabled } from './workViewGating'
import { PROJECT_MENU_GROUPS } from '../../components/workspace/projectMenuGroups'
import { hasDesktopConnections } from '../../stores/useConnectedWorkStore'
import { remoteProjectConnection } from '../../utils/remoteSessions'

export const CREW_MENU_GROUPS = PROJECT_MENU_GROUPS
export type CrewMenuGroup = typeof CREW_MENU_GROUPS[number]
export type CrewMenuItem = {
  id: string; label: string; icon: LucideIcon; group: CrewMenuGroup; subgroup?: string
  view?: WorkWorkspaceView; target?: string; reportPath?: string; external?: 'bullpen'
}

/** One inventory for the Crew menu and panel search. All old feature gates apply. */
export function crewProjectMenuItems({ panels, allowedProducts, enabledPanels, incomingEmail = false, projectId }: {
  panels: ProductPanel<WorkWorkspaceView>[]; allowedProducts?: string[] | null; enabledPanels?: Set<string>
  incomingEmail?: boolean; canShare?: boolean; projectId?: string
}): CrewMenuItem[] {
  const has = (view: WorkWorkspaceView) => panels.some(item => item.id === view)
  const bots = !enabledPanels || enabledPanels.has('bots')
  const items: CrewMenuItem[] = []
  const add = (view: WorkWorkspaceView, group: CrewMenuGroup, label: string, icon: LucideIcon, target?: string, subgroup?: string) => {
    if (has(view)) items.push({ id: target ? `${view}:${target}` : view, view, group, label, icon, target, subgroup })
  }
  add('dashboard', 'Build', 'Artifacts', LayoutDashboard)
  add('workflow', 'Build', 'Workflow', Route)
  add('schedules', 'Build', 'Automations', Zap)
  if (!enabledPanels || enabledPanels.has('skills')) add('memory', 'Integrations', 'Skills', Sparkles, 'skills')
  add('files', 'Build', 'Files', Files)
  add('database', 'Build', 'Database', Database)
  add('browser', 'Use', 'Browser', panels.find(item => item.id === 'browser')?.icon || Laptop)
  add('history', 'Use', 'Chat history', MessageSquareText)
  if (has('mcp')) {
    // Two rows for every Crew (owner, 11 Nov: the one Share row split): Team, the people who work on it with you, and
    // Online, how everyone else reaches it (Mac online, Online agents); your agents and actions are Team › Agents. Each opens its own page.
    add('mcp', 'Share', 'Team', UsersRound, 'team')
    // Remote Control turns on reaching a Mac project from your phone; a server project is always on the web,
    // so the row shows only for a project on this Mac (owner, 25 Nov: "on server i see.. phone why"). A server project
    // opened in the Mac app shows its panel as on the website, without either row (owner, 27 Nov).
    const onThisMac = isLocalDeployment() && !remoteProjectConnection(projectId)
    if (onThisMac) add('mcp', 'Share', 'Remote Control', Globe, 'online')
    // AgentBridge is its own row and page beside Remote Control (owner, 27 Nov), not a tab inside it.
    if (onThisMac) add('mcp', 'Share', 'AgentBridge', Cable, 'agentbridge')
    add('mcp', 'Share', 'Artifacts', LayoutDashboard, 'artifacts')
    // The Slack bot row opens Connections → Slack bot on server projects (owner, 14 Nov).
    // Local Mac projects keep Slack as me only (owner, 25 Nov).
    for (const item of projectChannelSections(bots && !isLocalDeployment(), bots)) {
      add('mcp', item.id === 'slack' ? 'Integrations' : 'Use', item.id === 'slack' ? 'Slack bot' : item.label, item.icon, item.id === 'slack' ? 'slack-bot' : item.id, item.id === 'slack' ? undefined : 'Use from other apps')
    }
    for (const item of projectConnectionSections({ allowedProducts, workMode: true, enabledPanels, onOpen: () => {} })) {
      // Linked projects sit with the Crew's connections; the main menu keeps the Share row (owner, 3 Nov).
      add('mcp', 'Integrations', item.id === 'mcp:folders' ? 'Linked projects' : item.label, item.icon, item.id.slice(4), item.subgroup)
    }
    if (hasDesktopConnections() || isLocalDeployment()) add('mcp', 'Integrations', 'Folders on this Mac', Folder, 'mac-folders')
    if (isWorkIntegrationTabEnabled('secrets', enabledPanels)) add('mcp', 'Integrations', 'GitHub', Github, 'github')
    if (isLocalDeployment()) add('mcp', 'Integrations', 'Computer use', MonitorCog, 'computer-use')
    if (!enabledPanels || enabledPanels.has('bots')) add('mcp', 'Integrations', 'My Slack account', MessageSquareText, 'slack-account')
  }
  add('identity', 'Setup', 'Instructions & identity', Settings, 'general')
  if (!enabledPanels || enabledPanels.has('memory')) add('memory', 'Setup', 'Memory', BookOpen, 'memory')
  add('costs', 'Setup', 'Costs & usage', DollarSign)
  add('shell', 'Setup', 'Terminal', Terminal, undefined, 'Advanced')
  const integrationOrder = ['mcp:connected', 'mcp:available', 'mcp:secrets', 'mcp:github', 'mcp:gmail', 'mcp:slack-account', 'mcp:computer-use', 'mcp:brain', 'mcp:folders', 'mcp:mac-folders']
  return items.sort((a, b) => CREW_MENU_GROUPS.indexOf(a.group) - CREW_MENU_GROUPS.indexOf(b.group) || (a.group === 'Integrations' ? integrationOrder.indexOf(a.id) - integrationOrder.indexOf(b.id) : 0))
}

// Full menu groups are presentation only. Callers still use the same
// admitted inventory for search, opening items, and the compact menu.
export function crewFullMenuGroups(inventory: CrewMenuItem[]) {
  const groups: { title: string; items: CrewMenuItem[] }[] = [
    { title: 'Files & data', items: [] },
    { title: 'Automations', items: [] },
    { title: 'Connections', items: [] },
    { title: 'Team & access', items: [] },
    { title: 'Project settings', items: [] },
  ]
  const order = [
    ['mcp:artifacts', 'dashboard', 'files', 'database', 'shell'],
    ['workflow', 'schedules'],
    ['browser', 'mcp:connected', 'mcp:cli', 'mcp:gmail', 'mcp:github', 'mcp:slack-account', 'mcp:slack-bot', 'mcp:whatsapp', 'mcp:folders', 'mcp:mac-folders', 'mcp:computer-use', 'mcp:secrets', 'memory:skills'],
    ['mcp:team', 'mcp:online', 'mcp:agentbridge'],
    ['identity:general', 'identity:models', 'memory:memory', 'history', 'costs'],
  ]
  const hasSharedArtifacts = inventory.some(item => item.id === 'mcp:artifacts')
  for (const item of inventory) {
    if (item.id === 'dashboard' && hasSharedArtifacts) continue
    let group = order.findIndex(ids => ids.includes(item.id))
    if (group === -1) group = item.group === 'Integrations' ? 2 : 4
    groups[group].items.push(item)
  }
  for (const [index, group] of groups.entries()) {
    const ids = order[index]
    const rank = (item: CrewMenuItem) => ids.includes(item.id) ? ids.indexOf(item.id) : ids.length
    group.items.sort((a, b) => rank(a) - rank(b))
  }
  return groups.filter(group => group.items.length > 0)
}

export function crewMenuTarget(view: WorkWorkspaceView) {
  if (view === 'schedules') return 'workshop' as const
  if (view === 'memory' || view === 'identity' || view === 'mcp') return view
  return 'context' as const
}
