import { useLocalSync } from './teamLocalSync'
import { openCrewAccess } from './MacSharedAgents'
import type { TeamTabSync, TeamTarget } from './teamApi'

/** The Team folder's one sync row in the right panel's Team section: "Team files" with one status word on the right
 * (Synced, Syncing 40%, Needs a look, Stopped) and "Changed by <name> · <when>" under it. The server writes every word:
 * this Mac's own copy in the Mac app, the server's state on the website; the full line is the tooltip and Team › Sync.
 * Red = stopped, amber = needs a look, a bar while files move. The row opens Team › Sync. */
export function TeamFilesRow({ projectId, target, sync, enabled }: { projectId: string; target?: TeamTarget; sync?: TeamTabSync; enabled: boolean }) {
  const onMac = !!target?.connection
  const local = useLocalSync(target, enabled && onMac)
  if (!enabled) return null
  const state = onMac
    ? local && { line: local.line || '', word: local.state, stale: local.stale, problem: !!local.problem, attention: !!local.attention, progress: local.progress || undefined }
    : sync && { line: sync.line, word: sync.state, stale: sync.stale, problem: !!sync.problem, attention: !!sync.attention, progress: sync.progress }
  if (!onMac && !state) return null
  const tone = !state ? 'text-muted-foreground' : state.problem ? 'text-destructive' : state.attention ? 'text-amber-600 dark:text-amber-400' : state.progress ? 'text-primary' : 'text-emerald-600 dark:text-emerald-400'
  const percent = state?.progress && state.progress.bytes_total > 0 ? Math.min(100, Math.round(state.progress.bytes_done * 100 / state.progress.bytes_total)) : 0
  const line = state?.line || 'Checking…'
  const word = state ? state.word || state.line : 'Checking…'
  return <section aria-label="Team files" className="border-t border-border pt-0.5">
    <button type="button" onClick={() => openCrewAccess(projectId, '', 'team', 'sync')} title={line} className="flex h-6 w-full min-w-0 items-center gap-1.5 rounded px-1 text-left text-[11px] hover:bg-background/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
      <span aria-hidden="true" className={`w-3.5 shrink-0 text-center ${tone}`}>{state?.progress ? (state.progress.direction === 'upload' ? '↑' : '↓') : '●'}</span>
      <span className="min-w-0 flex-1 truncate font-medium">Team files</span>
      <span role="status" className={`max-w-[60%] shrink-0 truncate ${state?.problem || state?.attention ? tone : 'text-muted-foreground'}`}>{word}</span>
    </button>
    {state?.progress && <div role="progressbar" aria-label="Sync progress" aria-valuemin={0} aria-valuemax={100} aria-valuenow={percent} className="mb-1 ml-6 mr-1 h-0.5 overflow-hidden rounded-full bg-muted"><div className="h-full rounded-full bg-primary transition-[width] duration-500" style={{ width: `${percent}%` }} /></div>}
    {state?.stale && !state.progress && <p className="truncate pb-0.5 pl-6 pr-1 text-[10px] text-amber-600 dark:text-amber-400">{state.stale}</p>}
    {sync?.changed_by && !state?.progress && <p className="flex min-w-0 pb-0.5 pl-6 pr-1 text-[10px] text-muted-foreground" title={sync.last}>
      <span className="min-w-0 truncate">Changed by {sync.changed_by}</span>
      {sync.changed_ago && <span className="shrink-0 whitespace-pre"> · {sync.changed_ago}</span>}
    </p>}
  </section>
}
