import { useState } from 'react'
import { ChevronDown } from 'lucide-react'
import type { ChatTab } from '../../stores/useChatStore'
import { useChatStore } from '../../stores/useChatStore'
import { openOnServerWebsite } from '../../utils/openOnServerWebsite'
import { teamAgentFor, teamChanged, teamFailure, teamRequest, type TeamTabState } from './teamApi'

type Share = 'only' | 'team' | 'chosen'

/**
 * The header line of the person's own agent in an online project (owner, 26 Nov, #4121): where it runs, Open ↗ on its
 * server's website, and who in the Team may ask it. Sharing goes through the Team's agent rule route as for Mac agents;
 * the server lets the person who made the agent change it (crewTeamRuleActor) and decides everything else.
 */
export function OnlineAgentLine({ tab, name, team }: { tab: ChatTab; name: string; team: TeamTabState }) {
  const online = tab.metadata?.agentProfileOnlineProject
  const key = tab.metadata?.agentProfileConversationKey
  const [open, setOpen] = useState(false)
  const [busy, setBusy] = useState(false)
  if (!online) return null
  const row = teamAgentFor(team.view?.agents, key)
  const share: Share = !row?.in_team ? 'only' : row.audience === 'chosen' ? 'chosen' : 'team'
  const chosen = new Set((row?.chosen_people || []).map(person => person.user_id))
  const people = (team.view?.people || []).filter(person => !person.you && person.note !== 'invited')
  const word = share === 'only' ? 'Only me' : share === 'team' ? 'Whole Team' : `Chosen: ${(row?.chosen_people || []).map(person => person.name).join(', ') || 'nobody yet'}`
  const save = async (body: Record<string, unknown>) => {
    if (!team.target || !key) return
    setBusy(true)
    try { await teamRequest(team.target, '/agent-rule', 'POST', { agent_key: row?.id || key, ...body }); teamChanged() }
    catch (cause) { useChatStore.getState().addToast(teamFailure(cause).message, 'error') }
    finally { setBusy(false) }
  }
  const pick = (next: Share) => {
    if (next === 'only') void save({ in_team: false })
    else if (next === 'team') void save({ in_team: true, audience: 'whole_team' })
    else void save({ in_team: true, audience: 'chosen', chosen_people: [...chosen] })
  }
  const togglePerson = (id: string) => {
    const next = new Set(chosen)
    if (next.has(id)) next.delete(id); else next.add(id)
    void save({ in_team: true, audience: 'chosen', chosen_people: [...next] })
  }
  const route = `/projects/${encodeURIComponent(online.id)}`
  return <div className="flex min-w-0 flex-1 items-center gap-2 text-sm">
    <span className="min-w-0 truncate font-medium">{name}</span>
    <span className="shrink-0 rounded-full bg-emerald-100 px-2 py-0.5 text-[11px] text-emerald-800 dark:bg-emerald-900 dark:text-emerald-200" title="It keeps running when your Mac is off.">Runs in {online.name} on the server</span>
    <button type="button" onClick={() => void openOnServerWebsite(online.connection, route)} className="shrink-0 text-xs text-primary hover:underline">Open ↗</button>
    {row && <span className="relative shrink-0">
      <button type="button" aria-haspopup="menu" aria-expanded={open} disabled={busy} onClick={() => setOpen(!open)} className="inline-flex items-center gap-1 rounded-md border border-border px-2 py-0.5 text-xs hover:bg-muted">Share: {word}<ChevronDown aria-hidden="true" className="h-3 w-3" /></button>
      {open && <div role="menu" aria-label="Share with the Team" className="absolute right-0 top-8 z-50 w-64 rounded-lg border border-border bg-surface-panel p-2 text-sm shadow-lg">
        <p className="px-1 pb-1 text-xs font-semibold">Share with the Team</p>
        {([['only', 'Only me', 'Only you see and use it'], ['team', 'Whole Team', 'Everyone in the Team can ask it'], ['chosen', 'Chosen people', 'Only the people you pick']] as const).map(([value, title, line]) =>
          <button key={value} type="button" role="menuitemradio" aria-checked={share === value} disabled={busy} onClick={() => pick(value)} className={`block w-full rounded-md px-2 py-1.5 text-left hover:bg-muted ${share === value ? 'bg-muted' : ''}`}>
            <span className="block font-medium">{title}</span><span className="block text-xs text-muted-foreground">{line}</span>
          </button>)}
        {share === 'chosen' && <div className="flex flex-wrap gap-1 px-1 pt-1">
          {people.length === 0 && <span className="text-xs text-muted-foreground">Nobody else is in the Team yet.</span>}
          {people.map(person => <button key={person.user_id} type="button" disabled={busy} aria-pressed={chosen.has(person.user_id)} onClick={() => togglePerson(person.user_id)} className={`rounded-full border px-2 py-0.5 text-xs ${chosen.has(person.user_id) ? 'border-primary bg-primary/10 text-primary' : 'border-border'}`}>{person.name}</button>)}
        </div>}
      </div>}
    </span>}
  </div>
}
