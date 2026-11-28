import { useEffect, useState } from 'react'
import api from '../../../services/api'
import { folderConnection } from '../../../utils/remoteSessions'
import { botError, readBotState, saveBot, type BotChange, type BotState } from './botsApi'

type DirectoryUser = { id: string; username?: string; email?: string }

// Ask AI (owner, 28 Nov): the agent's own chat sets the bot up and adds functions with the bots and function tools.
const SETUP_ASK = "Set this agent up as a bot others can use without joining this project. Suggest an icon, a short name and a one-line purpose from what it does, ask me who should use it (chosen people and Teams, or anyone at the company) and which of this project's functions it should share, then save it with the bots tool (action=set)."
const FUNCTION_ASK = "Add a function to this project that people using this agent as a bot can run. Ask me what it should do and what it needs as input, build and publish it with the existing function tools, then turn it on for this bot with the bots tool (action=set, functions)."
const askButton = 'text-left text-[11px] text-primary hover:underline disabled:opacity-50'

/**
 * Make one of your agents a bot (#4545): people use it without joining this project, each in their own private chat,
 * with the functions you pick as its buttons (Ask is always on). The server checks that the agent is yours and saves it.
 */
export function BotCard({ workspace, agentKey, onAsk }: { workspace: string; agentKey: string; onAsk?: (message: string) => void }) {
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
  return <div className="space-y-2 py-1">
    <label className="flex items-start gap-2">
      <input type="checkbox" checked={state.on} disabled={busy} onChange={event => void change({ on: event.target.checked })} className="mt-0.5" />
      <span><span className="block font-medium">Make it a bot</span><span className="block text-[11px] text-muted-foreground">It joins your company's Bots directory. People you pick add it to their projects and use it there, or from their own AI apps, each in their own private chat. It runs with this project's setup and its cost is theirs; they don't join this project.</span></span>
    </label>
    {onAsk && <button type="button" className={askButton} onClick={() => onAsk(SETUP_ASK)}>✦ Ask AI to set up this bot</button>}
    {state.on && <>
      <div className="flex items-center gap-2">
        <input aria-label="Icon" defaultValue={state.icon} maxLength={8} disabled={busy} onBlur={event => { if (event.target.value !== state.icon) void change({ icon: event.target.value }) }} className="w-12 rounded-md border border-input bg-background px-1 py-1 text-center text-base" />
        <input aria-label="Bot name" defaultValue={state.name} disabled={busy} onBlur={event => { const name = event.target.value.trim(); if (name && name !== state.name) void change({ name }) }} className="min-w-0 flex-1 rounded-md border border-input bg-background px-2 py-1" />
      </div>
      <textarea aria-label="Purpose" defaultValue={state.purpose} rows={2} placeholder="One plain sentence on what it does" disabled={busy} onBlur={event => { if (event.target.value !== (state.purpose ?? '')) void change({ purpose: event.target.value }) }} className="w-full rounded-md border border-input bg-background px-2 py-1" />
      <fieldset className="space-y-1">
        <legend className="font-medium">Who can use it</legend>
        <label className="flex items-center gap-2"><input type="radio" name={`bot-audience-${agentKey}`} checked={!state.everyone} disabled={busy} onChange={() => void change({ everyone: false })} />Chosen people and Teams</label>
        <label className="flex items-center gap-2"><input type="radio" name={`bot-audience-${agentKey}`} checked={state.everyone} disabled={busy} onChange={() => void change({ everyone: true })} />Anyone at the company</label>
      </fieldset>
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
      <fieldset className="space-y-1">
        <legend className="font-medium">Functions</legend>
        <p className="text-[11px] text-muted-foreground">What people can run with it: buttons in their chat, and by name over MCP.</p>
        <label className="flex items-center gap-2 text-muted-foreground"><input type="checkbox" checked disabled />Ask (its chat, always on)</label>
        {state.available_functions.map(fn => <label key={fn.name} title={fn.description} className="flex items-center gap-2"><input type="checkbox" checked={state.functions.includes(fn.name)} disabled={busy}
          onChange={event => void change({ functions: event.target.checked ? [...state.functions, fn.name] : state.functions.filter(name => name !== fn.name) })} />{fn.name}</label>)}
        {state.available_functions.length === 0 && <p className="text-[11px] text-muted-foreground">This project has no functions yet. Add one, and it can be a button here.</p>}
        {onAsk && <button type="button" className={askButton} onClick={() => onAsk(FUNCTION_ASK)}>✦ Ask AI to add a function</button>}
      </fieldset>
      <p className="text-[11px] text-muted-foreground">It shows under Bots in the left menu for the people you chose.</p>
    </>}
    {error && <p role="alert" className="text-destructive">{error}</p>}
  </div>
}
