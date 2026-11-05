import { useEffect, useState, type ReactNode } from 'react'
import { openAppPage } from '../../utils/appNavigation'
import { getRawActiveWorkspaceId } from '../../utils/desktopConnection'
import { Folder, Users } from 'lucide-react'
import api from '../../services/api'
import { Button } from '../../components/ui/Button'
import { WorkspaceViewHeader } from '../../components/workflow/WorkspaceViewHeader'
import { useAuthStore } from '../../stores/useAuthStore'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'
import { CrewTeamCard } from './CrewTeamCard'
import { TeamSyncPanel } from './TeamSyncPanel'
import { useTeamAgents } from './useTeamAgents'
import { teamError, teamRequest, useProjectTeam } from './teamApi'
import type { TeamAgentPerson } from './useTeamAgents'

type MemberAccess = {
  name: string
  people: TeamAgentPerson[]
  shared: string
  copy_status: string
  own_agents: { key: string; name: string; in_team: boolean }[]
}
type Access = { role: 'owner' | 'member' | 'shared'; access_view?: 'team' | 'summary'; access_summary?: string; place?: string; member_access?: MemberAccess }

/** PLAT-856: the server decides which Access page this signed-in person can use. */
export function TeamMemberAccess({ projectId, children }: { projectId: string; children: ReactNode }) {
  const user = useAuthStore(s => s.user?.id)
  const workspace = useWorkspaceConnectionStore(s => s.activeWorkspaceId)
  const [access, setAccess] = useState<Access>()
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [refresh, setRefresh] = useState(0)
  useEffect(() => {
    let live = true
    setAccess(undefined)
    const load = async () => {
      try {
        const { data } = await api.get<Access>(`/api/crew-sync/${encodeURIComponent(projectId)}`, { skipSessionContext: true })
        if (live) { setAccess(data); setError('') }
      } catch (e) { if (live) setError(teamError(e)) }
    }
    void load()
    const timer = window.setInterval(() => { if (!document.hidden) void load() }, 15000)
    window.addEventListener('agentworks:team-updated', load)
    return () => { live = false; window.clearInterval(timer); window.removeEventListener('agentworks:team-updated', load) }
  }, [projectId, user, workspace, refresh])
  const act = async (suffix: string, body?: unknown) => {
    setBusy(true); setError('')
    try {
      await teamRequest({ project: projectId }, suffix, 'POST', body)
      if (suffix === '/leave') openAppPage('/home')
      else window.dispatchEvent(new CustomEvent('agentworks:team-updated'))
    } catch (e) { setError(teamError(e)) }
    finally { setBusy(false) }
  }
  if (!access) return <div className="space-y-3"><WorkspaceViewHeader icon={Users} title="Access" />{error ? <><p role="alert">{error}</p><Button variant="outline" onClick={() => setRefresh(v => v + 1)}>Try again</Button></> : <p role="status" className="text-sm text-muted-foreground">Loading Team…</p>}</div>
  if (access.access_view === 'summary') return <div className="space-y-4"><WorkspaceViewHeader icon={Users} title="Access" /><p className="text-sm text-muted-foreground">{access.access_summary}</p></div>
  if (access.role === 'owner') return access.access_view === 'team' ? <OwnerTeamAccess projectId={projectId} place={access.place} />
    : getRawActiveWorkspaceId() === 'local' ? children : <>{children}<ServerCrewTeam projectId={projectId} access={access.member_access} busy={busy} error={error} onVisibility={(key, inTeam) => void act('/agent-visibility', { agent_key: key, in_team: inTeam })} /></>
  const member = access.member_access
  if (!member) return <p role="alert">Your Team access could not be loaded. Open the Team again.</p>
  return <div className="space-y-6">
    <WorkspaceViewHeader icon={Users} title="Team" />
    <h3 className="text-lg font-semibold">{member.name}</h3>
    <section aria-label="People" className="divide-y divide-border">
      {member.people.map(person => <div key={person.user_id} className="py-3">
        <div className="flex items-center justify-between gap-3"><span className="font-medium">{person.name}{person.you ? ' · You' : ''}</span><span className="text-sm text-muted-foreground">{person.active_label}</span></div>
        {!person.you && <p className="mt-1 text-sm text-muted-foreground">{person.agents.map(agent => agent.name).join(' · ') || 'No agents in team'}</p>}
      </div>)}
    </section>
    <section aria-label="Shared folder" className="flex gap-3"><Folder className="mt-0.5 h-5 w-5 shrink-0 text-muted-foreground" /><div><h4 className="font-medium">Shared folder</h4><p className="break-words text-sm">{member.shared}</p>{member.copy_status && <p className="text-sm text-muted-foreground">{member.copy_status}</p>}</div></section>
    <section aria-label="Your agents"><h4 className="mb-2 font-medium">Your agents</h4>{member.own_agents.map(agent => <div key={agent.key} className="flex items-center justify-between gap-3 py-2"><span>{agent.name}</span><select aria-label={`${agent.name} visibility`} value={agent.in_team ? 'team' : 'me'} disabled={busy} onChange={e => void act('/agent-visibility', { agent_key: agent.key, in_team: e.target.value === 'team' })} className="rounded-md border border-border bg-background px-3 py-2 text-sm"><option value="team">In team</option><option value="me">Only me</option></select></div>)}</section>
    {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
    <Button variant="outline" disabled={busy} onClick={() => void act('/leave')}>Leave Team</Button>
  </div>
}

/** A Crew that lives on the server has the same Team (owner, 5 Nov): under its owner's Access page, the switch and
 * people, and In team / Only me for the owner's own agents (server tabs, like a member's). */
function ServerCrewTeam({ projectId, access, busy, error, onVisibility }: { projectId: string; access?: MemberAccess; busy: boolean; error: string; onVisibility: (key: string, inTeam: boolean) => void }) {
  const { team } = useProjectTeam(projectId)
  return <section aria-label="Team" className="mt-6 space-y-4">
    <CrewTeamCard projectId={projectId} serverCrew />
    {team?.on && access && <>
      <section aria-label="Team agents" className="divide-y divide-border">{access.people.filter(person => !person.you).map(person => <div key={person.user_id} className="py-3"><h3 className="font-medium">{person.name}</h3><p className="mt-1 text-sm text-muted-foreground">{person.agents.map(agent => agent.name).join(' · ') || 'No agents in team'}</p></div>)}</section>
      <section aria-label="Your agents"><h4 className="mb-2 font-medium">Your agents</h4>{access.own_agents.map(agent => <div key={agent.key} className="flex items-center justify-between gap-3 py-2"><span>{agent.name}</span><select aria-label={`${agent.name} visibility`} value={agent.in_team ? 'team' : 'me'} disabled={busy} onChange={e => onVisibility(agent.key, e.target.value === 'team')} className="rounded-md border border-border bg-background px-3 py-2 text-sm"><option value="team">In team</option><option value="me">Only me</option></select></div>)}</section>
    </>}
    {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
  </section>
}

/** The online copy of a Mac Crew shows its Team, not this browser's laptop integration. */
function OwnerTeamAccess({ projectId, place }: { projectId: string; place?: string }) {
  const { team, target, error } = useProjectTeam(projectId)
  const people = useTeamAgents(projectId)
  return <div className="space-y-5">
    <WorkspaceViewHeader icon={Users} title="Team" />
    <p role="status" className="text-sm text-muted-foreground">{place === 'mac' ? 'Your Mac is online' : 'Your Mac is offline'}</p>
    {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
    <CrewTeamCard projectId={projectId} />
    {team?.on && <section aria-label="Team agents" className="divide-y divide-border">{people.map(person => <div key={person.user_id} className="py-3"><h3 className="font-medium">{person.name}{person.you ? ' · You' : ''}</h3><p className="mt-1 text-sm text-muted-foreground">{person.agents.map(agent => agent.name).join(' · ') || 'No agents in team'}</p></div>)}</section>}
    {team?.on && team.folder_access && target && <TeamSyncPanel target={target} name={team.name} projectId={projectId} />}
  </div>
}
