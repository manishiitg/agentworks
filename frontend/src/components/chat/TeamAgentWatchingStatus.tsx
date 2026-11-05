import { teamAgentFor, teamAgentStatus, useTeamAgents } from '../../products/work/useTeamAgents'
import { useChatStore } from '../../stores/useChatStore'
import { fillAskFromMyAgentDraft } from '../../utils/agentPurposeDraft'

/** The watched agent's availability and address are the same server reading used by the left menu; "Ask from my
 * agent" starts the request in your own Main with that address. */
export function TeamAgentWatchingStatus({ projectId, ownerId, chatKey, ownerName, agentName }: {
  projectId?: string; ownerId?: string; chatKey?: string; ownerName: string; agentName: string
}) {
  const people = useTeamAgents(projectId)
  const status = teamAgentStatus(people, ownerId, chatKey)
  const address = teamAgentFor(people, chatKey)?.address
  return <>
  <span className="inline-flex min-w-0 items-center gap-2">
    <span aria-hidden="true" className={`h-1.5 w-1.5 shrink-0 rounded-full ${status ? 'bg-muted-foreground/40' : 'bg-emerald-500'}`} />
    <span className="min-w-0">Watching {ownerName}'s {agentName}. Only they type here.{status && <> <span className="font-medium">{status}</span> · Messages arrive when the Mac is back.</>}</span>
  </span>
  {projectId && <button type="button" className="ml-auto shrink-0 rounded-full border border-border bg-background px-3 py-1 text-xs font-medium text-foreground shadow-sm hover:bg-muted"
    onClick={() => { if (!fillAskFromMyAgentDraft(projectId, ownerName, agentName, address)) useChatStore.getState().addToast('Open your own Main tab in this Crew, then ask from there.', 'error') }}>Ask from my agent</button>}
  </>
}
