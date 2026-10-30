import { ArrowUpCircle, BookOpen, Cable, Cloud, Code2, Cpu, Database, DollarSign, Laptop, Files, FileText, Globe, Hand, History, LayoutDashboard, MessageSquareText, Monitor, Route, Settings, BellRing, ShieldCheck, Target, Sparkles, type LucideIcon } from 'lucide-react'
import { projectAutomationSections, projectConnectionSections } from '../workspace/WorkspaceProjectNavigation'
import { PROJECT_MENU_GROUPS, type ProjectMenuGroup } from '../workspace/projectMenuGroups'
import type { WorkspaceViewId } from './workspaceViews'
import { visibleProductSurfaceIDs } from '../../products/productSurfaceConfig'

export type WorkflowMenuItem = {
  id: string; label: string; icon: LucideIcon; group: ProjectMenuGroup; subgroup?: string
  view?: WorkspaceViewId; target?: string; reportPath?: string; external?: 'bullpen'
}

/** The menu and panel search use the same destinations and existing product gates. */
export function workflowProjectMenuItems({ relayMode = false, isMultiUser = false, allowedProducts, incomingEmail = false, canShare = true }: {
  relayMode?: boolean; isMultiUser?: boolean; allowedProducts?: string[] | null
  incomingEmail?: boolean; canShare?: boolean
}): WorkflowMenuItem[] {
  const items: WorkflowMenuItem[] = []
  const add = (view: WorkspaceViewId, group: ProjectMenuGroup, label: string, icon: LucideIcon, target?: string, subgroup?: string) => {
    items.push({ id: target ? `${view}:${target}` : view, view, group, label, icon, target, subgroup })
  }
  add('report', 'Build', 'Dashboards', LayoutDashboard)
  add('flow', 'Build', relayMode ? 'Flow' : 'Plan & progress', Route, relayMode ? 'graph' : undefined)
  add('mcp', 'Build', 'Skills', Sparkles, 'skills')
  add('files', 'Build', 'Files', Files)
  if (!relayMode) {
    add('knowledge', 'Build', 'Knowledge base', BookOpen, 'knowledgebase')
    add('knowledge', 'Build', 'Database', Database, 'database')
    add('playbooks', 'Setup', 'Workflow playbooks', BookOpen)
  } else add('flow', 'Build', 'Code', Code2, 'source', 'Advanced')
  for (const item of projectAutomationSections({ relay: relayMode, incomingEmail })) {
    add('workshop', item.id === 'functions' ? 'Build' : 'Use', item.id === 'email' ? 'Email inbox' : item.label, item.icon, item.id, item.id === 'functions' ? undefined : item.id === 'email' ? 'Use from other apps' : 'Run automatically')
  }
  if (relayMode) add('flow', 'Use', 'Runs', History, 'calls')
  else {
    add('goals', 'Build', 'Goals & metrics', Target)
    add('human-actions', 'Use', 'Decisions', Hand)
    add('browser', 'Use', 'Browser', Monitor)
  }
  add('workshop', 'Use', 'Chat history', MessageSquareText, 'chats')
  // These retain the existing Slack destinations: both Goal rows open its Slack page.
  if (!relayMode) {
    add('mcp', 'Integrations', 'Slack bot', MessageSquareText, 'slack')
    items[items.length - 1].id = 'mcp:slack-bot'
  }
  add('mcp', 'Use', 'WhatsApp', MessageSquareText, 'whatsapp', 'Use from other apps')
  add('mcp', 'Use', 'MCP access', Cable, 'cli', 'Use from other apps')
  if (canShare) add('mcp', 'Share', 'Access & sharing', ShieldCheck, 'share')
  if (isMultiUser && !relayMode) add('access', 'Share', 'Team', ShieldCheck, 'workflow')
  if (!relayMode) {
    add('publish', 'Setup', 'Publish', Globe)
    add('notify', 'Setup', 'Notify', BellRing)
  }
  for (const item of projectConnectionSections({ allowedProducts, relayMode, onOpen: () => {} })) {
    add('mcp', 'Integrations', item.label, item.icon, item.id.slice(4), item.subgroup)
  }
  add('mcp', 'Integrations', 'My Slack account', MessageSquareText, 'slack-account')
  if (!relayMode) add('mcp', 'Integrations', 'Local laptop', Laptop, 'laptop')
  if (visibleProductSurfaceIDs(allowedProducts).includes('bullpen')) add('mcp', 'Integrations', 'AgentBridge', Cable, 'agentbridge')
  add('identity', 'Setup', 'Settings', Settings, 'general')
  add('identity', 'Setup', 'Models', Cpu, 'llm')
  add('costs', 'Setup', 'Costs and usage', DollarSign)
  add('execution-logs', 'Setup', 'Run logs', FileText)
  if (!relayMode) {
    add('identity', 'Setup', 'Upgrades', ArrowUpCircle, 'upgrades')
    add('backup', 'Setup', 'Backup', Cloud)
  }
  return items.sort((a, b) => PROJECT_MENU_GROUPS.indexOf(a.group) - PROJECT_MENU_GROUPS.indexOf(b.group))
}
