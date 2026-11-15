import api from '../../services/api'
import { captureChatIdentity, assertChatIdentityCurrent } from '../../utils/chatIdentity'
import { startTeamDashboardHandoff } from '../../utils/teamDashboardHandoff'
import { loadDashboardDirectory, dashboardDisplayName, type DashboardDirectoryEntry } from '../../services/dashboardDirectory'
import { useCapabilitiesStore } from '../../stores/useCapabilitiesStore'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'
import { useEffect, useState } from 'react'
import { FileText, Film, LayoutDashboard, type LucideIcon } from 'lucide-react'
import { Button } from '../../components/ui/Button'
import { PublicLinkControl } from '../../components/workflow/PublicLinkControl'
import { selectReportDocument } from '../../components/workflow/reportDocuments'
import { DashboardPeopleCard, teamCard } from './TeamTab'
import { fillMainDraft } from '../../utils/agentPurposeDraft'
import { useChatStore } from '../../stores/useChatStore'
import { crewDashboardsTab, teamFailure, teamPublicLink, useTeamTab, type CrewDashboardsTab as DashboardsTab, type TeamTarget } from './teamApi'

const kindIcon: Record<string, LucideIcon> = { dashboard: LayoutDashboard, artifact: FileText, motion: Film }
const openAccess = (projectId: string, section: 'mac') => window.dispatchEvent(new CustomEvent('agentworks:open-crew-access', { detail: { projectId, section } }))

/** The standalone Artifacts page (owner, 14 Nov): everything this
 * Crew publishes (dashboards, artifacts, motion), each with who can open it and, on a server Crew, its public link. The server's
 * /dashboards-tab writes every line and says what this person may change (owner: Change and the link; a teammate: the
 * list), and a Crew on a Mac says it needs to be online first; this only shows them. The same tab in the Mac app and on
 * the website. */
export function CrewDashboardsTab({ projectId }: { projectId: string }) {
  const { target, view, unbound, error: reachError, retry: retryTeam } = useTeamTab(projectId)
  const localMode = useCapabilitiesStore(s => Boolean(s.capabilities?.local_mode))
  const host = useWorkspaceConnectionStore(s => s.activeWorkspaceId)
  const onThisMac = localMode && host === 'local'
  const [adding, setAdding] = useState('')
  const [handoffMessage, setHandoffMessage] = useState('')
  useEffect(() => { setHandoffMessage(''); setAdding('') }, [projectId, host])
  const canAdd = Boolean(view?.team.on && view.team.can_manage && !view.team.roster_source)
  async function addToTeam(workspace: string, id: string) {
    if (adding) return
    const identity = captureChatIdentity()
    setAdding(id); setHandoffMessage('')
    try {
      const { data } = await api.post<{ project_id: string; prompt?: string; message?: string }>('/api/dashboards', { action: 'add_to_team', workspace, dashboard_id: id })
      assertChatIdentityCurrent(identity)
      if (data.prompt) startTeamDashboardHandoff(data.project_id, data.prompt)
      if (data.message) setHandoffMessage(data.message)
      window.dispatchEvent(new CustomEvent('agentworks:team-updated'))
    } catch (e) {
      try { assertChatIdentityCurrent(identity); useChatStore.getState().addToast(teamFailure(e).message, 'error') } catch { /* account changed */ }
    } finally { setAdding('') }
  }
  const [localRows, setLocalRows] = useState<DashboardDirectoryEntry[]>()
  const [localError, setLocalError] = useState('')
  const [attempt, setAttempt] = useState(0)
  useEffect(() => {
    const controller = new AbortController()
    setLocalRows(undefined); setLocalError('')
    if (onThisMac) void loadDashboardDirectory(controller.signal).then(result => {
      if (!controller.signal.aborted) setLocalRows(result.dashboards.filter(row => row.product === 'crew' && row.project_id === projectId))
    }).catch(() => { if (!controller.signal.aborted) setLocalError('Could not read the local artifacts. Reopen this pane to try again.') })
    return () => controller.abort()
  }, [projectId, onThisMac, host])
  // A Mac Crew that was never online has no server copy: its own Mac app's server answers.
  const where: TeamTarget | undefined = unbound ? { project: projectId } : target
  const [kind, setKind] = useState('all')
  const [tab, setTab] = useState<DashboardsTab>()
  const [error, setError] = useState('')
  useEffect(() => {
    if (!where) return
    let active = true
    const load = () => void crewDashboardsTab(where, kind).then(data => { if (active) { setTab(data); setError('') } }).catch(e => { if (active) setError(teamFailure(e).message) })
    load()
    window.addEventListener('agentworks:team-updated', load)
    return () => { active = false; window.removeEventListener('agentworks:team-updated', load) }
  }, [where?.project, where?.connection, kind, attempt]) // eslint-disable-line react-hooks/exhaustive-deps
  const failure = reachError || error
  useEffect(() => {
    if (!failure) { setAttempt(0); return }
    const timer = window.setTimeout(() => { retryTeam(); setAttempt(value => value + 1) }, Math.min(1000 * 2 ** Math.min(attempt, 5), 30000))
    return () => window.clearTimeout(timer)
  }, [failure, attempt, retryTeam])
  const local = onThisMac ? <section aria-label="Artifacts on this Mac" className={teamCard}>
    <h3 className="font-semibold">On this Mac</h3>
    {localError ? <p role="alert" className="text-xs text-muted-foreground">{localError}</p> : !localRows ? <p role="status">Loading local artifacts…</p> : !localRows.length ? <p className="text-sm text-muted-foreground">No published artifacts on this Mac.</p> : localRows.filter(row => kind === 'all' || kind === (row.motion ? 'motion' : row.artifact ? 'artifact' : 'dashboard')).map(row => <div key={row.dashboard_id} className="flex items-center gap-2 border-t border-border py-2 text-sm"><span className="min-w-0 flex-1 truncate">{dashboardDisplayName(row)}</span><Button size="sm" variant="outline" onClick={() => selectReportDocument(row.workspace, row.document_path, true)}>Open</Button>{canAdd && row.can_edit && <Button size="sm" variant="outline" disabled={Boolean(adding)} onClick={() => void addToTeam(row.workspace, row.dashboard_id)}>{adding === row.dashboard_id ? 'Adding…' : 'Add to Team'}</Button>}</div>)}
  </section> : null
  const notice = failure ? <p role="status" className="text-xs text-muted-foreground">Couldn't reach the Team server, retrying</p> : null
  if (!where || !tab) return <div className="space-y-3">{local}{handoffMessage && <p role="status" className="text-sm">{handoffMessage}</p>}{notice || <p role="status" className="text-sm text-muted-foreground">Loading artifacts…</p>}</div>
  return <div className="space-y-3">
  {local}{notice}
  {handoffMessage && <p role="status" className="text-sm">{handoffMessage}</p>}
  <section aria-label="Artifacts" className={teamCard}>
    <h3 className="font-semibold">{onThisMac ? 'Online with the Team' : 'Artifacts'}</h3>
    {tab.intro && <p className="text-xs text-muted-foreground">{tab.intro}</p>}
    {tab.filters.length > 1 && <div role="group" aria-label="Show" className="flex flex-wrap gap-1.5">
      {tab.filters.map(filter => <button key={filter.key} type="button" aria-pressed={tab.filter === filter.key} onClick={() => setKind(filter.key)}
        className={`rounded-full border px-2.5 py-0.5 text-xs ${tab.filter === filter.key ? 'border-primary bg-primary text-primary-foreground' : 'border-border hover:bg-muted'}`}>{filter.label} {filter.count}</button>)}
    </div>}
    {/* What the chosen kind is and how it works, and "Ask the Crew" for the owner when there is none yet (owner, 9 Nov). */}
    {(() => {
      const chosen = tab.filters.find(filter => filter.key === tab.filter)
      if (!chosen?.hint) return null
      return <div className="space-y-1.5 text-xs text-muted-foreground">
        <p>{chosen.hint}</p>
        {chosen.count === 0 && chosen.ask && <Button size="sm" variant="outline" className="h-7 rounded-full px-3" onClick={() => { if (!fillMainDraft(projectId, chosen.ask || '')) useChatStore.getState().addToast('Open your own Main tab in this Crew, then ask from there.', 'error') }}>Ask the Crew</Button>}
      </div>
    })()}
    {tab.note && <p className="text-xs text-muted-foreground">{tab.note}</p>}
    {tab.put_online && <div><Button size="sm" variant="outline" className="h-7 rounded-full px-3" onClick={() => openAccess(projectId, 'mac')}>Put it online</Button></div>}
    {tab.empty && <div className="space-y-1.5 rounded-lg bg-muted/40 px-3 py-3 text-[13px]">
      {tab.empty.title && <p className="font-medium">{tab.empty.title}</p>}
      <p className="text-muted-foreground">{tab.empty.line}</p>
      {tab.empty.kinds && <ul className="space-y-0.5 text-muted-foreground">
        {tab.empty.kinds.map(kind => <li key={kind.name}><span className="font-medium text-foreground">{kind.name}</span>: {kind.line}</li>)}
      </ul>}
      {tab.empty.ask && <Button size="sm" className="h-7 rounded-full px-3" onClick={() => { if (!fillMainDraft(projectId, tab.empty?.ask || '')) useChatStore.getState().addToast('Open your own Main tab in this Crew, then ask from there.', 'error') }}>Ask the Crew</Button>}
      {tab.empty.example && <p className="text-xs text-muted-foreground">{tab.empty.example}</p>}
    </div>}
    <div className="divide-y divide-border">
      {tab.items.map(item => {
        const Icon = kindIcon[item.kind] || LayoutDashboard
        return <div key={item.id} className="space-y-1.5 py-2.5">
          <div className="flex items-center gap-2">
            <Icon aria-hidden="true" className="h-4 w-4 shrink-0 text-muted-foreground" />
            <span className="min-w-0 flex-1"><span className="block truncate text-sm font-medium">{item.title}</span><span className="block text-xs text-muted-foreground">{item.line}</span></span>
            <Button size="sm" variant="outline" className="h-7 rounded-full px-3" onClick={() => selectReportDocument(tab.workspace, item.path, true)}>Open</Button>
            {canAdd && !onThisMac && tab.can_change && <Button size="sm" variant="outline" className="h-7 rounded-full px-3" disabled={Boolean(adding)} onClick={() => void addToTeam(tab.workspace, item.id)}>{adding === item.id ? 'Adding…' : 'Add to Team'}</Button>}
          </div>
          <div className="flex items-center gap-2 pl-6 text-xs text-muted-foreground">
            <span className="min-w-0 flex-1">Who can open it: <span className="text-foreground">{item.who}</span></span>
          </div>
          {item.public_link && <div className="pl-6"><PublicLinkControl link={item.public_link} change={mode => teamPublicLink(where, item.id, mode)} /></div>}
        </div>
      })}
    </div>
  </section>
  <DashboardPeopleCard target={where} />
  </div>
}
