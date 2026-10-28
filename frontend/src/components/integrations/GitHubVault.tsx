import { useCallback, useEffect, useState } from 'react'
import api from '../../services/api'
import { Button } from '../ui/Button'
import { CompanySignInSetup } from './CompanySignInSetup'

interface GitHubStatus { app_ready: boolean; connected: boolean; login?: string; next?: string; summary?: string; approval_page?: string }

const errorText = (cause: unknown, fallback: string) => {
  const data = (cause as { response?: { data?: { error?: string } | string } })?.response?.data
  return typeof data === 'string' ? data : data?.error || fallback
}

/** Connect GitHub in My Vault: the account becomes your GITHUB_TOKEN secret, so Git work in your Crews acts as you. */
export function GitHubVaultCard() {
  const [status, setStatus] = useState<GitHubStatus | null>(null)
  const [busy, setBusy] = useState(false)
  const [message, setMessage] = useState<string | null>(null)
  const load = useCallback(async () => {
    try { setStatus((await api.get<GitHubStatus>('/api/vault/github')).data) } catch (cause) { setMessage(errorText(cause, 'Could not read your GitHub connection.')) }
  }, [])
  useEffect(() => { void load() }, [load])
  const connect = async () => {
    setBusy(true); setMessage(null)
    try { window.location.assign((await api.post<{ auth_url: string }>('/api/vault/github/connect')).data.auth_url) } catch (cause) { setMessage(errorText(cause, 'Could not start the GitHub connection.')); setBusy(false) }
  }
  const disconnect = async () => {
    setBusy(true); setMessage(null)
    try {
      const result = (await api.delete<{ revoked_at_github: boolean }>('/api/vault/github')).data
      setMessage(result.revoked_at_github ? 'GitHub disconnected.' : 'Removed from My Vault. GitHub did not confirm it; you can also remove the app in your GitHub settings.')
      await load()
    } catch (cause) { setMessage(errorText(cause, 'Could not disconnect GitHub.')) } finally { setBusy(false) }
  }
  if (!status) return message ? <p role="alert" className="text-xs text-destructive">{message}</p> : null
  return <section aria-label="GitHub" className="space-y-2 rounded-lg border border-border p-3">
    <div className="flex items-center justify-between gap-3">
      <div>
        <h3 className="text-sm font-medium">GitHub</h3>
        <p className="text-xs text-muted-foreground">{status.connected ? `${status.summary || `Connected as ${status.login}`}. Git and pull requests in your Crews use this account.` : 'Connect your GitHub account so Git work in your Crews (clone, push, pull requests) acts as you.'}</p>
        {status.connected && status.approval_page && <a className="text-xs underline" href={status.approval_page} target="_blank" rel="noreferrer">Open the approval page on GitHub</a>}
      </div>
      {status.connected
        ? <Button size="sm" variant="outline" disabled={busy} onClick={() => void disconnect()}>Disconnect</Button>
        : <Button size="sm" disabled={busy || !status.app_ready} onClick={() => void connect()}>Connect GitHub</Button>}
    </div>
    {!status.app_ready && !status.connected && <p className="text-xs text-muted-foreground">{status.next}</p>}
    {message && <p role="status" className="text-xs text-muted-foreground">{message}</p>}
    <CompanySignInSetup provider="GitHub" />
  </section>
}

/** Finishes a GitHub connection when the browser comes back from GitHub (?github_connect=...), signed in as the person who started it. */
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
      .catch(cause => setNote(errorText(cause, 'GitHub could not be connected. Start again from My Vault.')))
  }, [])
  if (!note) return null
  return <div role="status" className="fixed right-4 top-16 z-50 flex max-w-md items-start gap-3 rounded-lg border border-border bg-popover p-3 text-sm text-popover-foreground shadow-lg">
    <span>{note}</span><button type="button" className="text-xs underline" onClick={() => setNote(null)}>Close</button>
  </div>
}
