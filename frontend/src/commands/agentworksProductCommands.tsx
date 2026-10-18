import { Activity, BellRing, Calendar, CheckCircle, Cloud, FileText, GitBranch, Globe, Layers, Play, Plug, RefreshCw, Share2, Target, Terminal, Wrench } from 'lucide-react'
import type { CommandDefinition } from './types'
import type { CommandContext } from './types'
import { renderCommandPrompt } from './commandPrompt'
export type AgentworksProductCommand = {
  name: string
  description: string
  icon: string
  aliases: string[]
  menuHidden: boolean
  prompt: string
}

export type AgentProfileResponse = {
  commands?: Array<Record<string, unknown>>
}

function asString(value: unknown): string {
  return typeof value === 'string' ? value : ''
}

function asStringArray(value: unknown): string[] {
  if (!Array.isArray(value)) return []
  return value.filter((entry): entry is string => typeof entry === 'string')
}

// Slash commands the builder ships with itself, declared in
// agentworksproduct/product.yaml. Same `{{context}}` contract as user-defined
// commands: whatever was typed before the slash is substituted in, and the
// placeholder is removed when nothing was.
export function parseAgentworksProductCommands(profile: AgentProfileResponse): AgentworksProductCommand[] {
  return (profile.commands ?? []).flatMap((command) => {
    const name = asString(command.name)
    const prompt = asString(command.prompt)
    if (!name || !prompt) return []
    return [{
      name,
      description: asString(command.description),
      icon: asString(command.icon) || 'terminal',
      aliases: asStringArray(command.aliases),
      menuHidden: command.menu_hidden === true,
      prompt,
    }]
  })
}

const icons: Record<string, typeof Terminal> = {
  activity: Activity,
  'bell-ring': BellRing,
  calendar: Calendar,
  'check-circle': CheckCircle,
  cloud: Cloud,
  'file-text': FileText,
  'git-branch': GitBranch,
  globe: Globe,
  layers: Layers,
  play: Play,
  plug: Plug,
  'refresh-cw': RefreshCw,
  'share-2': Share2,
  target: Target,
  terminal: Terminal,
  wrench: Wrench,
}

// Every builder command runs in the workflow surface under the plan-phase
// workshop gate — the yaml carries only name/description/prompt/aliases,
// this adapter owns the workflow-mode semantics all entries share.
export function toAgentworksCommandDefinitions(commands: AgentworksProductCommand[]): CommandDefinition[] {
  return commands.map((command) => {
    const Icon = icons[command.icon] ?? Terminal
    return {
      command: command.name,
      description: command.description,
      ...(command.aliases.length > 0 ? { aliases: command.aliases } : {}),
      icon: <Icon className="w-4 h-4" />,
      modes: ['workflow'],
      requiredWorkflowMode: 'plan',
      requiredWorkshopMode: 'workshop',
      ...(command.menuHidden ? { menuHidden: true } : {}),
      source: 'product',
      execute: (ctx) => { executeAgentworksProductCommand(command, ctx) },
    }
  })
}

export function executeAgentworksProductCommand(command: AgentworksProductCommand, ctx: CommandContext): void {
  const prompt = renderCommandPrompt(command.prompt, ctx.beforeSlash ?? '')
  if (prompt) ctx.onSubmit(prompt)
}
