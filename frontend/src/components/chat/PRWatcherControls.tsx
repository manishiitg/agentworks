import { useState } from 'react'
import axios from 'axios'
import { getApiBaseUrl, getAuthToken } from '../../services/api'
import { conditionTriggersApi, type ConditionTrigger } from '../../api/conditionTriggers'
import { useChatStore } from '../../stores/useChatStore'
import type { ReplyFileGroup } from '../../utils/replyFileChanges'

// These are ordinary Watchers. The server selects the account, originating
// conversation and PR head; the browser only sends the person's choice.
export function PRWatcherControls({ group, sessionId }: { group: Pick<ReplyFileGroup, 'root' | 'github_repo' | 'pr'>; sessionId: string }) {
  const [open, setOpen] = useState(false)
  const [mode, setMode] = useState('checks')
  const [reviewBots, setReviewBots] = useState('')
  const [watchers, setWatchers] = useState<ConditionTrigger[]>([])
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const tab = useChatStore(state => Object.values(state.chatTabs).find(item => item.sessionId === sessionId))
  const projectId = tab?.metadata?.agentProfileProjectId
  if (!projectId || tab?.metadata?.agentProfileId !== 'work' || group.pr?.state !== 'open' || !group.pr.number || !group.github_repo) return null
  const refresh = async () => {
    const result = await conditionTriggersApi.list(projectId, 'work', true)
    setWatchers(result.triggers.filter(item => item.github_pr && item.github_pr.repository === group.github_repo && item.github_pr.number === group.pr?.number && (item.github_pr.worktree ?? '') === group.root && item.github_pr.session_id === sessionId))
  }
  const showError = (e: unknown) => setError(axios.isAxiosError(e) && typeof e.response?.data === 'string' ? e.response.data : 'Could not update this PR watch. Try again.')
  const toggle = async () => {
    setOpen(value => !value)
    if (!open) { try { await refresh(); setError('') } catch (e) { showError(e) } }
  }
  const create = async () => {
    setBusy(true)
    try {
      const token = getAuthToken()
      await axios.post('/api/condition-triggers', {
        action: 'create', project_id: projectId, profile_id: 'work', request_id: crypto.randomUUID(),
        trigger: {
          name: `${mode === 'autofix' ? 'Auto-fix' : mode === 'deployment' ? 'Watch deployment' : 'Watch checks'} · ${group.github_repo} #${group.pr?.number}`,
          github_pr: { repository: group.github_repo, number: group.pr?.number, mode, worktree: group.root,
            review_bots: mode === 'autofix' ? reviewBots.split(',').map(value => value.trim()).filter(Boolean) : [] },
          target: { kind: 'agent', message: 'Report the PR result in this chat.' },
        },
      }, { baseURL: getApiBaseUrl(), headers: { ...(token ? { Authorization: `Bearer ${token}` } : {}), 'X-Session-ID': sessionId } })
      await refresh(); setError('')
    } catch (e) { showError(e) } finally { setBusy(false) }
  }
  const stop = async (watcher: ConditionTrigger) => {
    setBusy(true)
    try { await conditionTriggersApi.manage(projectId, 'update', watcher.id, { enabled: false }, watcher.revision, 'work'); await refresh(); setError('') }
    catch (e) { showError(e) } finally { setBusy(false) }
  }
  return <span className="relative">
    <button type="button" className="text-muted-foreground underline hover:text-foreground" onClick={() => void toggle()} aria-expanded={open}>Watch PR</button>
    {open && <div className="mt-2 flex max-w-md flex-col gap-2 rounded-md border border-border bg-background p-3 text-xs" role="group" aria-label="Watch pull request">
      {watchers.map(item => <div key={item.id} className="flex flex-wrap items-center gap-2">
        <span>{item.github_pr?.mode === 'autofix' ? 'Auto-fix' : item.github_pr?.mode === 'deployment' ? 'Deployment' : 'Checks'} · {item.completed_at ? 'Done' : item.enabled ? 'Watching' : 'Paused'} · {item.github_pr?.login}</span>
        {item.enabled && !item.completed_at && <button type="button" disabled={busy} className="underline" onClick={() => void stop(item)}>Stop</button>}
      </div>)}
      <label className="flex items-center gap-2">When GitHub updates
        <select aria-label="PR watch mode" value={mode} onChange={event => setMode(event.target.value)} className="rounded border border-border bg-background p-1">
          <option value="checks">Tell me when checks finish</option>
          <option value="deployment">Tell me when deployment finishes</option>
          <option value="autofix">Fix CI failures and review findings</option>
        </select>
      </label>
      {mode === 'autofix' ? <>
        <p className="text-muted-foreground">Fix, check and push in this chat’s worktree. Keeps watching new commits until the PR closes. Merging stays with you.</p>
        <label>Also accept findings from these review bots
          <input aria-label="Chosen review bot logins" value={reviewBots} onChange={event => setReviewBots(event.target.value)} placeholder="e.g. chatgpt-codex-connector[bot]" className="mt-1 w-full rounded border border-border bg-background p-2" />
        </label>
        <p className="text-muted-foreground">Repository collaborators are included. Bot logins are optional; separate them with commas.</p>
      </> : <p className="text-muted-foreground">Checks the current PR commit every minute, even for long runs. Sends one result here, then finishes.</p>}
      <button type="button" disabled={busy || watchers.some(item => item.github_pr?.mode === mode && item.enabled && !item.completed_at)} className="self-start rounded border border-border px-2 py-1 hover:bg-muted disabled:opacity-50" onClick={() => void create()}>{busy ? 'Saving…' : mode === 'autofix' ? 'Start auto-fix' : 'Start watch'}</button>
      {error && <p role="alert" className="text-destructive">{error}</p>}
    </div>}
  </span>
}
