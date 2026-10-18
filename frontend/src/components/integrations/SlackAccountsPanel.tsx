import { useCallback, useEffect, useState } from 'react'
import { useAuthStore } from '../../stores/useAuthStore'
import { McpAppsSection } from '../../products/work/McpAppsSection'
import api from '../../services/api'
import { Button } from '../ui/Button'

interface SlackAccount {
  id: string; owner: string; label: string; selected: boolean; connected: boolean; mine: boolean
  can_add: boolean; can_manage: boolean; can_reconnect: boolean; can_detach: boolean
}
interface SlackAccounts { accounts: SlackAccount[]; can_connect: boolean; notice: string; sign_in_ready?: boolean }

/** The server supplies every permission; this panel only displays and submits. One Slack account per person. */
export default function SlackAccountsPanel({ workspacePath }: { workspacePath: string }) {
  const isAdmin = useAuthStore(state => state.user?.is_admin === true)
  const [data, setData] = useState<SlackAccounts | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [message, setMessage] = useState<string | null>(null)
  const refresh = useCallback(async () => {
    try {
      const response = await api.get<SlackAccounts>('/api/integrations/slack/accounts', { params: { workspace_path: workspacePath } })
      setData(response.data); setError(null)
    } catch { setError('Could not load Slack accounts. Ask this item’s Builder to open Connections → Add connection → Slack.') }
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
      const response = await api.post('/api/integrations/slack/accounts', { workspace_path: workspacePath, action, connection_id: account?.id, connection_owner: account?.owner })
      if (response.data.auth_url) window.open(response.data.auth_url, '_blank', 'noopener,noreferrer')
      if (response.data.message || response.data.next) setMessage(response.data.message || response.data.next)
      await refresh()
    } catch (cause) {
      setError((cause as { response?: { data?: { error?: string } } }).response?.data?.error || 'Could not update the Slack account. Try again from Connections → Add connection → Slack.')
    } finally { setBusy(false) }
  }
  const signInReady = data?.sign_in_ready !== false
  const mine = data?.accounts.find(account => account.mine)
  const others = data?.accounts.filter(account => !account.mine) ?? []
  return <section aria-label="Personal Slack accounts" className="space-y-3 rounded-md border border-border p-3 text-xs">
    <h3 className="text-sm font-medium">Send messages as me</h3>
    {data?.notice && <p className="text-muted-foreground">{data.notice}</p>}
    {error && <p role="alert" className="text-destructive">{error}<Button variant="ghost" size="xs" onClick={() => void refresh()}>Retry</Button></p>}
    {message && <p role="status">{message}</p>}
    {!signInReady && (isAdmin
      ? <><p className="text-amber-600">Set up Slack sign-in once for this server (Slack below), then connect.</p><McpAppsSection /></>
      : <p className="text-amber-600">An administrator needs to set up Slack sign-in first.</p>)}
    {mine && <div className="flex flex-wrap items-center gap-2">
      <span className="flex-1">{mine.connected ? '✓ Your Slack is connected' : 'Your Slack needs sign-in'}{mine.connected && (mine.selected ? ' · used here' : ' · not used here')}</span>
      {signInReady && !mine.connected && mine.can_reconnect && <Button variant="outline" size="xs" disabled={busy} onClick={() => void act('reconnect', mine)}>Sign in</Button>}
      {mine.can_add && !mine.selected && <Button variant="outline" size="xs" disabled={busy} onClick={() => void act('attach', mine)}>Use here</Button>}
      {mine.can_detach && <Button variant="ghost" size="xs" disabled={busy} onClick={() => void act('detach', mine)}>Remove here</Button>}
      {mine.can_manage && <Button variant="ghost" size="xs" disabled={busy} onClick={() => void act('disconnect', mine)}>Sign out of Slack</Button>}
    </div>}
    {!mine && signInReady && data?.can_connect && <Button variant="outline" size="sm" disabled={busy} onClick={() => void act('connect')}>Connect my Slack</Button>}
    {others.map(account => <div key={`${account.owner}:${account.id}`} className="flex flex-wrap items-center gap-2 border-t border-border pt-2">
      <span className="flex-1">{account.label} · {account.selected ? 'used here' : 'saved'}{account.connected ? '' : ' · needs sign-in'}</span>
      {account.can_add && !account.selected && <Button variant="outline" size="xs" disabled={busy} onClick={() => void act('attach', account)}>Use here</Button>}
      {account.can_detach && <Button variant="ghost" size="xs" disabled={busy} onClick={() => void act('detach', account)}>Remove here</Button>}
    </div>)}
  </section>
}
