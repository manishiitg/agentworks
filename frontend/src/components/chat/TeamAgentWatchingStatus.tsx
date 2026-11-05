import { teamAgentFor, useTeamTab } from '../../products/work/teamApi'
import { useChatStore } from '../../stores/useChatStore'
import { fillAskFromMyAgentDraft } from '../../utils/agentPurposeDraft'
import { ownerShortName } from './chatTabName'

/** The watched agent's name, its person's display name, availability and address all come from the Team tab's one
 * server reading (the same rows as the Team tab and the left menu); "Ask from my agent" starts the request in your own
 * Main with that address. */
export function TeamAgentWatchingStatus({ projectId, chatKey, ownerName, agentName }: {
  projectId?: string; chatKey?: string; ownerName: string; agentName: string
}) {
  const agent = teamAgentFor(useTeamTab(projectId).view?.agents, chatKey)
  const status = agent?.status_label || ''
  const owner = agent?.owner_name || ownerShortName(ownerName)
  const name = agent?.name || agentName
  return <>
  <span className="inline-flex min-w-0 items-center gap-2">
    <span aria-hidden="true" className={`h-1.5 w-1.5 shrink-0 rounded-full ${status ? 'bg-muted-foreground/40' : 'bg-emerald-500'}`} />
    <span className="min-w-0">Watching {owner}'s {name}. Only they type here.{status && <> <span className="font-medium">{status}</span> · Messages arrive when the Mac is back.</>}</span>
  </span>
  {projectId && <button type="button" className="ml-auto shrink-0 rounded-full border border-border bg-background px-3 py-1 text-xs font-medium text-foreground shadow-sm hover:bg-muted"
    onClick={() => { if (!fillAskFromMyAgentDraft(projectId, owner, name, agent?.address)) useChatStore.getState().addToast('Open your own Main tab in this Crew, then ask from there.', 'error') }}>Ask from my agent</button>}
  </>
}
