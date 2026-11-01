// The one place to add, rename or remove a right-panel view in any product.
// Each product's toolbar renders its buttons from here, and the ⌘/Ctrl+K
// panel search lists what those toolbars show, so nothing else needs editing.
//
// Sections are the tabs inside a panel. ⌘/Ctrl+K lists them too ("Slack",
// "Models", "Gmail") and opens the panel on that tab; keywords add search words.
//
// Workflows and Relays keep their richer registry in
// components/workflow/workspaceViews.ts (VIEWS); WORKFLOW_PANELS re-exports it.
import {
  Archive, BookOpen, Brain, BrainCircuit, CalendarClock, Database, DollarSign, Files, Fingerprint, KeyRound, LayoutDashboard,
  Lightbulb, MessageSquareText, Monitor, Plus, Route, Server, ShieldCheck, Terminal, UsersRound, Zap, type LucideIcon,
} from 'lucide-react'
import type { WorkWorkspaceView } from './work/WorkWorkspacePane'
import type { KnowledgebaseView } from './knowledgebase/KnowledgebaseWorkspacePane'
import { PROJECT_INTEGRATION_SECTIONS } from '../components/integrations/integrationSections'
import { projectPluginTabs } from '../components/integrations/ProjectPluginsPanel'

export { WORKSPACE_VIEWS as WORKFLOW_PANELS } from '../components/workflow/workspaceViews'

export interface ProductPanel<Id extends string = string> { id: Id; label: string; icon: LucideIcon }

/** Crew and Code (WorkWorkspacePane). A Code reorders and filters these at render. */
export const WORK_PANELS: Record<'views' | 'ops' | 'setup', ProductPanel<WorkWorkspaceView>[]> = {
  views: [
    { id: 'dashboard', label: 'Dashboard', icon: LayoutDashboard },
    { id: 'plan', label: 'Workflows & programs', icon: Route },
    { id: 'memory', label: 'Memory & skills', icon: Brain },
    { id: 'browser', label: 'Browser', icon: Monitor },
    { id: 'schedules', label: 'Automations', icon: Zap },
    // Change requests from other users of this Crew; the owner reviews them.
    { id: 'suggestions', label: 'Suggestions', icon: Lightbulb },
  ],
  ops: [
    { id: 'files', label: 'Files', icon: Files },
    { id: 'history', label: 'Chat history', icon: MessageSquareText },
    // Code only: a terminal in this workspace, run as your own account and sandboxed to it (see showShell).
    { id: 'shell', label: 'Terminal', icon: Terminal },
    { id: 'database', label: 'Database', icon: Database },
    { id: 'costs', label: 'Costs and usage', icon: DollarSign },
  ],
  setup: [
    { id: 'identity', label: 'Settings', icon: Fingerprint },
    { id: 'mcp', label: 'Integrations', icon: Server },
  ],
}

/** Brain (KnowledgebaseSurface). */
export const BRAIN_PANELS: ProductPanel<KnowledgebaseView>[] = [
  { id: 'library', label: 'Library', icon: BookOpen },
  { id: 'access', label: 'Folder access', icon: ShieldCheck },
  { id: 'models', label: 'Assistant model', icon: BrainCircuit },
  { id: 'secrets', label: 'Brain secrets', icon: KeyRound },
  { id: 'schedules', label: 'Schedules', icon: CalendarClock },
  { id: 'backup', label: 'Backup', icon: Archive },
  { id: 'crew', label: 'Use in a Crew', icon: UsersRound },
]

/** Vault (GatewaySurface). */
export const VAULT_PANELS = [
  { id: 'access', label: 'Access', icon: ShieldCheck },
  { id: 'servers', label: 'Connected MCPs', icon: Server },
  { id: 'available-mcps', label: 'Available MCPs', icon: Plus },
  { id: 'secrets', label: 'Secrets', icon: KeyRound },
  { id: 'people', label: 'People', icon: UsersRound },
] as const

/** A tab inside a panel. The panel opens on it from ⌘/Ctrl+K. */
export interface PanelSection { id: string; label: string; keywords?: string }

/** Existing integration target IDs, flattened by MCPs & secrets navigation. */
export const INTEGRATION_SECTIONS: PanelSection[] = [
  { id: 'share', label: 'Access', keywords: 'owners share people agents' },
  { id: 'agentbridge', label: 'AgentBridge', keywords: 'agents computer network messages' },
  ...PROJECT_INTEGRATION_SECTIONS.filter(section => section.value !== 'apps').map(section => ({ id: section.value, label: section.value === 'apps' ? 'Connection overview' : section.value === 'cli' ? 'MCP access' : section.label, keywords: section.value === 'apps' ? `${section.description} Tools & secrets Apps & tools` : section.description })),
  ...projectPluginTabs().map(tab => ({ id: tab.value, label: tab.label, keywords: 'mcp tools' })),
]
/** Integrations sections a Relay offers (its panel shows only these). */
export const RELAY_INTEGRATION_SECTION_IDS = new Set(['share', 'agentbridge', 'cli', 'slack-account', 'apps', 'gmail', 'slack', 'whatsapp', ...projectPluginTabs().map(tab => tab.value)])

export const WORKFLOW_IDENTITY_SECTIONS: PanelSection[] = [
  { id: 'general', label: 'General', keywords: 'name soul persona identity' },
  { id: 'llm', label: 'Models', keywords: 'model llm provider ai' },
  { id: 'upgrades', label: 'Upgrades', keywords: 'contract version upgrade' },
]
export const WORK_IDENTITY_SECTIONS: PanelSection[] = [
  { id: 'general', label: 'General', keywords: 'name soul persona identity' },
  { id: 'models', label: 'Models', keywords: 'model llm provider ai' },
  // Code only (WorkWorkspacePane drops it for Crews).
  { id: 'location', label: 'Location', keywords: 'files location computer laptop local server cli folder connection start' },
]
export const AUTOMATION_SECTIONS: PanelSection[] = [
  { id: 'schedules', label: 'Schedules', keywords: 'cron schedule timer' },
  { id: 'triggers', label: 'Webhooks', keywords: 'webhook trigger' },
  { id: 'conditions', label: 'Watchers', keywords: 'watch watcher monitor python script condition ticket event once' },
  { id: 'functions', label: 'Functions', keywords: 'function api' },
  { id: 'chats', label: 'Chats', keywords: 'chat conversation' },
]
export const KNOWLEDGE_SECTIONS: PanelSection[] = [
  { id: 'learnings', label: 'Learnings', keywords: 'skills learnings' },
  { id: 'knowledgebase', label: 'Knowledgebase', keywords: 'notes kb' },
  { id: 'database', label: 'Database', keywords: 'db sqlite tables' },
]
export const ACCESS_SECTIONS: PanelSection[] = [
  { id: 'workflow', label: 'This workflow', keywords: 'share sharing permissions' },
  { id: 'users', label: 'Users', keywords: 'accounts people roles' },
  { id: 'slack', label: 'Slack', keywords: 'slack admin' },
]
export const VAULT_PEOPLE_SECTIONS: PanelSection[] = [
  { id: 'users', label: 'Users', keywords: 'people accounts' },
  { id: 'groups', label: 'Groups', keywords: 'teams' },
]

/** Sections by panel id, per product. */
export const WORKFLOW_PANEL_SECTIONS: Partial<Record<string, PanelSection[]>> = {
  mcp: [{ id: 'laptop', label: 'Local laptop', keywords: 'computer folders terminal' }, { id: 'slack-account', label: 'Slack account', keywords: 'personal slack' }, ...INTEGRATION_SECTIONS], identity: WORKFLOW_IDENTITY_SECTIONS, workshop: AUTOMATION_SECTIONS,
  knowledge: KNOWLEDGE_SECTIONS, access: ACCESS_SECTIONS,
}
export const WORK_PANEL_SECTIONS: Partial<Record<string, PanelSection[]>> = {
  mcp: [{ id: 'laptop', label: 'Local laptop', keywords: 'computer folders terminal' }, { id: 'slack-account', label: 'Slack account', keywords: 'personal slack' }, ...INTEGRATION_SECTIONS.filter(section => section.id !== 'skills')], identity: WORK_IDENTITY_SECTIONS, schedules: AUTOMATION_SECTIONS.filter(section => section.id !== 'bots' && section.id !== 'chats'),
  memory: [{ id: 'memory', label: 'Memory' }, { id: 'skills', label: 'Skills' }],
}
export const VAULT_PANEL_SECTIONS: Partial<Record<string, PanelSection[]>> = { people: VAULT_PEOPLE_SECTIONS }

/** The same admitted tabs are registered by the toolbar and searched by Ctrl+K. */
export function workPanelSections(panelId: string, isCode: boolean): PanelSection[] {
  const sections = WORK_PANEL_SECTIONS[panelId] ?? []
  return sections.filter(section => !(!isCode && panelId === 'identity' && section.id === 'location'))
}
