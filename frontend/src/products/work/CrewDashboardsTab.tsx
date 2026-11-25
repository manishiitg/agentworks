import { useCapabilitiesStore } from '../../stores/useCapabilitiesStore'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'
import { useEffect, useRef, useState } from 'react'
import { ArrowLeft, FileText, Film, LayoutDashboard, Link2, MoreHorizontal, Search, Trash2, Users, type LucideIcon } from 'lucide-react'
import { HtmlRenderer } from '../../components/ui/HtmlRenderer'
import { Button } from '../../components/ui/Button'
import { PublicLinkControl } from '../../components/workflow/PublicLinkControl'
import { selectReportDocument } from '../../components/workflow/reportDocuments'
import { DashboardPeopleCard, teamCard } from './TeamTab'
import { fillMainDraft } from '../../utils/agentPurposeDraft'
import { useChatStore } from '../../stores/useChatStore'
import { crewDashboardsTab, teamArtifactPage, teamDashboardAction, teamFailure, teamPublicLink, teamShareArtifact, useTeamTab, type CrewDashboardsTab as DashboardsTab, type TeamTarget } from './teamApi'

const kindIcon: Record<string, LucideIcon> = { dashboard: LayoutDashboard, artifact: FileText, motion: Film }
type Item = DashboardsTab['items'][number]

/** The Artifacts page (owner, 25 Nov mockup): everything this project publishes, newest first under Today / This week /
 * Older, with search, a filter per kind only when there is more than one kind, and per page Open, Copy link and Delete
 * (with Undo). The server writes every line, the order, the groups and what this person may do; this only shows them.
 * A project on this Mac is read from this Mac's own server, which holds its pages. */
export function CrewDashboardsTab({ projectId }: { projectId: string }) {
  const { target, error: reachError, retry: retryTeam } = useTeamTab(projectId)
  const localMode = useCapabilitiesStore(s => Boolean(s.capabilities?.local_mode))
  const host = useWorkspaceConnectionStore(s => s.activeWorkspaceId)
  const onThisMac = localMode && host === 'local'
  const where: TeamTarget | undefined = onThisMac ? { project: projectId } : target
  const [kind, setKind] = useState('all')
  const [query, setQuery] = useState('')
  const [menu, setMenu] = useState('')
  const [tab, setTab] = useState<DashboardsTab>()
  const [error, setError] = useState('')
  const [attempt, setAttempt] = useState(0)
  const [undo, setUndo] = useState<{ id: string; title: string }>()
  const [viewing, setViewing] = useState<{ title: string; html: string }>()
  const undoTimer = useRef<number | undefined>(undefined)
  const reload = useRef<() => void>(() => undefined)
  useEffect(() => {
    if (!where) return
    let active = true
    const load = () => void crewDashboardsTab(where, 'all').then(data => { if (active) { setTab(data); setError('') } }).catch(e => { if (active) setError(teamFailure(e).message) })
    reload.current = load
    load()
    window.addEventListener('agentworks:team-updated', load)
    return () => { active = false; window.removeEventListener('agentworks:team-updated', load) }
  }, [where?.project, where?.connection, attempt]) // eslint-disable-line react-hooks/exhaustive-deps
  const failure = (onThisMac ? '' : reachError) || error
  useEffect(() => {
    if (!failure) { setAttempt(0); return }
    const timer = window.setTimeout(() => { retryTeam(); setAttempt(value => value + 1) }, Math.min(1000 * 2 ** Math.min(attempt, 5), 30000))
    return () => window.clearTimeout(timer)
  }, [failure, attempt, retryTeam])
  useEffect(() => () => window.clearTimeout(undoTimer.current), [])

  const toast = (message: string, type: 'success' | 'info' | 'error' = 'info') => useChatStore.getState().addToast(message, type)
  const fail = (e: unknown) => toast(teamFailure(e).message || 'That did not work. Try again.', 'error')
  async function copyLink(item: Item) {
    if (!where) return
    try {
      const { url } = await teamDashboardAction(where, item.id, 'link')
      if (!url) throw new Error('No link came back for this page.')
      await navigator.clipboard.writeText(url)
      toast('Link copied', 'success')
    } catch (e) { fail(e) }
  }
  async function remove(item: Item) {
    if (!where) return
    try {
      await teamDashboardAction(where, item.id, 'delete')
      console.info('[CREW_ARTIFACTS]', { event: 'deleted', project: projectId, dashboard: item.id })
      setTab(current => current && { ...current, items: current.items.filter(row => row.id !== item.id) })
      setUndo({ id: item.id, title: item.title })
      window.clearTimeout(undoTimer.current)
      undoTimer.current = window.setTimeout(() => setUndo(undefined), 6000)
      reload.current()
    } catch (e) { fail(e) }
  }
  async function shareWithTeam(item: Item, on: boolean) {
    if (!where) return
    try {
      await teamShareArtifact(where, item.id, on)
      console.info('[CREW_ARTIFACTS]', { event: on ? 'team_shared' : 'team_unshared', project: projectId, dashboard: item.id })
      toast(on ? 'Shared with the Team' : 'Removed from the Team', 'success')
      reload.current()
    } catch (e) { fail(e) }
  }
  async function open(item: Item) {
    if (!item.from_team) { selectReportDocument(item.workspace || tab!.workspace, item.path, true); return }
    if (!where) return
    try {
      const { html } = await teamArtifactPage(where, item.id)
      setViewing({ title: item.title, html })
    } catch (e) { fail(e) }
  }
  async function bringBack() {
    if (!where || !undo) return
    const back = undo
    setUndo(undefined)
    try {
      await teamDashboardAction(where, back.id, 'undelete')
      console.info('[CREW_ARTIFACTS]', { event: 'undeleted', project: projectId, dashboard: back.id })
      reload.current()
    } catch (e) { fail(e) }
  }

  const items = tab?.items || []
  const filters = tab?.filters || []
  const shown = items.filter(item => (kind === 'all' || item.kind === kind) && item.title.toLowerCase().includes(query.trim().toLowerCase()))
  const chosen = filters.find(filter => filter.key === kind)
  let group = ''
  if (viewing) return <section aria-label={viewing.title} className={`${teamCard} flex h-full min-h-[70vh] flex-col`}>
    <div className="flex items-center gap-2">
      <button type="button" aria-label="Back to Artifacts" onClick={() => setViewing(undefined)} className="rounded p-1 text-muted-foreground hover:bg-muted hover:text-foreground"><ArrowLeft className="h-4 w-4" /></button>
      <h2 className="min-w-0 flex-1 truncate text-sm font-semibold">{viewing.title}</h2>
    </div>
    <div className="flex min-h-0 flex-1"><HtmlRenderer content={viewing.html} /></div>
  </section>
  return <div className="space-y-3">
    <section aria-label="Artifacts" className={teamCard}>
      <div className="flex flex-wrap items-center gap-2">
        <h2 className="text-sm font-semibold">Artifacts {tab && <span className="font-normal text-muted-foreground">{items.length}</span>}</h2>
        {items.length > 0 && <label className="ml-auto flex items-center gap-1.5 rounded-md border border-border px-2 py-1 text-xs">
          <Search aria-hidden="true" className="h-3.5 w-3.5 text-muted-foreground" />
          <input aria-label="Search artifacts" placeholder="Search" value={query} onChange={event => setQuery(event.target.value)} className="w-40 bg-transparent outline-none" />
        </label>}
      </div>
      {filters.length > 1 && <div role="group" aria-label="Show" className="flex flex-wrap gap-1.5">
        {filters.map(filter => <button key={filter.key} type="button" aria-pressed={kind === filter.key} onClick={() => setKind(filter.key)}
          className={`rounded-full border px-2.5 py-0.5 text-xs ${kind === filter.key ? 'border-primary bg-primary text-primary-foreground' : 'border-border hover:bg-muted'}`}>{filter.label} {filter.count}</button>)}
      </div>}
      {chosen?.hint && <p className="text-xs text-muted-foreground">{chosen.hint}</p>}
      {failure && <p role="status" className="text-xs text-muted-foreground">Couldn't reach the server, retrying</p>}
      {!failure && !tab && <p role="status" className="text-xs text-muted-foreground">Loading…</p>}
      {undo && <div role="status" className="flex items-center gap-3 rounded-md bg-muted/60 px-3 py-1.5 text-xs">
        <span className="min-w-0 flex-1 truncate">Deleted “{undo.title}”</span>
        <button type="button" onClick={() => void bringBack()} className="font-semibold text-primary hover:underline">Undo</button>
      </div>}
      {tab && !items.length && <div className="rounded-lg bg-muted/40 px-3 py-3 text-sm">
        <p>{tab.empty?.title || 'No artifacts yet'}</p>
        <p className="text-muted-foreground">{tab.empty?.line}</p>
        {tab.empty?.ask && <Button size="sm" onClick={() => { if (!fillMainDraft(projectId, tab.empty?.ask || '')) useChatStore.getState().addToast('Open your own Main tab in this project, then ask from there.', 'error') }}>Ask the project</Button>}
      </div>}
      {items.length > 0 && !shown.length && <p className="py-4 text-center text-xs text-muted-foreground">Nothing matches “{query}”.</p>}
      <div>
        {shown.map(item => {
          const Icon = kindIcon[item.kind] || LayoutDashboard
          const heading = item.group !== group ? (group = item.group) : ''
          return <div key={item.path}>
            {heading && <p className="mb-1 mt-3 text-[11px] uppercase tracking-wide text-muted-foreground">{heading}</p>}
            <div className="group relative flex items-center gap-2 rounded-md px-1 hover:bg-muted">
              <button type="button" className="flex min-w-0 flex-1 items-center gap-2 py-1.5 text-left focus-visible:ring-2 focus-visible:ring-ring" onClick={() => void open(item)}>
                <Icon aria-hidden="true" className="h-4 w-4 shrink-0 text-muted-foreground" />
                <span className="min-w-0 flex-1 truncate text-sm">{item.title}</span>
                {item.team_shared && !item.from_team && <Users aria-label="Shared with the Team" className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />}
                {item.when && <span className="shrink-0 text-[11px] text-muted-foreground">{item.when}</span>}
              </button>
              <span className="flex shrink-0 items-center opacity-0 group-hover:opacity-100 group-focus-within:opacity-100">
                {!item.from_team && <button type="button" aria-label={`Copy link to ${item.title}`} title="Copy link" onClick={() => void copyLink(item)} className="rounded p-1 text-muted-foreground hover:bg-background hover:text-foreground"><Link2 className="h-3.5 w-3.5" /></button>}
                {item.can_delete && <button type="button" aria-label={`Delete ${item.title}`} title="Delete" onClick={() => void remove(item)} className="rounded p-1 text-muted-foreground hover:bg-background hover:text-destructive"><Trash2 className="h-3.5 w-3.5" /></button>}
                <button type="button" aria-label={`More for ${item.title}`} aria-expanded={menu === item.path} onClick={() => setMenu(menu === item.path ? '' : item.path)} className="rounded p-1 text-muted-foreground hover:bg-background hover:text-foreground"><MoreHorizontal className="h-3.5 w-3.5" /></button>
              </span>
              {menu === item.path && <div role="menu" className="absolute right-0 top-full z-10 min-w-44 rounded-md border border-border bg-background p-1 text-sm shadow-md">
                <button type="button" role="menuitem" className="block w-full rounded px-2 py-1 text-left hover:bg-muted" onClick={() => { setMenu(''); void open(item) }}>Open</button>
                {!item.from_team && <button type="button" role="menuitem" className="block w-full rounded px-2 py-1 text-left hover:bg-muted" onClick={() => { setMenu(''); void copyLink(item) }}>Copy link</button>}
                {item.can_share_team && <button type="button" role="menuitem" className="block w-full rounded px-2 py-1 text-left hover:bg-muted" onClick={() => { setMenu(''); void shareWithTeam(item, !item.team_shared) }}>{item.team_shared ? 'Remove from Team' : 'Share with Team'}</button>}
                {item.public_link && where && <div className="px-2 py-1"><PublicLinkControl link={item.public_link} change={mode => teamPublicLink(where, item.id, mode)} /></div>}
                {item.can_delete && <button type="button" role="menuitem" className="block w-full rounded px-2 py-1 text-left text-destructive hover:bg-muted" onClick={() => { setMenu(''); void remove(item) }}>Delete</button>}
              </div>}
            </div>
          </div>
        })}
      </div>
    </section>
    {where && tab && <DashboardPeopleCard target={where} />}
  </div>
}
