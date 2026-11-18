import { useEffect, useState } from 'react'
import { CalendarClock } from 'lucide-react'
import { addAutomationToTeam, changeTeamWay, teamFailure, removeTeamRoutine, teamOnlineItems, teamWay, useTeamTab, type TeamOnlineItem, type TeamOnlineView, type TeamTarget, type TeamWayView } from './teamApi'

const teamCard = 'space-y-2.5 rounded-xl border border-border px-4 py-3 text-sm'

/** What this Crew's Team shares, read from the server and refreshed whenever the Team changes. */
export function useTeamWay(target?: TeamTarget, enabled = true) {
  const [state, setState] = useState<{ key: string; view?: TeamWayView; failed?: boolean }>()
  const key = target ? `${target.connection || ''}:${target.project}` : ''
  useEffect(() => {
    if (!target || !enabled) return
    let active = true
    const load = () => { void teamWay(target).then(view => { if (active) setState({ key, view }) }).catch(() => { if (active) setState({ key, failed: true }) }) }
    load()
    window.addEventListener('agentworks:team-updated', load)
    return () => { active = false; window.removeEventListener('agentworks:team-updated', load) }
  }, [key, enabled]) // eslint-disable-line react-hooks/exhaustive-deps
  return state?.key === key ? state : undefined
}

/** The routines running for this Team on its online home, refreshed whenever the Team changes. */
export function useTeamRoutines(target?: TeamTarget, enabled = true) {
  const [state, setState] = useState<{ key: string; view?: TeamOnlineView }>()
  const key = target ? `${target.connection || ''}:${target.project}` : ''
  useEffect(() => {
    if (!target || !enabled) return
    let active = true
    const load = () => { void teamOnlineItems(target).then(view => { if (active) setState({ key, view }) }).catch(() => { if (active) setState({ key }) }) }
    load()
    window.addEventListener('agentworks:team-updated', load)
    return () => { active = false; window.removeEventListener('agentworks:team-updated', load) }
  }, [key, enabled]) // eslint-disable-line react-hooks/exhaustive-deps
  return state?.key === key ? state.view : undefined
}

/** The routines that run online for this Team, with who added them; the rest of what the Team shares is in the Team kit
 * (TeamKit, #3141). Shown only when there are some. */
export function TeamRoutinesCard({ target }: { target: TeamTarget }) {
  const routines = useTeamRoutines(target)
  const [pending, setPending] = useState('')
  const [line, setLine] = useState('')
  const items = routines?.items ?? []
  if (!items.length) return null
  const removeRoutine = async (item: TeamOnlineItem) => {
    setPending(item.destination_item_id); setLine('')
    try {
      await removeTeamRoutine(target, routines!.home_project_id, item.destination_item_id, item.automation_kind)
      setLine('Removed from the Team.')
    } catch (e) { setLine(teamFailure(e).message) } finally { setPending('') }
  }
  return <section aria-label="Runs for the Team" className={teamCard}>
    <h3 className="font-semibold">Runs for the Team</h3>
    <ul className="divide-y divide-border">
      {items.map(item => <li key={item.destination_item_id} className={`flex flex-wrap items-center gap-2 py-2 ${pending === item.destination_item_id ? 'opacity-60' : ''}`}>
        <CalendarClock className="h-4 w-4 shrink-0 text-muted-foreground" aria-hidden="true" />
        <span className="min-w-0 flex-1">
          <span className="block truncate">{item.name}</span>
          <span className="block text-xs text-muted-foreground">{[item.when_runs, item.added_by_name && `Added by ${item.added_by_name}`].filter(Boolean).join(' · ')}</span>
        </span>
        <button type="button" aria-label={`Remove ${item.name} from the Team`} disabled={Boolean(pending)} onClick={() => { void removeRoutine(item) }} className="rounded-full border border-border px-2.5 py-0.5 text-xs hover:bg-muted disabled:opacity-50">Remove</button>
      </li>)}
    </ul>
    {line && <p role="status" className="text-xs text-muted-foreground">{line}</p>}
  </section>
}

/** Add to Team beside a Vault row of this Crew's Team; a row already shared shows a quiet "On the Team". Not shown
 * without a Team, or to someone who is not In team. */
export function useAddToTeam(projectId?: string) {
  const { view, target } = useTeamTab(projectId)
  const inTeam = Boolean(view?.team.on && view.people.some(person => person.you && person.note !== 'invited'))
  const way = useTeamWay(target, inTeam)
  const [pending, setPending] = useState('')
  const [line, setLine] = useState('')
  if (!inTeam || !target || !way?.view) return undefined
  const add = async (kind: 'app' | 'secret', id: string) => {
    setPending(id); setLine('')
    try { setLine((await changeTeamWay(target, { op: 'add', kind, id })).notice || '') }
    catch (e) { setLine(teamFailure(e).message) } finally { setPending('') }
  }
  return { line, onTeam: (id: string) => way.view!.on_team.includes(id), pending, add }
}

type AddToTeam = NonNullable<ReturnType<typeof useAddToTeam>>
/** One Vault row's Team control: Add to Team, then a quiet "On the Team". Only the person's own apps and keys. */
export function AddToTeamControl({ team, kind, id, provider, signIn }: { team: AddToTeam; kind: 'app' | 'secret'; id: string; provider?: string; signIn?: boolean }) {
  const shared = team.onTeam(signIn ? `need:${(provider || '').toLowerCase()}` : id)
  return <span className="flex shrink-0 items-center gap-2">
    {signIn && <span className="rounded-full border border-border px-2 py-0.5 text-xs text-muted-foreground">Each person signs in</span>}
    {shared
      ? <span className="text-xs text-muted-foreground">On the Team</span>
      : <button type="button" disabled={Boolean(team.pending)} onClick={() => { void team.add(kind, id) }} className="rounded-full border border-border px-2.5 py-0.5 text-xs hover:bg-muted disabled:opacity-50">Add to Team</button>}
  </span>
}

/** Add to Team on a schedule or Watcher of this Crew's Team; a routine already on the Team shows a quiet "On the Team".
 * Not shown without a Team, or to someone who is not In team. The server decides what moves and what to tell the person. */
export function useAddRoutineToTeam(projectId?: string) {
  const { view, target } = useTeamTab(projectId)
  // The Team's own Crew only (the online home has a roster_source); the home is made by the first Add.
  const inTeam = Boolean(view?.team.on && !view.team.roster_source && view.people.some(person => person.you && person.note !== 'invited'))
  const routines = useTeamRoutines(target, inTeam)
  const [pending, setPending] = useState('')
  const [line, setLine] = useState('')
  if (!inTeam || !target) return undefined
  const add = async (id: string) => {
    setPending(id); setLine('')
    try { const result = await addAutomationToTeam(target, id); setLine([`${result.name || 'It'} now runs for the Team.`, ...(result.notes ?? [])].join(' ')) }
    catch (e) { setLine(teamFailure(e).message) } finally { setPending('') }
  }
  return { line, onTeam: (id: string) => (routines?.items ?? []).some(item => item.kind === 'automation' && item.source_project_id === target.project && item.item_id === id), pending, add }
}
export type RoutineTeam = NonNullable<ReturnType<typeof useAddRoutineToTeam>>
