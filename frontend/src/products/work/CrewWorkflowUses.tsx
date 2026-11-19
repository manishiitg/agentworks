import { useEffect, useState } from 'react'
import { crewWorkflowUsesApi, type CrewWorkflowTeamNeed, type CrewWorkflowTeamNeeds, type CrewWorkflowUses, type CrewWorkflowUsesItem } from '../../api/crewWorkflowUses'

/** Reads what a workflow uses. Undefined while loading or when it could not be read (the page then shows no Uses rows). */
export function useCrewWorkflowUses(workspacePath: string): CrewWorkflowUses | undefined {
  const [state, setState] = useState<{ workspacePath: string; uses?: CrewWorkflowUses }>()
  useEffect(() => {
    let current = true
    crewWorkflowUsesApi.get(workspacePath)
      .then(uses => { if (current) setState({ workspacePath, uses }) })
      .catch(() => { if (current) setState({ workspacePath }) })
    return () => { current = false }
  }, [workspacePath])
  return state?.workspacePath === workspacePath ? state.uses : undefined
}

function Chip({ item, dot }: { item: CrewWorkflowUsesItem; dot?: boolean }) {
  return <span className="inline-flex items-center gap-1.5 whitespace-nowrap rounded-full border border-border px-2.5 py-0.5 text-xs text-foreground">
    {dot && <span aria-hidden="true" className={`h-2 w-2 rounded-full ${item.ready ? 'bg-emerald-500' : 'bg-red-500'}`} />}
    {item.value !== undefined ? <><span className="text-muted-foreground">{item.name}</span> {item.value}</> : item.name}
    {dot && !item.ready && <span className="text-muted-foreground">· missing</span>}
  </span>
}

/**
 * What the workflow uses: its inputs, the keys its script needs (names only), the apps and skills a run gets. The server
 * says whether each key and app is there; a missing key gets one line and a way to add it through the Crew chat.
 */
export function CrewWorkflowUsesBlock({ uses, workspacePath, onAsk }: { uses: CrewWorkflowUses; workspacePath?: string; onAsk: (task: string) => void | Promise<void> }) {
  const rows: Array<[string, CrewWorkflowUsesItem[], boolean]> = [
    ['Inputs', [...uses.inputs, ...uses.presets.map(name => ({ name: 'Preset', value: name, ready: true }))], false],
    ['Keys', uses.keys, true],
    ['Apps', uses.apps, true],
    ['Skills', uses.skills.map(name => ({ name, ready: true })), false],
  ].filter(([, items]) => (items as CrewWorkflowUsesItem[]).length > 0) as Array<[string, CrewWorkflowUsesItem[], boolean]>
  const missing = uses.keys.filter(key => !key.ready)
  if (!rows.length) return null
  return <div data-testid="crew-workflow-uses">
    <div className="grid grid-cols-[4.5rem_1fr] items-center gap-x-3 gap-y-2 px-4 py-3">
      {rows.map(([label, items, dot]) => <div key={label} className="contents">
        <span className="text-xs text-muted-foreground">{label}</span>
        <span className="flex flex-wrap gap-1.5">{items.map((item, index) => <Chip key={`${item.name}:${index}`} item={item} dot={dot} />)}</span>
      </div>)}
    </div>
    {missing.map(key => <div key={key.name} role="status" className="mx-4 mb-3 flex items-center gap-3 rounded-lg border border-red-500/40 bg-red-500/10 px-3 py-2 text-xs">
      <span aria-hidden="true" className="h-2 w-2 shrink-0 rounded-full bg-red-500" />
      <span className="min-w-0 flex-1">This workflow needs <b>{key.name}</b>, which isn't in your Vault yet.</span>
      <button type="button" onClick={() => void onAsk(`Help me add the key ${key.name} to my Vault for this workflow.`)}
        className="shrink-0 rounded-full border border-border px-3 py-0.5 text-xs hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">Add key</button>
    </div>)}
    {uses.team && workspacePath && <TeamNeeds team={uses.team} workspacePath={workspacePath} />}
  </div>
}

const needWhat: Record<CrewWorkflowTeamNeed['state'], string> = {
  on_team: 'already on the Team',
  can_copy: '',
  not_in_vault: 'not in your Vault either. Add it to your Vault first, then copy it to the Team.',
  this_mac_only: "only in this Mac's Vault, so it can't be copied from here. Add it to your Vault on the server, then copy it to the Team.",
  needs_sign_in: "never copied: the Team needs its own sign-in (a Team or shared account). Signing in for the Team isn't available yet, so online runs can't use this app.",
}

/**
 * "What it needs online": online, a workflow runs as the Team with only the Team's Vault. The server says what is on
 * the Team; Copy sends one key or app there (the value goes Vault to Vault, never through this page or an agent).
 */
function TeamNeeds({ team: initial, workspacePath }: { team: CrewWorkflowTeamNeeds; workspacePath: string }) {
  const [team, setTeam] = useState(initial)
  const [pending, setPending] = useState('')
  const [line, setLine] = useState('')
  useEffect(() => { setTeam(initial) }, [initial])
  const copy = async (need: CrewWorkflowTeamNeed) => {
    if (!need.id || (need.kind !== 'key' && need.kind !== 'app')) return
    setPending(need.id); setLine('')
    try {
      const { notice } = await crewWorkflowUsesApi.copyToTeam(workspacePath, need.kind, need.id)
      setLine(notice || '')
      const fresh = await crewWorkflowUsesApi.get(workspacePath)
      if (fresh.team) setTeam(fresh.team)
    } catch (cause) {
      const data = (cause as { response?: { data?: unknown } })?.response?.data
      setLine(typeof data === 'string' && data.trim() ? data.trim() : 'It could not be copied to the Team. Try again.')
    } finally { setPending('') }
  }
  return <div data-testid="crew-workflow-team-needs" className="mx-4 mb-3 rounded-lg border border-border">
    <div className="flex items-baseline justify-between gap-2 border-b border-border px-3 py-2 text-xs font-medium">
      <span>What it needs online</span>
      <span className="font-normal text-muted-foreground">{team.need_you > 0 ? `${team.need_you} ${team.need_you === 1 ? 'thing needs' : 'things need'} you` : "uses only the Team's Vault"}</span>
    </div>
    <ul className="divide-y divide-border text-xs">
      {team.items.map((need, index) => {
        const ok = need.state === 'on_team'
        const tag = need.kind === 'key' ? 'key' : need.kind === 'app' ? (need.state === 'needs_sign_in' ? 'app · sign-in' : 'app') : ''
        const what = need.kind === 'copied' ? 'copied with the workflow' : ok && need.added_by ? `already on the Team · added by ${need.added_by}` : needWhat[need.state]
        return <li key={`${need.kind}:${need.name}:${index}`} className="flex items-center gap-2 px-3 py-2">
          <span aria-hidden="true" className={`h-2 w-2 shrink-0 rounded-full ${ok ? 'bg-emerald-500' : 'bg-amber-500'}`} />
          <span className="font-medium">{need.name}</span>
          {tag && <span className="rounded-full border border-border px-1.5 text-[11px] text-muted-foreground">{tag}</span>}
          <span className="ml-auto min-w-0 text-right text-muted-foreground">
            {need.state === 'can_copy'
              ? <button type="button" disabled={Boolean(pending)} onClick={() => void copy(need)}
                  className="rounded-full border border-border px-3 py-0.5 text-xs text-foreground hover:bg-muted disabled:opacity-50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
                  {need.kind === 'key' ? 'Copy my key to the Team' : 'Copy to the Team'}</button>
              : what}
          </span>
        </li>
      })}
    </ul>
    <p className="px-3 py-2 text-[11px] text-muted-foreground">Key values go server to server inside Vault; no agent or chat sees them. Sign-ins are never copied: use a Team or shared account, not a personal one.</p>
    {line && <p role="status" className="px-3 pb-2 text-xs text-muted-foreground">{line}</p>}
  </div>
}
