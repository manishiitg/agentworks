import { FolderSync } from 'lucide-react'
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
  // One line (owner, 25 Nov): icon with the state dot, "Team files", then size and the one state word on the right.
  // Who changed what, and the full line, are the tooltip; Team › Sync has the detail. An older Mac app sends no word yet,
  // so its flags pick the word until it updates.
  const word = !state ? 'Checking…' : state.stale || state.word || (state.problem ? 'Stopped' : state.attention ? 'Needs a look' : state.progress ? 'Syncing' : 'Synced')
  const changed = sync?.changed_by ? `Changed by ${sync.changed_by}${sync.changed_ago ? ` · ${sync.changed_ago}` : ''}` : ''
  const hint = [state?.line, changed].filter(Boolean).join('\n') || 'Checking…'
  const warn = !!state && (state.problem || state.attention || !!state.stale)
  return <section aria-label="Team files">
    <button type="button" onClick={() => openCrewAccess(projectId, '', 'team', 'sync')} title={hint} className="flex min-h-8 w-full min-w-0 items-center gap-2.5 rounded-md px-1 py-1.5 text-left text-xs text-foreground/85 hover:bg-background/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
      <span aria-hidden="true" className="relative inline-flex h-3.5 w-3.5 shrink-0">
        <FolderSync className="h-3.5 w-3.5 text-muted-foreground" />
        <span className={`absolute -bottom-0.5 -right-0.5 text-[7px] leading-none ${tone}`}>{state?.progress ? (state.progress.direction === 'upload' ? '↑' : '↓') : '●'}</span>
      </span>
      <span className="shrink-0">Team files</span>
      <span role="status" className="min-w-0 flex-1 truncate text-right text-[10px] text-muted-foreground">
        {sync?.size && <>{sync.size} · </>}<span className={warn ? (state?.problem ? 'text-destructive' : 'text-amber-600 dark:text-amber-400') : state?.progress ? 'text-primary' : undefined}>{word}</span>
      </span>
    </button>
  </section>
}
