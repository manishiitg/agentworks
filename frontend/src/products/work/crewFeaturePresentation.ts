import { BookOpen, Braces, CalendarClock, CheckCircle2, Code2, Database, DollarSign, Files, FolderTree, KeyRound, LayoutDashboard, MessageSquareText, Mic, Monitor, Network, PanelsTopLeft, Plug, Settings, Sparkles, Terminal, UsersRound, Webhook, Workflow, Zap, type LucideIcon } from 'lucide-react'
import type { AgentProfileFeature } from '../../utils/agentProfileCapabilities'
import type { ProductMode } from '../../platform/chat/productProjects'
import type { CrewStarterType } from './crewStarterTypes'

type FeatureCopy = { label: string; icon: LucideIcon; example: string; location: string; setup?: boolean }

/** Copy describes enabled server features; this map never enables or selects them. */
const COPY: Record<string, FeatureCopy> = {
  'live-chat': { label: 'Chat', icon: MessageSquareText, example: 'Help me plan this project.', location: 'Chat' },
  coding: { label: 'Code', icon: Code2, example: 'Explain this code and help me fix the issue.', location: 'Chat' },
  files: { label: 'Files', icon: Files, example: 'Summarize the document I attached.', location: 'Build → Files' },
  browser: { label: 'Browser', icon: Monitor, example: 'Research this topic using the browser.', location: 'Use → Browser' },
  secrets: { label: 'Secrets', icon: KeyRound, example: 'Help me set up the credentials this task needs.', location: 'Integrations → Secrets', setup: true },
  mcp: { label: 'Connected apps', icon: Plug, example: 'Help me connect a tool for this project.', location: 'Integrations → MCPs', setup: true },
  skills: { label: 'Skills', icon: Sparkles, example: 'Help me install a skill for this task.', location: 'Build → Skills', setup: true },
  knowledgebase: { label: 'Brain', icon: BookOpen, example: 'Help me connect the company Brain folders I can access.', location: 'Integrations → Brain', setup: true },
  'attached-folders': { label: 'Linked folders', icon: FolderTree, example: 'Help me link a folder this Crew can use.', location: 'Integrations → Linked projects & folders', setup: true },
  'workflow-references': { label: 'Functions', icon: Braces, example: 'Help me create a reusable function for this task.', location: 'Build → Functions', setup: true },
  terminal: { label: 'Terminal', icon: Terminal, example: 'Run the project checks and explain the results.', location: 'Setup → Terminal' },
  voice: { label: 'Voice', icon: Mic, example: 'Tap the microphone in chat to dictate a request.', location: 'Chat → Microphone' },
  models: { label: 'AI models', icon: Settings, example: 'Choose the AI model this Crew uses.', location: 'Setup → Models' },
  schedules: { label: 'Schedules', icon: CalendarClock, example: 'Set up a weekday briefing at 9 AM.', location: 'Use → Schedules', setup: true },
  'condition-triggers': { label: 'Watchers', icon: Zap, example: 'Watch for a matching event and call my function.', location: 'Use → Watchers', setup: true },
  triggers: { label: 'Webhooks', icon: Webhook, example: 'Help me trigger a task from another app.', location: 'Use → Webhooks', setup: true },
  bots: { label: 'Messaging & email', icon: UsersRound, example: 'Help me connect a messaging or email account.', location: 'Use / Integrations', setup: true },
  database: { label: 'Database', icon: Database, example: 'Keep these records in this project’s database.', location: 'Build → Database' },
  dashboard: { label: 'Dashboard', icon: LayoutDashboard, example: 'Build a dashboard from these records.', location: 'Build → Project dashboard' },
  memory: { label: 'Memory', icon: BookOpen, example: 'Remember this preference for this project.', location: 'Setup → Memory' },
  costs: { label: 'Usage', icon: DollarSign, example: 'See this Crew’s model usage and costs.', location: 'Setup → Costs & usage' },
  'background-work': { label: 'Background tasks', icon: Workflow, example: 'Work on this independent task in the background.', location: 'Chat' },
  'workspace-ui': { label: 'Project views', icon: PanelsTopLeft, example: 'Open this project’s files.', location: 'Project menu' },
}

const PRIORITIES: Record<CrewStarterType, string[]> = {
  code: ['coding', 'files', 'terminal', 'skills'],
  cowork: ['files', 'browser', 'dashboard', 'mcp'],
  personal: ['memory', 'bots', 'schedules', 'mcp'],
  specialist: ['skills', 'files', 'memory', 'dashboard'],
  automation: ['workflow-references', 'skills', 'schedules', 'condition-triggers'],
  brain: ['knowledgebase', 'attached-folders', 'files', 'schedules'],
  'chief-of-staff': ['workflow-references', 'attached-folders', 'dashboard', 'schedules', 'memory'],
}

export function crewFeaturePresentation(features: AgentProfileFeature[], type: CrewStarterType | undefined, mode: ProductMode) {
  const priority = type ? PRIORITIES[type] : ['files', 'memory', 'skills', 'dashboard']
  return [...new Map(features.map(feature => [feature.id, feature])).values()].map(feature => {
    const fallback = feature.id.replace(/[-_]/g, ' ').replace(/^./, text => text.toUpperCase())
    const copy = type === 'chief-of-staff' && feature.id === 'workflow-references'
      ? { label: 'Crews & workflows', icon: Network, example: 'Show the Crews and workflows I can access, then help me coordinate the ones I choose.', location: 'Chat · Build → Functions', setup: true }
      : COPY[feature.id] || { label: fallback, icon: CheckCircle2, example: `Ask how to use ${fallback.toLowerCase()} in this Crew.`, location: 'Chat' }
    // Cowork keeps code tools in chat, while its menu omits the terminal page.
    return { id: feature.id, ...copy, location: mode === 'cowork' && feature.id === 'terminal' ? 'Chat' : copy.location }
  }).sort((a, b) => {
    const rank = (id: string) => { const index = priority.indexOf(id); return index < 0 ? priority.length : index }
    return rank(a.id) - rank(b.id)
  })
}
