import { useEffect, useState } from 'react'
import { Loader2 } from 'lucide-react'
import api from '../../services/api'

/** One MCP server found in the person's own Claude, Codex, Cursor, agy or Claude Desktop settings (names only). */
export type LocalMcpServer = {
  id: string; name: string; sources: string[]; scope: 'project' | 'all'; kind: 'url' | 'command'; program: string
  keys?: string[]; in_vault?: boolean; importable: boolean; note?: string
}
type ImportResult = { id: string; name: string; status: 'added' | 'needs_sign_in' | 'failed'; message?: string; auth_url?: string }
export type LocalMcpTarget = { projectId?: string; workspacePath?: string }

const target = ({ projectId, workspacePath }: LocalMcpTarget) => workspacePath ? { workspace_path: workspacePath } : { project_id: projectId }

/** Servers that could be imported, or null when this computer cannot import (not the Mac app, not the owner). */
export function useLocalMcpServers(scope: LocalMcpTarget, enabled = true) {
  const [servers, setServers] = useState<LocalMcpServer[] | null>(null)
  const [refresh, setRefresh] = useState(0)
  useEffect(() => {
    if (!enabled || (!scope.projectId && !scope.workspacePath)) return
    const abort = new AbortController()
    void api.get<{ servers: LocalMcpServer[] }>('/api/vault/local-mcp', { params: target(scope), signal: abort.signal })
      .then(({ data }) => { if (!abort.signal.aborted) setServers(data.servers || []) })
      .catch(() => { if (!abort.signal.aborted) setServers(null) })
    return () => abort.abort()
  }, [scope.projectId, scope.workspacePath, enabled, refresh])
  return { servers, reload: () => setRefresh(n => n + 1) }
}

export function LocalMcpImport({ servers, scope, onImported }: { servers: LocalMcpServer[]; scope: LocalMcpTarget; onImported?: () => void }) {
  const [picked, setPicked] = useState<Set<string>>(() => new Set(servers.filter(s => s.importable && !s.in_vault).map(s => s.id)))
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [results, setResults] = useState<Record<string, ImportResult>>({})
  const run = async () => {
    if (!picked.size || busy) return
    setBusy(true); setError('')
    try {
      const { data } = await api.post<{ results: ImportResult[] }>('/api/vault/local-mcp', { ...target(scope), ids: [...picked] })
      setResults(Object.fromEntries(data.results.map(r => [r.id, r])))
      setPicked(new Set())
      onImported?.()
    } catch (cause) {
      const reply = (cause as { response?: { data?: { error?: string | { message?: string } } } })?.response?.data?.error
      setError(typeof reply === 'string' ? reply : reply?.message || 'Could not import. Try again.')
    } finally { setBusy(false) }
  }
  if (!servers.length) return <p className="text-sm text-muted-foreground">No MCP servers found in your Claude, Codex, Cursor or agy settings.</p>
  return <div data-testid="local-mcp-import">
    <ul className="divide-y divide-border rounded-lg border border-border">
      {servers.map(server => {
        const done = results[server.id]
        return <li key={server.id} className="flex items-start gap-3 p-3">
          <input type="checkbox" aria-label={`Import ${server.name}`} className="mt-1" disabled={!server.importable || busy || !!done}
            checked={picked.has(server.id)} onChange={event => setPicked(current => { const next = new Set(current); if (event.target.checked) next.add(server.id); else next.delete(server.id); return next })} />
          <div className="min-w-0 flex-1 text-sm">
            <p className="font-medium">{server.name} <span className="font-normal text-muted-foreground">· {server.kind === 'command' ? `runs ${server.program} on this Mac` : server.program}</span></p>
            <p className="text-xs text-muted-foreground">From {server.sources.join(', ')}{server.scope === 'project' ? ' · this folder' : ''}{server.in_vault ? ' · already in your Vault' : ''}</p>
            {!!server.keys?.length && <p className="text-xs text-muted-foreground">Keys {server.keys.join(', ')} move into your Vault, encrypted.</p>}
            {server.note && <p className="text-xs text-amber-600 dark:text-amber-400">{server.note}</p>}
            {done && <p role="status" className={`text-xs ${done.status === 'failed' ? 'text-destructive' : 'text-foreground'}`}>{done.message}{done.auth_url && <> <a href={done.auth_url} target="_blank" rel="noreferrer" className="underline">Sign in</a></>}</p>}
          </div>
        </li>
      })}
    </ul>
    {error && <p role="alert" className="mt-2 text-sm text-destructive">{error}</p>}
    <div className="mt-3 flex items-center justify-between gap-3">
      <p className="text-xs text-muted-foreground">They are added to your Vault and selected for this project.</p>
      <button type="button" disabled={!picked.size || busy} onClick={() => void run()} className="inline-flex items-center gap-2 rounded-lg bg-primary px-3 py-2 text-sm font-medium text-primary-foreground disabled:opacity-50">
        {busy && <Loader2 className="h-4 w-4 animate-spin" />}Import {picked.size || ''}
      </button>
    </div>
  </div>
}
