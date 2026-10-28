import { createElement } from 'react'
import { commandsApi } from '../api/commands'
import type { UserCommand } from '../types/commands'
import type { CommandDefinition } from './types'
import type { ModeCategory } from '../stores/useModeStore'
import { setUserCommands } from './registry'
import { renderCommandPrompt } from './commandPrompt'
import { CommandIcon } from './CommandIcon'

type CommandMode = Exclude<ModeCategory, null>
let commandLoadGeneration = 0

function toCommandDefinition(uc: UserCommand): CommandDefinition {
  const icon = createElement(CommandIcon, { name: uc.frontmatter.icon, className: 'w-4 h-4' })

  const modes = uc.frontmatter.modes && uc.frontmatter.modes.length > 0
    ? uc.frontmatter.modes
      .map(mode => mode === 'chat' ? 'multi-agent' : mode)
      .filter((mode): mode is CommandMode => mode === 'multi-agent' || mode === 'workflow')
    : undefined

  return {
    command: uc.frontmatter.name,
    description: uc.frontmatter.description,
    icon,
    modes,
    source: 'user',
    execute: (ctx) => {
      const prompt = renderCommandPrompt(uc.content, ctx.beforeSlash ?? '')
      if (prompt) {
        ctx.onSubmit(prompt)
      }
    }
  }
}

export async function loadAndRegisterUserCommands(workspacePath?: string): Promise<void> {
  const generation = ++commandLoadGeneration
  // The registry is process-global. Clear the previous project's commands
  // before loading the newly active scope so they can never flash in another
  // project's menu while the request is in flight.
  setUserCommands([])
  try {
    const response = await commandsApi.listCommands(workspacePath)
    const commands = (response.commands || []).map(toCommandDefinition)
    if (generation === commandLoadGeneration) setUserCommands(commands)
  } catch {
    if (generation === commandLoadGeneration) setUserCommands([])
  }
}
