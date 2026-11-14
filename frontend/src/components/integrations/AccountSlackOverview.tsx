import { useEffect, useState } from 'react'
import { Bot } from 'lucide-react'
import api from '../../services/api'
import { Button } from '../ui/Button'
import PersonalAccountsPanel from './PersonalAccountsPanel'
import { slackBotName, type SlackBotState } from './ProductSlackPanel'

/** Personal sign-in and saved bots are different identities; neither selects a Crew here. */
export function AccountSlackOverview() {
  const [data, setData] = useState<SlackBotState>()
  const [error, setError] = useState(false)
  const [revision, setRevision] = useState(0)
  useEffect(() => {
    const abort = new AbortController()
    setError(false)
    void api.get<SlackBotState>('/api/integrations/slack/bots', { signal: abort.signal, skipSessionContext: true }).then(result => {
      if (!abort.signal.aborted) setData(result.data)
    }).catch(() => { if (!abort.signal.aborted) setError(true) })
    return () => abort.abort()
  }, [revision])
  useEffect(() => {
    const refresh = () => setRevision(value => value + 1)
    window.addEventListener('focus', refresh)
    return () => window.removeEventListener('focus', refresh)
  }, [])
  return <div className="space-y-4" aria-label="Slack connections">
    <PersonalAccountsPanel provider="slack" hideHeading heading="Slack as me" />
    <section aria-label="Slack bots" className="space-y-3 rounded-lg border border-border p-4">
      <div className="flex items-center gap-2"><Bot className="h-4 w-4 text-muted-foreground" aria-hidden="true" /><h3 className="flex-1 text-sm font-medium">Slack bots</h3><Button size="xs" variant="ghost" onClick={() => setRevision(value => value + 1)}>Refresh</Button></div>
      <p className="text-xs text-muted-foreground">Messages from these connections appear as the bot, not as you.</p>
      {error ? <p role="alert" className="text-xs text-destructive">Could not load Slack bots. Try Refresh.</p> : !data ? <p role="status" className="text-xs text-muted-foreground">Loading Slack bots…</p> : !data.bots.length ? <p className="text-xs text-muted-foreground">No saved bots available to your account.</p> : <ul className="divide-y divide-border">{data.bots.map(bot => <li key={bot.id} className="space-y-1 py-3">
        <div className="flex items-center gap-3"><span className="min-w-0 flex-1 break-words text-sm font-medium">{slackBotName(bot)}</span><span className="text-xs text-muted-foreground">{!bot.configured ? 'Needs setup' : !bot.enabled ? 'Paused' : bot.identity?.verified ? 'Connected' : 'Configured · not verified'}</span></div>
        <p className="text-xs text-muted-foreground">{bot.identity?.workspace ? `Workspace: ${bot.identity.workspace}` : 'Workspace identity not available'}{bot.scope_label && ` · ${bot.scope_label}`}</p>
        <p className="text-xs text-muted-foreground"><span className="font-medium">Used in: </span>{bot.used_in.length ? bot.used_in.map(item => item.label).join(', ') : 'No accessible project usage found.'}</p>
      </li>)}</ul>}
      <p className="text-xs text-muted-foreground">Choose or manage a bot in a Crew’s Slack settings. Only projects you can access are listed.</p>
    </section>
  </div>
}
