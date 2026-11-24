import { useEffect, useState } from 'react'
import { KeyRound, ListChecks, Plug, ScrollText, Sparkles } from 'lucide-react'
import { shortAgo } from '../../components/chat/AgentIcon'
import { sendToOpenChat } from '../../utils/agentPurposeDraft'
import { useChatStore } from '../../stores/useChatStore'
import { changeTeamWay, teamFailure, teamKit, teamRequest, type TeamKitItem, type TeamKitView, type TeamTarget } from './teamApi'
import { TeamSetups, type TeamSetupList } from './TeamSetups'
import { TeamCommands } from './TeamCommands'
import { TeamSkills } from './TeamSkills'
import { TeamSchedules } from './TeamSchedules'
import { TeamThemes } from './TeamThemes'

/** The Team kit (#3141, owner 18 Nov): everything a teammate's agent needs to work the Team's way, in one list. The
 * server builds it (setup_kit); this only shows it and sends the person's own requests. Setup steps keep their own
 * list and detail (TeamSetups). */

type Section = TeamKitItem['section']
type Filter = 'all' | Section | 'mine' | 'themes' | 'schedules' | 'commands'
const sections: { id: Section; title: string; line: string; icon: typeof Plug }[] = [
  { id: 'instructions', title: 'Team instructions', line: 'every agent follows these automatically', icon: ScrollText },
  { id: 'skills', title: 'Skills', line: 'what agents in this Team know how to do', icon: Sparkles },
  { id: 'tools', title: 'Tools & keys', line: 'apps, MCP tools, sign-ins and key names', icon: Plug },
  { id: 'steps', title: 'Setup steps', line: 'your agent does them and ticks each step', icon: ListChecks },
]
const chips: { id: Filter; label: string }[] = [{ id: 'all', label: 'All' }, { id: 'instructions', label: 'Instructions' }, { id: 'skills', label: 'Skills' }, { id: 'tools', label: 'Tools & keys' }, { id: 'steps', label: 'Setup steps' }, { id: 'themes', label: 'Themes' }, { id: 'schedules', label: 'Schedules' }, { id: 'commands', label: 'Commands' }, { id: 'mine', label: 'Missing for me' }]
const mineLabel: Record<string, [string, string]> = {
  auto: ['Everyone has it', 'text-muted-foreground'],
  have: ['✓ You have it', 'text-emerald-500'],
  missing: ['You need it', 'text-amber-500'],
  unknown: ['Not checked yet', 'text-muted-foreground'],
}
const kindLabel: Record<string, string> = { mcp: 'MCP tool', app: 'Team app', key: 'Key', sign_in: 'Sign-in', skill: 'Skill' }
const needsMe = (item: TeamKitItem) => item.mine === 'missing' || item.mine === 'unknown'

/** The server's kit, read again whenever the Team or its setups change. */
function useTeamKit(target: TeamTarget, refreshKey: number) {
  const [state, setState] = useState<{ view?: TeamKitView; error?: string }>({})
  useEffect(() => {
    let active = true
    const load = () => { void teamKit(target).then(view => { if (active && view) setState({ view }) }).catch(e => { if (active) setState(old => ({ ...old, error: teamFailure(e).message })) }) }
    load()
    window.addEventListener('agentworks:team-updated', load)
    return () => { active = false; window.removeEventListener('agentworks:team-updated', load) }
  }, [target.project, target.connection, refreshKey]) // eslint-disable-line react-hooks/exhaustive-deps
  return state
}

/** Small faces of the people an item is for; faded until they have it. */
function Faces({ item, names }: { item: TeamKitItem; names: Record<string, string> }) {
  if (item.everyone) return <span className="shrink-0 text-xs text-muted-foreground">Everyone</span>
  const people = item.people ?? []
  if (!people.length) return null
  const have = people.filter(p => p.state === 'have').length
  return <span className="flex shrink-0 items-center gap-1.5 text-xs text-muted-foreground" aria-label={`${have} of ${people.length} have it`}>
    <span className="flex" aria-hidden="true">{people.slice(0, 6).map(p => <span key={p.person_id} title={names[p.person_id]} className={`-ml-1.5 grid h-5 w-5 place-items-center rounded-full border-2 border-background bg-muted text-[10px] text-foreground first:ml-0 ${p.state === 'have' ? '' : 'opacity-35'}`}>{(names[p.person_id] || '?').charAt(0).toUpperCase()}</span>)}</span>
    {have} of {people.length}
  </span>
}

function InstructionsText({ target }: { target: TeamTarget }) {
  const [text, setText] = useState<string | null>()
  useEffect(() => {
    let active = true
    void teamRequest<string>(target, `/file?path=${encodeURIComponent('TEAM_INSTRUCTIONS.md')}`, 'GET', undefined, true).then(value => { if (active) setText(value) }).catch(() => { if (active) setText(null) })
    return () => { active = false }
  }, [target.project, target.connection]) // eslint-disable-line react-hooks/exhaustive-deps
  if (text === undefined) return <p className="text-muted-foreground">Loading…</p>
  return text ? <pre className="max-h-80 overflow-auto whitespace-pre-wrap rounded-lg bg-muted p-3 text-[11px]">{text}</pre> : <p className="text-muted-foreground">None yet. Anyone in the Team can add them by asking their agent.</p>
}

function KitRow({ item, names, target, projectId, onChanged }: { item: TeamKitItem; names: Record<string, string>; target: TeamTarget; projectId: string; onChanged: (line: string) => void }) {
  const [open, setOpen] = useState(false)
  const [busy, setBusy] = useState(false)
  const Icon = item.kind === 'key' ? KeyRound : sections.find(s => s.id === item.section)?.icon ?? Plug
  const meta = [item.by && `${item.by_verb || 'Shared by'} ${item.by}`, shortAgo(item.at)].filter(Boolean).join(' · ')
  const [mine, tone] = mineLabel[item.mine || ''] ?? ['Not for you', 'text-muted-foreground']
  const remove = async () => {
    const [kind, id] = item.id.split(/:(.*)/s)
    setBusy(true)
    try { onChanged((await changeTeamWay(target, { op: 'remove', kind: kind === 'key' ? 'secret' : 'app', id })).notice || 'Removed from the Team.') }
    catch (e) { onChanged(teamFailure(e).message) } finally { setBusy(false) }
  }
  return <li className={`rounded-xl border border-border bg-muted/20 ${busy ? 'opacity-60' : ''}`}>
    <button type="button" aria-expanded={open} onClick={() => setOpen(!open)} className="flex w-full items-center gap-3 px-3 py-2.5 text-left">
      <Icon className="h-4 w-4 shrink-0 text-muted-foreground" aria-hidden="true" />
      <span className="min-w-0 flex-1">
        <span className="flex items-center gap-1.5"><span className="truncate font-medium">{item.name}</span>{kindLabel[item.kind] && item.section !== 'skills' && <span className="shrink-0 rounded border border-border px-1.5 text-[10px] text-muted-foreground">{kindLabel[item.kind]}</span>}</span>
        {item.line && <span className="block truncate text-xs text-muted-foreground">{item.line}</span>}
        {meta && <span className="block text-[11px] text-muted-foreground">{meta}</span>}
      </span>
      <Faces item={item} names={names} />
      <span className={`w-24 shrink-0 text-right text-[11px] ${tone}`}>{mine}</span>
    </button>
    {open && <div className="space-y-2 border-t border-border px-3 py-2.5 text-xs">
      {item.kind === 'instructions' && <InstructionsText target={target} />}
      {!!item.people?.length && <ul className="grid grid-cols-2 gap-x-3 gap-y-0.5">{item.people.map(p => <li key={p.person_id} className={p.state === 'have' ? 'text-emerald-500' : p.state === 'missing' ? 'text-amber-500' : 'text-muted-foreground'}>
        {p.state === 'have' ? '✓' : p.state === 'missing' ? '–' : '?'} {names[p.person_id] || 'Someone'}{p.total ? ` · ${p.done ?? 0} of ${p.total}` : p.state === 'unknown' ? ' · not checked yet' : ''}</li>)}</ul>}
      <div className="flex flex-wrap gap-2">
        {item.kind === 'sign_in' && item.mine === 'missing' && <button type="button" className="rounded-lg border border-border px-2.5 py-1 hover:bg-muted" onClick={() => { if (!sendToOpenChat(projectId, `Help me connect my ${item.name} account so I can use it with this Team.`)) useChatStore.getState().addToast('Open one of your own agent chats in this project, then try again.', 'info') }}>Connect {item.name}</button>}
        {(item.kind === 'app' || item.kind === 'key') && <button type="button" disabled={busy} className="rounded-lg border border-border px-2.5 py-1 hover:bg-muted disabled:opacity-50" aria-label={`Remove ${item.name} from the Team`} onClick={() => void remove()}>Remove from the Team</button>}
      </div>
    </div>}
  </li>
}

/** One row per person, one column per item that is not automatic: names and states only, never a value. */
function ByPerson({ view }: { view: TeamKitView }) {
  const items = view.items.filter(item => !item.everyone && item.people?.length)
  if (!items.length) return <p className="text-xs text-muted-foreground">Everything in the kit reaches every agent automatically.</p>
  const cell = (item: TeamKitItem, person: string) => item.people?.find(p => p.person_id === person)?.state
  return <div className="overflow-x-auto"><table className="w-full border-collapse text-left text-[12px]">
    <thead><tr><th className="py-1 pr-2 font-medium text-muted-foreground">Person</th>{items.map(item => <th key={item.id} className="px-2 py-1 font-medium">{item.name}</th>)}<th className="px-2 py-1 font-medium">Has</th></tr></thead>
    <tbody>{view.people.map(person => {
      const theirs = items.filter(item => cell(item, person.person_id))
      return <tr key={person.person_id} className="border-t border-border"><td className="py-1.5 pr-2">{person.you ? 'You' : person.name}</td>
        {items.map(item => { const state = cell(item, person.person_id); return <td key={item.id} className={`px-2 py-1.5 ${state === 'have' ? 'text-emerald-500' : state === 'missing' ? 'text-amber-500' : 'text-muted-foreground'}`}>{state === 'have' ? '✓' : state === 'missing' ? '–' : state ? '?' : ''}</td> })}
        <td className="px-2 py-1.5 text-muted-foreground">{theirs.filter(item => cell(item, person.person_id) === 'have').length} of {theirs.length}</td></tr>
    })}</tbody></table>
    <p className="mt-1 text-[11px] text-muted-foreground">✓ has it · – missing · ? their agent has not checked yet. Names and states only; never a key's value.</p>
  </div>
}

export function TeamKit({ target, projectId, list, refreshKey, folder }: { target: TeamTarget; projectId: string; list?: TeamSetupList; refreshKey: number; folder?: string }) {
  const { view, error } = useTeamKit(target, refreshKey)
  const [filter, setFilter] = useState<Filter>('all')
  const [byPerson, setByPerson] = useState(false)
  const [stepOpen, setStepOpen] = useState(false)
  const [line, setLine] = useState('')
  const names = Object.fromEntries((view?.people ?? []).map(p => [p.person_id, p.you ? 'You' : p.name]))
  const shown = (item: TeamKitItem) => filter === 'all' || (filter === 'mine' ? needsMe(item) : item.section === filter)
  const steps = <TeamSetups key={`${target.connection || ''}:${target.project}`} target={target} projectId={projectId} list={list} refreshKey={refreshKey} onOpen={setStepOpen} />
  if (stepOpen) return steps
  const apply = () => {
    if (!view?.apply_ask) return
    if (sendToOpenChat(projectId, view.apply_ask)) setLine('Asked your agent to set up what you are missing. It ticks each item once it has checked it.')
    else useChatStore.getState().addToast('Open one of your own agent chats in this project, then try again. Nothing was sent.', 'info')
  }
  return <div className="space-y-3 text-sm">
    <section aria-label="Team kit" className="space-y-2 rounded-xl border border-border px-4 py-3">
      <div className="flex flex-wrap items-start gap-3">
        <div className="min-w-0 flex-1"><h3 className="font-semibold">Team kit</h3><p className="text-xs text-muted-foreground">What every agent in this Team works with. Apply it and your agent sets up each piece and checks it. Key values stay in the Vault.</p></div>
        {view && view.total > 0 && <div className="w-52 shrink-0 space-y-1.5 rounded-lg border border-border p-2.5 text-xs">
          <p><span className="font-medium">You:</span> {view.have} of {view.total} set up</p>
          <div role="progressbar" aria-label="Your kit" aria-valuemin={0} aria-valuemax={view.total} aria-valuenow={view.have} className="h-1.5 overflow-hidden rounded bg-muted"><div className="h-full rounded bg-emerald-500" style={{ width: `${Math.round(view.have / view.total * 100)}%` }} /></div>
          {view.missing > 0 ? <button type="button" onClick={apply} className="w-full rounded-lg bg-primary px-2.5 py-1.5 font-semibold text-primary-foreground hover:opacity-90">Apply what's missing</button> : <p className="text-emerald-500">All set ✓</p>}
        </div>}
      </div>
      {line && <p role="status" className="text-xs text-muted-foreground">{line}</p>}
      {error && !view && <p role="alert" className="text-xs text-destructive">{error}</p>}
    </section>
    {view && <div className="flex flex-wrap items-center gap-2">
      <div className="flex flex-1 flex-wrap gap-1.5">{chips.map(chip => <button key={chip.id} type="button" aria-pressed={filter === chip.id} onClick={() => { setFilter(chip.id); setByPerson(false) }} className={`rounded-full border px-2.5 py-0.5 text-xs ${filter === chip.id && !byPerson ? 'border-primary text-foreground' : 'border-border text-muted-foreground hover:bg-muted'}`}>{chip.label}</button>)}</div>
      <button type="button" aria-pressed={byPerson} onClick={() => setByPerson(!byPerson)} className="rounded-lg border border-border px-2.5 py-0.5 text-xs text-muted-foreground hover:bg-muted">{byPerson ? 'By item' : 'By person'}</button>
    </div>}
    {view && byPerson && <ByPerson view={view} />}
    {!byPerson && sections.map(section => {
      if (filter !== 'all' && filter !== 'mine' && filter !== section.id) return null
      const rows = (view?.items ?? []).filter(item => item.section === section.id && item.kind !== 'step' && shown(item))
      if (section.id === 'steps') return filter === 'mine' ? null : <section key={section.id} aria-label={section.title} className="space-y-1.5">
        <h4 className="text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">{section.title} <span className="font-normal normal-case tracking-normal">· {section.line}</span></h4>
        {steps}
      </section>
      if (!rows.length) return null
      return <section key={section.id} aria-label={section.title} className="space-y-1.5">
        <h4 className="text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">{section.title} <span className="font-normal normal-case tracking-normal">· {section.line}</span></h4>
        <ul className="space-y-1.5">{rows.map(item => <KitRow key={item.id} item={item} names={names} target={target} projectId={projectId} onChanged={setLine} />)}</ul>
      </section>
    })}
    {view && filter === 'mine' && !byPerson && !view.items.some(item => item.kind !== 'step' && needsMe(item)) && <p className="text-xs text-muted-foreground">Nothing missing for you outside the setup steps.</p>}
    {!byPerson && (filter === 'all' || filter === 'themes') && <TeamThemes target={target} folder={folder} />}
    {!byPerson && (filter === 'all' || filter === 'schedules') && <TeamSchedules target={target} folder={folder} />}
    {!byPerson && (filter === 'all' || filter === 'skills') && <TeamSkills target={target} projectId={projectId} folder={folder} />}
    {!byPerson && (filter === 'all' || filter === 'commands') && <TeamCommands target={target} folder={folder} />}
  </div>
}
