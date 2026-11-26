import { useCallback, useEffect, useId, useState } from 'react'
import { ChevronRight } from 'lucide-react'
import api from '../../services/api'
import { Button } from '../ui/Button'
import { githubConnectionChanged } from '../../utils/githubConnectionEvents'
import { isLocalDeployment } from '../../products/productSurfaceConfig'
import { isGitHubRepoReachable } from '../workspace/ChatChangesRow'
import { CompanySignInSetup } from './CompanySignInSetup'

interface SavedGitHubConnection {
  id: string
  login: string
  source: string
  orgs?: string[]
}

interface GitHubStatus {
  orgs?: string[]
  needs_approval?: { org: string; why: string }[]
  connection_id?: string
  selection_error?: string
  connections?: SavedGitHubConnection[]
  app_ready: boolean
  connected: boolean
  gh_available: boolean
  gh_accounts?: string[]
  gh_accounts_note?: string
  source?: string
  login?: string
  summary?: string
  approval_page?: string
}

type ConnectBody = { method: 'saved' | 'gh' | 'pat' | 'oauth'; connection_id?: string; login?: string; token?: string }

interface SwitcherAccount {
  key: string
  login: string
  sourceLabel: string
  detail: string
  active: boolean
  orgs?: string[]
  connectBody: ConnectBody
}

const errorText = (cause: unknown, fallback: string) => {
  const data = (cause as { response?: { data?: { error?: string } | string } })?.response?.data
  return typeof data === 'string' ? data : data?.error || fallback
}

const sourceBadge = (source?: string) => source === 'gh' ? 'GitHub CLI' : source === 'pat' ? 'Token' : 'Browser sign-in'

const sourceDetail = (source?: string) =>
  source === 'gh' ? 'Saved from GitHub CLI'
    : source === 'pat' ? 'Personal access token'
    : 'Saved browser sign-in'

/** Deduplicates saved connections and local gh CLI logins into one 1-click list. */
export function buildGitHubAccountChoices(status: GitHubStatus): SwitcherAccount[] {
  const choices: SwitcherAccount[] = []
  const savedGhLogins = new Set<string>()
  for (const conn of status.connections ?? []) {
    if (conn.source === 'gh') savedGhLogins.add(conn.login.toLowerCase())
    choices.push({
      key: `saved:${conn.id}`,
      login: conn.login,
      sourceLabel: sourceBadge(conn.source),
      detail: sourceDetail(conn.source),
      active: status.connected && status.connection_id === conn.id,
      orgs: conn.orgs,
      connectBody: { method: 'saved', connection_id: conn.id },
    })
  }
  if (status.gh_available) {
    for (const login of status.gh_accounts ?? []) {
      if (savedGhLogins.has(login.toLowerCase())) continue
      choices.push({
        key: `gh:${login}`,
        login,
        sourceLabel: 'GitHub CLI',
        detail: 'GitHub CLI on this computer',
        active: false,
        connectBody: { method: 'gh', login },
      })
    }
  }
  return choices
}

/** Calm connected view with project-repo reachability, 1-click deduplicated switcher, and folded token entry when connected. */
export function GitHubVaultCard({ workspacePath }: { workspacePath: string; changeConnectionOpen?: boolean }) {
  const [status, setStatus] = useState<GitHubStatus | null>(null)
  const [projectRepos, setProjectRepos] = useState<string[]>([])
  const [busy, setBusy] = useState(false)
  const [token, setToken] = useState('')
  const endpoint = `/api/vault/github?workspace_path=${encodeURIComponent(workspacePath)}`
  const tokenId = useId()
  const [message, setMessage] = useState<string | null>(null)
  const [failed, setFailed] = useState(false)

  const load = useCallback(async () => {
    const [statusResult, gitResult] = await Promise.allSettled([
      api.get<GitHubStatus>(endpoint),
      api.get<{ repos?: { github_repo?: string }[] }>('/api/workspace-git', { params: { workspace_path: workspacePath, op: 'status' } }),
    ])
    if (statusResult.status === 'fulfilled') {
      setStatus(statusResult.value.data)
    } else {
      setFailed(true)
      setMessage(errorText(statusResult.reason, 'Could not read your GitHub connection.'))
    }
    if (gitResult.status === 'fulfilled') {
      const seen = new Set<string>()
      const repos: string[] = []
      for (const repo of gitResult.value.data.repos ?? []) {
        const full = repo.github_repo?.trim()
        if (!full) continue
        const key = full.toLowerCase()
        if (seen.has(key)) continue
        seen.add(key)
        repos.push(full)
      }
      setProjectRepos(repos)
    } else {
      setProjectRepos([])
    }
  }, [endpoint, workspacePath])

  useEffect(() => { void load() }, [load])

  const connect = async (body: ConnectBody) => {
    setBusy(true); setMessage(null)
    try {
      const result = (await api.post<{ auth_url?: string; login?: string }>(`/api/vault/github/connect?workspace_path=${encodeURIComponent(workspacePath)}`, body)).data
      if (result.auth_url) { window.location.assign(result.auth_url); return }
      setFailed(false)
      setMessage(result.login ? `This project now uses @${result.login} for git and pull requests.` : 'GitHub account updated.')
      await load()
      window.dispatchEvent(new Event(githubConnectionChanged))
    } catch (cause) {
      setFailed(true)
      setMessage(errorText(cause, 'Could not connect GitHub.'))
    } finally {
      setToken(''); setBusy(false)
    }
  }

  const disconnect = async () => {
    setBusy(true); setMessage(null)
    try {
      const result = (await api.delete<{ message: string }>(endpoint)).data
      setFailed(false)
      setMessage(result.message)
      await load()
      window.dispatchEvent(new Event(githubConnectionChanged))
    } catch (cause) {
      setFailed(true)
      setMessage(errorText(cause, 'Could not disconnect GitHub.'))
    } finally {
      setBusy(false)
    }
  }

  if (!status) {
    return message
      ? <p role="alert" className="text-xs text-destructive">{message}</p>
      : <p role="status" className="text-xs text-muted-foreground">Checking your GitHub connection…</p>
  }

  const choices = buildGitHubAccountChoices(status)
  const orgs = status.orgs ?? []
  const local = isLocalDeployment()

  return <section aria-label="GitHub" className="space-y-3 text-sm">
    {/* Active account card */}
    <div className="space-y-3 rounded-lg border border-border p-3.5">
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="flex flex-wrap items-center gap-2">
            <span aria-hidden="true" className={`h-2 w-2 shrink-0 rounded-full ${status.connected ? 'bg-emerald-500' : 'bg-muted-foreground/50'}`} />
            <h3 className="truncate font-semibold">{status.connected && status.login ? `@${status.login}` : 'Not connected'}</h3>
            {status.connected && status.source && <span className="rounded-full border border-border bg-muted/60 px-2 py-0.5 text-[11px] text-muted-foreground">{sourceBadge(status.source)}</span>}
          </div>
          <p className="mt-1 text-xs text-muted-foreground">
            {status.connected
              ? 'Used by git, gh, and pull requests in this project.'
              : 'Choose a saved account below, or paste a token so this project can push code and open pull requests.'}
          </p>
          {status.connected && !!status.needs_approval?.length && <p className="mt-1.5 text-xs text-amber-700 dark:text-amber-300">{status.summary}</p>}
          {status.connected && status.approval_page && <a className="mt-1 inline-block text-xs underline" href={status.approval_page} target="_blank" rel="noreferrer">Open the approval page on GitHub</a>}
        </div>
        {status.connected && <Button type="button" size="sm" variant="outline" disabled={busy} onClick={() => void disconnect()}>Disconnect</Button>}
      </div>

      {status.selection_error && <p role="alert" className="text-xs text-destructive">{status.selection_error}</p>}

      {status.connected && (projectRepos.length > 0 || orgs.length > 0) && <div className="space-y-2 border-t border-border pt-3">
        {projectRepos.length > 0 && <div className="space-y-1.5">
          <p className="text-xs font-medium text-muted-foreground">Repositories in this project ({projectRepos.length})</p>
          <div className="space-y-1">
            {projectRepos.map(full => {
              const reachable = isGitHubRepoReachable(full, status.login, orgs)
              const suggested = reachable === false
                ? choices.find(choice => !choice.active && isGitHubRepoReachable(full, choice.login, choice.orgs) === true)
                : undefined
              return <div key={full} className="flex items-center justify-between gap-2 py-0.5 text-xs">
                <span className="flex min-w-0 items-center gap-2">
                  <span aria-hidden="true" className={`h-1.5 w-1.5 shrink-0 rounded-full ${reachable === false ? 'bg-amber-500' : 'bg-emerald-500'}`} />
                  <span className="truncate font-mono">{full}</span>
                </span>
                {reachable === false
                  ? suggested
                    ? <Button type="button" size="xs" variant="outline" disabled={busy} onClick={() => void connect(suggested.connectBody)}>Use @{suggested.login}</Button>
                    : <span className="shrink-0 rounded-full border border-amber-500/30 bg-amber-500/10 px-2 py-0.5 text-[11px] text-amber-700 dark:text-amber-300">Not in orgs</span>
                  : reachable === true
                    ? <span className="shrink-0 rounded-full border border-emerald-500/30 bg-emerald-500/10 px-2 py-0.5 text-[11px] text-emerald-700 dark:text-emerald-300">Reachable</span>
                    : <span className="shrink-0 text-[11px] text-muted-foreground">Token access</span>}
              </div>
            })}
          </div>
        </div>}

        {orgs.length > 0 && <details className="text-xs text-muted-foreground">
          <summary className="cursor-pointer select-none hover:text-foreground">
            Reaches {orgs.length} {orgs.length === 1 ? 'organization' : 'organizations'} &amp; your personal repos
          </summary>
          <div className="mt-2 flex flex-wrap gap-1.5">
            {orgs.map(org => <span key={org} className="rounded-full border border-border bg-muted/50 px-2 py-0.5 text-[11px]">{org}</span>)}
            {status.login && <span className="rounded-full border border-border bg-muted/50 px-2 py-0.5 text-[11px]">@{status.login}</span>}
          </div>
        </details>}
      </div>}

      {message && <p role={failed ? 'alert' : 'status'} className={`text-xs ${failed ? 'text-destructive' : 'text-emerald-700 dark:text-emerald-400'}`}>{message}</p>}
    </div>

    {/* 1-click account switcher */}
    {(choices.length > 0 || status.app_ready) && <div className="space-y-2 rounded-lg border border-border p-3.5">
      <p className="text-xs font-medium text-muted-foreground">{status.connected ? 'Switch account for this project' : 'Choose a saved account'}</p>
      {choices.length > 0 && <div className="space-y-1.5" role="radiogroup" aria-label="GitHub accounts">
        {choices.map(choice => <button key={choice.key} type="button" role="radio" aria-checked={choice.active} disabled={busy || choice.active}
          onClick={() => void connect(choice.connectBody)}
          className={`flex w-full items-center justify-between gap-3 rounded-lg border px-3 py-2 text-left transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring ${choice.active ? 'border-foreground/30 bg-muted/60' : 'border-border bg-background hover:bg-muted/40'} disabled:cursor-default`}>
          <span className="flex min-w-0 items-center gap-2.5">
            <span aria-hidden="true" className={`flex h-3.5 w-3.5 shrink-0 items-center justify-center rounded-full border ${choice.active ? 'border-foreground' : 'border-muted-foreground/50'}`}>
              {choice.active && <span className="h-1.5 w-1.5 rounded-full bg-foreground" />}
            </span>
            <span className="min-w-0">
              <span className="block truncate font-medium text-foreground">@{choice.login}</span>
              <span className="block truncate text-xs text-muted-foreground">{choice.detail}</span>
            </span>
          </span>
          <span className="shrink-0 rounded-full border border-border bg-muted/50 px-2 py-0.5 text-[11px] text-muted-foreground">{choice.active ? 'Active' : choice.sourceLabel}</span>
        </button>)}
      </div>}
      {status.gh_accounts_note && <p className="text-xs text-muted-foreground">{status.gh_accounts_note}</p>}
      {status.app_ready && <Button type="button" size="sm" variant="outline" disabled={busy} onClick={() => void connect({ method: 'oauth' })}>Sign in with GitHub in the browser</Button>}
      <p className="text-xs text-muted-foreground">Other projects keep their own GitHub account.</p>
    </div>}

    {/* Token form: folded when connected, open when disconnected */}
    <details key={status.connected ? 'connected-fold' : 'disconnected-fold'} open={!status.connected} className="group rounded-lg border border-border p-3.5">
      <summary className="flex cursor-pointer list-none items-center justify-between gap-2 text-sm font-medium">
        <span>+ Connect with a GitHub token</span>
        <ChevronRight aria-hidden="true" className="h-4 w-4 text-muted-foreground transition-transform group-open:rotate-90" />
      </summary>
      <form className="mt-3 space-y-2.5 border-t border-border pt-3" onSubmit={event => { event.preventDefault(); void connect({ method: 'pat', token }) }}>
        <ol className="list-decimal space-y-1 pl-4 text-xs text-muted-foreground">
          <li><a href="https://github.com/settings/personal-access-tokens/new" target="_blank" rel="noreferrer" className="underline">Create a token on GitHub</a>.</li>
          <li>Pick the repositories this project works on, and allow Contents and Pull requests to read and write.</li>
          <li>Paste the token here &mdash; stored encrypted and never shown again.</li>
        </ol>
        <div className="flex flex-wrap gap-2">
          <label htmlFor={tokenId} className="sr-only">GitHub token</label>
          <input id={tokenId} type="password" autoComplete="off" spellCheck={false} value={token} onChange={event => setToken(event.target.value)} placeholder="Paste a GitHub token" className="min-w-0 flex-1 rounded-md border border-border bg-background px-3 py-1.5 text-sm" disabled={busy} />
          <Button type="submit" size="sm" disabled={busy || !token.trim()}>Connect token</Button>
        </div>
      </form>
    </details>

    {/* OAuth sign-in setup is only needed on servers; local Mac uses gh CLI or tokens. */}
    {!local && <CompanySignInSetup provider="GitHub" />}
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
      .catch(cause => setNote(errorText(cause, "GitHub could not be connected. Start again from the project's GitHub panel or ask in chat.")))
  }, [])
  if (!note) return null
  return <div role="status" className="fixed right-4 top-16 z-50 flex max-w-md items-start gap-3 rounded-lg border border-border bg-popover p-3 text-sm text-popover-foreground shadow-lg">
    <span>{note}</span><button type="button" className="text-xs underline" onClick={() => setNote(null)}>Close</button>
  </div>
}
