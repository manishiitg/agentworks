import { Bell, BookOpen, Braces, BriefcaseBusiness, CalendarClock, CheckCircle2, Code2, FileCheck2, Files, FolderTree, LayoutDashboard, ListChecks, Network, Plug, Search, UserRound, UsersRound, Workflow, Wrench } from 'lucide-react'

/** Onboarding suggestions only. The live menu inventory still admits every link. */
export const CREW_STARTER_TYPES = [
  {
    id: 'code', label: 'Code', icon: Code2,
    summary: 'Build & fix software',
    flow: [{ icon: Code2, label: 'Your code', features: ['files', 'attached-folders'] }, { icon: Wrench, label: 'Build & fix', features: ['coding', 'terminal', 'skills'] }, { icon: FileCheck2, label: 'Changes', features: ['files', 'dashboard'] }],
    purpose: 'Help me build and maintain software, understand code and fix issues.',
    example: 'Review a project, explain how it works or help make a change.',
    shortcuts: [
      { id: 'files', label: 'Files' }, { id: 'identity:location', label: 'Local laptop' },
      { id: 'shell', label: 'Terminal' }, { id: 'memory:skills', label: 'Skills' },
    ],
  },
  {
    id: 'cowork', label: 'Cowork', icon: BriefcaseBusiness,
    summary: 'Research · Write · Organize',
    flow: [{ icon: Files, label: 'Your documents', features: ['files', 'attached-folders', 'knowledgebase'] }, { icon: BriefcaseBusiness, label: 'Work together', features: ['live-chat', 'browser', 'skills'] }, { icon: LayoutDashboard, label: 'Results', features: ['files', 'dashboard', 'database'] }],
    purpose: 'Help me research, prepare documents and organize everyday business work.',
    example: 'Research a topic, prepare a brief or organize a set of documents.',
    shortcuts: [
      { id: 'files', label: 'Files' }, { id: 'browser', label: 'Browser' },
      { id: 'dashboard', label: 'Project dashboard' }, { id: 'mcp:connected', label: 'MCPs' },
    ],
  },
  {
    id: 'personal', label: 'Personal assistant', icon: UserRound,
    summary: 'Plan · Brief · Follow up',
    flow: [{ icon: Plug, label: 'Your apps', features: ['mcp', 'bots'] }, { icon: CalendarClock, label: 'Plan', features: ['live-chat', 'memory', 'files'] }, { icon: Bell, label: 'Follow up', features: ['schedules', 'condition-triggers', 'bots'] }],
    purpose: 'Help me organize my work, prepare briefings and follow up on tasks.',
    example: 'Prepare a daily briefing or help me plan the week.',
    shortcuts: [
      { id: 'memory:memory', label: 'Memory' }, { id: 'mcp:gmail', label: 'Google account' },
      { id: 'schedules:schedules', label: 'Schedules' }, { id: 'mcp:connected', label: 'MCPs' },
    ],
  },
  {
    id: 'specialist', label: 'Team specialist', icon: UsersRound,
    summary: 'One job for your team',
    flow: [{ icon: ListChecks, label: 'Your process', features: ['files', 'skills', 'knowledgebase'] }, { icon: UsersRound, label: 'Expert help', features: ['live-chat', 'memory', 'mcp'] }, { icon: FileCheck2, label: 'Team results', features: ['dashboard', 'files', 'bots'] }],
    purpose: 'Help my team with a specific job, using our instructions and reference files.',
    example: 'Help process invoices, answer support questions or onboard customers.',
    shortcuts: [
      { id: 'identity:general', label: 'Instructions & identity' }, { id: 'memory:skills', label: 'Skills' },
      { id: 'files', label: 'Files' }, { id: 'mcp:share', label: 'People & access' },
    ],
  },
  {
    id: 'automation', label: 'Automation', icon: Workflow,
    summary: 'Run repeatable tasks',
    flow: [{ icon: CalendarClock, label: 'Time or event', features: ['schedules', 'condition-triggers', 'triggers'] }, { icon: Braces, label: 'Skills + code', features: ['skills', 'workflow-references', 'coding'] }, { icon: CheckCircle2, label: 'Results', features: ['dashboard', 'database', 'files', 'bots'] }],
    purpose: 'Help me build a repeatable process using skills, reusable functions and schedules or events.',
    example: 'Use skills and Python code for precise steps, then run on a schedule or an event.',
    shortcuts: [
      { id: 'schedules:functions', label: 'Functions' }, { id: 'memory:skills', label: 'Skills' },
      { id: 'schedules:schedules', label: 'Schedules' }, { id: 'schedules:conditions', label: 'Watchers' },
      { id: 'schedules:triggers', label: 'Webhooks' },
    ],
  },
  {
    id: 'brain', label: 'Company Brain assistant', icon: BookOpen,
    summary: 'Find & organize knowledge',
    flow: [{ icon: BookOpen, label: 'Company Brain', features: ['knowledgebase', 'attached-folders'] }, { icon: Search, label: 'Find answers', features: ['live-chat', 'files', 'memory'] }, { icon: FolderTree, label: 'Organize', features: ['knowledgebase', 'skills', 'schedules'] }],
    purpose: 'Help my company find answers in Brain, explain processes and suggest improvements to our knowledge.',
    example: 'Find policies, suggest duplicate merges or connect related knowledge. Choose Brain access after creation.',
    shortcuts: [
      { id: 'mcp:brain', label: 'Brain' }, { id: 'mcp:folders', label: 'Linked projects & folders' },
      { id: 'files', label: 'Files' }, { id: 'schedules:schedules', label: 'Schedules' },
    ],
  },
  {
    id: 'chief-of-staff', label: 'Chief of Staff', icon: Network,
    summary: 'Coordinate your Crews & workflows',
    flow: [{ icon: Network, label: 'Connect your work', features: ['workflow-references', 'attached-folders', 'knowledgebase'] }, { icon: ListChecks, label: 'Coordinate the work', features: ['live-chat', 'background-work', 'memory'] }, { icon: LayoutDashboard, label: 'Stay informed', features: ['dashboard', 'schedules', 'bots'] }],
    purpose: 'Coordinate my Crews and workflows: track progress, route work through their available functions and triggers, follow up on blockers and prepare status summaries. Help me choose the projects to connect. Use only the projects and actions I have access to.',
    example: 'Coordinate a launch across my Crews and workflows, highlight blockers and prepare a daily brief.',
    shortcuts: [
      { id: 'schedules:functions', label: 'Functions' }, { id: 'mcp:folders', label: 'Linked projects & folders' },
      { id: 'dashboard', label: 'Project dashboard' }, { id: 'schedules:schedules', label: 'Schedules' },
      { id: 'memory:memory', label: 'Memory' },
    ],
  },
] as const

export type CrewStarterType = typeof CREW_STARTER_TYPES[number]['id']
export type CrewOnboarding = { type?: CrewStarterType; openSharing: boolean }

export function crewStarterShortcuts(type?: CrewStarterType): string[] {
  return CREW_STARTER_TYPES.find(item => item.id === type)?.shortcuts.map(item => item.id) || []
}
