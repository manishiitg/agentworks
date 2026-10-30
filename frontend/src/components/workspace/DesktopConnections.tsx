import { isChatIdentityCurrent } from '../../utils/chatIdentity'
import { getRawActiveWorkspaceId } from '../../utils/desktopConnection'
import { useEffect, useRef, useState, type ReactNode } from 'react'
import { Plus, Server, X } from 'lucide-react'
import { desktopRequest } from '../../services/desktopConnections'
import { connectedLocations, hasDesktopConnections, useConnectedWorkStore } from '../../stores/useConnectedWorkStore'
import { isLocalDeployment } from '../../products/productSurfaceConfig'
import ModalPortal from '../ui/ModalPortal'
import { SidebarProjectRow, openSidebarProject } from './SidebarServerGroup'

type SidebarProject = { id: string; name: string; kind: 'crew' | 'goal'; icon?: string; selected?: boolean }
export function DesktopConnections({ children, projects = [] }: { children?: ReactNode; projects?: SidebarProject[] }) {
  if (!hasDesktopConnections()) {
    const name = window.electronAPI || isLocalDeployment() ? 'Local' : 'Server'
    return <div className="min-h-0 flex-1 overflow-y-auto px-2 pb-3">
      <h2 className="px-2.5 py-2 text-xs font-medium text-muted-foreground">Your work</h2>
      {projects.map(project => <SidebarProjectRow key={`${project.kind}:${project.id}`} {...project} location={name} onOpen={() => openSidebarProject(project.kind, project.id)} />)}
      {children}
      {window.electronAPI?.getDesktopConnection && <p className="px-2.5 py-2 text-xs text-muted-foreground">Start the app with the desktop launcher to connect another server.</p>}
    </div>
  }
  return <ConnectedServers projects={projects}>{children}</ConnectedServers>
}
function ConnectedServers({ children, projects }: { children?: ReactNode; projects: SidebarProject[] }) {
  const active = getRawActiveWorkspaceId()
  const state = useConnectedWorkStore()
  const connections = connectedLocations(state)
  const inventory = isChatIdentityCurrent(state.identity) ? state.inventory : {}
  const { refresh, error: directoryError } = state
  const [filter, setFilter] = useState('all')
  useEffect(() => { if (filter !== 'all' && !connections.some(connection => connection.id === filter)) setFilter('all') }, [connections, filter])
  const [adding, setAdding] = useState(false)
  const [showConnectForm, setShowConnectForm] = useState(false)
  const dialog = useRef<HTMLDialogElement>(null)
  useEffect(() => {
    if (adding) dialog.current?.showModal()
    else dialog.current?.close()
  }, [adding])
  const [server, setServer] = useState('')
  const [pending, setPending] = useState<string | null>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const beginConnect = (address = '') => { setError(''); setServer(address); setShowConnectForm(true); setAdding(true) }

  useEffect(() => {
    void refresh()
    const onFocus = () => void refresh()
    const timer = window.setInterval(() => { if (!document.hidden) void refresh() }, 60_000)
    window.addEventListener('focus', onFocus)
    return () => { window.clearInterval(timer); window.removeEventListener('focus', onFocus) }
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

  const rows = connections.flatMap(connection => {
    const byId = new Map<string, SidebarProject>()
    for (const project of inventory[connection.id]?.projects || []) byId.set(`${project.kind}:${project.id}`, project)
    if (connection.id === active) for (const project of projects) byId.set(`${project.kind}:${project.id}`, project)
    return [...byId.values()].map(project => ({ ...project, connection }))
  }).sort((a, b) => a.name.localeCompare(b.name) || a.connection.name.localeCompare(b.connection.name) || a.id.localeCompare(b.id))
  const visible = rows.filter(row => filter === 'all' || row.connection.id === filter)
  const locations = connections.filter(connection => filter === 'all' || connection.id === filter)
  const connected = connections.filter(connection => connection.id !== 'local' && inventory[connection.id]?.status === 'connected').length
  return <div className="flex min-h-0 flex-1 flex-col" aria-label="Connected work">
    <div className="flex shrink-0 items-center justify-between gap-2 px-4 py-2">
      <h2 className="text-xs font-medium text-muted-foreground">Your work</h2>
      <select aria-label="Work location" value={filter} onChange={event => setFilter(event.target.value)} className="max-w-[55%] rounded bg-transparent py-1 text-xs text-muted-foreground outline-none focus-visible:ring-2 focus-visible:ring-ring">
        <option value="all">All</option>{connections.map(connection => <option key={connection.id} value={connection.id}>{connection.name}</option>)}
      </select>
    </div>
    <div className="min-h-0 flex-1 overflow-y-auto px-2 pb-3">
      {visible.map(project => <SidebarProjectRow key={`${project.connection.id}:${project.kind}:${project.id}`} {...project}
        selected={project.connection.id === active && project.selected} location={project.connection.name}
        onOpen={() => project.connection.id === active ? openSidebarProject(project.kind, project.id) : open(project.connection.id, project.kind === 'crew' ? `/crew/projects/${encodeURIComponent(project.id)}` : `/workflows/${encodeURIComponent(project.id)}`)} />)}
      {(filter === 'all' || filter === active) && children}
      {locations.map(connection => {
        const status = inventory[connection.id]?.status
        if (!status) return <p key={connection.id} role="status" className="px-2.5 py-2 text-xs text-muted-foreground">Loading {connection.name}…</p>
        if (status === 'unavailable') return <button key={connection.id} className="block px-2.5 py-2 text-left text-xs text-muted-foreground underline" onClick={() => void refresh()}>{connection.name} unavailable · Retry</button>
        if (status === 'sign_in') return <button key={connection.id} className="block px-2.5 py-2 text-left text-xs text-muted-foreground underline" onClick={() => beginConnect(connection.server)}>{connection.name} · Sign in again</button>
        return null
      })}
      {!visible.length && locations.every(connection => inventory[connection.id]?.status === 'connected') && <p className="px-2.5 py-2 text-xs text-muted-foreground">No Crews or Goals here yet.</p>}
    </div>
    <div className="shrink-0 border-t border-border/50 p-2">
      <button onClick={() => { setError(''); setShowConnectForm(false); setAdding(true) }} className="flex h-9 w-full items-center gap-2.5 rounded-lg px-2.5 text-[13px] text-muted-foreground hover:bg-muted hover:text-foreground"><Server className="h-3.5 w-3.5" />Connections<span className="ml-auto text-xs">{connected} connected</span></button>
      {(error || directoryError) && !adding && <p role="alert" className="px-3 text-xs text-destructive">{error || directoryError}</p>}
    </div>
    <ModalPortal><dialog ref={dialog} aria-labelledby="connect-server-title" aria-describedby="connect-server-description"
      onCancel={() => setAdding(false)} onClick={event => { if (event.target === event.currentTarget) setAdding(false) }}
      className="fixed inset-0 m-auto w-[calc(100vw_-_2rem)] max-w-lg rounded-xl border border-border bg-background p-0 text-foreground shadow-xl backdrop:bg-black/50">
      <div className="p-5">
        <div className="mb-2 flex items-center justify-between gap-3">
          <h2 id="connect-server-title" className="text-lg font-semibold">{showConnectForm ? 'Connect server' : 'Connections'}</h2>
          <button type="button" aria-label="Close connections" onClick={() => setAdding(false)} className="rounded p-1.5 text-muted-foreground hover:bg-muted"><X className="h-4 w-4" /></button>
        </div>
        {!showConnectForm ? <div className="space-y-3">
          <p id="connect-server-description" className="text-sm text-muted-foreground">Your work stays on the computer or server where it runs.</p>
          {connections.map(connection => <div key={connection.id} className="rounded-lg border border-border p-3">
            <div className="flex items-center justify-between gap-2"><span className="font-medium">{connection.name}</span><span className="text-xs text-muted-foreground">{inventory[connection.id]?.status === 'connected' ? 'Connected' : inventory[connection.id]?.status === 'sign_in' ? 'Sign in again' : 'Unavailable'}</span></div>
            {connection.user && <p className="truncate text-xs text-muted-foreground">{connection.user.email || connection.user.username}</p>}
            <div className="mt-2 flex gap-4 text-xs">
              <button className="text-primary" onClick={() => open(connection.id, '/home')}>Open</button>
              {connection.id !== 'local' && <><button className="text-primary" onClick={() => beginConnect(connection.server)}>Reconnect</button><button className="text-muted-foreground" onClick={() => void disconnect(connection.id)}>Disconnect</button></>}
            </div>
          </div>)}
          {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
          <button className="flex items-center gap-2 rounded-md bg-primary px-3 py-2 text-sm text-primary-foreground" onClick={() => beginConnect()}><Plus className="h-4 w-4" />Connect server</button>
        </div> : <>
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
        <button type="button" className="mt-3 text-xs text-muted-foreground underline" onClick={() => setShowConnectForm(false)}>Back to connections</button>
        </>}
      </div>
    </dialog></ModalPortal>
  </div>
}
