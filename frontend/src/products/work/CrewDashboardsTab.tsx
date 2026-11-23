import { loadDashboardDirectory, dashboardDisplayName, type DashboardDirectoryEntry } from '../../services/dashboardDirectory'
import { useCapabilitiesStore } from '../../stores/useCapabilitiesStore'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'
import { useEffect, useState } from 'react'
import { FileText, Film, LayoutDashboard, MoreHorizontal, type LucideIcon } from 'lucide-react'
import { Button } from '../../components/ui/Button'
import { PublicLinkControl } from '../../components/workflow/PublicLinkControl'
import { selectReportDocument } from '../../components/workflow/reportDocuments'
import { DashboardPeopleCard, teamCard } from './TeamTab'
import { fillMainDraft } from '../../utils/agentPurposeDraft'
import { useChatStore } from '../../stores/useChatStore'
import { crewDashboardsTab, teamFailure, teamPublicLink, useTeamTab, type CrewDashboardsTab as DashboardsTab, type TeamTarget } from './teamApi'

const kindIcon: Record<string, LucideIcon> = { dashboard: LayoutDashboard, artifact: FileText, motion: Film }

/** The standalone Artifacts page (owner, 14 Nov): everything this
 * Crew publishes (dashboards, artifacts, motion), each with who can open it and, on a server Crew, its public link. The server's
 * /dashboards-tab writes every line and says what this person may change (owner: Change and the link; a teammate: the
 * list), and a Crew on a Mac says it needs to be online first; this only shows them. The same tab in the Mac app and on
 * the website. */
export function CrewDashboardsTab({ projectId }: { projectId: string }) {
  const { target, unbound, error: reachError, retry: retryTeam } = useTeamTab(projectId)
  const localMode = useCapabilitiesStore(s => Boolean(s.capabilities?.local_mode))
  const host = useWorkspaceConnectionStore(s => s.activeWorkspaceId)
  const onThisMac = localMode && host === 'local'
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
    const load = () => void crewDashboardsTab(where, 'all').then(data => { if (active) { setTab(data); setError('') } }).catch(e => { if (active) setError(teamFailure(e).message) })
    load()
    window.addEventListener('agentworks:team-updated', load)
    return () => { active = false; window.removeEventListener('agentworks:team-updated', load) }
  }, [where?.project, where?.connection, attempt]) // eslint-disable-line react-hooks/exhaustive-deps
  const failure = reachError || error
  useEffect(() => {
    if (!failure) { setAttempt(0); return }
    const timer = window.setTimeout(() => { retryTeam(); setAttempt(value => value + 1) }, Math.min(1000 * 2 ** Math.min(attempt, 5), 30000))
    return () => window.clearTimeout(timer)
  }, [failure, attempt, retryTeam])
  // Fetch all once: filters and counts describe both places, not just the last server response.
  const rows = [
    ...(onThisMac ? (localRows || []).map(row => ({ id: `local:${row.dashboard_id}`, sourceId: row.dashboard_id, title: dashboardDisplayName(row), kind: row.motion ? 'motion' : row.artifact ? 'artifact' : 'dashboard', workspace: row.workspace, path: row.document_path, place: 'This Mac', publicLink: undefined as DashboardsTab['items'][number]['public_link'] })) : []),
    ...((!onThisMac || !unbound) && tab ? tab.items.map(item => ({ id: `remote:${item.workspace || tab.workspace}:${item.id}`, sourceId: item.id, title: item.title, kind: item.kind, workspace: item.workspace || tab.workspace, path: item.path, place: tab.on_mac ? 'This Mac' : 'On the Team', publicLink: item.public_link })) : []),
  ]
  function openRow(row: typeof rows[number]) {
    selectReportDocument(row.workspace, row.path, true)
  }
  const complete = Boolean(tab) && (!onThisMac || Boolean(localRows)) && !failure && !localError
  const filters = [{ key: 'all', label: 'All' }, { key: 'dashboard', label: 'Dashboards' }, { key: 'artifact', label: 'Artifacts' }, { key: 'motion', label: 'Motion' }]
  const chosen = tab?.filters.find(filter => filter.key === kind)
  return <div className="space-y-3">
    <section aria-label="Artifacts" className={teamCard}>
      <div role="group" aria-label="Show" className="flex flex-wrap gap-1.5">
        {filters.map(filter => <button key={filter.key} type="button" aria-pressed={kind === filter.key} onClick={() => setKind(filter.key)}
          className={`rounded-full border px-2.5 py-0.5 text-xs ${kind === filter.key ? 'border-primary bg-primary text-primary-foreground' : 'border-border hover:bg-muted'}`}>{filter.label} {rows.filter(row => filter.key === 'all' || row.kind === filter.key).length}</button>)}
      </div>
      {failure && <p role="status" className="text-xs text-muted-foreground">Couldn't reach the Team server, retrying</p>}
      {localError && <p role="alert" className="text-xs text-muted-foreground">{localError}</p>}
      {!failure && !localError && !complete && <p role="status" className="text-xs text-muted-foreground">Loading artifacts…</p>}
      {chosen?.hint && <p className="text-xs text-muted-foreground">{chosen.hint}</p>}
      {complete && !rows.length && <div className="rounded-lg bg-muted/40 px-3 py-3 text-sm">
        <p>{tab?.empty?.title || 'No artifacts yet'}</p>
        <p className="text-muted-foreground">{tab?.empty?.line}</p>
        {tab?.empty?.ask && <Button size="sm" onClick={() => { if (!fillMainDraft(projectId, tab.empty?.ask || '')) useChatStore.getState().addToast('Open your own Main tab in this Crew, then ask from there.', 'error') }}>Ask the Crew</Button>}
      </div>}
      <div className="divide-y divide-border">
        {rows.filter(row => kind === 'all' || kind === row.kind).map(row => {
          const Icon = kindIcon[row.kind] || LayoutDashboard
          return <div key={row.id} className="group flex items-center gap-2 py-1">
            <button type="button" className="flex min-w-0 flex-1 items-center gap-2 rounded px-1 py-2 text-left hover:bg-muted focus-visible:ring-2 focus-visible:ring-ring" onClick={() => openRow(row)}>
              <Icon aria-hidden="true" className="h-4 w-4 shrink-0 text-muted-foreground" />
              <span className="min-w-0 flex-1 truncate text-sm font-medium">{row.title}</span>
              <span className="shrink-0 text-[11px] text-muted-foreground">{row.place}</span>
            </button>
            {row.publicLink && <details className="relative">
              <summary aria-label={`Options for ${row.title}`} className="cursor-pointer list-none rounded p-1 text-muted-foreground hover:bg-muted"><MoreHorizontal className="h-4 w-4" /></summary>
              <div className="absolute right-0 z-10 min-w-40 rounded border border-border bg-background p-2 shadow-md">
                {row.publicLink && where && <PublicLinkControl link={row.publicLink} change={mode => teamPublicLink(where, row.sourceId, mode)} />}
              </div>
            </details>}
          </div>
        })}
      </div>
    </section>
    {where && tab && <DashboardPeopleCard target={where} />}
  </div>
}
