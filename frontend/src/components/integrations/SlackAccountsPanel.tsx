import { useCallback, useEffect, useState } from 'react'
import { useAuthStore } from '../../stores/useAuthStore'
import { McpAppsSection } from '../../products/work/McpAppsSection'
import api from '../../services/api'
import { Input } from '../ui/Input'
import { Button } from '../ui/Button'

interface SlackAccount {
  id: string; owner: string; label: string; selected: boolean; connected: boolean
  can_add: boolean; can_manage: boolean; can_reconnect: boolean; can_detach: boolean
}
interface SlackAccounts { accounts: SlackAccount[]; can_connect: boolean; notice: string }

/** The server supplies every permission; this panel only displays and submits. */
export default function SlackAccountsPanel({ workspacePath }: { workspacePath: string }) {
  const isAdmin = useAuthStore(state => state.user?.is_admin === true)
  const [data, setData] = useState<SlackAccounts | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [label, setLabel] = useState('Work Slack')
  const [busy, setBusy] = useState(false)
  const [message, setMessage] = useState<string | null>(null)
  const refresh = useCallback(async () => {
    try {
      const response = await api.get<SlackAccounts>('/api/integrations/slack/accounts', { params: { workspace_path: workspacePath } })
      setData(response.data); setError(null)
    } catch { setError('Could not load Slack accounts. Ask this item’s Builder to open Integrations → Slack.') }
  }, [workspacePath])
  useEffect(() => { void refresh() }, [refresh])
  useEffect(() => {
    const focus = () => { void refresh() }
    window.addEventListener('focus', focus)
    return () => window.removeEventListener('focus', focus)
  }, [refresh])
  const act = async (action: string, account?: SlackAccount) => {
    setBusy(true); setError(null); setMessage(null)
    try {
      const response = await api.post('/api/integrations/slack/accounts', { workspace_path: workspacePath, action, connection_id: account?.id, connection_owner: account?.owner, ...(action === 'connect' ? { label } : {}) })
      if (response.data.auth_url) window.open(response.data.auth_url, '_blank', 'noopener,noreferrer')
      if (response.data.message || response.data.next) setMessage(response.data.message || response.data.next)
      await refresh()
    } catch (cause) {
      setError((cause as { response?: { data?: { error?: string } } }).response?.data?.error || 'Could not update the Slack account. Try again from Integrations → Slack.')
    } finally { setBusy(false) }
  }
  return <section aria-label="Personal Slack accounts" className="space-y-3 rounded-md border border-border p-3 text-xs">
    <h3 className="text-sm font-medium">Your Slack account</h3>
    <p className="text-muted-foreground">Connect once, then explicitly add the saved account to each item that should use it. The Slack bot is a separate connection.</p>
    {data?.notice && <p className="text-muted-foreground">{data.notice}</p>}
    {error && <p role="alert" className="text-destructive">{error}<Button variant="ghost" size="xs" onClick={() => void refresh()}>Retry</Button></p>}
    {message && <p role="status">{message}</p>}
    {data?.accounts.map(account => <div key={`${account.owner}:${account.id}`} className="flex flex-wrap items-center gap-2 border-t border-border pt-2">
      <span className="flex-1">{account.label} · {account.selected ? 'Added here' : 'Saved account'} · {account.connected ? 'Connected' : 'Sign-in needed'}</span>
      {account.can_add && !account.selected && <Button variant="outline" size="xs" disabled={busy} onClick={() => void act('attach', account)}>Add here</Button>}
      {account.can_detach && <Button variant="outline" size="xs" disabled={busy} onClick={() => void act('detach', account)}>Remove here</Button>}
      {account.can_reconnect && <Button variant="ghost" size="xs" disabled={busy} onClick={() => void act('reconnect', account)}>Sign in again</Button>}
      {account.can_manage && <>
        <Button variant="ghost" size="xs" disabled={busy} onClick={() => void act('disconnect', account)}>Disconnect everywhere</Button>
      </>}
    </div>)}
    {isAdmin && <McpAppsSection />}
    {data?.can_connect && <div className="flex flex-wrap gap-2"><Input aria-label="Name for new Slack account" value={label} onChange={event => setLabel(event.target.value)} placeholder="Work Slack" /><Button variant="outline" size="sm" disabled={busy} onClick={() => void act('connect')}>Connect Slack account and add here</Button></div>}
  </section>
}
