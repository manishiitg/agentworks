import { ChevronDown, Eye } from 'lucide-react'
import { useEffect, useState } from 'react'
import axios from 'axios'
import { conditionTriggersApi, type ConditionTrigger } from '../../api/conditionTriggers'
import { useChatStore } from '../../stores/useChatStore'
import type { ReplyFileGroup } from '../../utils/replyFileChanges'
import { sendWorkspacePaneMessageToChat } from '../../utils/workspacePaneChat'

// Watch & fix (#4323): one click starts the server's PR Watcher for this chat. It wakes the chat to fix failing
// checks, conflicts, an out-of-date branch and review comments until the PR is merge-ready; it never merges
// (the person or their team does). The menu also offers talking it through first (custom watches).
export function PRWatcherControls({ group, sessionId }: { compact?: boolean; group: Pick<ReplyFileGroup, 'root' | 'github_repo' | 'pr'>; sessionId: string }) {
  const [watchers, setWatchers] = useState<ConditionTrigger[]>([])
  const [busy, setBusy] = useState(false)
  const [sent, setSent] = useState(false)
  const [menu, setMenu] = useState(false)
  const [error, setError] = useState('')
  const tab = useChatStore(state => Object.values(state.chatTabs).find(item => item.sessionId === sessionId))
  const projectId = tab?.metadata?.agentProfileProjectId
  const eligible = !!projectId && tab?.metadata?.agentProfileId === 'work' && group.pr?.state === 'open' && !!group.pr.number && !!group.github_repo
  const load = async () => {
    const result = await conditionTriggersApi.list(projectId!, 'work')
    return result.triggers.filter(item => item.github_pr && item.github_pr.repository === group.github_repo && item.github_pr.number === group.pr?.number && (item.github_pr.worktree ?? '') === group.root && item.github_pr.session_id === sessionId)
  }
  useEffect(() => {
    setWatchers([]); setSent(false); setError(''); setMenu(false)
    if (!eligible) return
    let live = true
    const refresh = () => {
      void load().then(items => { if (live) { setWatchers(items); setError('') } }).catch(() => { if (live) setError('Could not load PR watches. Try again.') })
    }
    refresh()
    const timer = window.setInterval(() => { if (document.visibilityState !== 'hidden') refresh() }, 60_000)
    window.addEventListener('focus', refresh)
    return () => { live = false; window.clearInterval(timer); window.removeEventListener('focus', refresh) }
  }, [eligible, projectId, sessionId, group.root, group.github_repo, group.pr?.number])
  if (!eligible) return null
  const showError = (e: unknown) => setError(axios.isAxiosError(e) && typeof e.response?.data === 'string' ? e.response.data : e instanceof Error ? e.message : 'Could not update this PR watch. Try again.')
  const ask = async () => {
    setBusy(true); setError(''); setSent(false)
    try {
      // Resolve again at click time: a retained card must never select a different chat.
      const original = Object.values(useChatStore.getState().chatTabs).find(item => item.sessionId === sessionId && item.metadata?.agentProfileProjectId === projectId && item.metadata.agentProfileId === 'work')
      if (!original) throw new Error('Open the original chat to set up this watch.')
      const url = group.pr?.url || `https://github.com/${group.github_repo}/pull/${group.pr?.number}`
      const message = `Help me set up background watching for PR #${group.pr?.number} in ${group.github_repo}: ${url}\nWorking folder: ${group.root || 'project root'}.\n\nUse what we’ve already discussed to decide what to monitor and what should happen; ask me if either is unclear. There may be several conditions or watchers. Check existing watches first, then create or update them through Watchers and confirm the conditions, actions and when they stop. Keep the chat free while waiting.`
      await sendWorkspacePaneMessageToChat({ tabId: original.tabId, message })
      setSent(true)
    } catch (e) { showError(e) } finally { setBusy(false) }
  }
  const stop = async (watcher: ConditionTrigger) => {
    setBusy(true)
    try { await conditionTriggersApi.manage(projectId!, 'update', watcher.id, { enabled: false }, watcher.revision, 'work'); setWatchers(await load()); setError('') }
    catch (e) { showError(e) } finally { setBusy(false) }
  }
  const start = async () => {
    setBusy(true); setError(''); setMenu(false)
    try { await conditionTriggersApi.watchPR(projectId!, sessionId, { repository: group.github_repo!, number: group.pr!.number!, worktree: group.root || undefined }, 'ready'); setWatchers(await load()) }
    catch (e) { showError(e) } finally { setBusy(false) }
  }
  const active = watchers.filter(item => item.enabled)
  const readyWatch = active.some(item => item.github_pr?.mode === 'ready')
  const iconBtn = 'inline-flex h-6 shrink-0 items-center gap-1 whitespace-nowrap px-1.5 text-xs text-muted-foreground hover:bg-muted hover:text-foreground disabled:opacity-50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring'
  return <span className="relative inline-flex items-center gap-2">
    {active.length === 0
      ? <span className="inline-flex items-center rounded-md border border-border">
        <button type="button" disabled={busy} data-testid="pr-watch-fix" title="Fix failing checks, conflicts and review comments until the PR is ready to merge. It never merges: you or your team do." className={`${iconBtn} rounded-l-md`} onClick={() => void start()}><Eye className="h-3.5 w-3.5" aria-hidden="true" />Watch &amp; fix</button>
        <button type="button" disabled={busy} aria-label="More watch options" aria-expanded={menu} className={`${iconBtn} rounded-r-md border-l border-border px-1`} onClick={() => setMenu(value => !value)}><ChevronDown className="h-3 w-3" aria-hidden="true" /></button>
      </span>
      : <span className="inline-flex items-center gap-1 text-xs">
        <span data-testid="pr-watch-status" className="inline-flex items-center gap-1.5 rounded-full bg-primary/10 px-2 py-0.5 text-primary"><span className="h-1.5 w-1.5 animate-pulse rounded-full bg-current" aria-hidden="true" />{readyWatch ? 'Watching · until merge-ready' : 'Watching · fixing'}</span>
        <button type="button" disabled={busy} className="rounded px-1.5 text-muted-foreground hover:bg-muted hover:text-foreground" onClick={() => void Promise.all(active.map(stop))}>Stop</button>
      </span>}
    {menu && <span className="absolute left-0 top-full z-50 mt-1 flex w-72 flex-col rounded-lg border border-border bg-background p-1 text-left text-xs shadow-lg" role="menu">
      <button type="button" role="menuitem" className="rounded-md px-2 py-1.5 text-left hover:bg-muted" onClick={() => void start()}><span className="block text-sm text-foreground">Watch &amp; fix until merge-ready</span><span className="text-muted-foreground">Fix checks, conflicts and review comments; tells you when it's ready. You merge.</span></button>
      <span className="my-1 border-t border-border" />
      <button type="button" role="menuitem" className="rounded-md px-2 py-1.5 text-left hover:bg-muted" onClick={() => { setMenu(false); void ask() }}><span className="block text-sm text-foreground">Talk it through first…</span><span className="text-muted-foreground">Ask the agent what to watch.</span></button>
    </span>}
    {sent && <span role="status" className="text-xs text-muted-foreground">Sent to chat</span>}
    {error && <span role="alert" className="text-xs text-destructive">{error}</span>}
  </span>
}
