import { useEffect, useState } from 'react'
import type { ComponentProps } from 'react'
import api from '../../services/api'
import { ReportView } from '../../components/workflow/ReportViewer'

type Dashboard = { workspace_path: string; name: string }
// A migrated report keeps its original workflow database and API scope. The
// server supplies admitted destinations; the browser never constructs a path.
export function CrewDashboardPanel({ projectId, ...props }: ComponentProps<typeof ReportView> & { projectId: string }) {
  const [catalog, setCatalog] = useState<{ dashboards: Dashboard[]; default_dashboard_workspace: string } | null>(null)
  const [selected, setSelected] = useState('')
  const [error, setError] = useState('')
  useEffect(() => {
    const controller = new AbortController()
    setCatalog(null); setSelected(''); setError('')
    void api.get('/api/crew-goals', { params: { crew_id: projectId }, signal: controller.signal }).then(({ data }) => setCatalog(data)).catch(() => { if (!controller.signal.aborted) setError('Could not load dashboards. Reopen this panel to try again.') })
    return () => controller.abort()
  }, [projectId])
  if (error) return <p role="alert" className="p-4 text-sm text-destructive">{error}</p>
  if (!catalog) return <p className="p-4 text-sm text-muted-foreground">Loading dashboard…</p>
  const choices = [...(catalog.default_dashboard_workspace === props.workspacePath ? [{ workspace_path: props.workspacePath, name: 'Crew dashboard' }] : []), ...catalog.dashboards]
  const workspace = selected || catalog.default_dashboard_workspace || props.workspacePath
  return <div className="flex h-full min-h-0 flex-col">
    {choices.length > 1 && <label className="flex items-center gap-3 border-b px-4 py-2 text-sm">Dashboard<select aria-label="Dashboard" className="min-w-0 rounded-md border bg-background px-2 py-1" value={workspace} onChange={e => setSelected(e.target.value)}>{choices.map(item => <option key={item.workspace_path} value={item.workspace_path}>{item.name}</option>)}</select></label>}
    <div className="min-h-0 flex-1"><ReportView {...props} key={workspace} workspacePath={workspace} /></div>
  </div>
}
