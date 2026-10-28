import { useState, type ReactNode } from 'react'
import { Check, Plus } from 'lucide-react'
import SlackAdminPanel from '../admin/SlackAdminPanel'
import { Button } from '../ui/Button'
import { Input } from '../ui/Input'
import { SecretField } from '../ui/SecretField'
import { ToggleRow } from '../ui/ToggleRow'
import { SlackChecksView, SlackManifestSetup, SlackPermissionsChecklist, SlackTokenHint } from '../workflow/bots/SlackAppSetupSteps'
import { slackErrorText } from '../workflow/bots/SlackSlugsSection'
import type { SlackTestResponse } from '../../services/api-types'
import { slackBotName, slackBotsApi, type SlackBotState, type VaultSlackBot } from './ProductSlackPanel'

type Props = {
  data: SlackBotState
  workspacePath: string
  busy: boolean
  onSelect: (id: string) => Promise<void>
  onUpdated: () => Promise<void>
  children?: ReactNode
}

/** One list for saved bots. Every mutation uses the existing server checks. */
export function VaultSlackBots({ data, workspacePath, busy: parentBusy, onSelect, onUpdated, children }: Props) {
  const [view, setView] = useState<'existing' | 'new'>('existing')
  const [editing, setEditing] = useState<VaultSlackBot | 'new' | null>(null)
  const [scopes, setScopes] = useState<SlackBotState['scopes']>([])
  const [name, setName] = useState('')
  const [scope, setScope] = useState(workspacePath)
  const [bot, setBot] = useState('')
  const [app, setApp] = useState('')
  const [enabled, setEnabled] = useState(true)
  const [working, setWorking] = useState(false)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [checks, setChecks] = useState<SlackTestResponse | null>(null)
  const busy = parentBusy || working
  const defaultBot = data.bots.find(item => item.default)

  const edit = (item: VaultSlackBot | 'new') => {
    setEditing(item)
    setName(item === 'new' ? '' : item.name)
    setEnabled(item === 'new' ? true : item.enabled)
    setBot(''); setApp(''); setChecks(null); setError(''); setNotice('')
  }
  const existing = () => {
    setView('existing'); setEditing(null); setBot(''); setApp(''); setError('')
  }
  const add = async () => {
    setView('new'); edit('new'); setWorking(true)
    try {
      const next = await slackBotsApi.list()
      setScopes(next.scopes)
      setScope(next.scopes.some(item => item.workspace_path === workspacePath) ? workspacePath : '')
    } catch (cause) { setError(slackErrorText(cause, 'Could not load the projects you manage.')) }
    finally { setWorking(false) }
  }
  const save = async () => {
    // Non-submit controls in this form must never save an unfinished bot.
    if (busy || !name.trim() || (editing === 'new' && (!scope || !bot || !app))) return
    setWorking(true)
    try {
      await slackBotsApi.act('save', {
        connection_id: editing === 'new' ? '' : editing?.id,
        workspace_path: editing === 'new' ? scope : '',
        display_name: name, enabled,
        ...(bot ? { bot_token: bot } : {}), ...(app ? { app_token: app } : {}),
      })
      existing(); setNotice('Bot saved. Choose Use here to use it in this project.')
      await onUpdated()
    } catch (cause) { setError(slackErrorText(cause, 'Could not save this bot.')) }
    finally { setWorking(false) }
  }
  const act = async (action: string, item: VaultSlackBot) => {
    setWorking(true); setChecks(null)
    try {
      const result = await slackBotsApi.act(action, { connection_id: item.id })
      if (action === 'test') setChecks(result as SlackTestResponse)
      else { existing(); setNotice('Bot removed.'); await onUpdated() }
      setError('')
    } catch (cause) { setError(slackErrorText(cause, 'Could not update this bot.')) }
    finally { setWorking(false) }
  }

  return <div aria-label="Slack bot choices" className="space-y-3">
    <div className="flex flex-wrap gap-2">
      <Button type="button" size="sm" variant={view === 'existing' ? 'secondary' : 'outline'} disabled={busy} onClick={() => { existing(); void onUpdated() }}>Use existing</Button>
      <Button type="button" size="sm" variant={view === 'new' ? 'secondary' : 'outline'} disabled={busy} onClick={() => void add()}><Plus className="mr-1.5 h-3.5 w-3.5"/>Add new</Button>
    </div>
    {error && <p role="alert" className="text-xs text-destructive">{error}</p>}
    {notice && <p role="status" className="text-xs">{notice}</p>}
    {view === 'existing' && !editing && <>
      {!data.bots.length && <p className="text-sm text-muted-foreground">No saved bot yet. Add one to get started.</p>}
      {data.bots.map(item => <div key={item.id} className="space-y-3 rounded-lg border border-border p-3 text-sm">
        <div className="flex flex-wrap items-center gap-2">
          <div className="min-w-0 flex-1">
            <p className="break-words font-medium">{slackBotName(item)}</p>
            <p className="mt-1 text-xs text-muted-foreground">{[item.identity?.workspace, !item.configured ? 'Needs setup' : !item.enabled ? 'Paused' : item.identity?.verified ? 'Identity verified' : 'Saved'].filter(Boolean).join(' · ')}</p>
          </div>
          {item.id === data.selected_id ? <span className="inline-flex items-center gap-1 text-xs text-primary"><Check className="h-3.5 w-3.5"/>Used here</span> : <Button type="button" size="xs" variant="outline" disabled={busy || !item.can_select} onClick={() => void onSelect(item.id)}>Use here</Button>}
          {item.can_manage && <Button type="button" size="xs" variant="ghost" disabled={busy} onClick={() => edit(item)}>Settings</Button>}
        </div>
        <div className="text-xs text-muted-foreground">
          <span className="font-medium text-foreground">Used in </span>
          {item.used_in?.length ? item.used_in.map((usage, i) => <span key={`${usage.kind}:${usage.label}:${i}`}>{i > 0 ? ', ' : ''}{usage.kind === 'workflows' ? 'Goal' : usage.kind === 'crew' ? 'Crew' : 'Project'} · {usage.label}</span>) : 'No projects visible to you yet'}
        </div>
        {item.id === data.selected_id && children}
      </div>)}
    </>}
    {editing && editing !== 'new' && editing.default ? <div className="space-y-3">
      <div className="flex items-center justify-between gap-2"><p className="text-sm font-medium">{slackBotName(editing)}</p><Button type="button" size="xs" variant="ghost" onClick={() => { existing(); void onUpdated() }}>Back to bots</Button></div>
      <SlackAdminPanel/>
    </div> : editing && <form aria-label="Save Slack bot" className="space-y-3 rounded-lg border border-border p-4 text-xs" onSubmit={event => { event.preventDefault(); void save() }}>
      <label className="block">Label in AgentWorks<Input aria-label="Bot label" value={name} onChange={event => setName(event.target.value)}/></label>
      {editing === 'new' && !working && !scopes.length && <p className="text-muted-foreground">Create or open a project you manage to add its bot.</p>}
      {editing === 'new' && <label className="flex flex-col gap-1.5">Managed by<select aria-label="Bot management scope" className="h-9 w-full rounded-md border border-border bg-background px-2" value={scope} onChange={event => setScope(event.target.value)}><option value="" disabled>Choose a Crew or Goal you own</option>{scopes.map(item => <option key={item.workspace_path} value={item.workspace_path}>{item.label}</option>)}</select></label>}
      <SecretField label="Bot token" hint={<SlackTokenHint kind="bot"/>} value={bot} onChange={setBot} placeholder={editing === 'new' ? 'xoxb-…' : 'Leave blank to keep the saved token'}/>
      <SecretField label="App token" hint={<SlackTokenHint kind="app"/>} value={app} onChange={setApp} placeholder={editing === 'new' ? 'xapp-…' : 'Leave blank to keep the saved token'}/>
      <p className="text-muted-foreground">Tokens are encrypted. Saved values are never shown here.</p>
      <ToggleRow label="Enabled" checked={enabled} onCheckedChange={setEnabled}/>
      <details><summary className="cursor-pointer">Slack app setup instructions</summary><div className="mt-2 space-y-3"><SlackManifestSetup defaultName={name || 'AgentWorks'} finalStep="Save the bot here, then choose Use here."/><SlackPermissionsChecklist/></div></details>
      <div className="flex flex-wrap gap-2">
        <Button type="submit" size="sm" disabled={busy || !name.trim() || (editing === 'new' && (!scope || !bot || !app))}>{working ? 'Saving…' : 'Save bot'}</Button>
        <Button type="button" size="sm" variant="outline" disabled={busy} onClick={existing}>Cancel</Button>
        {editing !== 'new' && editing.can_manage && <>
          <Button type="button" size="sm" variant="outline" disabled={busy} onClick={() => void act('test', editing)}>Test</Button>
          <Button type="button" size="sm" variant="ghost" disabled={busy} onClick={() => void act('remove', editing)}>Remove bot</Button>
        </>}
      </div>
    </form>}
    {view === 'new' && data.can_create_company && !defaultBot && <details className="text-sm"><summary className="cursor-pointer text-muted-foreground">Administrator bot settings</summary><div className="mt-3"><SlackAdminPanel/></div></details>}
    {checks && <SlackChecksView result={checks}/>}
  </div>
}
