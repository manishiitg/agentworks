import { ChevronDown, Eye } from 'lucide-react'
import { useEffect, useState } from 'react'
import axios from 'axios'
import { conditionTriggersApi, type ConditionTrigger } from '../../api/conditionTriggers'
import { useChatStore } from '../../stores/useChatStore'
import type { ReplyFileGroup } from '../../utils/replyFileChanges'
import { sendWorkspacePaneMessageToChat } from '../../utils/workspacePaneChat'

// Watching starts in the original chat. This control displays existing PR
// watches and pauses them; the agent configures requirements through Watchers.
export function PRWatcherControls({ group, sessionId, compact = false }: { compact?: boolean; group: Pick<ReplyFileGroup, 'root' | 'github_repo' | 'pr'>; sessionId: string }) {
  const [open, setOpen] = useState(false)
  const [watchers, setWatchers] = useState<ConditionTrigger[]>([])
  const [busy, setBusy] = useState(false)
  const [sent, setSent] = useState(false)
  const [error, setError] = useState('')
  const tab = useChatStore(state => Object.values(state.chatTabs).find(item => item.sessionId === sessionId))
  const projectId = tab?.metadata?.agentProfileProjectId
  const eligible = !!projectId && tab?.metadata?.agentProfileId === 'work' && group.pr?.state === 'open' && !!group.pr.number && !!group.github_repo
  const load = async () => {
    const result = await conditionTriggersApi.list(projectId!, 'work')
    return result.triggers.filter(item => item.github_pr && item.github_pr.repository === group.github_repo && item.github_pr.number === group.pr?.number && (item.github_pr.worktree ?? '') === group.root && item.github_pr.session_id === sessionId)
  }
  useEffect(() => {
    setWatchers([]); setSent(false); setError(''); setOpen(false)
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
  return <span className="relative inline-flex items-center gap-2">
    <button type="button" disabled={busy} title="Discuss what to watch in this chat" aria-label="Watch PR" className={compact ? 'inline-flex h-6 w-6 items-center justify-center rounded text-muted-foreground hover:bg-muted hover:text-foreground disabled:opacity-50' : 'text-muted-foreground underline hover:text-foreground disabled:opacity-50'} onClick={() => void ask()}>{compact ? <Eye className="h-3.5 w-3.5" /> : 'Watch PR'}</button>
    {watchers.length > 0 && <button type="button" aria-label="PR watch status" aria-expanded={open} className="inline-flex items-center gap-1 text-xs text-muted-foreground" onClick={() => setOpen(value => !value)}>{watchers.some(item => item.enabled) ? 'Watching' : 'Paused'}{watchers.length > 1 ? ` · ${watchers.length}` : ''}<ChevronDown className="h-3 w-3" /></button>}
    {open && watchers.length > 0 && <span className="absolute right-0 top-full z-50 mt-2 flex w-72 flex-col gap-2 rounded-md border border-border bg-background p-3 text-xs shadow-lg" role="group" aria-label="PR watch status">
      {watchers.map(item => <span key={item.id} className="flex items-center gap-2">
        <span className="min-w-0 flex-1 truncate" title={item.name}>{item.name}</span><span className="text-muted-foreground">{item.enabled ? 'Watching' : 'Paused'}</span>
        {item.enabled && <button type="button" disabled={busy} className="underline" onClick={() => void stop(item)}>Stop</button>}
      </span>)}
    </span>}
    {sent && <span role="status" className="text-xs text-muted-foreground">Sent to chat</span>}
    {error && <span role="alert" className="text-xs text-destructive">{error}</span>}
  </span>
}
