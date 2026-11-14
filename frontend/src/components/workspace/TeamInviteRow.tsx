import { useState } from 'react'
import { desktopRequest } from '../../services/desktopConnections'
import { answerTeamInvite, useTeamInvites, type TeamInvite } from '../../products/work/teamApi'
import { openSidebarProject } from './SidebarServerGroup'
import { hasDesktopConnections } from '../../stores/useConnectedWorkStore'
import { getRawActiveWorkspaceId } from '../../stores/useWorkspaceConnectionStore'

/** Team invites, shown above the Crews (owner, 5 Nov: "we only have crews"); once accepted, the Crew is in the list. */
export function useTeamInviteRows() {
  return useTeamInvites().map(invite => <TeamInviteRow key={`${invite.connection || ''}:${invite.project_id}`} invite={invite} />)
}

/** A Team invite (owner, 10 Nov): accepted in the Mac app it is always added to this Mac (its shared folder syncs
 * here, through the server the invite was listed from); accepted on the website it joins on the server. One action
 * each, and Decline. The server says whether the Crew can be added to a Mac. */
export function TeamInviteRow({ invite }: { invite: TeamInvite }) {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  // "Add to my Mac": the folder the shared files sync to, ~/AgentWorks/<Crew>/team unless the person picks another.
  const [folder, setFolder] = useState('')
  const [projects, setProjects] = useState<JoinProject[]>([])
  const [target, setTarget] = useState('new')
  const [selectedAgents, setSelectedAgents] = useState<string[]>([])
  const [instructionsNotice, setInstructionsNotice] = useState('')
  const chosen = projects.find(project => project.project_id === target)
  // The network this Mac reaches the invite's server through: the one the invite was listed from, or the selected
  // workspace (a server this Mac already added), so nothing new is asked.
  const activeWorkspace = getRawActiveWorkspaceId()
  const connection = invite.connection || (activeWorkspace !== 'local' ? activeWorkspace : undefined)
  const inMacApp = hasDesktopConnections()
  const canAdd = invite.can_add_to_mac === true && Boolean(connection)
  const join = connection ? `/${encodeURIComponent(connection)}/crew-teams/${encodeURIComponent(invite.project_id)}/join` : ''
  const chooseFolder = async () => {
    setBusy(true); setError('')
    try {
      const options = await desktopRequest<JoinOptions>(`${join}?name=${encodeURIComponent(invite.name)}`)
      setFolder(options.folder); setProjects(options.projects); setInstructionsNotice(options.instructions_notice)
    }
    catch (cause) { setError(cause instanceof Error ? cause.message : 'This Mac could not suggest a folder.') } finally { setBusy(false) }
  }
  const addToMac = async () => {
    setBusy(true); setError('')
    try {
      await desktopRequest(join, 'POST', target === 'new' ? { folder } : { local_project: target, agents: selectedAgents, confirm: true })
      window.dispatchEvent(new CustomEvent('agentworks:team-updated'))
    }
    catch (cause) { setError(cause instanceof Error ? cause.message : 'The team could not be added to this Mac. Try again.') } finally { setBusy(false) }
  }
  const answer = async (accept: boolean) => {
    setBusy(true); setError('')
    try {
      await answerTeamInvite(invite, accept)
      if (accept && !invite.connection) openSidebarProject('crew', invite.project_id)
    } catch (cause) { setError(cause instanceof Error ? cause.message : 'The invite could not be answered. Try again.') } finally { setBusy(false) }
  }
  const line = !inMacApp ? 'Join to open this team here. You can also add its shared folder to your Mac later.'
    : invite.can_add_to_mac !== true ? "This team has no shared folder, so it can't be added to this Mac."
    : !connection ? "Add this team's network to your Mac first (Servers), then try again."
      : 'Add it to this Mac: its shared folder syncs here and you can chat with its shared agents.'
  return <div role="group" aria-label={`Team invite: ${invite.name}`} className="rounded-lg border border-border px-3 py-2 text-[13px]">
    <p className="leading-5"><b className="font-medium">{invite.owner_name}</b> invited you to the {invite.name} team.</p>
    <p className="text-[11px] leading-4 text-muted-foreground">{line}</p>
    <div className="mt-1.5 flex flex-wrap gap-1.5">
      {!folder && (!inMacApp || canAdd) && <button type="button" disabled={busy} onClick={() => void (inMacApp ? chooseFolder() : answer(true))} className="rounded-full border border-border bg-background px-2.5 py-0.5 text-xs shadow-sm hover:bg-muted">{inMacApp ? 'Add to my Mac' : 'Join'}</button>}
      {!folder && <button type="button" disabled={busy} onClick={() => void answer(false)} className="rounded-full px-2.5 py-0.5 text-xs text-muted-foreground hover:bg-muted">Decline</button>}
    </div>
    {folder && <div className="mt-1.5 space-y-1.5 text-[11px] text-muted-foreground">
      <label className="flex items-center gap-2"><input type="radio" name={`join-${invite.project_id}`} checked={target === 'new'} onChange={() => { setTarget('new'); setSelectedAgents([]) }} disabled={busy} />New project</label>
      <label className="flex items-center gap-2"><input type="radio" name={`join-${invite.project_id}`} checked={target !== 'new'} disabled={busy || !projects.some(project => project.can_join)} onChange={() => { setTarget(projects.find(project => project.can_join)!.project_id); setSelectedAgents([]) }} />Use an existing project</label>
      {projects.length > 0 && <select aria-label="Existing project" value={target === 'new' ? '' : target} disabled={busy} onChange={event => { setTarget(event.target.value); setSelectedAgents([]) }} className="w-full rounded border border-border bg-background px-2 py-1 text-foreground">
        <option value="" disabled>Choose a project</option>
        {projects.map(project => <option key={project.project_id} value={project.project_id} disabled={!project.can_join}>{project.name}{project.reason ? ` — ${project.reason}` : ''}</option>)}
      </select>}
      {target === 'new' ? <p className="break-all">The shared folder goes to <span className="text-foreground">{folder}</span></p> : chosen && <>
        <p>{instructionsNotice}</p>
        <p>Your other folders and unselected agents stay private.</p>
        <fieldset disabled={busy} className="space-y-1"><legend>Agents to share with the Team (optional)</legend>
          {chosen.agents.map(agent => <label key={agent.tab} className="flex items-center gap-2"><input type="checkbox" checked={selectedAgents.includes(agent.tab)} onChange={event => setSelectedAgents(current => event.target.checked ? [...current, agent.tab] : current.filter(tab => tab !== agent.tab))} />{agent.name}</label>)}
        </fieldset>
      </>}
      {!invite.folder?.whole && !!invite.folder?.folders?.length && <p>Shared folders: {invite.folder.folders.map(entry => `${entry.path}/`).join(', ')}. Same-name folders are kept beside yours.</p>}
      <div className="flex flex-wrap gap-1.5">
        <button type="button" disabled={busy || (target !== 'new' && !chosen?.can_join)} onClick={() => void addToMac()} className="rounded-full border border-border bg-background px-2.5 py-0.5 text-xs text-foreground shadow-sm hover:bg-muted">OK</button>
        {target === 'new' && window.electronAPI?.pickWorkflowFolder && <button type="button" disabled={busy} onClick={async () => { const picked = await window.electronAPI?.pickWorkflowFolder?.(); if (picked) setFolder(picked) }} className="rounded-full px-2.5 py-0.5 text-xs hover:bg-muted">Choose another folder</button>}
        <button type="button" disabled={busy} onClick={() => void answer(false)} className="rounded-full px-2.5 py-0.5 text-xs hover:bg-muted">Decline</button>
      </div>
    </div>}
    {error && <p role="alert" className="mt-1 text-[11px] text-destructive">{error}</p>}
  </div>
}

type JoinProject = { project_id: string; name: string; can_join: boolean; reason?: string; agents: { tab: string; name: string }[] }
type JoinOptions = { folder: string; projects: JoinProject[]; instructions_notice: string }
