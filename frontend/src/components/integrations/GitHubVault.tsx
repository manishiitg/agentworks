import { useCallback, useEffect, useId, useState } from 'react'
import api from '../../services/api'
import { Button } from '../ui/Button'
import { githubConnectionChanged } from '../../utils/githubConnectionEvents'
import { CompanySignInSetup } from './CompanySignInSetup'

interface GitHubStatus { orgs?: string[]; needs_approval?: { org: string; why: string }[]; connection_id?: string; selection_error?: string; connections?: { id: string; login: string; source: string }[]; app_ready: boolean; connected: boolean; gh_available: boolean; gh_accounts?: string[]; gh_accounts_note?: string; source?: string; login?: string; summary?: string; approval_page?: string }
type ConnectBody = { method: 'saved' | 'gh' | 'pat' | 'oauth'; connection_id?: string; login?: string; token?: string }

const errorText = (cause: unknown, fallback: string) => {
  const data = (cause as { response?: { data?: { error?: string } | string } })?.response?.data
  return typeof data === 'string' ? data : data?.error || fallback
}
const sourceName = (source?: string) => source === 'gh' ? 'GitHub CLI' : source === 'pat' ? 'Personal access token' : 'Browser sign-in'

/** Token first: most people paste a GitHub token. Other accounts stay one fold away; a choice changes only this project. */
export function GitHubVaultCard({ workspacePath, changeConnectionOpen = false }: { workspacePath: string; changeConnectionOpen?: boolean }) {
  const [status, setStatus] = useState<GitHubStatus | null>(null)
  const [busy, setBusy] = useState(false)
  const [token, setToken] = useState('')
  const [other, setOther] = useState('')
  const endpoint = `/api/vault/github?workspace_path=${encodeURIComponent(workspacePath)}`
  const tokenId = useId()
  const [message, setMessage] = useState<string | null>(null)
  const [failed, setFailed] = useState(false)
  const load = useCallback(async () => {
    try {
      setStatus((await api.get<GitHubStatus>(endpoint)).data)
      setOther('')
    } catch (cause) { setMessage(errorText(cause, 'Could not read your GitHub connection.')) }
  }, [endpoint])
  useEffect(() => { void load() }, [load])
  const connect = async (body: ConnectBody) => {
    setBusy(true); setMessage(null)
    try {
      const result = (await api.post<{ auth_url?: string; login?: string }>(`/api/vault/github/connect?workspace_path=${encodeURIComponent(workspacePath)}`, body)).data
      if (result.auth_url) { window.location.assign(result.auth_url); return }
      setFailed(false); setMessage(`Connected as ${result.login}.`)
      await load()
      window.dispatchEvent(new Event(githubConnectionChanged))
    } catch (cause) { setFailed(true); setMessage(errorText(cause, 'Could not connect GitHub.')) } finally { setToken(''); setBusy(false) }
  }
  const chooseOther = () => {
    const split = other.indexOf(':')
    const kind = other.slice(0, split), value = other.slice(split + 1)
    if (kind === 'saved') void connect({ method: 'saved', connection_id: value })
    if (kind === 'gh') void connect({ method: 'gh', login: value })
  }
  const disconnect = async () => {
    setBusy(true); setMessage(null)
    try {
      const result = (await api.delete<{ message: string }>(endpoint)).data
      setMessage(result.message)
      await load()
      window.dispatchEvent(new Event(githubConnectionChanged))
    } catch (cause) { setMessage(errorText(cause, 'Could not disconnect GitHub.')) } finally { setBusy(false) }
  }
  if (!status) return message ? <p role="alert" className="text-xs text-destructive">{message}</p> : null
  const saved = (status.connections ?? []).filter(connection => connection.id !== status.connection_id)
  const onComputer = status.gh_available ? status.gh_accounts ?? [] : []
  const otherCount = saved.length + onComputer.length
  const reach = status.orgs?.length ? `Reaches ${status.orgs.join(', ')}` : 'Reaches your own repositories'
  return <section aria-label="GitHub" className="space-y-3 rounded-lg border border-border p-3">
    <div className="flex items-start justify-between gap-3">
      <div className="min-w-0">
        <h3 className="text-sm font-medium">GitHub</h3>
        <p className="text-sm">{status.connected ? `Connected as ${status.login}` : 'Not connected'}</p>
        <p className="text-xs text-muted-foreground">{status.connected ? reach : 'Connect an account so this project can push code and open pull requests.'}</p>
        {status.connected && !!status.needs_approval?.length && <p className="mt-1 text-xs text-muted-foreground">{status.summary}</p>}
        {status.connected && status.approval_page && <a className="text-xs underline" href={status.approval_page} target="_blank" rel="noreferrer">Open the approval page on GitHub</a>}
      </div>
      {status.connected && <Button type="button" size="sm" variant="outline" disabled={busy} onClick={() => void disconnect()}>Disconnect</Button>}
    </div>
    {status.selection_error && <p role="alert" className="text-xs text-destructive">{status.selection_error}</p>}
    <form className="space-y-2 rounded-md border border-border p-3" onSubmit={event => { event.preventDefault(); void connect({ method: 'pat', token }) }}>
      <h4 className="text-sm font-medium">{status.connected ? 'Add another token' : 'Connect with a GitHub token'}</h4>
      <ol className="list-decimal space-y-1 pl-4 text-xs text-muted-foreground">
        <li><a href="https://github.com/settings/personal-access-tokens/new" target="_blank" rel="noreferrer" className="underline">Create a token on GitHub</a>.</li>
        <li>Pick the repositories this project works on, and allow Contents and Pull requests to read and write.</li>
        <li>Paste the token here.</li>
      </ol>
      <div className="flex flex-wrap gap-2">
        <label htmlFor={tokenId} className="sr-only">GitHub token</label>
        <input id={tokenId} type="password" autoComplete="off" spellCheck={false} value={token} onChange={event => setToken(event.target.value)} placeholder="Paste a GitHub token" className="min-w-0 flex-1 rounded-md border border-border bg-background px-3 py-2 text-sm" disabled={busy}/>
        <Button type="submit" size="sm" disabled={busy || !token.trim()}>Connect token</Button>
      </div>
      <p className="text-xs text-muted-foreground">Stored encrypted and never shown again.</p>
    </form>
    {(otherCount > 0 || status.app_ready) && <details open={changeConnectionOpen && otherCount > 0 ? true : undefined} className="rounded-md border border-border p-3">
      <summary className="cursor-pointer text-sm font-medium">Use another account{otherCount ? ` (${otherCount})` : ''}</summary>
      <div className="mt-2 space-y-2">
        {otherCount > 0 && <div className="flex flex-wrap gap-2">
          <label className="sr-only" htmlFor={`${tokenId}-other`}>GitHub account</label>
          <select id={`${tokenId}-other`} value={other} disabled={busy} onChange={event => setOther(event.target.value)} className="min-w-0 flex-1 rounded-md border border-border bg-background px-3 py-2 text-sm">
            <option value="">Choose an account</option>
            {saved.length > 0 && <optgroup label="Saved">{saved.map(connection => <option key={connection.id} value={`saved:${connection.id}`}>{connection.login} · {sourceName(connection.source)}</option>)}</optgroup>}
            {onComputer.length > 0 && <optgroup label="On this computer">{onComputer.map(login => <option key={login} value={`gh:${login}`}>{login}</option>)}</optgroup>}
          </select>
          <Button type="button" size="sm" variant="outline" disabled={busy || !other} onClick={chooseOther}>Use</Button>
        </div>}
        {status.gh_accounts_note && <p className="text-xs text-muted-foreground">{status.gh_accounts_note}</p>}
        {status.app_ready && <Button type="button" size="sm" variant="outline" disabled={busy} onClick={() => void connect({ method: 'oauth' })}>Sign in with GitHub in the browser</Button>}
        <p className="text-xs text-muted-foreground">Git and pull requests in this project act as the chosen account. Other projects are unchanged.</p>
      </div>
    </details>}
    <CompanySignInSetup provider="GitHub" />
    {message && <p role={failed ? 'alert' : 'status'} className={`text-xs ${failed ? 'text-destructive' : 'text-muted-foreground'}`}>{message}</p>}
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
      .then(result => { setNote(`${result.data.summary || `GitHub connected as ${result.data.login}`}. The project where you started sign-in can now use it for Git work.`); window.dispatchEvent(new Event(githubConnectionChanged)) })
      .catch(cause => setNote(errorText(cause, "GitHub could not be connected. Start again from the project's Secrets panel or ask in chat.")))
  }, [])
  if (!note) return null
  return <div role="status" className="fixed right-4 top-16 z-50 flex max-w-md items-start gap-3 rounded-lg border border-border bg-popover p-3 text-sm text-popover-foreground shadow-lg">
    <span>{note}</span><button type="button" className="text-xs underline" onClick={() => setNote(null)}>Close</button>
  </div>
}
