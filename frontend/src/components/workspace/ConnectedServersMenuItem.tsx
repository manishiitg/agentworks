import { useEffect, useRef, useState } from 'react'
import { Plus, Server, X } from 'lucide-react'
import { serverIconColors } from '../../utils/connectedWorkPresentation'
import { desktopRequest } from '../../services/desktopConnections'
import { getRawActiveWorkspaceId } from '../../utils/desktopConnection'
import { isChatIdentityCurrent } from '../../utils/chatIdentity'
import { connectedLocations, hasDesktopConnections, useConnectedWorkStore } from '../../stores/useConnectedWorkStore'
import { useSidebarExpansionStore } from '../../stores/useSidebarExpansionStore'
import ModalPortal from '../ui/ModalPortal'

export function openConnectedServers(server = '') {
  window.dispatchEvent(new CustomEvent('open-connected-servers', { detail: { server } }))
}

export function ConnectedServersMenuItem() {
  return hasDesktopConnections() ? <ConnectedServersControl /> : null
}

function ConnectedServersControl() {
  const active = getRawActiveWorkspaceId()
  const state = useConnectedWorkStore()
  const connections = connectedLocations(state)
  const inventory = isChatIdentityCurrent(state.identity) ? state.inventory : {}
  const serverColors = serverIconColors(connections)
  const refresh = state.refresh
  const [adding, setAdding] = useState(false)
  const [renaming, setRenaming] = useState<string | null>(null)
  const [connectionName, setConnectionName] = useState('')
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

  useEffect(() => {
    const show = (event: Event) => {
      const address = (event as CustomEvent<{ server?: string }>).detail?.server || ''
      setError(''); setServer(address); setShowConnectForm(Boolean(address)); setAdding(true)
    }
    window.addEventListener('open-connected-servers', show)
    return () => window.removeEventListener('open-connected-servers', show)
  }, [])
  useEffect(() => { if (adding) void refresh() }, [adding, refresh])
  return <>
    <button type="button" role="menuitem" data-product-navigation-action aria-label="Connected servers"
      onClick={() => { setError(''); setShowConnectForm(false); setAdding(true) }}>
      <Server className="h-4 w-4" /><span>Connected servers</span>
    </button>
    <ModalPortal><dialog ref={dialog} aria-labelledby="connect-server-title" aria-describedby="connect-server-description"
      onCancel={() => setAdding(false)} onClick={event => { if (event.target === event.currentTarget) setAdding(false) }}
      className="fixed inset-0 m-auto w-[calc(100vw_-_2rem)] max-w-lg rounded-xl border border-border bg-background p-0 text-foreground shadow-xl backdrop:bg-black/50">
      <div className="p-5">
        <div className="mb-2 flex items-center justify-between gap-3">
          <h2 id="connect-server-title" className="text-lg font-semibold">{showConnectForm ? 'Connect server' : 'Connected servers'}</h2>
          <button type="button" aria-label="Close connections" onClick={() => setAdding(false)} className="rounded p-1.5 text-muted-foreground hover:bg-muted"><X className="h-4 w-4" /></button>
        </div>
        {!showConnectForm ? <div className="space-y-3">
          <p id="connect-server-description" className="text-sm text-muted-foreground">Your work stays on the computer or server where it runs.</p>
          {connections.map(connection => <div key={connection.id} className="rounded-lg border border-border p-3">
            {renaming === connection.id ? <form className="mb-2 flex items-center gap-2" onSubmit={event => {
              event.preventDefault()
              const name = connectionName.trim()
              if (!name) return
              useSidebarExpansionStore.getState().renameConnection(connection.id, name)
              useConnectedWorkStore.setState(state => ({ connections: state.connections.map(item => item.id === connection.id ? { ...item, name } : item) }))
              setRenaming(null)
            }}>
              <input aria-label="Connection name" autoFocus maxLength={24} required value={connectionName} onChange={event => setConnectionName(event.target.value)} className="min-w-0 flex-1 rounded border border-border bg-background px-2 py-1 text-sm" />
              <button className="text-xs text-primary">Save</button>
              <button type="button" onClick={() => setRenaming(null)} className="text-xs text-muted-foreground">Cancel</button>
            </form> : null}
            <div className="flex items-center justify-between gap-2"><span className="flex min-w-0 items-center gap-2 font-medium">{connection.id !== 'local' && <Server aria-hidden="true" className={`h-3.5 w-3.5 shrink-0 ${serverColors[connection.server]}`} />}<span className="truncate">{connection.name}</span></span><span className="text-xs text-muted-foreground">{inventory[connection.id]?.status === 'connected' ? 'Connected' : inventory[connection.id]?.status === 'sign_in' ? 'Sign in again' : 'Unavailable'}</span></div>
            {connection.server && <p className="mt-1 truncate text-xs text-muted-foreground" title={connection.server}>{connection.server}</p>}
            {connection.user && <p className="truncate text-xs text-muted-foreground">{connection.user.email || connection.user.username}</p>}
            <div className="mt-2 flex gap-4 text-xs">
              <button className="text-primary" onClick={() => open(connection.id, '/home')}>Open</button>
              {connection.id !== 'local' && <><button className="text-muted-foreground" onClick={() => { setRenaming(connection.id); setConnectionName(connection.name) }}>Rename</button><button className="text-primary" onClick={() => beginConnect(connection.server)}>Reconnect</button><button className="text-muted-foreground" onClick={() => void disconnect(connection.id)}>Disconnect</button></>}
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
  </>
}
