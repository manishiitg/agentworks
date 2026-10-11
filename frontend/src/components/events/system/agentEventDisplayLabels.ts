export function completionTitle(agentName: string | undefined, isAgentItem: boolean, label: string): string {
  if (isAgentItem) return 'Completed'
  return `${label} completed: ${agentName || 'Agent'}`
}
