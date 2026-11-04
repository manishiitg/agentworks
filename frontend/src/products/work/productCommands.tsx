import { renderCommandPrompt } from '../../commands/commandPrompt'
import { Brain, Terminal } from 'lucide-react'
import type { CommandDefinition } from '../../commands/types'
import type { WorkProductCommand } from './workData'

// Icons are named in product.yaml rather than imported there, so the manifest
// stays free of frontend concerns. An unknown name falls back rather than
// failing: a command with a generic icon is still usable, one that throws is not.
const icons: Record<string, typeof Terminal> = {
  brain: Brain,
  terminal: Terminal,
}

/**
 * Product commands submit their prompt on the user's behalf, following the same
 * `{{context}}` contract as user-defined commands: whatever was typed before the
 * slash is substituted in, and the placeholder is removed when nothing was.
 */
export function toProductCommandDefinitions(commands: WorkProductCommand[]): CommandDefinition[] {
  return commands.map((command) => {
    const Icon = icons[command.icon] ?? Terminal
    return {
      command: command.name,
      description: command.description,
      icon: <Icon className="w-4 h-4" />,
      modes: ['multi-agent'],
      source: 'product',
      execute: (ctx) => {
        const prompt = renderCommandPrompt(command.prompt, ctx.beforeSlash ?? '')
        if (prompt) ctx.onSubmit(prompt)
      },
    }
  })
}
