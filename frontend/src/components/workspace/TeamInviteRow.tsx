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
function TeamInviteRow({ invite }: { invite: TeamInvite }) {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  // "Add to my Mac": the folder the shared files sync to, ~/AgentWorks/<Crew>/team unless the person picks another.
  const [folder, setFolder] = useState('')
  // The network this Mac reaches the invite's server through: the one the invite was listed from, or the selected
  // workspace (a server this Mac already added), so nothing new is asked.
  const activeWorkspace = getRawActiveWorkspaceId()
  const connection = invite.connection || (activeWorkspace !== 'local' ? activeWorkspace : undefined)
  const inMacApp = hasDesktopConnections()
  const canAdd = invite.can_add_to_mac === true && Boolean(connection)
  const join = connection ? `/${encodeURIComponent(connection)}/crew-teams/${encodeURIComponent(invite.project_id)}/join` : ''
  const chooseFolder = async () => {
    setBusy(true); setError('')
    try { setFolder((await desktopRequest<{ folder: string }>(`${join}?name=${encodeURIComponent(invite.name)}`)).folder) }
    catch (cause) { setError(cause instanceof Error ? cause.message : 'This Mac could not suggest a folder.') } finally { setBusy(false) }
  }
  const addToMac = async () => {
    setBusy(true); setError('')
    try { await desktopRequest(join, 'POST', { folder }); window.dispatchEvent(new CustomEvent('agentworks:team-updated')) }
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
      <p className="break-all">The shared folder goes to <span className="text-foreground">{folder}</span></p>
      {!invite.folder?.whole && !!invite.folder?.folders?.length && <p>A folder that already has files is fine: they stay on this Mac, and only {invite.folder.folders.map(name => `${name}/`).join(', ')} syncs with the team.</p>}
      <div className="flex flex-wrap gap-1.5">
        <button type="button" disabled={busy} onClick={() => void addToMac()} className="rounded-full border border-border bg-background px-2.5 py-0.5 text-xs text-foreground shadow-sm hover:bg-muted">OK</button>
        {window.electronAPI?.pickWorkflowFolder && <button type="button" disabled={busy} onClick={async () => { const picked = await window.electronAPI?.pickWorkflowFolder?.(); if (picked) setFolder(picked) }} className="rounded-full px-2.5 py-0.5 text-xs hover:bg-muted">Choose another folder</button>}
        <button type="button" disabled={busy} onClick={() => setFolder('')} className="rounded-full px-2.5 py-0.5 text-xs hover:bg-muted">Back</button>
      </div>
    </div>}
    {error && <p role="alert" className="mt-1 text-[11px] text-destructive">{error}</p>}
  </div>
}
