import { useEffect, useState } from 'react'
import { changeTeamWay, teamFailure, teamWay, useTeamTab, type TeamTarget, type TeamWayView } from './teamApi'

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
