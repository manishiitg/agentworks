import { useEffect, useState } from 'react'
import { agentApi, type CrewStorageStatus } from '../../services/api'
import { Button } from '../ui/Button'

const stateLabel: Record<string, string> = {
  ready: 'Ready', needs_attention: 'Needs attention', move_failed: 'Could not move',
  already_organised: 'Already in Crew', moved: 'Moved to Crew',
}

/** Administrator view of the same server operation exposed through MCP. */
export default function CrewStoragePanel() {
  const [operation, setOperation] = useState('')
  const [status, setStatus] = useState<CrewStorageStatus>()
  const [offset, setOffset] = useState(0)
  const [refreshVersion, setRefreshVersion] = useState(0)
  const [pending, setPending] = useState(false)
  const [error, setError] = useState('')
  useEffect(() => {
    if (!operation) return
    let disposed = false
    let timer: ReturnType<typeof setTimeout>
    const refresh = async () => {
      try {
        const result = await agentApi.getCrewStorageCheck(operation, offset)
        if (disposed) return
        setStatus(result)
        if (result.state === 'scanning' || result.state === 'backing_up' || result.state === 'moving') timer = setTimeout(() => { void refresh() }, 2000)
      } catch {
        if (!disposed) setError('Could not load this check. Try checking the projects again.')
      }
    }
    void refresh()
    return () => { disposed = true; clearTimeout(timer) }
  }, [operation, offset, refreshVersion])

  const start = async () => {
    setPending(true); setError('')
    try {
      const result = await agentApi.startCrewStorageCheck()
      setStatus(undefined); setOffset(0); setOperation(result.operation_id)
    } catch { setError('Could not start the storage check. Try again.') }
    finally { setPending(false) }
  }
  const backup = async (workspace: string) => {
    setPending(true); setError('')
    try {
      await agentApi.backupCrewStorageProject(operation, workspace)
      setStatus(await agentApi.getCrewStorageCheck(operation, offset))
      setRefreshVersion(value => value + 1)
    } catch { setError('Could not start this backup. Run a fresh check and try again.') }
    finally { setPending(false) }
  }
  const move = async (workspace?: string) => {
    setPending(true); setError('')
    try {
      await agentApi.moveCrewStorageProjects(operation, workspace)
      setStatus(await agentApi.getCrewStorageCheck(operation, offset))
      setRefreshVersion(value => value + 1)
    } catch { setError('Could not start this move. Check the project status and try again.') }
    finally { setPending(false) }
  }
  const working = pending || status?.state === 'scanning' || status?.state === 'backing_up' || status?.state === 'moving'
  return <section aria-label="Crew storage" className="mt-8 rounded-lg border border-border p-4">
    <h2 className="text-sm font-semibold">Crew storage</h2>
    <p className="mt-1 text-sm text-muted-foreground">Move existing projects into Crew. Backups are verified first, and old links keep working.</p>
    <Button className="mt-3" disabled={working} onClick={() => { void start() }}>{working ? 'Checking files…' : 'Check existing projects'}</Button>
    {error || status?.error ? <p role="alert" className="mt-2 text-sm text-destructive">{error || status?.error}</p> : null}
    {status?.move_available ? <Button className="ml-2" disabled={working} onClick={() => { void move() }}>Move ready projects to Crew</Button> : null}
    {status ? <p className="mt-2 text-sm text-muted-foreground">{status.total} projects · {status.state === 'complete' ? 'Complete' : status.state === 'moving' ? 'Moving projects…' : status.state === 'backing_up' ? 'Verifying backup…' : 'Checking files…'}.</p> : null}
    {status?.projects.length ? <div className="mt-3 overflow-x-auto"><table className="w-full text-left text-sm">
      <thead><tr><th className="p-2">Project</th><th className="p-2">Owner</th><th className="p-2">Size</th><th className="p-2">Status</th><th className="p-2">Backup</th><th className="p-2">Move</th></tr></thead>
      <tbody>{status.projects.map(project => <tr key={project.key} className="border-t border-border">
        <td className="p-2">{project.name}</td><td className="p-2">{project.owner_name || 'Unknown'}</td>
        <td className="p-2">{project.bytes ? (project.bytes / 1048576).toLocaleString(undefined, { maximumFractionDigits: 1 }) + ' MB' : '—'}</td>
        <td className="p-2">{stateLabel[project.state] || project.state}{project.reasons?.map(reason => <p key={reason} className="text-xs text-muted-foreground">{reason}</p>)}</td>
        <td className="p-2">{project.restore_verified ? 'Project archive and restore verified' : project.backup_state === 'archive_verified' ? 'Project archive verified' : project.state === 'ready' && project.workspace_id
          ? <Button disabled={working} onClick={() => { void backup(project.workspace_id!) }}>Back up and verify restore</Button>
          : project.backup_state === 'not_backed_up' ? 'Not backed up' : '—'}</td>
        <td className="p-2">{project.state === 'ready' && project.workspace_id && status.move_available ? <Button disabled={working} onClick={() => { void move(project.workspace_id!) }}>Move to Crew</Button> : '—'}</td>
      </tr>)}</tbody>
    </table>
      <div className="mt-2 flex gap-2"><Button disabled={working || offset === 0} onClick={() => setOffset(Math.max(0, offset - 25))}>Previous</Button><Button disabled={working || !status.has_more} onClick={() => setOffset(status.next_offset)}>Next</Button></div>
    </div> : null}
  </section>
}
