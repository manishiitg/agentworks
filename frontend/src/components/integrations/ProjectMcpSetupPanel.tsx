import { useEffect, useState } from 'react'
import api from '../../services/api'
import { CliMcpSetupPanel } from './CliMcpSetupPanel'

type ProjectSetup = { id: string; kind: string; title: string; view_url: string; use_url: string }

/** The server resolves the workspace to a stable project ID. No token is
 * created here; every recipient signs in and approves their own connection. */
export function ProjectMcpSetupPanel({ workspacePath }: { workspacePath: string }) {
  const [project, setProject] = useState<ProjectSetup | null>(null)
  const [access, setAccess] = useState<'view' | 'use'>('use')
  const [error, setError] = useState('')
  useEffect(() => {
    let active = true
    setProject(null); setError('')
    api.get<ProjectSetup>('/api/oauth/mcp/project', { params: { workspace: workspacePath } })
      .then(({ data }) => { if (active) setProject(data) })
      .catch(() => { if (active) setError('Could not load this project’s connection. Check your access and the server’s browser sign-in configuration.') })
    return () => { active = false }
  }, [workspacePath])
  if (error) return <p role="alert" className="text-sm text-destructive">{error}</p>
  if (!project) return <p role="status" className="text-sm text-muted-foreground">Loading project connection…</p>
  const endpoint = access === 'use' ? project.use_url : project.view_url
  return <div className="space-y-4">
    <section aria-label="Project connection" className="space-y-3 rounded-lg border border-border p-3">
      <div className="flex flex-wrap items-center justify-between gap-3"><div><span className="text-xs text-muted-foreground">Project</span><h3 className="font-semibold">{project.title}</h3></div>
        <label className="flex items-center gap-2 text-sm">Access<select aria-label="Project MCP access" value={access} onChange={event => setAccess(event.target.value as 'view' | 'use')} className="rounded-md border border-border bg-background p-2"><option value="view">View</option><option value="use">Use</option></select></label>
      </div>
      <p className="text-xs text-muted-foreground">{access === 'view' ? 'Read permitted files, results and dashboards.' : 'View results, start permitted runs and call published functions.'} This project only.</p>
      <details className="text-xs text-muted-foreground"><summary className="cursor-pointer">Access and sharing</summary><p className="mt-2">Share this project first. They sign in with their own account. The URL grants no access. Existing roles and function permissions apply; settings and building are excluded. Removing project access disconnects the person.</p></details>
    </section>
    <CliMcpSetupPanel key={endpoint} endpoint={endpoint} projectName={project.title} connectionName={`agentworks-${project.kind}-${project.id}`} />
  </div>
}
