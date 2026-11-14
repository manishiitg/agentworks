// The server owns program support; unknown or not-yet-loaded means unavailable.
export function supportsNativeGoal(capabilities: Record<string, boolean> | undefined, provider: string | null | undefined): boolean {
  return Boolean(provider && capabilities?.[provider] === true)
}
export function visibleNativeGoalCommands<T extends { command: string }>(commands: T[], supported: boolean): T[] {
  return commands.filter(command => command.command !== 'goal' || supported)
}
