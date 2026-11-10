import { useEffect, useState } from 'react'
import { desktopRequest } from '../../services/desktopConnections'
import { answerTeamInvite, useTeamInvites, type TeamInvite } from '../../products/work/teamApi'
import { openSidebarProject } from './SidebarServerGroup'
import { hasDesktopConnections, useConnectedWorkStore } from '../../stores/useConnectedWorkStore'

/** Team invites, shown above the Crews (owner, 5 Nov: "we only have crews"); once accepted, the Crew is in the list. */
export function useTeamInviteRows() {
  return useTeamInvites().map(invite => <TeamInviteRow key={`${invite.connection || ''}:${invite.project_id}`} invite={invite} />)
}

/** A Team invite (owner, 10 Nov): accepted in the Mac app it is always added to this Mac (its shared folder syncs
 * here, connecting this Mac to the invite's server in the same click when needed); accepted on the website it joins on
 * the server. One action each, and Decline. The server says whether the Crew can be added to a Mac. */
function TeamInviteRow({ invite }: { invite: TeamInvite }) {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  // "Add to my Mac": the folder the shared files sync to, ~/AgentWorks/<Crew>/team unless the person picks another.
  const [folder, setFolder] = useState('')
  // The network this Mac reaches the invite's server through: the invite's own, or the one made by connecting here.
  const [connection, setConnection] = useState(invite.connection)
  const [signIn, setSignIn] = useState<string | null>(null)
  const inMacApp = hasDesktopConnections()
  // The server says whether it can be added; connecting uses the server's own address, never this app's local one.
  const canAdd = invite.can_add_to_mac === true && (Boolean(connection) || Boolean(invite.server))
  const join = connection ? `/${encodeURIComponent(connection)}/crew-teams/${encodeURIComponent(invite.project_id)}/join` : ''
  const folderFor = async (target: string) => {
    const route = `/${encodeURIComponent(target)}/crew-teams/${encodeURIComponent(invite.project_id)}/join`
    setFolder((await desktopRequest<{ folder: string }>(`${route}?name=${encodeURIComponent(invite.name)}`)).folder)
  }
  // The same click connects this Mac to the invite's server when it is not connected yet, then suggests the folder.
  const chooseFolder = async () => {
    setBusy(true); setError('')
    try {
      if (connection) { await folderFor(connection); return }
      const started = await desktopRequest<{ state: string; url: string }>('', 'POST', { server: invite.server })
      setSignIn(started.state)
      await window.electronAPI?.openExternal?.(started.url)
    } catch (cause) { setSignIn(null); setError(cause instanceof Error ? cause.message : 'This Mac could not suggest a folder.') } finally { setBusy(false) }
  }
  useEffect(() => {
    if (!signIn) return
    let disposed = false
    let timer: ReturnType<typeof setTimeout>
    const check = async () => {
      try {
        const data = await desktopRequest<{ connection_id: string; error: string }>(`/sign-in/${signIn}`)
        if (disposed) return
        if (data.error) throw new Error(data.error)
        if (!data.connection_id) { timer = setTimeout(() => void check(), 1500); return }
        setSignIn(null); setConnection(data.connection_id)
        void useConnectedWorkStore.getState().refresh()
        await folderFor(data.connection_id)
      } catch (cause) { if (!disposed) { setSignIn(null); setError(cause instanceof Error ? cause.message : 'Sign-in did not finish. Try again.') } }
    }
    void check()
    return () => { disposed = true; clearTimeout(timer) }
  }, [signIn]) // eslint-disable-line react-hooks/exhaustive-deps
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
    : !canAdd ? "This team can't be added to this Mac: it has no shared folder, or its server has no public address."
      : signIn ? 'Finish signing in in your browser; this continues by itself.'
        : connection ? 'Add it to this Mac: its shared folder syncs here and you can chat with its shared agents.'
          : 'Add it to this Mac: its shared folder syncs here. This Mac signs in to the server first, in your browser.'
  return <div role="group" aria-label={`Team invite: ${invite.name}`} className="rounded-lg border border-border px-3 py-2 text-[13px]">
    <p className="leading-5"><b className="font-medium">{invite.owner_name}</b> invited you to the {invite.name} team.</p>
    <p className="text-[11px] leading-4 text-muted-foreground">{line}</p>
    <div className="mt-1.5 flex flex-wrap gap-1.5">
      {!folder && (!inMacApp || canAdd) && <button type="button" disabled={busy || !!signIn} onClick={() => void (inMacApp ? chooseFolder() : answer(true))} className="rounded-full border border-border bg-background px-2.5 py-0.5 text-xs shadow-sm hover:bg-muted">{inMacApp ? 'Add to my Mac' : 'Join'}</button>}
      {!folder && <button type="button" disabled={busy || !!signIn} onClick={() => void answer(false)} className="rounded-full px-2.5 py-0.5 text-xs text-muted-foreground hover:bg-muted">Decline</button>}
    </div>
    {folder && <div className="mt-1.5 space-y-1.5 text-[11px] text-muted-foreground">
      <p className="break-all">The shared folder goes to <span className="text-foreground">{folder}</span></p>
      <div className="flex flex-wrap gap-1.5">
        <button type="button" disabled={busy} onClick={() => void addToMac()} className="rounded-full border border-border bg-background px-2.5 py-0.5 text-xs text-foreground shadow-sm hover:bg-muted">OK</button>
        {window.electronAPI?.pickWorkflowFolder && <button type="button" disabled={busy} onClick={async () => { const picked = await window.electronAPI?.pickWorkflowFolder?.(); if (picked) setFolder(picked) }} className="rounded-full px-2.5 py-0.5 text-xs hover:bg-muted">Choose another folder</button>}
        <button type="button" disabled={busy} onClick={() => setFolder('')} className="rounded-full px-2.5 py-0.5 text-xs hover:bg-muted">Back</button>
      </div>
    </div>}
    {error && <p role="alert" className="mt-1 text-[11px] text-destructive">{error}</p>}
  </div>
}
