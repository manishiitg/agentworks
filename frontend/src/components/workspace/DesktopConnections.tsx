import { getRawActiveWorkspaceId } from '../../utils/desktopConnection'
import { useCallback, useEffect, useState, type ReactNode } from 'react'
import { ChevronDown, ChevronRight, Globe, Laptop, Plus } from 'lucide-react'
import { desktopRequest, type DesktopConnection, type DesktopInventory } from '../../services/desktopConnections'
import { getLocalApiBaseUrl, getDesktopApiBaseUrl } from '../../services/api'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'
import { openAppProduct } from '../../utils/appNavigation'

const local: DesktopConnection = { id: 'local', name: 'Local', server: '' }
export function DesktopConnections({ children }: { children?: ReactNode }) {
  if (!window.electronAPI?.getDesktopConnection) return <>{children}</>
  if (!window.electronAPI.getDesktopConnection()) return <>{children}<p className="px-3 py-2 text-xs text-muted-foreground">Start the app with the desktop launcher to use connected servers.</p></>
  return <ConnectedServers>{children}</ConnectedServers>
}
function ConnectedServers({ children }: { children?: ReactNode }) {
  const active = getRawActiveWorkspaceId()
  const [connections, setConnections] = useState<DesktopConnection[]>([local])
  const [inventory, setInventory] = useState<Record<string, DesktopInventory>>({})
  const [collapsed, setCollapsed] = useState<Record<string, boolean>>({})
  const [adding, setAdding] = useState(false)
  const [server, setServer] = useState('')
  const [pending, setPending] = useState<string | null>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  const refresh = useCallback(async () => {
    try {
      const data = await desktopRequest<{ connections: DesktopConnection[] }>()
      const all = [local, ...data.connections]
      setConnections(all)
      useWorkspaceConnectionStore.getState().setProfiles(all.map(c => ({ id: c.id, name: c.name, type: c.id === 'local' ? 'local' : 'remote', apiBaseUrl: c.id === 'local' ? getLocalApiBaseUrl() : `${getDesktopApiBaseUrl()}/api/connections/${c.id}` })))
      await Promise.allSettled(all.map(async c => {
        try {
          const result = await desktopRequest<DesktopInventory>(`/${c.id}/inventory`)
          setInventory(current => ({ ...current, [c.id]: result }))
        } catch { setInventory(current => ({ ...current, [c.id]: { projects: [], status: 'unavailable' } })) }
      }))
    } catch (e) { setError((e as Error).message) }
  }, [])

  useEffect(() => {
    void refresh()
    const onFocus = () => void refresh()
    window.addEventListener('focus', onFocus)
    return () => window.removeEventListener('focus', onFocus)
  }, [refresh])
  useEffect(() => {
    if (!pending) return
    let disposed = false
    let timer: ReturnType<typeof setTimeout>
    const check = async () => {
      try {
        const data = await desktopRequest<{ connection_id: string; error: string }>(`/sign-in/${pending}`)
        if (disposed) return
        if (data.error) throw new Error(data.error)
        if (data.connection_id) { if (data.connection_id === active) { open(active, '/crew'); return }; setPending(null); setAdding(false); setServer(''); void refresh(); return }
        timer = setTimeout(() => void check(), 1500)
      } catch (e) { if (!disposed) { setPending(null); setError((e as Error).message) } }
    }
    void check()
    return () => { disposed = true; clearTimeout(timer) }
  }, [pending, refresh, active])

  const open = (id: string, path: string) => {
    void window.electronAPI?.openDesktopConnection?.(id, path).catch(() => setError('Could not open this server. Try again.'))
  }
  const connect = async () => {
    setBusy(true); setError('')
    try {
      const result = await desktopRequest<{ state: string; url: string }>('', 'POST', { server })
      setPending(result.state)
      window.electronAPI?.openExternal?.(result.url)
    } catch (e) { setError((e as Error).message) }
    finally { setBusy(false) }
  }
  const disconnect = async (id: string) => {
    try { await desktopRequest(`/${id}`, 'DELETE'); if (active === id) open('local', '/home'); else void refresh() }
    catch (e) { setError((e as Error).message) }
  }

  return <div className="space-y-3" aria-label="Connected servers">
    {connections.map(c => {
      const current = c.id === active, items = inventory[c.id]
      return <section key={c.id} className="space-y-1">
        <div className="flex items-center gap-1">
          <button aria-expanded={!collapsed[c.id]} onClick={() => setCollapsed(old => ({ ...old, [c.id]: !old[c.id] }))} className="flex min-w-0 flex-1 items-center gap-2 rounded-lg px-2 py-2 text-left text-sm font-medium hover:bg-muted">
            {collapsed[c.id] ? <ChevronRight className="h-3 w-3" /> : <ChevronDown className="h-3 w-3" />}
            {c.id === 'local' ? <Laptop className="h-4 w-4" /> : <Globe className="h-4 w-4" />}
            <span className="truncate">{c.name}</span>
            {current && <span className="ml-auto h-1.5 w-1.5 shrink-0 rounded-full bg-emerald-500" aria-label="Open here" />}
          </button>
        </div>
        {!collapsed[c.id] && <div className="space-y-1">
          {c.user && <p className="truncate px-3 text-[11px] text-muted-foreground" title={c.server}>{c.user.email || c.user.username}</p>}
          {current && c.id !== 'local' && <p className="px-3 text-xs text-muted-foreground">Chats run on {c.name}</p>}
          {current && children}
          <>
            {items?.projects.filter(project => !current || !children || project.mode === 'run').map(project => {
              const path = project.kind === 'crew' ? `/crew/projects/${encodeURIComponent(project.id)}` : `/workflows/${encodeURIComponent(project.id)}`
              return <button key={`${project.kind}:${project.id}`} onClick={() => { if (current) openAppProduct({ surface: project.kind === 'crew' ? 'work' : 'agentworks', id: project.id }); else open(c.id, path) }} className="block w-full truncate rounded-lg px-4 py-2 text-left text-[13px] hover:bg-muted" title={project.name}>
                <span className="mr-2 text-[10px] text-muted-foreground">{project.kind === 'crew' ? 'Crew' : 'Goal'}</span>{project.name}
              </button>
            })}
            {!items && (!current || !children) && <p role="status" className="px-4 py-2 text-xs text-muted-foreground">Loading projects…</p>}
            {(!current || !children) && items?.status === 'connected' && !items.projects.length && <p className="px-4 py-2 text-xs text-muted-foreground">No accessible Crews or Goals.</p>}
          </>
          {items?.status === 'unavailable' && <button onClick={() => void refresh()} className="px-3 text-xs text-muted-foreground underline">Server unavailable · Retry</button>}
          {c.id !== 'local' && <div className="flex gap-3 px-3 text-xs text-muted-foreground">
            <button onClick={() => { setServer(c.server); setAdding(true) }}>{items?.status === 'sign_in' ? 'Sign in again' : 'Reconnect'}</button>
            <button onClick={() => void disconnect(c.id)}>Disconnect</button>
          </div>}
          {!current && c.id === 'local' && <button onClick={() => open('local', '/home')} className="px-3 text-xs text-muted-foreground underline">Open Local</button>}
        </div>}
      </section>
    })}
    <button onClick={() => setAdding(v => !v)} className="flex w-full items-center gap-2 rounded-lg px-3 py-2 text-sm text-muted-foreground hover:bg-muted"><Plus className="h-4 w-4" />Connect server</button>
    {adding && <form onSubmit={e => { e.preventDefault(); void connect() }} className="space-y-2 rounded-lg border border-border p-3">
      <label className="block text-xs">Server address<input required type="url" value={server} onChange={e => setServer(e.target.value)} placeholder="https://example.com" className="mt-1 w-full rounded border border-border bg-background p-2 text-sm" /></label>
      <button disabled={busy || !!pending} className="rounded bg-primary px-3 py-2 text-xs text-primary-foreground disabled:opacity-50">{pending ? 'Waiting for browser sign-in…' : busy ? 'Opening…' : 'Sign in with browser'}</button>
    </form>}
    {error && <p role="alert" className="px-3 text-xs text-destructive">{error}</p>}
  </div>
}
