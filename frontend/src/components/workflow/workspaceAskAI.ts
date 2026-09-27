import { buildAskAIMessage } from '../../utils/askAIMessage'
import { getWorkspaceView, type WorkspaceViewId } from './workspaceViews'

// Every right-side workspace view has one useful first message for Builder.
// Keeping this exhaustive means a newly registered view cannot silently ship
// without the shared Ask AI action.
//
// These summaries are USER-VISIBLE: the chat surface shows them and hides the
// builder instructions below. Keep them in plain business words — no tool
// calls, skill paths, or config keys. Anything Builder needs that a
// small-business owner should not see belongs in WORKSPACE_ASK_AI_INSTRUCTIONS.
export const WORKSPACE_ASK_AI_MESSAGE = {
  report: "Help me understand my results page. Explain what happened and what the numbers mean in plain words, and ask what I want to look into or improve.",
  flow: "Walk me through the plan for my helper — what it does, step by step. If there is no plan yet, help me build one. Point out anything missing or risky, and ask what I want to change before changing anything.",
  costs: "Help me understand what my helper costs to run — and bring it down if I want. Explain in plain words where the money goes and what's costing the most, then ask what I want to change.",
  'execution-logs': "Help me investigate this workflow's execution logs. Identify failures or unusual behavior, explain the evidence, and ask which run or issue I want to examine.",
  knowledge: "Help me use what my helper knows — what it has learned, its background notes, and its stored records. Explain in plain words and ask what I want to find, add, or change.",
  schedules: "Help me set up or change a schedule for this workflow. Ask what should run, when it should run, and any route requirements.",
  webhooks: "Help me set up or change a webhook for this workflow. Ask which external event should trigger it, what should run, and which authentication and routing it needs.",
  files: "Help me work with this workflow's files. Ask what I want to find, understand, create, or change before modifying anything.",
  pulse: "Tell me what Pulse is and how it can help me. Explain in plain words, then ask what I want to review or act on.",
  'human-actions': "Help me review the pending human actions for this workflow. Explain the evidence and options for the action I choose.",
  backup: "Help me set up or change backup for this workflow. Explain the current state and ask what should be backed up and where.",
  publish: "Help me publish this workflow. Explain its current publishing state and ask which target and release process I want.",
  notify: "Help me change this workflow's notifications. Explain the current setup, including the Gmail sender, and ask which events, channels, sender, and recipients I want.",
  access: "Help me understand or change access to this workflow. Explain the current permissions and ask who should have which level of access.",
  playbooks: "Help me choose and use an AgentWorks playbook for this workflow. Ask what outcome I need, compare relevant playbooks, and explain the setup before changing anything.",
  mcp: "Help me add an app connection or a skill for this workflow. Ask what I want to connect or which capability it needs, then help me set it up.",
  browser: "Help me configure browser access for this workflow. Ask which sites, accounts, and browser tasks it needs to handle.",
  workshop: "Help me review my helper's past chats and automatic jobs. Ask which one I want to understand.",
  identity: "Help me manage this workflow's identity — its name, icon, purpose, secrets, folders, and LLMs.",
} satisfies Record<WorkspaceViewId, string>

// Builder-only additions per view. Hidden from chat display (see
// utils/askAIMessage): reference docs to load, run modes, safety rules.
const WORKSPACE_ASK_AI_INSTRUCTIONS: Partial<Record<WorkspaceViewId, string>> = {
}

// The Integrations view holds five tabs, so its header Ask AI follows the
// active tab instead of the view. Same marker-block shape as views: a
// user-visible plain-words summary plus hidden builder instructions.
export type IntegrationTabId = 'apps' | 'skills' | 'slack' | 'whatsapp' | 'gmail' | 'cli'

const INTEGRATION_TAB_ASK_AI_MESSAGE: Record<IntegrationTabId, { label: string; summary: string; instructions?: string }> = {
  apps: {
    label: 'Integrations · MCPs',
    summary: "Help me add an app connection for this workflow. Ask what I want to connect, then help me set it up.",
  },
  skills: {
    label: 'Integrations · Skills',
    summary: "Help me find, create, or configure a skill for this workflow. Ask what capability or guidance it should provide.",
  },
  slack: {
    label: 'Integrations · Slack',
    summary: "Help me connect Slack to this workflow. Inspect current settings, explain the connection and route setup, and ask which channel I need.",
    instructions: "Load builder-reference/references/slack-bot-routing.md before advising. Use run mode without asking for a grant. Never request tokens in chat.",
  },
  whatsapp: {
    label: 'Integrations · WhatsApp',
    summary: "Help me connect WhatsApp to this workflow. Inspect current settings, explain the connection and route setup, and ask which number I need.",
  },
  gmail: {
    label: 'Integrations · Gmail',
    summary: "Help me connect or configure Gmail, default recipients, and email access settings.",
  },
  cli: {
    label: 'Integrations · Connect',
    summary: "Help me connect an AI agent to this installation through MCP. Explain the HTTP MCP URL and browser sign-in, and ask which AI app I use.",
  },
}

/**
 * Ask AI is a product entry point, not just a panel-specific canned prompt.
 * Every request loads the same compact guide so Builder explains unfamiliar
 * workflow concepts consistently while keeping the current view as context.
 *
 * The returned text is a marker block (utils/askAIMessage): Builder receives
 * everything, but chat surfaces show only the plain-words summary.
 */
export function getWorkspaceAskAIMessage(view: WorkspaceViewId): string {
  const label = getWorkspaceView(view).label
  const instructions = `First read the workflow help guide with read_skill(skills=[{"name":"builder-reference","path":"references/workflow-guide.md"}]), then help me with the ${label} view. Use the current workflow evidence, explain what matters, and ask what I want to do before changing anything.`
  const extra = WORKSPACE_ASK_AI_INSTRUCTIONS[view]
  return buildAskAIMessage({
    view: label,
    summary: WORKSPACE_ASK_AI_MESSAGE[view],
    instructions: extra ? `${instructions} ${extra}` : instructions,
  })
}

export function getIntegrationTabAskAIMessage(tab: IntegrationTabId): string {
  const entry = INTEGRATION_TAB_ASK_AI_MESSAGE[tab]
  const instructions = `First read the workflow help guide with read_skill(skills=[{"name":"builder-reference","path":"references/workflow-guide.md"}]), then help me with the ${entry.label} tab. Use the current workflow evidence, explain what matters, and ask what I want to do before changing anything.`
  return buildAskAIMessage({
    view: entry.label,
    summary: entry.summary,
    instructions: entry.instructions ? `${instructions} ${entry.instructions}` : instructions,
  })
}

// The Identity view holds five tabs, so its header Ask AI follows the
// active tab instead of the view.
export type IdentityTabId = 'general' | 'secrets' | 'folders' | 'llm' | 'upgrades'

const IDENTITY_TAB_ASK_AI_MESSAGE: Record<IdentityTabId, { label: string; summary: string; instructions?: string }> = {
  general: {
    label: 'Identity · General',
    summary: "Help me manage this workflow's identity. Explain its name, icon, and purpose in plain words, and ask what I want to change.",
    instructions: "The workflow's purpose lives in soul/soul.md (## Objective, ## Success Criteria). Read it from the workspace files before advising; never ask the user to paste it.",
  },
  secrets: {
    label: 'Identity · Secrets',
    summary: "Help me configure a secret for this workflow. Ask which credential is needed and where it should come from without asking me to reveal it in chat.",
  },
  folders: {
    label: 'Identity · File access',
    summary: "Help me attach things to this workflow: folders, shared knowledge bases, or other workflows as read-only context. Ask what it needs and why, then set it up; folder access should be read-only unless writing is truly needed.",
  },
  llm: {
    label: 'Identity · Models',
    summary: "Help me choose or configure the LLMs for this workflow. Explain the current setup and ask about quality, speed, and cost requirements.",
  },
  upgrades: {
    label: 'Identity · Upgrades',
    summary: "Help me understand this workflow's platform upgrades. Explain what is pending, what the workflow already satisfies, and help me start pending upgrades only if I ask.",
  },
}

// The Knowledge view holds three tabs, so its header Ask AI follows the
// active tab instead of the view.
export type KnowledgeTabId = 'learnings' | 'knowledgebase' | 'database'

const KNOWLEDGE_TAB_ASK_AI_MESSAGE: Record<KnowledgeTabId, { label: string; summary: string; instructions?: string }> = {
  learnings: {
    label: 'Knowledge · Learnings',
    summary: "Help me understand or change what this workflow has learned. Explain the relevant learnings and ask which behavior or step I want to examine.",
  },
  knowledgebase: {
    label: 'Knowledge · Knowledgebase',
    summary: "Help me use my helper's background info and notes. Explain in plain words what it knows, and ask what I want to find, add, or improve.",
  },
  database: {
    label: 'Knowledge · Database',
    summary: "Help me understand or change this workflow's database. Use the current schema and data, then ask what I want to inspect or update.",
  },
}

export function getKnowledgeTabAskAIMessage(tab: KnowledgeTabId): string {
  const entry = KNOWLEDGE_TAB_ASK_AI_MESSAGE[tab]
  const instructions = `First read the workflow help guide with read_skill(skills=[{"name":"builder-reference","path":"references/workflow-guide.md"}]), then help me with the ${entry.label} tab. Use the current workflow evidence, explain what matters, and ask what I want to do before changing anything.`
  return buildAskAIMessage({
    view: entry.label,
    summary: entry.summary,
    instructions: entry.instructions ? `${instructions} ${entry.instructions}` : instructions,
  })
}

export function getIdentityTabAskAIMessage(tab: IdentityTabId): string {
  const entry = IDENTITY_TAB_ASK_AI_MESSAGE[tab]
  const instructions = `First read the workflow help guide with read_skill(skills=[{"name":"builder-reference","path":"references/workflow-guide.md"}]), then help me with the ${entry.label} tab. Use the current workflow evidence, explain what matters, and ask what I want to do before changing anything.`
  return buildAskAIMessage({
    view: entry.label,
    summary: entry.summary,
    instructions: entry.instructions ? `${instructions} ${entry.instructions}` : instructions,
  })
}

// The Access view holds two tabs, so its header Ask AI follows the
// active tab instead of the view.
export type AccessTabId = 'workflow' | 'users' | 'slack'

const ACCESS_TAB_ASK_AI_MESSAGE: Record<AccessTabId, { label: string; summary: string; instructions?: string }> = {
  workflow: {
    label: 'Access · This workflow',
    summary: "Help me share this workflow. Explain who can see or edit it now, and ask who should be added, removed, or changed.",
  },
  users: {
    label: 'Access · Users',
    summary: "Help me manage this deployment's accounts and roles. Explain the current setup and ask what should change, without asking me to reveal passwords in chat.",
  },
  slack: {
    label: 'Access · Slack',
    summary: "Help me manage this deployment's shared Slack bot and see which workflows have their own bot. Guide me through Slack app setup in the settings UI without asking for tokens in chat.",
  },
}

export function getAccessTabAskAIMessage(tab: AccessTabId): string {
  const entry = ACCESS_TAB_ASK_AI_MESSAGE[tab]
  const instructions = `First read the workflow help guide with read_skill(skills=[{"name":"builder-reference","path":"references/workflow-guide.md"}]), then help me with the ${entry.label} tab. Use the current workflow evidence, explain what matters, and ask what I want to do before changing anything.`
  return buildAskAIMessage({
    view: entry.label,
    summary: entry.summary,
    instructions: entry.instructions ? `${instructions} ${entry.instructions}` : instructions,
  })
}
