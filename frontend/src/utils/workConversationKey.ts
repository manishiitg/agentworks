/** The conversation key a Crew deep link names. The link's agent is the key after the Crew's id: "agent-x" for a side
 * chat (`<crew>:chat:agent-x`), "schedule:<id>" for a message schedule's own chat (`<crew>:schedule:<id>`), "main" for Main. */
export function workConversationKey(projectId: string, agent?: string | null): string {
  if (!agent || agent === 'main') return projectId
  return agent.startsWith('schedule:') ? `${projectId}:${agent}` : `${projectId}:chat:${agent}`
}

/** The deep-link agent for a listed conversation key: the part after the Crew's id, so the link keeps its full identity. */
export function workConversationAgent(key: string): string | undefined {
  return key.split(':chat:')[1] ?? (key.includes(':schedule:') ? key.slice(key.indexOf(':schedule:') + 1) : undefined)
}
