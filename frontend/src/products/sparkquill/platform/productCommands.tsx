import { Sparkles } from 'lucide-react'
import { renderCommandPrompt } from '../../../commands/commandPrompt'
import type { CommandDefinition } from '../../../commands/types'
import type { QuickCommand } from '../stores/types'

/** Manifest-owned shortcuts use the platform prompt/context contract. */
export function toProductCommandDefinitions(commands: QuickCommand[]): CommandDefinition[] {
  return commands.map((c) => {
    return {
      command: c.name,
      description: c.label,
      ...(c.aliases?.length ? { aliases: c.aliases } : {}),
      ...(c.menuHidden ? { menuHidden: true } : {}),
      icon: <Sparkles className="w-4 h-4" />,
      modes: ['multi-agent'],
      source: 'product',
      execute: (ctx) => {
        const prompt = renderCommandPrompt(c.message, ctx.beforeSlash ?? '')
        if (prompt) ctx.onSubmit(prompt)
      },
    }
  })
}
