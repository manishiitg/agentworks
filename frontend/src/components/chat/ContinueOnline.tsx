import { useEffect, useState } from 'react'
import { createPortal } from 'react-dom'
import { Cloud, Loader2 } from 'lucide-react'
import { teamError, teamRequest } from '../../products/work/teamApi'
import { sendToTab } from '../../utils/agentPurposeDraft'

/** What the Mac says would move (continue_online.go continueOnlinePlan); the dialog only shows it. */
type ContinueOnlinePlan = {
  server: string; project_name: string; new_project: boolean; chat: string; messages: number
  repos: { name: string; branch: string; changed: number; unpushed: number }[]
  files: { path: string; to: string }[]
  setup: { path: string; to: string }[]
  skipped: string[]; problem?: string; ready: boolean
}

/** The message Continue sends to this chat: its agent writes the handoff note and runs the move (#4520). */
export const CONTINUE_ONLINE_ASK = 'Continue this chat online. Write a handoff note for the online agent (the task, where it stands, decisions made, what is next, keys it needs by name) and call server_project_files action=continue_online with it. If the online agent asks for something, answer it with continue_online_answer, then tell me in one line where the chat continues.'

/** Continue online (#4520): a readiness check, then one message to this chat's agent. The Mac decides what moves. */
export function ContinueOnlineDialog({ project, chatKey, tabId, name, close }: { project: string; chatKey: string; tabId: string; name: string; close: () => void }) {
  const [plan, setPlan] = useState<ContinueOnlinePlan | null>(null)
  const [error, setError] = useState('')
  useEffect(() => {
    let live = true
    teamRequest<ContinueOnlinePlan>({ project }, `/continue-online?key=${encodeURIComponent(chatKey)}`)
      .then(result => { if (live) setPlan(result) })
      // 404: the screen reloaded with the new feature but this Mac's AgentWorks still runs the version before it.
      .catch(cause => { if (live) setError((cause as { status?: number; response?: { status?: number } })?.status === 404 || (cause as { response?: { status?: number } })?.response?.status === 404 ? 'This Mac\'s AgentWorks is running an older version. Restart AgentWorks, then try again.' : teamError(cause)) })
    return () => { live = false }
  }, [project, chatKey])
  useEffect(() => {
    const escape = (event: KeyboardEvent) => { if (event.key === 'Escape') { event.stopPropagation(); close() } }
    document.addEventListener('keydown', escape)
    return () => document.removeEventListener('keydown', escape)
  }, [close])
  const go = () => { sendToTab(tabId, CONTINUE_ONLINE_ASK); close() }
  const row = 'flex items-start justify-between gap-3 border-t border-border py-1.5'
  return createPortal(<div className="fixed inset-0 z-[100] flex items-center justify-center bg-background/70 p-4" onClick={close}>
    <section role="dialog" aria-modal="true" aria-labelledby="continue-online-title" className="w-full max-w-md max-h-[calc(100dvh-2rem)] overflow-y-auto space-y-3 rounded-xl border border-border bg-background p-5 shadow-xl" onClick={event => event.stopPropagation()}>
      <h2 id="continue-online-title" className="flex items-center gap-2 font-semibold"><Cloud aria-hidden className="h-4 w-4 shrink-0 text-primary" />Continue this chat online?</h2>
      <p className="-mt-1 text-xs text-muted-foreground">{name}</p>
      {!plan && !error && <p className="flex items-center gap-2 text-sm text-muted-foreground"><Loader2 aria-hidden className="h-3.5 w-3.5 animate-spin" />Checking what would move…</p>}
      {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
      {plan && <>
        {plan.server && <p className="text-sm">Goes to <b>{plan.project_name}</b> on {plan.server}{plan.new_project ? ': your Team has no online project yet, so a new one is made for it.' : ', your Team’s online project.'} A new agent there carries on and keeps working with your Mac closed.</p>}
        <div className="text-xs">
          <p className="pb-1 font-medium">Copied</p>
          <div className={row}><span>Conversation</span><span className="text-muted-foreground">{plan.messages} messages</span></div>
          {plan.repos.map(repo => <div key={repo.name} className={row}><span>{repo.name}</span><span className="min-w-0 truncate text-right text-muted-foreground">{[repo.changed ? `${repo.changed} changed files` : '', repo.unpushed ? `${repo.unpushed} unpushed commits` : ''].filter(Boolean).join(', ')} → branch {repo.branch}</span></div>)}
          {plan.files.length > 0 && <div className={row}><span>Files</span><span className="min-w-0 text-right text-muted-foreground">{plan.files.length} into from-mac/</span></div>}
          {plan.setup.length > 0 && <div className={row}><span>Instructions, memory, skills</span><span className="text-muted-foreground">{plan.setup.length} files</span></div>}
          <div className={row}><span>Keys</span><span className="text-right text-muted-foreground">named in the handoff note; add them to the online project’s vault</span></div>
          {plan.skipped.length > 0 && <>
            <p className="pb-1 pt-2 font-medium">Not copied</p>
            {plan.skipped.map(item => <p key={item} className="border-t border-border py-1.5 text-muted-foreground">{item}</p>)}
          </>}
        </div>
        <p className={`rounded-md p-2 text-sm ${plan.ready ? 'bg-success/10 text-success' : 'bg-warning/10 text-warning'}`}>
          {plan.ready ? 'Works with your Mac closed. This chat writes a handoff note, the online agent checks it has everything, then works on its own. This chat stays usable; anything you type here afterwards is separate.' : plan.problem}
        </p>
      </>}
      <div className="flex justify-end gap-2">
        <button type="button" className="rounded-md px-3 py-2 text-sm hover:bg-muted" onClick={close}>Cancel</button>
        <button type="button" disabled={!plan?.ready} className="rounded-md bg-primary px-3 py-2 text-sm text-primary-foreground disabled:opacity-50" onClick={go}>Continue online</button>
      </div>
    </section>
  </div>, document.body)
}
