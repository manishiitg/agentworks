import { useCallback, useEffect, useId, useState } from 'react'
import api from '../../services/api'
import { Button } from '../ui/Button'
import { CompanySignInSetup } from './CompanySignInSetup'

interface GitHubStatus { app_ready: boolean; connected: boolean; gh_available: boolean; gh_accounts?: string[]; gh_accounts_note?: string; source?: string; login?: string; summary?: string; approval_page?: string }

const errorText = (cause: unknown, fallback: string) => {
  const data = (cause as { response?: { data?: { error?: string } | string } })?.response?.data
  return typeof data === 'string' ? data : data?.error || fallback
}
const sourceName = (source?: string) => source === 'gh' ? 'GitHub CLI' : source === 'pat' ? 'Personal access token' : 'Browser sign-in'

/** Every method saves the same owner-scoped GITHUB_TOKEN used by Git work and PR status. */
export function GitHubVaultCard() {
  const [status, setStatus] = useState<GitHubStatus | null>(null)
  const [busy, setBusy] = useState(false)
  const [token, setToken] = useState('')
  const [ghLogin, setGhLogin] = useState('')
  const tokenId = useId()
  const [message, setMessage] = useState<string | null>(null)
  const load = useCallback(async () => {
    try {
      const next = (await api.get<GitHubStatus>('/api/vault/github')).data
      setStatus(next)
      const accounts = next.gh_accounts ?? []
      setGhLogin(current => accounts.includes(current) ? current : next.login && accounts.includes(next.login) ? next.login : accounts.length === 1 ? accounts[0] : '')
    } catch (cause) { setMessage(errorText(cause, 'Could not read your GitHub connection.')) }
  }, [])
  useEffect(() => { void load() }, [load])
  const connect = async (method: 'gh' | 'pat' | 'oauth') => {
    setBusy(true); setMessage(null)
    try {
      const result = (await api.post<{ auth_url?: string; login?: string }>('/api/vault/github/connect', { method, ...(method === 'pat' ? { token } : method === 'gh' ? { login: ghLogin } : {}) })).data
      if (result.auth_url) { window.location.assign(result.auth_url); return }
      setMessage(`Connected as ${result.login}.`)
      await load()
    } catch (cause) { setMessage(errorText(cause, 'Could not connect GitHub.')) } finally { setToken(''); setBusy(false) }
  }
  const disconnect = async () => {
    setBusy(true); setMessage(null)
    try {
      const result = (await api.delete<{ message: string }>('/api/vault/github')).data
      setMessage(result.message)
      await load()
    } catch (cause) { setMessage(errorText(cause, 'Could not disconnect GitHub.')) } finally { setBusy(false) }
  }
  if (!status) return message ? <p role="alert" className="text-xs text-destructive">{message}</p> : null
  const methods = <div className="space-y-3 pt-2">
    {status.gh_available && <div className="flex flex-wrap items-start justify-between gap-2 rounded-md border border-border p-3">
      <div className="min-w-0 flex-1">
        <h4 className="text-sm font-medium">Use your GitHub CLI login</h4>
        <p className="mt-1 text-xs text-muted-foreground">Choose an account saved on this computer. Your terminal’s active account stays unchanged.</p>
      </div>
      <div className="flex w-full flex-wrap items-center gap-2">
        <label className="sr-only" htmlFor={`${tokenId}-gh`}>GitHub account</label>
        <select id={`${tokenId}-gh`} value={ghLogin} disabled={busy || !status.gh_accounts?.length} onChange={event => setGhLogin(event.target.value)} className="min-w-0 flex-1 rounded-md border border-border bg-background px-3 py-2 text-sm">
          <option value="">Choose an account</option>
          {status.gh_accounts?.map(login => <option key={login} value={login}>{login}{status.login === login ? ' · Connected' : ''}</option>)}
        </select>
        <Button type="button" size="sm" variant="outline" disabled={busy || !ghLogin} onClick={() => void connect('gh')}>Use account</Button>
      </div>
      {status.gh_accounts_note && <p className="text-xs text-muted-foreground">{status.gh_accounts_note}</p>}
    </div>}
    <form className="space-y-2 rounded-md border border-border p-3" onSubmit={event => { event.preventDefault(); void connect('pat') }}>
      <label htmlFor={tokenId} className="block text-sm font-medium">Personal access token</label>
      <p className="text-xs text-muted-foreground">Works on your computer or a server. Choose the repositories and permissions you need when creating the token.</p>
      <div className="flex flex-wrap gap-2">
        <input id={tokenId} type="password" autoComplete="off" spellCheck={false} value={token} onChange={event => setToken(event.target.value)} placeholder="Paste a GitHub token" className="min-w-0 flex-1 rounded-md border border-border bg-background px-3 py-2 text-sm" disabled={busy}/>
        <Button type="submit" size="sm" disabled={busy || !token.trim()}>Connect token</Button>
      </div>
      <a href="https://github.com/settings/personal-access-tokens/new" target="_blank" rel="noreferrer" className="text-xs underline">Create a token on GitHub</a>
      <p className="text-xs text-muted-foreground">Stored encrypted. Saved tokens are never shown here.</p>
    </form>
    <details className="rounded-md border border-border p-3">
      <summary className="cursor-pointer text-sm font-medium">Browser sign-in · optional</summary>
      <div className="mt-2 space-y-2">
        <p className="text-xs text-muted-foreground">{status.app_ready ? 'Approve access to your account on GitHub.' : 'Requires an OAuth app. You can use a personal access token without setting this up.'}</p>
        {status.app_ready && <Button type="button" size="sm" variant="outline" disabled={busy} onClick={() => void connect('oauth')}>Sign in with GitHub</Button>}
        <CompanySignInSetup provider="GitHub" />
      </div>
    </details>
  </div>
  return <section aria-label="GitHub" className="space-y-2 rounded-lg border border-border p-3">
    <div className="flex items-start justify-between gap-3">
      <div className="min-w-0">
        <h3 className="text-sm font-medium">GitHub{status.connected ? ` · ${status.login}` : ''}</h3>
        <p className="text-xs text-muted-foreground">{status.connected ? `${sourceName(status.source)}. Git and pull requests in your Crews use this saved account.` : 'Connect your account for Git work and pull requests in your Crews.'}</p>
        {status.connected && <p className="mt-1 text-xs text-muted-foreground">{status.summary}</p>}
        {status.connected && status.approval_page && <a className="text-xs underline" href={status.approval_page} target="_blank" rel="noreferrer">Open the approval page on GitHub</a>}
      </div>
      {status.connected && <Button type="button" size="sm" variant="outline" disabled={busy} onClick={() => void disconnect()}>Disconnect</Button>}
    </div>
    {status.connected ? <details><summary className="cursor-pointer text-xs text-muted-foreground">Change connection</summary>{methods}</details> : methods}
    {message && <p role="status" className="text-xs text-muted-foreground">{message}</p>}
  </section>
}

/** Finishes a connection as the person who started it. */
export function GitHubConnectCompletion() {
  const [note, setNote] = useState<string | null>(null)
  useEffect(() => {
    const params = new URLSearchParams(window.location.search)
    const state = params.get('github_connect')
    if (!state) return
    params.delete('github_connect')
    const rest = params.toString()
    window.history.replaceState(null, '', `${window.location.pathname}${rest ? `?${rest}` : ''}${window.location.hash}`)
    api.post<{ login: string; summary?: string }>('/api/vault/github/complete', { state })
      .then(result => setNote(`${result.data.summary || `GitHub connected as ${result.data.login}`}. Your Crews can now use it for Git work.`))
      .catch(cause => setNote(errorText(cause, "GitHub could not be connected. Start again from the project's Secrets panel or ask in chat.")))
  }, [])
  if (!note) return null
  return <div role="status" className="fixed right-4 top-16 z-50 flex max-w-md items-start gap-3 rounded-lg border border-border bg-popover p-3 text-sm text-popover-foreground shadow-lg">
    <span>{note}</span><button type="button" className="text-xs underline" onClick={() => setNote(null)}>Close</button>
  </div>
}
