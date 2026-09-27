import { findProductCommand } from '../commands/registry'
import type { CommandContext } from '../commands/types'

// The chat message a setup command expands to, as if typed in the chat box.
// Installed playbooks are named so the setup starts from them.
export function goalSetupChatMessage(command: string, playbooks: Array<{ title: string; skill_name: string }> = []): string {
  const focus = playbooks.length > 0
    ? `Requested from goal setup. Start from the installed playbook${playbooks.length > 1 ? 's' : ''} ${playbooks.map(p => `"${p.title}" (skill ${p.skill_name})`).join(', ')}: read the skill and follow its setup checks.`
    : 'Requested from goal setup.'
  let message = ''
  findProductCommand(command, 'workflow', 'workshop')?.execute({
    beforeSlash: focus,
    onSubmit: (submitted: string) => { message = submitted },
    workshopMode: 'workshop',
  } as unknown as CommandContext)
  // Before the product commands load, send what the command's prompt says.
  return message || `Call get_workflow_command_guidance(kind="${command}", focus=${JSON.stringify(focus)}) and follow the returned instructions verbatim.`
}
