import { useEffect, useState } from 'react'
import api from '../../../services/api'
import { folderConnection } from '../../../utils/remoteSessions'
import { botError, readBotState, saveBot, type BotChange, type BotFunction, type BotState } from './botsApi'

type DirectoryUser = { id: string; username?: string; email?: string }

// Ask AI (owner, 28 Nov): the agent's own chat sets the bot up and adds functions with the bots and function tools.
const SETUP_ASK = "Set this agent up as a bot others can use without joining this project. Suggest an icon, a short name and a one-line description of what it does, ask me who should use it (chosen people and Teams, or anyone at the company) and which of this project's functions other agents should be able to call, then save it with the bots tool (action=set)."
const FUNCTION_ASK = "Add a function to this project that other agents can call by name when this agent is a bot (from a project it was added to, or over MCP). Suggest two or three from what this agent does, ask me which one and what it needs as input, build and publish it with the existing function tools, then turn it on for this bot with the bots tool (action=set, functions)."

/** The one line the Bot row shows when closed: off, or who can use it and how many functions agents can call. */
export function botSummary(state: BotState | null): string {
  if (!state) return 'Share it for others to use'
  if (!state.on) return 'Off · share it for others to use'
  const who = state.everyone ? 'Anyone at the company' : state.people.length + state.teams.length === 0 ? 'Nobody chosen yet' : [...state.people.map(person => person.name), ...state.teams.map(id => state.team_choices.find(team => team.team_id === id)?.name ?? 'a Team')].join(', ')
  const count = state.functions.length
  return `${who} · ${count} function${count === 1 ? '' : 's'}`
}

const needs = (fn: BotFunction) => Object.entries(fn.input_schema?.properties ?? {}).map(([name, field]) => field.description ? `${name}: ${field.description}` : name).join(', ')

function Switch({ on, disabled, label, onChange }: { on: boolean; disabled?: boolean; label: string; onChange: (next: boolean) => void }) {
  return <button type="button" role="switch" aria-checked={on} aria-label={label} disabled={disabled} onClick={() => onChange(!on)}
    className={`relative h-5 w-9 shrink-0 rounded-full transition-colors disabled:opacity-50 ${on ? 'bg-primary' : 'bg-muted-foreground/30'}`}>
    <span className={`absolute top-0.5 h-4 w-4 rounded-full bg-background shadow transition-all ${on ? 'left-[18px]' : 'left-0.5'}`} />
  </button>
}

/**
 * Make one of your agents a bot (#4545): people add it to their projects and chat with it privately, without joining
 * this project; other agents call the functions you pick by name. The server checks that you may change it and saves it.
 */
export function BotCard({ workspace, agentKey, onAsk, onSummary }: { workspace: string; agentKey: string; onAsk?: (message: string) => void; onSummary?: (summary: string) => void }) {
  const [state, setState] = useState<BotState | null>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [directory, setDirectory] = useState<DirectoryUser[]>([])
  const [pick, setPick] = useState('')
  useEffect(() => {
    let cancelled = false
    void readBotState(workspace, agentKey).then(next => { if (!cancelled) setState(next) })
      .catch(reason => { if (!cancelled) setError(botError(reason, 'Could not load this agent\'s bot.')) })
    return () => { cancelled = true }
  }, [workspace, agentKey])
  useEffect(() => { if (state) onSummary?.(botSummary(state)) }, [state, onSummary])
  useEffect(() => {
    if (!state?.on || state.everyone) return
    let cancelled = false
    void api.get<{ users?: DirectoryUser[] }>('/api/users/directory', { connection: folderConnection(workspace) })
      .then(({ data }) => { if (!cancelled) setDirectory(data.users ?? []) }).catch(() => undefined)
    return () => { cancelled = true }
  }, [state?.on, state?.everyone, workspace])
  const change = async (next: BotChange) => {
    setBusy(true); setError('')
    try { setState(await saveBot(workspace, agentKey, next)) } catch (reason) { setError(botError(reason, 'Could not save the bot.')) } finally { setBusy(false) }
  }
  if (!state) return <p role={error ? 'alert' : 'status'} className="py-1 text-muted-foreground">{error || 'Loading…'}</p>
  const people = state.people.map(person => person.user_id)
  const shared = state.available_functions.filter(fn => state.functions.includes(fn.name))
  return <div className="space-y-2 py-1">
    <div className="flex items-center gap-2">
      <Switch on={state.on} disabled={busy} label="Make it a bot" onChange={on => void change({ on })} />
      <span className="min-w-0 flex-1 font-medium">{state.on ? 'It is a bot' : 'Make it a bot'}</span>
      {onAsk && <button type="button" title="Ask AI to set up this bot" aria-label="Ask AI to set up this bot" onClick={() => onAsk(SETUP_ASK)} className="rounded-md px-1.5 py-0.5 text-primary hover:bg-muted">✦</button>}
    </div>
    <p className="text-[11px] text-muted-foreground">Others add it to their projects and chat with it privately. It runs with this project's setup; they pay for their use.</p>
    {state.on && <>
      <div className="rounded-lg border border-border p-2">
        <p className="mb-1.5 text-[11px] text-muted-foreground">How it shows in Bots</p>
        <div className="flex items-start gap-2">
          <input aria-label="Icon" title="Icon" defaultValue={state.icon} maxLength={8} disabled={busy} onBlur={event => { if (event.target.value !== state.icon) void change({ icon: event.target.value }) }}
            className="h-9 w-9 shrink-0 rounded-lg border border-dashed border-border bg-primary/10 text-center text-base" />
          <div className="min-w-0 flex-1 space-y-1">
            <input aria-label="Bot name" defaultValue={state.name} disabled={busy} onBlur={event => { const name = event.target.value.trim(); if (name && name !== state.name) void change({ name }) }}
              className="w-full rounded-md border border-input bg-background px-2 py-1 font-semibold" />
            <label className="block text-[11px] text-muted-foreground">What it does
              <textarea defaultValue={state.purpose} rows={2} placeholder="One plain sentence" disabled={busy} onBlur={event => { if (event.target.value !== (state.purpose ?? '')) void change({ purpose: event.target.value }) }}
                className="mt-0.5 block w-full rounded-md border border-input bg-background px-2 py-1 text-xs text-foreground" /></label>
          </div>
        </div>
      </div>
      <label className="block font-medium">Who can use it
        <select value={state.everyone ? 'everyone' : 'people'} disabled={busy} onChange={event => void change({ everyone: event.target.value === 'everyone' })}
          className="mt-1 block w-full rounded-md border border-input bg-background px-1 py-1 font-normal">
          <option value="everyone">Anyone at the company</option>
          <option value="people">Chosen people and Teams</option>
        </select>
      </label>
      {!state.everyone && <div className="space-y-1">
        {state.people.map(person => <div key={person.user_id} className="flex items-center justify-between gap-2"><span className="truncate">{person.name}</span><button type="button" disabled={busy} onClick={() => void change({ people: people.filter(id => id !== person.user_id) })} className="text-muted-foreground hover:text-destructive">Remove</button></div>)}
        <div className="flex gap-1">
          <select aria-label="Add a person" value={pick} onChange={event => setPick(event.target.value)} className="min-w-0 flex-1 rounded-md border border-input bg-background px-1 py-1">
            <option value="">Add a person…</option>
            {directory.filter(user => !people.includes(user.id)).map(user => <option key={user.id} value={user.id}>{user.email || user.username || user.id}</option>)}
          </select>
          <button type="button" disabled={!pick || busy} onClick={() => { void change({ people: [...people, pick] }); setPick('') }} className="rounded-md border border-border px-2 disabled:opacity-50">Add</button>
        </div>
        {state.team_choices.length > 0 && <div className="pt-1">
          <span className="block text-[11px] text-muted-foreground">Teams you are in</span>
          {state.team_choices.map(team => <label key={team.team_id} className="flex items-center gap-2"><input type="checkbox" checked={state.teams.includes(team.team_id)} disabled={busy}
            onChange={event => void change({ teams: event.target.checked ? [...state.teams, team.team_id] : state.teams.filter(id => id !== team.team_id) })} />{team.name}</label>)}
        </div>}
      </div>}
      <section aria-label="Functions" className="space-y-1.5">
        <p><span className="font-medium">Functions</span> <span className="text-[11px] text-muted-foreground">· what other agents can call by name</span></p>
        <p className="text-[11px] text-muted-foreground">People just chat with the bot. Their agents, in a project the bot was added to or over MCP, can also call these.</p>
        {state.available_functions.map(fn => <div key={fn.name} className="rounded-lg border border-border px-2 py-1.5">
          <div className="flex items-center gap-2"><code className="min-w-0 flex-1 truncate font-semibold">{fn.name}</code>
            <Switch on={state.functions.includes(fn.name)} disabled={busy} label={`Let other agents call ${fn.name}`}
              onChange={on => void change({ functions: on ? [...state.functions, fn.name] : state.functions.filter(name => name !== fn.name) })} /></div>
          {fn.description && <p className="text-[11px]">{fn.description}</p>}
          {needs(fn) && <p className="text-[11px] text-muted-foreground">Needs: {needs(fn)}</p>}
        </div>)}
        <p className="rounded-md bg-muted px-2 py-1.5 text-[11px] text-muted-foreground">{shared.length > 0
          ? <>How people use it: in a project with this bot, they tell their agent <i>"use {state.name} to …"</i>, and the agent calls <code>{shared[0].name}</code>.</>
          : 'No functions yet. People can still chat with it; add one so other agents can call it by name.'}</p>
        {onAsk && <button type="button" onClick={() => onAsk(FUNCTION_ASK)} className="rounded-md border border-border px-2 py-1 hover:bg-muted">＋ Add a function</button>}
      </section>
    </>}
    {error && <p role="alert" className="text-destructive">{error}</p>}
  </div>
}
