import { useEffect, useState } from 'react'
import { Workflow } from 'lucide-react'
import { workflowManifestApi } from '../../services/api'
import type { DiscoveredWorkflow } from '../../services/api-types'
import { useCapabilitiesStore } from '../../stores/useCapabilitiesStore'
import { useProductSurfaceStore } from '../../stores/useProductSurfaceStore'
import { openCrewWorkflow } from '../../products/work/workflowRunNavigation'
import { captureChatIdentity, isChatIdentityCurrent } from '../../utils/chatIdentity'
import { openAppProduct } from '../../utils/appNavigation'
import { openProductWorkspace } from '../../utils/productWorkspaceNavigation'
import { PageRow } from '../automation/AutomationKit'
import { WorkspacePageContent } from '../workspace/WorkspacePageContent'
import { useWorkspaceProjects } from '../workspace/WorkspaceProjectsProvider'
import { AutomationOverviewToolbar } from './AutomationOverviewToolbar'

export default function GlobalWorkflowsView({ crew }: { crew: boolean }) {
  const directory = useWorkspaceProjects()
  const local = useCapabilitiesStore(state => state.capabilities?.local_mode)
  const [items, setItems] = useState<DiscoveredWorkflow[]>([])
  const [loading, setLoading] = useState(true)
  const [failed, setFailed] = useState(false)
  const [revision, setRevision] = useState(0)
  const [search, setSearch] = useState('')
  const [openRow, setOpenRow] = useState<string | null>(null)

  useEffect(() => {
    const identity = captureChatIdentity()
    let disposed = false
    const current = () => !disposed && isChatIdentityCurrent(identity)
    setLoading(true)
    setFailed(false)
    // Discovery is indexed and permission-filtered on the server. No per-Crew
    // folder reads, watcher lookups or counting runs to find saved workflows.
    void workflowManifestApi.listWorkflowManifests().then(result => {
      if (current()) setItems(result.workflows ?? [])
    }).catch(() => {
      if (current()) { setItems([]); setFailed(true) }
    }).finally(() => { if (current()) setLoading(false) })
    return () => { disposed = true }
  }, [revision])

  const refresh = () => {
    if (crew && directory.error) directory.retry()
    setRevision(value => value + 1)
  }
  const crews = new Map(directory.projects.map(project => [project.id, project]))
  const rows = items.flatMap(item => {
    if (item.manifest.kind === 'relay' || !item.manifest.id) return []
    const owner = item.crew_id ? crews.get(item.crew_id) : undefined
    // Join only on the server-resolved parent, never on its name, a saved
    // workflow field or a folder prefix. The directory also admits the link.
    if (crew ? !owner : item.crew_id) return []
    const name = item.manifest.label || 'Untitled workflow'
    const where = owner?.location ?? (owner?.where === 'mac' ? 'mac' : owner?.where === 'web' ? 'server' : undefined)
    const place = where === 'server' || (!owner && local === false) ? 'Online'
      : where === 'mac' ? local ? 'On this Mac' : 'On a Mac'
        : !owner && local ? 'On this Mac' : ''
    return [{ item, owner, name, place }]
  }).sort((a, b) => a.name.localeCompare(b.name) || (a.owner?.name ?? '').localeCompare(b.owner?.name ?? ''))
  const query = search.trim().toLowerCase()
  const visible = rows.filter(row => `${row.name} ${row.owner?.name ?? ''}`.toLowerCase().includes(query))
  const error = failed || (crew && directory.error)
  const pending = loading || (crew && directory.loading)

  return <WorkspacePageContent className="flex h-full min-h-0 w-full flex-col">
    <AutomationOverviewToolbar label="workflows" search={search} onSearch={setSearch} loading={pending} onRefresh={refresh} />
    <div className="min-h-0 flex-1 overflow-y-auto">
      {error ? <p role="alert" className="rounded-lg border border-border bg-card p-4 text-sm">Could not load workflows. <button type="button" className="underline" onClick={refresh}>Retry</button></p>
        : pending && rows.length === 0 ? <p role="status" className="rounded-lg border border-border bg-card p-4 text-sm text-muted-foreground">Loading workflows…</p>
          : rows.length === 0 ? <p className="rounded-lg border border-border bg-card p-4 text-sm text-muted-foreground">No workflows yet. Ask an agent in a {crew ? 'Project' : 'project'} to create one.</p>
            : visible.length === 0 ? <p className="rounded-lg border border-border bg-card p-4 text-sm text-muted-foreground">No matching workflows. <button type="button" className="underline" onClick={() => setSearch('')}>Clear search</button></p>
              : <div className="divide-y divide-border overflow-hidden rounded-lg border border-border bg-card">
                {visible.map(({ item, owner, name, place }) => <PageRow key={item.workspace_path} lead={<Workflow aria-hidden="true" className="h-4 w-4 text-muted-foreground" />}
                  title={<span title={name}>{name}</span>} lines={[owner?.name]} aside={place}
                  open={openRow === item.workspace_path} onToggle={() => setOpenRow(openRow === item.workspace_path ? null : item.workspace_path)}>
                  {item.manifest.description && <p className="whitespace-pre-wrap break-words text-sm text-muted-foreground">{item.manifest.description}</p>}
                  <button type="button" className="text-primary hover:underline" onClick={() => {
                    if (owner) {
                      useProductSurfaceStore.getState().setSelectedWorkProjectId(owner.id)
                      if (owner.mode === 'builder') openCrewWorkflow(owner.id, item.manifest.id)
                      openProductWorkspace('work')
                      openAppProduct({ surface: 'work', id: owner.id, view: owner.mode === 'builder' ? 'workflow' : undefined })
                    } else {
                      openAppProduct({ surface: 'agentworks', id: item.manifest.id })
                    }
                  }}>{owner?.mode === 'run' ? 'Open project' : 'Open workflow'}</button>
                </PageRow>)}
              </div>}
    </div>
  </WorkspacePageContent>
}
