import { useState } from 'react'
import api from '../../services/api'

type TeamPerson = { id: string; name: string }
/** One of the project's agents a Team can use (crew_team_projects_view.go): the viewer's own and others' shared with them. */
export type ProjectTeamAgent = { id: string; name: string; maker: string; mine: boolean; sharing: 'only_me' | 'whole_team' | 'chosen'; chosen?: TeamPerson[] }

const words = { only_me: 'Only me', whole_team: 'Whole Team', chosen: 'Chosen people' } as const

/**
 * The project's agents under a Team in its Users (owner, 26 Nov): who made each and who in the Team may ask it. The
 * person who made an agent changes its sharing here, through the Team's agent rule route; the server decides.
 */
export function ProjectTeamAgents({ teamId, agents, members, busy, run, changed }: {
  teamId: string; agents: ProjectTeamAgent[]; members: TeamPerson[]; busy: boolean
  run: (work: () => Promise<void>, fallback?: string) => Promise<void>; changed: () => void
}) {
  const [picking, setPicking] = useState<string>()
  const save = (agent: ProjectTeamAgent, body: Record<string, unknown>) => run(async () => {
    await api.post(`/api/crew-teams/${encodeURIComponent(teamId)}/agent-rule`, { agent_key: agent.id, ...body })
    changed()
  }, 'Could not change who it is shared with.')
  const pick = (agent: ProjectTeamAgent, next: ProjectTeamAgent['sharing']) => {
    setPicking(next === 'chosen' ? agent.id : undefined)
    if (next === 'only_me') void save(agent, { in_team: false })
    else if (next === 'whole_team') void save(agent, { in_team: true, audience: 'whole_team' })
    else void save(agent, { in_team: true, audience: 'chosen', chosen_people: (agent.chosen || []).map(person => person.id) })
  }
  const toggle = (agent: ProjectTeamAgent, id: string) => {
    const chosen = new Set((agent.chosen || []).map(person => person.id))
    if (chosen.has(id)) chosen.delete(id); else chosen.add(id)
    void save(agent, { in_team: true, audience: 'chosen', chosen_people: [...chosen] })
  }
  if (!agents.length) return <p className="pl-9 text-xs text-muted-foreground">No agents shared with this Team yet.</p>
  return <ul aria-label="Agents this Team can use" className="space-y-1.5 pl-9">
    {agents.map(agent => <li key={agent.id} className="space-y-1">
      <div className="flex min-w-0 items-center gap-2 text-xs">
        <span className="min-w-0 flex-1 truncate"><span className="font-medium">{agent.name}</span> <span className="text-muted-foreground">· {agent.mine ? 'yours' : agent.maker}</span></span>
        {agent.mine
          ? <select aria-label={`Who can ask ${agent.name}`} value={agent.sharing} disabled={busy} onChange={event => pick(agent, event.target.value as ProjectTeamAgent['sharing'])} className="h-7 rounded-md border border-border bg-background px-1.5 text-xs">
            {Object.entries(words).map(([value, text]) => <option key={value} value={value}>{text}</option>)}
          </select>
          : <span className="shrink-0 text-muted-foreground">{agent.sharing === 'chosen' ? `Chosen: ${(agent.chosen || []).map(person => person.name).join(', ')}` : words[agent.sharing]}</span>}
      </div>
      {agent.mine && agent.sharing === 'chosen' && <p className="truncate text-[11px] text-muted-foreground">{(agent.chosen || []).map(person => person.name).join(', ') || 'Nobody chosen yet'} <button type="button" className="text-primary hover:underline" onClick={() => setPicking(picking === agent.id ? undefined : agent.id)}>{picking === agent.id ? 'Done' : 'Change'}</button></p>}
      {agent.mine && picking === agent.id && <div role="group" aria-label={`People who can ask ${agent.name}`} className="flex flex-wrap gap-x-3 gap-y-1">
        {members.map(person => <label key={person.id} className="inline-flex items-center gap-1 text-[11px]"><input type="checkbox" disabled={busy} checked={(agent.chosen || []).some(item => item.id === person.id)} onChange={() => toggle(agent, person.id)} />{person.name}</label>)}
      </div>}
    </li>)}
    <li className="text-[11px] text-muted-foreground">Agents set to Only me show only to the person who made them.</li>
  </ul>
}
