import { useState } from 'react'
import { Laptop, Globe } from 'lucide-react'
import { desktopRequest } from '../../services/desktopConnections'
import { teamRequest, useProjectTeam } from './teamApi'

/** One working copy per Team member (owner, 5 Nov): a member's own agents in a Crew work either on the website or on
 * their Mac. The server says which (team.where); in the other place the Crew is one plain screen with one button that
 * moves the working copy there. */
export function useCrewWorksElsewhere(projectId: string) {
  const { team, target } = useProjectTeam(projectId)
  const onMacApp = Boolean(target?.connection)
  const elsewhere: 'mac' | 'web' | undefined = !team?.where ? undefined
    : onMacApp && team.where === 'web' ? 'web'
    : !onMacApp && team.where === 'mac' ? 'mac'
    : undefined
  return { elsewhere, team, target }
}

export function CrewWorksElsewhere({ projectId }: { projectId: string }) {
  const { elsewhere, target } = useCrewWorksElsewhere(projectId)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  if (!elsewhere || !target) return null
  const move = async () => {
    setBusy(true); setError('')
    try {
      if (elsewhere === 'mac') await teamRequest(target, '/where', 'POST', { where: 'web' })
      else await desktopRequest(`/${encodeURIComponent(target.connection || '')}/crew-teams/${encodeURIComponent(target.project)}/add-to-mac`, 'POST')
      window.dispatchEvent(new CustomEvent('agentworks:team-updated'))
    } catch (e) { setError(e instanceof Error ? e.message : 'Could not move it. Try again.') } finally { setBusy(false) }
  }
  const Icon = elsewhere === 'mac' ? Laptop : Globe
  return <div className="flex h-full min-h-0 flex-1 items-center justify-center p-6">
    <div className="max-w-sm text-center">
      <Icon aria-hidden="true" className="mx-auto mb-3 h-6 w-6 text-muted-foreground" />
      <h2 className="text-base font-semibold">{elsewhere === 'mac' ? 'This Crew works on your Mac' : 'This Crew works on the website'}</h2>
      <p className="mt-1 text-sm text-muted-foreground">{elsewhere === 'mac'
        ? 'Your agents here answer on your Mac. Open it in the AgentWorks app on your Mac to use them.'
        : 'Your agents here answer on the website now. Open it there to use them.'}</p>
      <button type="button" disabled={busy} onClick={() => void move()} className="mt-4 rounded-md border border-border px-3 py-1.5 text-sm font-medium hover:bg-muted disabled:opacity-50">
        {elsewhere === 'mac' ? 'Use it on the web instead' : 'Add to this Mac instead'}
      </button>
      {error && <p role="alert" className="mt-2 text-xs text-destructive">{error}</p>}
    </div>
  </div>
}

/** On the Mac, in a member's working copy: where it works and the way back to the website. */
export function CrewWhereRow({ projectId }: { projectId: string }) {
  const { team, target } = useProjectTeam(projectId)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  if (team?.where !== 'mac' || !target?.connection) return null
  const back = async () => {
    setBusy(true); setError('')
    try {
      await desktopRequest(`/${encodeURIComponent(target.connection || '')}/crew-teams/${encodeURIComponent(target.project)}/remove-from-mac`, 'POST')
      window.dispatchEvent(new CustomEvent('agentworks:team-updated'))
    } catch (e) { setError(e instanceof Error ? e.message : 'Could not move it. Try again.') } finally { setBusy(false) }
  }
  return <div className="mt-4 flex items-center gap-2 rounded-md border border-border px-3 py-2 text-[13px]">
    <Laptop aria-hidden="true" className="h-4 w-4 shrink-0 text-muted-foreground" />
    <span className="min-w-0 flex-1"><span className="font-medium">On this Mac</span> <span className="text-muted-foreground">· your agents here work on this Mac</span></span>
    <button type="button" disabled={busy} onClick={() => void back()} className="shrink-0 rounded-full border border-border px-2.5 py-0.5 text-xs hover:bg-muted disabled:opacity-50">Use it on the web instead</button>
    {error && <span role="alert" className="text-xs text-destructive">{error}</span>}
  </div>
}
