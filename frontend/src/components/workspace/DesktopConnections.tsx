import { getRawActiveWorkspaceId } from '../../utils/desktopConnection'
import { useCallback, useEffect, useRef, useState, type ReactNode } from 'react'
import { MoreHorizontal, Plus, X } from 'lucide-react'
import { desktopRequest, type DesktopConnection, type DesktopInventory } from '../../services/desktopConnections'
import { getLocalApiBaseUrl, getDesktopApiBaseUrl } from '../../services/api'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'
import { isLocalDeployment } from '../../products/productSurfaceConfig'
import ModalPortal from '../ui/ModalPortal'
import { SidebarProjectRow, SidebarServerGroup, openSidebarProject } from './SidebarServerGroup'

const local: DesktopConnection = { id: 'local', name: 'Local', server: '' }
export function DesktopConnections({ children }: { children?: ReactNode }) {
  if (!window.electronAPI?.getDesktopConnection?.()) {
    const name = window.electronAPI || isLocalDeployment() ? 'Local' : 'Server'
    return <SidebarServerGroup id="local" name={name}>{children}
      {window.electronAPI?.getDesktopConnection && <p className="px-2.5 py-2 text-xs text-muted-foreground">Start the app with the desktop launcher to connect another server.</p>}
    </SidebarServerGroup>
  }
  return <ConnectedServers>{children}</ConnectedServers>
}
function ConnectedServers({ children }: { children?: ReactNode }) {
  const active = getRawActiveWorkspaceId()
  const [connections, setConnections] = useState<DesktopConnection[]>([local])
  const [inventory, setInventory] = useState<Record<string, DesktopInventory>>({})
  const [adding, setAdding] = useState(false)
  const dialog = useRef<HTMLDialogElement>(null)
  useEffect(() => {
    if (adding) dialog.current?.showModal()
    else dialog.current?.close()
  }, [adding])
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
      await window.electronAPI?.openExternal?.(result.url)
    } catch (e) { setPending(null); setError((e as Error).message) }
    finally { setBusy(false) }
  }
  const disconnect = async (id: string) => {
    try { await desktopRequest(`/${id}`, 'DELETE'); if (active === id) open('local', '/home'); else void refresh() }
    catch (e) { setError((e as Error).message) }
  }

  return <div className="space-y-3" aria-label="Connected servers">
    {connections.map(c => {
      const current = c.id === active, items = inventory[c.id]
      const options = (c.id !== 'local' || !current) && <details className="group relative shrink-0">
        <summary aria-label={`Options for ${c.name}`} title={`Options for ${c.name}`} className="flex h-7 w-7 cursor-pointer list-none items-center justify-center rounded text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring [&::-webkit-details-marker]:hidden"><MoreHorizontal className="h-3.5 w-3.5" /></summary>
        <div className="absolute right-0 top-full z-30 min-w-44 rounded-lg border border-border bg-background p-1 text-xs shadow-lg" onClick={event => { const details = event.currentTarget.closest('details'); if (details) details.open = false }}>
          {c.user && <p className="max-w-60 truncate px-2 py-1 text-muted-foreground">{c.user.email || c.user.username}</p>}
          {!current && <button type="button" onClick={() => open(c.id, '/home')} className="block w-full rounded px-2 py-2 text-left hover:bg-muted">Open {c.name}</button>}
          {c.id !== 'local' && <>
            <button type="button" onClick={() => { setError(''); setServer(c.server); setAdding(true) }} className="block w-full rounded px-2 py-2 text-left hover:bg-muted">Reconnect</button>
            <button type="button" onClick={() => void disconnect(c.id)} className="block w-full rounded px-2 py-2 text-left hover:bg-muted">Disconnect</button>
          </>}
        </div>
      </details>
      return <SidebarServerGroup key={c.id} id={c.id} name={c.name} actions={options}>
        {current && children}
        {items?.projects.filter(project => !current || !children || project.mode === 'run')
          .sort((a, b) => a.name.localeCompare(b.name) || a.kind.localeCompare(b.kind) || a.id.localeCompare(b.id))
          .map(project => <SidebarProjectRow key={`${project.kind}:${project.id}`} name={project.name} kind={project.kind} onOpen={() => {
            if (current) openSidebarProject(project.kind, project.id)
            else open(c.id, project.kind === 'crew' ? `/crew/projects/${encodeURIComponent(project.id)}` : `/workflows/${encodeURIComponent(project.id)}`)
          }} />)}
        {!items && (!current || !children) && <p role="status" className="px-2.5 py-2 text-xs text-muted-foreground">Loading projects…</p>}
        {(!current || !children) && items?.status === 'connected' && !items.projects.length && <p className="px-2.5 py-2 text-xs text-muted-foreground">No accessible Crews or Goals.</p>}
        {items?.status === 'unavailable' && <button type="button" onClick={() => void refresh()} className="px-2.5 py-2 text-xs text-muted-foreground underline">Server unavailable · Retry</button>}
        {items?.status === 'sign_in' && <button type="button" onClick={() => { setError(''); setServer(c.server); setAdding(true) }} className="px-2.5 py-2 text-xs text-muted-foreground underline">Sign in again</button>}
      </SidebarServerGroup>
    })}
    <button onClick={() => { setError(''); setAdding(true) }} className="flex h-9 w-full items-center gap-2.5 rounded-lg px-2.5 text-[13px] text-muted-foreground hover:bg-muted hover:text-foreground"><Plus className="h-3.5 w-3.5" />Connect server</button>
    <ModalPortal><dialog ref={dialog} aria-labelledby="connect-server-title" aria-describedby="connect-server-description"
      onCancel={() => setAdding(false)} onClick={event => { if (event.target === event.currentTarget) setAdding(false) }}
      className="fixed inset-0 m-auto w-[calc(100vw_-_2rem)] max-w-lg rounded-xl border border-border bg-background p-0 text-foreground shadow-xl backdrop:bg-black/50">
      <div className="p-5">
        <div className="mb-2 flex items-center justify-between gap-3">
          <h2 id="connect-server-title" className="text-lg font-semibold">Connect server</h2>
          <button type="button" aria-label="Close connect server" onClick={() => setAdding(false)} className="rounded p-1.5 text-muted-foreground hover:bg-muted"><X className="h-4 w-4" /></button>
        </div>
        <p id="connect-server-description" className="mb-5 text-sm text-muted-foreground">Paste any link from your server’s browser page. We’ll find the server address for you.</p>
        <form onSubmit={e => { e.preventDefault(); void connect() }} className="space-y-4">
          <label className="block text-sm font-medium">Server address<input autoFocus required type="url" value={server} onChange={e => setServer(e.target.value)} placeholder="Paste a link from your browser" disabled={busy || !!pending} className="mt-2 w-full rounded-md border border-border bg-background p-2.5 text-sm disabled:opacity-60" /></label>
          {error && <p role="alert" className="rounded-md border border-destructive/30 bg-destructive/5 p-3 text-sm text-destructive">{error}</p>}
          {pending && <p role="status" className="text-sm text-muted-foreground">Finish signing in in your browser, then return here.</p>}
          <div className="flex justify-end gap-2">
            <button type="button" onClick={() => setAdding(false)} className="rounded-md border border-border px-3 py-2 text-sm">Close</button>
            <button disabled={busy || !!pending} className="rounded-md bg-primary px-3 py-2 text-sm text-primary-foreground disabled:opacity-50">{pending ? 'Waiting for sign-in…' : busy ? 'Opening…' : 'Sign in with browser'}</button>
          </div>
        </form>
      </div>
    </dialog></ModalPortal>
    {error && !adding && <p role="alert" className="px-3 text-xs text-destructive">{error}</p>}
  </div>
}
