import { useEffect, useState } from 'react'
import { FileText, Film, LayoutDashboard, type LucideIcon } from 'lucide-react'
import { Button } from '../../components/ui/Button'
import { PublicLinkControl } from '../../components/workflow/PublicLinkControl'
import { selectReportDocument } from '../../components/workflow/reportDocuments'
import { teamCard } from './TeamTab'
import { fillMainDraft } from '../../utils/agentPurposeDraft'
import { useChatStore } from '../../stores/useChatStore'
import { crewDashboardsTab, teamFailure, teamPublicLink, useTeamTab, type CrewDashboardsTab as DashboardsTab, type TeamTarget } from './teamApi'

const kindIcon: Record<string, LucideIcon> = { dashboard: LayoutDashboard, artifact: FileText, motion: Film }
const openAccess = (projectId: string, section: 'mac' | 'team') => window.dispatchEvent(new CustomEvent('agentworks:open-crew-access', { detail: { projectId, section } }))

/** The Access page's Dashboards tab, next to Share (owner, 6 Nov mockup; its own tab again 9 Nov): everything this
 * Crew publishes (dashboards, artifacts, motion), each with who can open it and, on a server Crew, its public link. The server's
 * /dashboards-tab writes every line and says what this person may change (owner: Change and the link; a teammate: the
 * list), and a Crew on a Mac says it needs to be online first; this only shows them. The same tab in the Mac app and on
 * the website. */
export function CrewDashboardsTab({ projectId }: { projectId: string }) {
  const { target, unbound } = useTeamTab(projectId)
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
  }, [where?.project, where?.connection, kind]) // eslint-disable-line react-hooks/exhaustive-deps
  if (!where || !tab) return error ? <p role="alert" className="text-xs text-muted-foreground">{error}</p> : <p role="status" className="text-sm text-muted-foreground">Loading dashboards…</p>
  return <section aria-label="Dashboards" className={teamCard}>
    <h3 className="font-semibold">Dashboards</h3>
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
          </div>
          <div className="flex items-center gap-2 pl-6 text-xs text-muted-foreground">
            <span className="min-w-0 flex-1">Who can open it: <span className="text-foreground">{item.who}</span></span>
            {tab.can_change && !tab.on_mac && <button type="button" onClick={() => openAccess(projectId, 'team')} className="shrink-0 text-primary hover:underline">Change</button>}
          </div>
          {item.public_link && <div className="pl-6"><PublicLinkControl link={item.public_link} change={mode => teamPublicLink(where, item.id, mode)} /></div>}
        </div>
      })}
    </div>
  </section>
}
