import { Avatar } from '../ui/Avatar'
import { createContext, useContext, useEffect, useId, useRef, useState, type ReactNode } from 'react'
import { createPortal } from 'react-dom'
import { desktopRequest } from '../../services/desktopConnections'
import { answerTeamInvite, useTeamInvites, type TeamInvite } from '../../products/work/teamApi'
import { openSidebarProject } from './SidebarServerGroup'
import { hasDesktopConnections } from '../../stores/useConnectedWorkStore'
import { getRawActiveWorkspaceId, useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'
import { useAuthStore } from '../../stores/useAuthStore'
import { useTeamInviteNotice } from './teamInviteNotice'

const inviteKey = (invite: TeamInvite) => `${invite.connection || ''}:${invite.project_id}`

const TeamInvitesContext = createContext<TeamInvite[]>([])

// The app shell owns observation and the portal, even while mobile navigation
// is unmounted. A new account/network starts with a fresh in-memory notice.
export function TeamInviteProvider({ children }: { children: ReactNode }) {
  const person = useAuthStore(state => state.user?.id)
  const workspace = useWorkspaceConnectionStore(state => state.activeWorkspaceId)
  return <ScopedTeamInvites key={JSON.stringify([person, workspace])}>{children}</ScopedTeamInvites>
}
function ScopedTeamInvites({ children }: { children: ReactNode }) {
  const invites = useTeamInvites()
  const [minimized, setMinimized] = useState<Record<string, true>>({})
  const count = invites.length
  useEffect(() => {
    useTeamInviteNotice.setState({ count })
    return () => useTeamInviteNotice.setState({ count: 0 })
  }, [count])
  useEffect(() => {
    const pending = new Set(invites.map(inviteKey))
    setMinimized(current => Object.keys(current).some(key => !pending.has(key))
      ? Object.fromEntries(Object.entries(current).filter(([key]) => pending.has(key))) : current)
  }, [invites])
  const waiting = invites.filter(invite => !minimized[inviteKey(invite)])
  const minimize = () => {
    console.info('[TEAM_INVITE_UI]', { action: 'minimized', pending_count: count })
    setMinimized(current => ({ ...current, ...Object.fromEntries(waiting.map(invite => [inviteKey(invite), true as const])) }))
  }
  return <TeamInvitesContext.Provider value={invites}>
    {children}
    {waiting.length > 0 && <TeamInvitePopup key={inviteKey(waiting[0])} invite={waiting[0]} count={waiting.length} minimize={minimize} />}
  </TeamInvitesContext.Provider>
}

/** Cards only; they use the shell's same pending list and acceptance component. */
export function TeamInvites() {
  const invites = useContext(TeamInvitesContext)
  return invites.length > 0 ? <div aria-label="Team invites" className="shrink-0 space-y-1 px-2 pb-2">{invites.map(invite => <TeamInviteRow key={inviteKey(invite)} invite={invite} />)}</div> : null
}

function TeamInvitePopup({ invite, count, minimize }: { invite: TeamInvite; count: number; minimize: () => void }) {
  const dialog = useRef<HTMLElement>(null)
  useEffect(() => {
    const previous = document.activeElement instanceof HTMLElement ? document.activeElement : null
    const shell = document.querySelector<HTMLElement>('[data-app-shell]')
    const wasInert = shell?.inert
    if (shell) shell.inert = true
    dialog.current?.querySelector<HTMLButtonElement>('button')?.focus()
    console.info('[TEAM_INVITE_UI]', { action: 'shown', project: invite.project_id, connection: invite.connection || 'current' })
    return () => { if (shell) shell.inert = Boolean(wasInert); if (previous?.isConnected) previous.focus() }
  }, [invite.connection, invite.project_id])
  return createPortal(<div className="fixed inset-0 z-[100] flex items-center justify-center bg-black/30 p-4">
    <section ref={dialog} role="dialog" aria-modal="true" aria-labelledby="team-invite-title" onKeyDown={event => {
      if (event.key === 'Escape') { event.preventDefault(); event.stopPropagation(); minimize() }
      if (event.key === 'Tab') {
        const controls = [...(dialog.current?.querySelectorAll<HTMLElement>('button:not(:disabled), input:not(:disabled), select:not(:disabled), [tabindex="0"]') || [])]
        const first = controls[0], last = controls[controls.length - 1]
        if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last?.focus() }
        else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first?.focus() }
      }
    }} className="max-h-[calc(100dvh-2rem)] w-full max-w-md overflow-y-auto rounded-xl border border-border bg-background p-5 shadow-xl">
      <div className="mb-3 flex items-start justify-between gap-3">
        <div>
          <h2 id="team-invite-title" className="font-semibold">Join the {invite.name} team?</h2>
          {count > 1 && <p className="text-xs text-muted-foreground">Invite 1 of {count}</p>}
        </div>
        <button type="button" onClick={minimize} title="Keep it in the left menu" className="shrink-0 rounded-md px-2 py-1 text-xs text-muted-foreground hover:bg-muted">Minimize</button>
      </div>
      <TeamInviteRow invite={invite} />
    </section>
  </div>, document.body)
}

/** A Team invite (owner, 10 Nov): accepted in the Mac app it is always added to this Mac (its shared folder syncs
 * here, through the server the invite was listed from); accepted on the website it joins on the server. One action
 * each, and Decline. The server says whether the Crew can be added to a Mac. */
export function TeamInviteRow({ invite }: { invite: TeamInvite }) {
  const radioGroup = useId()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  // "Add to my Mac": the folder the shared files sync to, ~/AgentWorks/<Crew>/team unless the person picks another.
  const [folder, setFolder] = useState('')
  const [projects, setProjects] = useState<JoinProject[]>([])
  const [target, setTarget] = useState('new')
  const [selectedAgents, setSelectedAgents] = useState<string[]>([])
  const [instructionsNotice, setInstructionsNotice] = useState('')
  const chosen = projects.find(project => project.project_id === target)
  // Every agent of the chosen project starts In team (owner, 15 Nov); the person unticks the ones to keep private.
  const pick = (project: string) => { setTarget(project); setSelectedAgents(projects.find(item => item.project_id === project)?.agents.filter(agent => agent.in_team).map(agent => agent.tab) || []) }
  // The network this Mac reaches the invite's server through: the one the invite was listed from, or the selected
  // workspace (a server this Mac already added), so nothing new is asked.
  const activeWorkspace = getRawActiveWorkspaceId()
  const connection = invite.connection || (activeWorkspace !== 'local' ? activeWorkspace : undefined)
  const inMacApp = hasDesktopConnections()
  const canAdd = invite.can_add_to_mac === true && Boolean(connection)
  const join = connection ? `/${encodeURIComponent(connection)}/crew-teams/${encodeURIComponent(invite.project_id)}/join` : ''
  const log = (action: string, reason?: string) => console.info('[TEAM_INVITE_UI]', { action, project: invite.project_id, connection: connection || 'current', ...(reason ? { reason } : {}) })
  const chooseFolder = async () => {
    setBusy(true); setError('')
    try {
      const options = await desktopRequest<JoinOptions>(`${join}?name=${encodeURIComponent(invite.name)}`)
      log('options_loaded'); setFolder(options.folder); setProjects(options.projects); setInstructionsNotice(options.instructions_notice)
    }
    catch (cause) { log('failed', 'join_options_failed'); setError(cause instanceof Error ? cause.message : 'This Mac could not suggest a folder.') } finally { setBusy(false) }
  }
  const addToMac = async () => {
    setBusy(true); setError('')
    try {
      await desktopRequest(join, 'POST', target === 'new' ? { folder } : { local_project: target, agents: selectedAgents, confirm: true })
      log('accepted'); window.dispatchEvent(new CustomEvent('agentworks:team-updated'))
    }
    catch (cause) { log('failed', 'join_failed'); setError(cause instanceof Error ? cause.message : 'The team could not be added to this Mac. Try again.') } finally { setBusy(false) }
  }
  const answer = async (accept: boolean) => {
    setBusy(true); setError('')
    try {
      await answerTeamInvite(invite, accept)
      log(accept ? 'accepted' : 'declined')
      if (accept && !invite.connection) openSidebarProject('crew', invite.project_id)
    } catch (cause) { log('failed', 'answer_failed'); setError(cause instanceof Error ? cause.message : 'The invite could not be answered. Try again.') } finally { setBusy(false) }
  }
  const line = !inMacApp ? 'Join to open this team here. You can also add its shared folder to your Mac later.'
    : invite.can_add_to_mac !== true ? "This team has no shared folder, so it can't be added to this Mac."
    : !connection ? "Add this team's network to your Mac first (Servers), then try again."
      : 'Add it to this Mac: its shared folder syncs here and you can chat with its shared agents.'
  return <div role="group" aria-label={`Team invite: ${invite.name}`} className="rounded-lg border border-border px-3 py-2 text-[13px]">
    <p className="flex items-center gap-2 leading-5"><Avatar {...invite} name={invite.owner_name} connection={connection} /><span><b className="font-medium">{invite.owner_name}</b> invited you to the {invite.name} team.</span></p>
    <p className="text-[11px] leading-4 text-muted-foreground">{line}</p>
    <div className="mt-1.5 flex flex-wrap gap-1.5">
      {!folder && (!inMacApp || canAdd) && <button type="button" disabled={busy} onClick={() => void (inMacApp ? chooseFolder() : answer(true))} className="rounded-full border border-border bg-background px-2.5 py-0.5 text-xs shadow-sm hover:bg-muted">{inMacApp ? 'Add to my Mac' : 'Join'}</button>}
      {!folder && <button type="button" disabled={busy} onClick={() => void answer(false)} className="rounded-full px-2.5 py-0.5 text-xs text-muted-foreground hover:bg-muted">Decline</button>}
    </div>
    {folder && <div className="mt-1.5 space-y-1.5 text-[11px] text-muted-foreground">
      <label className="flex items-center gap-2"><input type="radio" name={`join-${radioGroup}`} checked={target === 'new'} onChange={() => { setTarget('new'); setSelectedAgents([]) }} disabled={busy} />New project</label>
      <label className="flex items-center gap-2"><input type="radio" name={`join-${radioGroup}`} checked={target !== 'new'} disabled={busy || !projects.some(project => project.can_join)} onChange={() => pick(projects.find(project => project.can_join)!.project_id)} />Use an existing project</label>
      {projects.length > 0 && <select aria-label="Existing project" value={target === 'new' ? '' : target} disabled={busy} onChange={event => pick(event.target.value)} className="w-full rounded border border-border bg-background px-2 py-1 text-foreground">
        <option value="" disabled>Choose a project</option>
        {projects.map(project => <option key={project.project_id} value={project.project_id} disabled={!project.can_join}>{project.name}{project.reason ? ` — ${project.reason}` : ''}</option>)}
      </select>}
      {target === 'new' ? <p className="break-all">The shared folder goes to <span className="text-foreground">{folder}</span></p> : chosen && <>
        <p>{instructionsNotice}</p>
        <p>Your other folders stay private. Untick an agent to keep it private too.</p>
        <fieldset disabled={busy} className="space-y-1"><legend>Agents to share with the Team</legend>
          {chosen.agents.map(agent => <label key={agent.tab} className="flex items-center gap-2"><input type="checkbox" checked={selectedAgents.includes(agent.tab)} onChange={event => setSelectedAgents(current => event.target.checked ? [...current, agent.tab] : current.filter(tab => tab !== agent.tab))} />{agent.name}</label>)}
        </fieldset>
      </>}
      {!!invite.folder?.folders?.length && <p>Shared folders: {invite.folder.folders.map(entry => `${entry.path}/`).join(', ')}. Same-name folders are kept beside yours.</p>}
      <div className="flex flex-wrap gap-1.5">
        <button type="button" disabled={busy || (target !== 'new' && !chosen?.can_join)} onClick={() => void addToMac()} className="rounded-full border border-border bg-background px-2.5 py-0.5 text-xs text-foreground shadow-sm hover:bg-muted">OK</button>
        {target === 'new' && window.electronAPI?.pickWorkflowFolder && <button type="button" disabled={busy} onClick={async () => { const picked = await window.electronAPI?.pickWorkflowFolder?.(); if (picked) setFolder(picked) }} className="rounded-full px-2.5 py-0.5 text-xs hover:bg-muted">Choose another folder</button>}
        <button type="button" disabled={busy} onClick={() => void answer(false)} className="rounded-full px-2.5 py-0.5 text-xs hover:bg-muted">Decline</button>
      </div>
    </div>}
    {error && <p role="alert" className="mt-1 text-[11px] text-destructive">{error}</p>}
  </div>
}

type JoinProject = { project_id: string; name: string; can_join: boolean; reason?: string; agents: { tab: string; name: string; in_team:boolean }[] }
type JoinOptions = { folder: string; projects: JoinProject[]; instructions_notice: string }
