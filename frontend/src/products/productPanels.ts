// The one place to add, rename or remove a right-panel view in any product.
// Each product's toolbar renders its buttons from here, and the ⌘/Ctrl+J
// panel search lists what those toolbars show, so nothing else needs editing.
//
// Workflows and Relays keep their richer registry in
// components/workflow/workspaceViews.ts (VIEWS); WORKFLOW_PANELS re-exports it.
import {
  BookOpen, Brain, BrainCircuit, CalendarClock, Database, DollarSign, Files, Fingerprint, KeyRound, LayoutDashboard,
  Lightbulb, Monitor, Plus, Route, Server, ShieldCheck, Terminal, UsersRound, Zap, type LucideIcon,
} from 'lucide-react'
import type { WorkWorkspaceView } from './work/WorkWorkspacePane'
import type { KnowledgebaseView } from './knowledgebase/KnowledgebaseWorkspacePane'

export { WORKSPACE_VIEWS as WORKFLOW_PANELS } from '../components/workflow/workspaceViews'

export interface ProductPanel<Id extends string = string> { id: Id; label: string; icon: LucideIcon }

/** Crew and Code (WorkWorkspacePane). A Code reorders and filters these at render. */
export const WORK_PANELS: Record<'views' | 'ops' | 'setup', ProductPanel<WorkWorkspaceView>[]> = {
  views: [
    { id: 'dashboard', label: 'Dashboard', icon: LayoutDashboard },
    { id: 'plan', label: 'Plan', icon: Route },
    { id: 'memory', label: 'Memory', icon: Brain },
    { id: 'browser', label: 'Browser', icon: Monitor },
    { id: 'schedules', label: 'Automation', icon: Zap },
    // Change requests from other users of this Crew; the owner reviews them.
    { id: 'suggestions', label: 'Suggestions', icon: Lightbulb },
  ],
  ops: [
    { id: 'files', label: 'Files', icon: Files },
    // Code only: a terminal in this workspace, run as your own account and sandboxed to it (see showShell).
    { id: 'shell', label: 'Terminal', icon: Terminal },
    { id: 'database', label: 'Database', icon: Database },
    { id: 'costs', label: 'Costs and usage', icon: DollarSign },
  ],
  setup: [
    { id: 'identity', label: 'Identity', icon: Fingerprint },
    { id: 'mcp', label: 'Integrations', icon: Server },
  ],
}

/** Brain (KnowledgebaseSurface). */
export const BRAIN_PANELS: ProductPanel<KnowledgebaseView>[] = [
  { id: 'library', label: 'Files', icon: BookOpen },
  { id: 'access', label: 'Access', icon: ShieldCheck },
  { id: 'models', label: 'Models', icon: BrainCircuit },
  { id: 'secrets', label: 'Secrets', icon: KeyRound },
  { id: 'schedules', label: 'Schedules', icon: CalendarClock },
]

/** Vault (GatewaySurface). */
export const VAULT_PANELS = [
  { id: 'access', label: 'Access', icon: ShieldCheck },
  { id: 'servers', label: 'Connected MCPs', icon: Server },
  { id: 'available-mcps', label: 'Available MCPs', icon: Plus },
  { id: 'secrets', label: 'Secrets', icon: KeyRound },
  { id: 'people', label: 'People', icon: UsersRound },
  { id: 'models', label: 'Models', icon: BrainCircuit },
] as const
