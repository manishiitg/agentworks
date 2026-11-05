import { teamAgentStatus, useTeamAgents } from '../../products/work/useTeamAgents'

/** The watched agent's availability is the same server reading used by the left menu. */
export function TeamAgentWatchingStatus({ projectId, ownerId, chatKey, ownerName, agentName }: {
  projectId?: string; ownerId?: string; chatKey?: string; ownerName: string; agentName: string
}) {
  const people = useTeamAgents(projectId)
  const status = teamAgentStatus(people, ownerId, chatKey)
  return <span className="inline-flex min-w-0 items-center gap-2">
    <span aria-hidden="true" className={`h-1.5 w-1.5 shrink-0 rounded-full ${status ? 'bg-muted-foreground/40' : 'bg-emerald-500'}`} />
    <span className="min-w-0">Watching {ownerName}'s {agentName}. Only they type here.{status && <> <span className="font-medium">{status}</span> · Messages arrive when the Mac is back.</>}</span>
  </span>
}
