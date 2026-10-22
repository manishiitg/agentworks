import { useCallback, useEffect, useState } from 'react'
import api, { getApiBaseUrl } from '../../services/api'
import { useAuthStore } from '../../stores/useAuthStore'
import type { GmailConnection } from '../../services/api-types'
import { useAppStore } from '../../stores/useAppStore'
import { Button } from '../ui/Button'
import ConnectionIcon from '../connectors/ConnectionIcon'
import { GoogleAccountConnect } from '../../products/work/GoogleAccountConnect'
import { changeGoogleAccountAccess, googleAccessSummary, googleAccessLevels, googleGrantedAccess, googleAccessLabel, GOOGLE_ACCESS_SERVICES } from '../../products/work/googleAccountAccess'

type Provider = 'google' | 'slack'
interface Account {
  id: string; owner: string; label: string; mine: boolean; selected: boolean; connected: boolean
  can_add: boolean; can_manage: boolean; can_reconnect: boolean; can_detach: boolean
  // Why it last failed to connect, in the service's own words (Slack).
  problem?: string
  google?: GmailConnection
}
interface Accounts { accounts: Account[]; can_connect: boolean; notice: string; sign_in_ready?: boolean; inbox_available?: boolean }
const errorText = (cause: unknown) => {
  const data = (cause as { response?: { data?: { error?: string } | string } })?.response?.data
  return typeof data === 'string' ? data : data?.error || 'Could not update your accounts. Please try again.'
}

/** Personal records stay in their existing stores. Products only select those records. */
export default function PersonalAccountsPanel({ workspacePath, provider, onChanged }: { workspacePath?: string; provider?: Provider; onChanged?: () => void }) {
  const actor = useAuthStore(state => state.user?.id)
  const openVault = useAppStore(state => state.setAdminPage)
  return <section aria-label={workspacePath ? 'Saved personal accounts' : 'Your connected accounts'} className="space-y-4">
    {!workspacePath && <div><h2 className="text-base font-semibold">Your connected accounts</h2><p className="mt-1 text-xs text-muted-foreground">Sign in once. Choose Use here in each product that should use an account. These accounts stay separate from your shared vaults.</p></div>}
    {(['google', 'slack'] as Provider[]).filter(item => !provider || item === provider).map(item => <ProviderAccounts key={`${getApiBaseUrl()}:${actor}:${item}:${workspacePath || ''}`} provider={item} workspacePath={workspacePath} onChanged={onChanged} />)}
    {workspacePath && <Button variant="outline" size="sm" onClick={() => openVault('vaults')}>Manage accounts in My Vault</Button>}
  </section>
}

function ProviderAccounts({ provider, workspacePath, onChanged }: { provider: Provider; workspacePath?: string; onChanged?: () => void }) {
  const [data, setData] = useState<Accounts | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [message, setMessage] = useState<string | null>(null)
  const central = !workspacePath
  const endpoint = `/api/integrations/${provider}/accounts`
  const label = provider === 'google' ? 'Google' : 'Slack'
  const refresh = useCallback(async () => {
    try {
      const response = await api.get<Accounts>(endpoint, { params: { workspace_path: workspacePath || '' } })
      setData(response.data); setError(null)
    } catch (cause) { setError(errorText(cause)) }
  }, [endpoint, workspacePath])
  useEffect(() => { void refresh() }, [refresh])
  useEffect(() => {
    const focus = () => { void refresh(); onChanged?.() }
    window.addEventListener('focus', focus)
    return () => window.removeEventListener('focus', focus)
  }, [refresh, onChanged])
  useEffect(() => {
    if (!data?.accounts.some(account => account.google?.auth?.checking)) return
    const timer = window.setTimeout(() => { void refresh() }, 2000)
    return () => window.clearTimeout(timer)
  }, [data, refresh])
  const act = async (action: string, account?: Account) => {
    setBusy(true); setError(null); setMessage(null)
    try {
      const result = (await api.post(endpoint, { action, workspace_path: workspacePath || '', connection_id: account?.id, connection_owner: account?.owner })).data
      if (result.auth_url) window.open(result.auth_url, '_blank', 'noopener,noreferrer')
      setMessage(result.message || result.next || null)
      await refresh(); onChanged?.()
    } catch (cause) { setError(errorText(cause)) }
    finally { setBusy(false) }
  }
  return <div aria-label={`${label} accounts`} className="space-y-3 rounded-lg border border-border p-4">
    <div className="flex items-center gap-2"><ConnectionIcon icon={provider} name={label} size="sm" /><h3 className="text-sm font-medium">{label}</h3></div>
    {workspacePath && data?.notice && <p className="text-xs text-muted-foreground">{data.notice}</p>}
    {central && provider === 'google' && data?.inbox_available && <p className="text-xs text-muted-foreground">Choose one Gmail account for Crew inboxes. Your Crews receive email by default; ask a Crew’s Builder to turn receiving off or back on. Only mail addressed to enabled Crews is received. Gmail read access is required; disconnecting this account stops delivery to all your Crews.</p>}
    {central && provider === 'slack' && <p className="text-xs text-muted-foreground">Only you can read or search your Slack. People using a product you add it to can send messages as you.</p>}
    {error && <div role="alert" className="text-xs text-destructive">{error} <Button variant="ghost" size="xs" onClick={() => void refresh()}>Retry</Button></div>}
    {message && <p role="status" className="text-xs">{message}</p>}
    {!data && !error && <p role="status" className="text-xs text-muted-foreground">Loading accounts…</p>}
    {data?.accounts.length === 0 && <p className="text-xs text-muted-foreground">{central ? `No ${label} account saved yet.` : `No saved ${label} account available. Connect one in My Vault.`}</p>}
    {data?.accounts.map(account => <div key={`${account.owner}:${account.id}`} className="rounded-md border border-border p-3">
      <div className="flex flex-wrap items-center gap-2">
        <div className="min-w-0 flex-1"><p className="break-all text-sm font-medium">{account.label || account.google?.display_name || `Your ${label}`}</p><p className="mt-1 text-xs text-muted-foreground">{account.google?.enabled === false ? 'Paused' : account.google?.auth?.checking ? 'Checking sign-in…' : account.connected ? 'Connected' : 'Needs sign-in'}{account.google?.personal === false && ' · Company account'}{!central && (account.selected ? ' · Used here' : ' · Not used here')}</p></div>
        {!central && account.can_add && !account.selected && <Button size="xs" variant="outline" disabled={busy} onClick={() => void act('attach', account)}>Use here</Button>}
        {!central && account.can_detach && <Button size="xs" variant="outline" disabled={busy} onClick={() => void act('detach', account)}>Remove here</Button>}
        {central && account.can_reconnect && <Button size="xs" variant="outline" disabled={busy} onClick={() => provider === 'google' && account.google ? changeGoogleAccountAccess(account.google, '') : void act('reconnect', account)}>{provider === 'google' ? 'Change access' : 'Reconnect'}</Button>}
        {central && provider === 'google' && data?.inbox_available && account.mine && account.google?.personal && <Button size="xs" variant="outline" disabled={busy || account.google.agent_inbox} onClick={() => void act('set_inbox_account', account)}>{account.google.agent_inbox ? 'Crew inbox account' : 'Use for Crew inboxes'}</Button>}
        {central && account.can_manage && <Button size="xs" variant="ghost" disabled={busy} onClick={() => void act('disconnect', account)}>Disconnect everywhere</Button>}
      </div>
      {account.problem && <p role="alert" className="mt-2 break-words text-xs text-destructive">Not working: {account.problem}</p>}
      {account.google && <><p className="mt-2 text-xs text-muted-foreground">{googleAccessSummary(account.google)}</p><GooglePermissions account={account.google} /></>}
    </div>)}
    {central && provider === 'google' && data && (data.can_connect || data.accounts.some(account => account.can_reconnect)) && <GoogleAccountConnect initiallyExpanded={data.accounts.length === 0} canConnect={data.can_connect} onChanged={() => { void refresh(); onChanged?.() }} />}
    {central && provider === 'slack' && data?.can_connect && !data.accounts.some(account => account.mine) && <Button variant="outline" size="sm" disabled={busy || data.sign_in_ready === false} onClick={() => void act('connect')}>Connect Slack account</Button>}
    {central && data?.sign_in_ready === false && <p className="text-xs text-muted-foreground">An administrator needs to open My vaults → Company app setup → Sign-in apps → Slack and save this server’s Slack app before you connect your account.</p>}
  </div>
}

function GooglePermissions({ account }: { account: GmailConnection }) {
  const saved = googleAccessLevels(account)
  const granted = account.auth?.scopes?.length ? googleGrantedAccess(account.auth.scopes) : null
  return <details className="mt-2 text-xs">
    <summary className="cursor-pointer text-muted-foreground">Permissions</summary>
    <table className="mt-2 w-full text-left"><thead><tr><th className="py-1 font-medium">Service</th><th className="py-1 font-medium">Allowed in AgentWorks</th><th className="py-1 font-medium">Granted by Google</th></tr></thead><tbody>
      {GOOGLE_ACCESS_SERVICES.map(service => {
        const allowed = service.key === 'gmail' ? saved.gmail : saved.levels[service.key] || 'off'
        const actual = granted ? service.key === 'gmail' ? granted.gmail : granted.levels[service.key] || 'off' : null
        const actualLabel = actual === null ? 'Not checked yet' : service.key === 'gmail' && actual === 'off' && !granted?.gmailSend ? 'No access' : googleAccessLabel(service.key, actual)
        return <tr key={service.key} className="border-t border-border"><th className="py-2 font-normal">{service.label}</th><td className="py-2">{googleAccessLabel(service.key, allowed)}</td><td className="py-2">{actualLabel}</td></tr>
      })}
    </tbody></table>
    <p className="mt-1 text-muted-foreground">Change access in My Vault to update AgentWorks permissions and approve them with Google.</p>
  </details>
}
