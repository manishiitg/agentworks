import { BookOpen, BriefcaseBusiness, Code2, Network, UserRound, UsersRound, Workflow } from 'lucide-react'

/** Onboarding suggestions only. The live menu inventory still admits every link. */
export const CREW_STARTER_TYPES = [
  {
    id: 'code', label: 'Code', icon: Code2,
    summary: 'Build & fix software',
    purpose: 'Help me build and maintain software, understand code and fix issues.',
    example: 'Review a project, explain how it works or help make a change.',
    shortcuts: [
      { id: 'files', label: 'Files' }, { id: 'mcp:connected', label: 'MCPs' },
      { id: 'shell', label: 'Terminal' }, { id: 'memory:skills', label: 'Skills' },
    ],
  },
  {
    id: 'cowork', label: 'Cowork', icon: BriefcaseBusiness,
    summary: 'Research · Write · Organize',
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
    purpose: 'Help me organize my work, prepare briefings and follow up on tasks.',
    example: 'Prepare a daily briefing or help me plan the week.',
    shortcuts: [
      { id: 'memory:memory', label: 'Memory' }, { id: 'mcp:connected', label: 'MCPs' },
      { id: 'schedules:schedules', label: 'Schedules' },
    ],
  },
  {
    id: 'specialist', label: 'Team specialist', icon: UsersRound,
    summary: 'One job for your team',
    purpose: 'Help my team with a specific job, using our instructions and reference files.',
    example: 'Help process invoices, answer support questions or onboard customers.',
    shortcuts: [
      { id: 'identity:general', label: 'Instructions & identity' }, { id: 'memory:skills', label: 'Skills' },
      { id: 'files', label: 'Files' }, { id: 'mcp:share', label: 'Access & sharing' },
    ],
  },
  {
    id: 'automation', label: 'Automation', icon: Workflow,
    summary: 'Run repeatable tasks',
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
