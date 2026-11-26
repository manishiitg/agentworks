import type { ModeCategory } from '../stores/useModeStore'
import type { CommandDefinition, WorkshopMode } from './types'
import { builtinCommands } from './builtin-commands'
import { sessionCommands } from './sessionCommands'

let revision = 0
const listeners = new Set<() => void>()
export function subscribeCommands(listener: () => void) {
  listeners.add(listener)
  return () => { listeners.delete(listener) }
}
export function getCommandRevision() { return revision }
function notifyCommandsChanged() {
  revision++
  listeners.forEach(listener => listener())
}

let userCommands: CommandDefinition[] = []
let productCommands: CommandDefinition[] = []

function matchesMode(cmd: CommandDefinition, mode?: ModeCategory, workshopMode?: WorkshopMode, canWriteWorkflow = true): boolean {
  if (cmd.hidden) return false
  if (mode === undefined || mode === null) return true

  if (mode === 'workflow') {
    if (!(cmd.modes?.includes('workflow') ?? false)) return false
    // Readers remain constrained to Run even if a restored tab says Builder.
    // A slash command must not silently escape the current chat's tool policy.
    const permittedMode = canWriteWorkflow ? workshopMode : 'run'
    if (permittedMode && cmd.requiredWorkshopMode) {
      const allowed = Array.isArray(cmd.requiredWorkshopMode)
        ? cmd.requiredWorkshopMode
        : [cmd.requiredWorkshopMode]
      return allowed.includes(permittedMode)
    }
    return true
  }

  if (mode === 'multi-agent') {
    return cmd.modes?.includes('multi-agent') ?? cmd.modes === undefined
  }

  return true
}

export function setUserCommands(cmds: CommandDefinition[]) {
  userCommands = cmds
  notifyCommandsChanged()
}

// Registered when a product's profile loads. Cleared by passing an empty list
// so switching products cannot leave the previous product's commands in the
// menu, offering flows the current agent has no skills for.
export function setProductCommands(cmds: CommandDefinition[]) {
  productCommands = cmds
  notifyCommandsChanged()
}

/** Commands owned by the currently mounted product's product.yaml. */
export function getProductCommands(mode?: ModeCategory, workshopMode?: WorkshopMode, canWriteWorkflow = true): CommandDefinition[] {
  return productCommands.filter(cmd => !cmd.menuHidden && matchesMode(cmd, mode, workshopMode, canWriteWorkflow))
}

export function findProductCommand(name: string, mode?: ModeCategory, workshopMode?: WorkshopMode, canWriteWorkflow = true): CommandDefinition | undefined {
  return productCommands.find(cmd => matchesName(cmd, name) && matchesMode(cmd, mode, workshopMode, canWriteWorkflow))
}

export function getProductAndUserCommands(mode?: ModeCategory, workshopMode?: WorkshopMode, canWriteWorkflow = true): CommandDefinition[] {
  return [...sessionCommands, ...productCommands, ...userCommands].filter(cmd => !cmd.menuHidden && matchesMode(cmd, mode, workshopMode, canWriteWorkflow))
}

export function findProductOrUserCommand(name: string, mode?: ModeCategory, workshopMode?: WorkshopMode, canWriteWorkflow = true): CommandDefinition | undefined {
  return [...sessionCommands, ...productCommands, ...userCommands].find(cmd => matchesName(cmd, name) && matchesMode(cmd, mode, workshopMode, canWriteWorkflow))
}

export function getCommands(mode?: ModeCategory, workshopMode?: WorkshopMode, canWriteWorkflow = true): CommandDefinition[] {
  return [...sessionCommands, ...productCommands, ...builtinCommands, ...userCommands].filter(cmd => !cmd.menuHidden && matchesMode(cmd, mode, workshopMode, canWriteWorkflow))
}

export function findCommand(name: string, mode?: ModeCategory, workshopMode?: WorkshopMode, canWriteWorkflow = true): CommandDefinition | undefined {
  return [...sessionCommands, ...productCommands, ...builtinCommands, ...userCommands].find(cmd =>
    matchesName(cmd, name) && matchesMode(cmd, mode, workshopMode, canWriteWorkflow)
  )
}

function matchesName(cmd: CommandDefinition, name: string): boolean {
  return cmd.command === name || (cmd.aliases?.includes(name) ?? false)
}

export function findCommandAnyMode(name: string): CommandDefinition | undefined {
  return sessionCommands.find(c => matchesName(c, name))
    ?? productCommands.find(c => matchesName(c, name))
    ?? builtinCommands.find(c => matchesName(c, name))
    ?? userCommands.find(c => matchesName(c, name))
}
