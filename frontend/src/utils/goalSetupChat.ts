import { findProductCommand } from '../commands/registry'
import type { CommandContext } from '../commands/types'

// The chat message a setup command expands to, as if typed in the chat box.
export function goalSetupChatMessage(command: string): string {
  const focus = 'Requested from goal setup.'
  let message = ''
  findProductCommand(command, 'workflow', 'workshop')?.execute({
    beforeSlash: focus,
    onSubmit: (submitted: string) => { message = submitted },
    workshopMode: 'workshop',
  } as unknown as CommandContext)
  // Before the product commands load, send what the command's prompt says.
  return message || `Call get_workflow_command_guidance(kind="${command}", focus=${JSON.stringify(focus)}) and follow the returned instructions verbatim.`
}
