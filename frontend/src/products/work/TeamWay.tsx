import { useEffect, useState } from 'react'
import { KeyRound, Plug, ScrollText, Sparkles } from 'lucide-react'
import { Button } from '../../components/ui/Button'
import { sendToMain } from '../../utils/agentPurposeDraft'
import { changeTeamWay, teamFailure, teamWay, useTeamTab, type TeamTarget, type TeamWayItem, type TeamWayView } from './teamApi'

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

/** The Team's own tools: shown only to a person who is In team, in a Crew that has a Team. */
export function TeamWayCard({ target, projectId }: { target: TeamTarget; projectId: string }) {
  const way = useTeamWay(target)
  const [pending, setPending] = useState('')
  const [line, setLine] = useState('')
  if (!way?.view) return null
  const remove = async (item: TeamWayItem) => {
    setPending(`${item.kind}:${item.id}`); setLine('')
    try { setLine((await changeTeamWay(target, { op: 'remove', kind: item.kind, id: item.id || item.name })).notice || '') }
    catch (e) { setLine(teamFailure(e).message) } finally { setPending('') }
  }
  return <section aria-label="How we work" className={teamCard}>
    <h3 className="font-semibold">How we work</h3>
    {way.view.items.length === 0 && !way.view.rules && way.view.skills.length === 0 && <p className="text-xs text-muted-foreground">Nothing yet. Add an app or a key from your Vault with Add to Team.</p>}
    <ul className="divide-y divide-border">
      {way.view.items.map(item => {
        const signIn = item.kind === 'needs_sign_in'
        const Icon = item.kind === 'secret' ? KeyRound : Plug
        return <li key={`${item.kind}:${item.id}`} className={`flex flex-wrap items-center gap-2 py-2 ${pending === `${item.kind}:${item.id}` ? 'opacity-60' : ''}`}>
          <Icon className="h-4 w-4 shrink-0 text-muted-foreground" aria-hidden="true" />
          <span className="min-w-0 flex-1">
            <span className="block truncate">{item.name}</span>
            {item.added_by && <span className="block text-xs text-muted-foreground">Added by {item.added_by}</span>}
          </span>
          {signIn && <span className="rounded-full border border-border px-2 py-0.5 text-xs text-muted-foreground">Each person signs in</span>}
          {signIn && (item.connected
            ? <span className="text-xs text-muted-foreground">Connected</span>
            : <Button size="sm" variant="outline" onClick={() => { sendToMain(projectId, `Help me connect my ${item.name} account so I can use it with this Team.`) }}>Connect {item.name}</Button>)}
          <button type="button" aria-label={`Remove ${item.name} from the Team`} disabled={Boolean(pending)} onClick={() => { void remove(item) }} className="rounded-full border border-border px-2.5 py-0.5 text-xs hover:bg-muted disabled:opacity-50">Remove</button>
        </li>
      })}
    </ul>
    {(way.view.rules || way.view.skills.length > 0) && <>
      <ul className="divide-y divide-border border-t border-border">
        {way.view.rules && <li className="flex items-center gap-2 py-2"><ScrollText className="h-4 w-4 shrink-0 text-muted-foreground" aria-hidden="true" /><span className="min-w-0 flex-1 truncate">Team rules</span></li>}
        {way.view.skills.map(name => <li key={name} className="flex items-center gap-2 py-2"><Sparkles className="h-4 w-4 shrink-0 text-muted-foreground" aria-hidden="true" /><span className="min-w-0 flex-1 truncate">{name}</span></li>)}
      </ul>
      <p className="text-xs text-muted-foreground">Rules and skills are changed in this Crew's settings or by asking in chat; everyone's agents use them.</p>
    </>}
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
