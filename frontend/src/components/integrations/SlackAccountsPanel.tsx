import { useCallback, useEffect, useState } from 'react'
import { LockKeyhole, Plus } from 'lucide-react'
import api from '../../services/api'
import { useAuthStore } from '../../stores/useAuthStore'
import { Button } from '../ui/Button'
import { SlackSignInSetup } from './SlackSignInSetup'
import { slackErrorText } from '../workflow/bots/SlackSlugsSection'

type SlackAccount = { id: string; owner: string; label: string; mine: boolean; selected: boolean; connected: boolean; can_add: boolean; can_detach: boolean; can_manage: boolean; can_reconnect: boolean; problem?: string }
type SlackAccounts = { accounts: SlackAccount[]; can_connect: boolean; sign_in_ready?: boolean }
const key = (account: SlackAccount) => `${account.owner}:${account.id}`

/** A single account view; saving a sign-in and lending it to a project keep their separate server checks. */
export default function SlackAccountsPanel({ workspacePath, noun = 'Crew', embedded = false, savedBotNames = [], onStatus }: { workspacePath: string; noun?: string; tabId?: string; embedded?: boolean; savedBotNames?: string[]; onStatus?: (status: string) => void }) {
 const [saved, setSaved] = useState<SlackAccounts | null>(null)
 const [scoped, setScoped] = useState<SlackAccounts | null>(null)
 const [busy, setBusy] = useState(false)
 const [error, setError] = useState('')
 const [notice, setNotice] = useState('')
 const [authURL, setAuthURL] = useState('')
 const admin = useAuthStore(state => state.user?.is_admin === true)
 const refresh = useCallback(async () => {
  try {
   const [all, here] = await Promise.all([api.get<SlackAccounts>('/api/integrations/slack/accounts', { params: { workspace_path: '' } }), api.get<SlackAccounts>('/api/integrations/slack/accounts', { params: { workspace_path: workspacePath } })])
   setSaved(all.data); setScoped(here.data); setError(''); onStatus?.(here.data.accounts.some(account => account.selected && account.connected) ? 'Connected' : all.data.accounts.some(account => account.connected) ? 'Choose your account' : all.data.sign_in_ready === false ? 'Sign-in setup needed' : 'Not connected')
  } catch (cause) { setError(slackErrorText(cause, 'Could not load Slack accounts.')); onStatus?.('Could not load') }
 }, [workspacePath, onStatus])
 useEffect(() => { void refresh(); const focus = () => void refresh(); window.addEventListener('focus', focus); return () => window.removeEventListener('focus', focus) }, [refresh])
 const act = async (action: string, account?: SlackAccount) => {
  setBusy(true); setError(''); setNotice(''); setAuthURL('')
  try {
   const result = (await api.post('/api/integrations/slack/accounts', { action, workspace_path: action === 'attach' || action === 'detach' ? workspacePath : '', connection_id: account?.id, connection_owner: account?.owner })).data
   if (result.auth_url) { setAuthURL(result.auth_url); window.open(result.auth_url, '_blank', 'noopener,noreferrer') }
   setNotice(result.message || result.next || '')
   await refresh()
  } catch (cause) { setError(slackErrorText(cause, 'Could not update your Slack account.')) }
  finally { setBusy(false) }
 }
 const scopedByKey = new Map((scoped?.accounts || []).map(account => [key(account), account]))
 const accounts = [...(saved?.accounts || []), ...(scoped?.accounts || []).filter(account => !saved?.accounts.some(item => key(item) === key(account)))]
 return <section aria-label="My Slack account" className="space-y-5">
  {!embedded && <h3 className="text-base font-semibold">Let this {noun} send messages as you</h3>}
  <p className="text-sm text-muted-foreground">Messages appear from your Slack account.</p>
  {!!savedBotNames.length && <p className="rounded-md bg-muted/30 p-3 text-xs text-muted-foreground"><b className="text-foreground">Saved bots available: {savedBotNames.join(', ')}.</b> Connect your personal account here to send messages as you. You can reuse the same Slack app for sign-in.</p>}
  {error && <p role="alert" className="text-sm text-destructive">{error} <button className="underline" onClick={() => void refresh()}>Retry</button></p>}
  {notice && <p role="status" className="text-sm">{notice}</p>}
  {authURL && <a href={authURL} target="_blank" rel="noreferrer" className="inline-flex text-sm text-primary underline">Continue sign-in in Slack</a>}
  {!saved && !error && <p role="status" className="text-sm text-muted-foreground">Loading accounts…</p>}
  {saved && scoped && <>
   {!accounts.length && <div className="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-border p-4"><div><p className="text-sm font-medium">No account connected</p><p className="mt-1 text-xs text-muted-foreground">Connect your account, then choose to use it in this {noun}.</p></div>{saved.can_connect && saved.sign_in_ready !== false && <Button size="sm" variant="outline" disabled={busy} onClick={() => void act('connect')}><Plus className="mr-1.5 h-4 w-4"/>Connect my Slack</Button>}</div>}
   {accounts.map(account => {
    const here = scopedByKey.get(key(account))
    return <div key={key(account)} className="space-y-3 rounded-lg border border-border p-4">
     <div className="flex flex-wrap items-center gap-3"><div className="min-w-0 flex-1"><p className="break-words text-sm font-medium">{account.label || 'Slack account'}</p><p className="mt-1 text-xs text-muted-foreground">{account.connected ? 'Connected' : 'Needs sign-in'} · {here?.selected ? `Used by this ${noun}` : `Not used by this ${noun}`}</p></div>
      {here?.can_add && !here.selected && <Button size="sm" variant="outline" disabled={busy} onClick={() => void act('attach', account)}>Use here</Button>}
      {here?.can_detach && <Button size="sm" variant="outline" disabled={busy} onClick={() => void act('detach', account)}>Remove from this {noun}</Button>}
      {account.can_reconnect && !account.connected && <Button size="sm" variant="outline" disabled={busy || saved.sign_in_ready === false} onClick={() => void act('reconnect', account)}>Sign in again</Button>}
     </div>
     {account.problem && <p role="alert" className="text-xs text-destructive">{account.problem}</p>}
     {(account.can_manage || account.can_reconnect) && <details className="text-xs"><summary className="cursor-pointer text-muted-foreground">Account settings</summary><div className="mt-3 flex flex-wrap gap-2">{account.can_reconnect && <Button size="xs" variant="outline" disabled={busy || saved.sign_in_ready === false} onClick={() => void act('reconnect', account)}>Reconnect</Button>}{account.can_manage && <Button size="xs" variant="ghost" disabled={busy} onClick={() => void act('disconnect', account)}>Disconnect everywhere</Button>}</div></details>}
    </div>
   })}
   {!!accounts.length && saved.can_connect && !accounts.some(account => account.mine) && saved.sign_in_ready !== false && <Button size="sm" variant="outline" disabled={busy} onClick={() => void act('connect')}>Connect my Slack</Button>}
   {saved.sign_in_ready === false && <p className="text-xs text-muted-foreground">{admin ? 'Complete the sign-in setup below, then connect your account.' : 'Ask an administrator to set up Slack sign-in, then connect your account.'}</p>}
  </>}
  <p className="flex items-start gap-2 text-xs leading-5 text-muted-foreground"><LockKeyhole className="mt-0.5 h-3.5 w-3.5 shrink-0"/>Only you can read or search your Slack. People using this {noun} can send messages as you through it.</p>
  <SlackSignInSetup onSaved={refresh} ready={saved?.sign_in_ready}/>
 </section>
}
