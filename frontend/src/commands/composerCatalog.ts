import type { ModeCategory } from '../stores/useModeStore'
import { visibleNativeGoalCommands } from '../utils/nativeGoalCapability'
import { getCommands, getProductAndUserCommands } from './registry'
import type { CommandDefinition, WorkshopMode } from './types'

export interface ComposerCatalogOptions {
  modeCategory?: ModeCategory
  workshopMode?: WorkshopMode
  canWriteWorkflow?: boolean
  agentProfileId?: string
  nativeGoalSupported?: boolean
  /** A chat-specific list replaces the global and personal commands. */
  commands?: CommandDefinition[]
}

export function getComposerCommands({ commands, agentProfileId, modeCategory, workshopMode, canWriteWorkflow = true, nativeGoalSupported = false }: ComposerCatalogOptions) {
  return visibleNativeGoalCommands(commands ?? (agentProfileId
    ? getProductAndUserCommands(modeCategory, workshopMode, canWriteWorkflow)
    : getCommands(modeCategory, workshopMode, canWriteWorkflow)), nativeGoalSupported)
}
