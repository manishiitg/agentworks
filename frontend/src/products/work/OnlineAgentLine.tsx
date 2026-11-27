import { useState } from 'react'
import { ChevronDown } from 'lucide-react'
import type { ChatTab } from '../../stores/useChatStore'
import { openProjectInPlace } from '../../utils/openProjectInPlace'
import { HeaderAgentSharing } from './ShareCrew'
import { teamAgentFor, type TeamTabState } from './teamApi'

/**
 * The header line of the person's own agent in an online project (owner, 26 Nov, #4121): where it runs, Open ↗ on its
 * server's website, and its Sharing: the same server-backed card a Mac agent has (who can ask, instructions for
 * teammates, what they see of the chat), opened from the Share button. The server decides everything.
 */
export function OnlineAgentLine({ tab, name, team }: { tab: ChatTab; name: string; team: TeamTabState }) {
  const online = tab.metadata?.agentProfileOnlineProject
  const key = tab.metadata?.agentProfileConversationKey
  const projectId = tab.metadata?.agentProfileProjectId
  const [open, setOpen] = useState(false)
  if (!online) return null
  const row = teamAgentFor(team.view?.agents, key)
  const word = !row?.in_team ? 'Only me' : row.audience === 'chosen' ? 'Chosen people' : 'Whole Team'
  return <div className="flex min-w-0 flex-1 items-center gap-2 text-sm">
    <span className="min-w-0 truncate font-medium">{name}</span>
    <span className="shrink-0 rounded-full bg-emerald-100 px-2 py-0.5 text-[11px] text-emerald-800 dark:bg-emerald-900 dark:text-emerald-200" title="It keeps running when your Mac is off.">Runs in {online.name} on the server</span>
    <button type="button" onClick={() => openProjectInPlace(online.connection, { surface: 'work', id: online.id })} className="shrink-0 text-xs text-primary hover:underline">Open project</button>
    {row && projectId && <span className="relative shrink-0">
      <button type="button" aria-haspopup="dialog" aria-expanded={open} onClick={() => setOpen(!open)} className="inline-flex items-center gap-1 rounded-md border border-border px-2 py-0.5 text-xs hover:bg-muted">Share: {word}<ChevronDown aria-hidden="true" className="h-3 w-3" /></button>
      {open && <div role="dialog" aria-label={`Sharing for ${name}`} className="absolute right-0 top-8 z-50 max-h-[70vh] w-96 overflow-y-auto rounded-lg border border-border bg-surface-panel p-3 text-sm shadow-lg">
        <HeaderAgentSharing projectId={projectId} agentKey={row.id} />
      </div>}
    </span>}
  </div>
}
