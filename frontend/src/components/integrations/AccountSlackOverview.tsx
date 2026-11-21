import { VaultSlackBots } from './VaultSlackBots'
import { useEffect, useState } from 'react'
import { Bot } from 'lucide-react'
import api from '../../services/api'
import { Button } from '../ui/Button'
import PersonalAccountsPanel from './PersonalAccountsPanel'
import { type SlackBotState } from './ProductSlackPanel'

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
      {error ? <p role="alert" className="text-xs text-destructive">Could not load Slack bots. Try Refresh.</p> : !data ? <p role="status" className="text-xs text-muted-foreground">Loading Slack bots…</p> : <VaultSlackBots data={data} workspacePath="" manageOnly busy={false} onSelect={async () => {}} onUpdated={async () => { setRevision(value => value + 1) }} />}

      <p className="text-xs text-muted-foreground">Configure saved bots here. Choose which bot a Crew uses in that Crew’s Slack settings. Only authorized project usage is listed.</p>
    </section>
  </div>
}
