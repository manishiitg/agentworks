import { useEffect, useState } from 'react'
import { Loader2, Plus, Server } from 'lucide-react'
import { desktopRequest } from '../../services/desktopConnections'
import type { ProviderManifestEntry } from '../../services/llm-config-api'
import { codingProviderName } from '../../utils/codingProviderName'
import { takePendingServerAddress } from '../workspace/ConnectedServersMenuItem'
import { useConnectedWorkStore } from '../../stores/useConnectedWorkStore'
import { getRawActiveWorkspaceId } from '../../utils/desktopConnection'

/** One box of the overview, as the Mac's backend decides it (desktop_overview.go). */
type OverviewCell = { state: 'ready' | 'sign_in' | 'off' | 'absent'; use?: string; detail?: string; action?: string }
type OverviewPlace = { id: string; name: string; signed_in_as?: string; reachable: boolean }
type Overview = { places: OverviewPlace[]; cells: Record<string, Record<string, OverviewCell>> }

function useOverview(providers: ProviderManifestEntry[]) {
  const [overview, setOverview] = useState<Overview | null>(null)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)
  const ids = providers.map(provider => provider.id).join(',')
  const load = () => {
    if (!ids) return
    setLoading(true); setError('')
    desktopRequest<Overview>(`/overview?providers=${encodeURIComponent(ids)}`)
      .then(setOverview)
      .catch(loadError => setError(loadError instanceof Error ? loadError.message : 'Could not load.'))
      .finally(() => setLoading(false))
  }
  useEffect(load, [ids])
  useEffect(() => {
    window.addEventListener('provider-connections-changed', load)
    return () => window.removeEventListener('provider-connections-changed', load)
  }, [ids])
  return { overview, error, loading }
}

/** The connected servers, one card each: who you are there and the AI accounts that are on there for you. Servers
 *  are connected and disconnected right here. */
export function ServersOverview({ providers }: { providers: ProviderManifestEntry[] }) {
  const { overview, error, loading } = useOverview(providers)
  const servers = (overview?.places ?? []).filter(place => place.id !== 'local')
  const name = (id: string) => codingProviderName(providers.find(provider => provider.id === id) ?? { id, display_name: id } as ProviderManifestEntry)
  const [actionError, setActionError] = useState('')
  // Disconnect asks on the card itself (a browser confirm can be blocked in the app window).
  const [confirming, setConfirming] = useState<string | null>(null)
  const disconnect = async (id: string) => {
    setConfirming(null); setActionError('')
    try {
      await desktopRequest(`/${encodeURIComponent(id)}`, 'DELETE')
      if (getRawActiveWorkspaceId() === id) void window.electronAPI?.openDesktopConnection?.('local', '/home')
      serversChanged()
    }
    catch (disconnectError) { setActionError(disconnectError instanceof Error ? disconnectError.message : 'Could not disconnect.') }
  }
  return (
    <div>
      <h2 className="text-xl font-semibold text-gray-950 dark:text-white">Servers</h2>
      <p className="mt-1 mb-5 text-sm text-gray-600 dark:text-gray-300">The company servers this Mac app is connected to.</p>
      {(error || actionError) && <p role="alert" className="mb-4 rounded-lg bg-red-50 px-3 py-2 text-xs text-red-700 dark:bg-red-500/10 dark:text-red-300">{actionError || error}</p>}
      {!overview && loading && <p className="flex items-center gap-2 text-sm text-gray-500"><Loader2 className="h-4 w-4 animate-spin" />Checking your servers…</p>}
      <div className="grid gap-4">
        {servers.map(server => {
          const on = Object.entries(overview?.cells ?? {})
            .map(([id, byPlace]) => ({ id, cell: byPlace[server.id] }))
            .filter(entry => entry.cell?.state === 'ready')
          return (
            <section key={server.id} className="rounded-xl border border-gray-200 p-4 dark:border-gray-700">
              <div className="flex flex-wrap items-center gap-3">
                <span className="grid h-9 w-9 place-items-center rounded-lg bg-gray-100 text-gray-500 dark:bg-gray-800 dark:text-gray-400"><Server className="h-4 w-4" /></span>
                <div className="min-w-0">
                  <p className="font-semibold text-gray-900 dark:text-gray-100">{server.name}</p>
                  <p className="text-xs text-gray-500 dark:text-gray-400">{server.reachable ? <span className="text-emerald-600 dark:text-emerald-400">● Connected</span> : <span className="text-amber-600">● Cannot reach</span>}{server.signed_in_as && <> · signed in as {server.signed_in_as}</>}</p>
                </div>
                <div className="ml-auto flex gap-2">
                  <button type="button" onClick={() => void window.electronAPI?.openDesktopConnection?.(server.id, '/crew')} className="rounded-lg border border-gray-300 px-3 py-1.5 text-xs dark:border-gray-600">Open</button>
                  {confirming === server.id
                    ? <>
                      <button type="button" onClick={() => void disconnect(server.id)} className="rounded-lg bg-red-600 px-3 py-1.5 text-xs font-medium text-white hover:bg-red-500">Disconnect {server.name}</button>
                      <button type="button" onClick={() => setConfirming(null)} className="rounded-lg border border-gray-300 px-3 py-1.5 text-xs dark:border-gray-600">Cancel</button>
                    </>
                    : <button type="button" onClick={() => setConfirming(server.id)} className="rounded-lg border border-gray-300 px-3 py-1.5 text-xs text-gray-600 hover:text-red-600 dark:border-gray-600 dark:text-gray-300">Disconnect</button>}
                </div>
              </div>
              {on.length > 0 && <div className="mt-4 flex flex-wrap gap-2">
                {on.map(entry => <span key={entry.id} className="rounded-lg border border-gray-200 px-2.5 py-1.5 text-xs dark:border-gray-700"><strong className="font-medium">{name(entry.id)}</strong> <span className="text-emerald-600 dark:text-emerald-400">✓ {entry.cell!.use === 'Yours' ? 'your account' : 'company'}</span></span>)}
              </div>}
            </section>
          )
        })}
        <ConnectServerForm />
      </div>
    </div>
  )
}

/** Every place that shows servers refreshes after one is connected or disconnected. */
function serversChanged() {
  void useConnectedWorkStore.getState().refresh()
  window.dispatchEvent(new Event('provider-connections-changed'))
}

/** Connect a server: its address, then sign in in the browser; the server's card appears when that finishes. */
export function ConnectServerForm({ onConnected }: { onConnected?: () => void } = {}) {
  const [server, setServer] = useState(() => takePendingServerAddress())
  const [pending, setPending] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  useEffect(() => {
    const prefill = (event: Event) => { const address = (event as CustomEvent<{ server?: string }>).detail?.server; if (address) setServer(address) }
    window.addEventListener('show-servers-page', prefill)
    return () => window.removeEventListener('show-servers-page', prefill)
  }, [])
  useEffect(() => {
    if (!pending) return
    let disposed = false
    let timer: ReturnType<typeof setTimeout>
    const check = async () => {
      try {
        const data = await desktopRequest<{ connection_id: string; error: string }>(`/sign-in/${pending}`)
        if (disposed) return
        if (data.error) throw new Error(data.error)
        if (data.connection_id) { setPending(null); setServer(''); serversChanged(); onConnected?.(); return }
        timer = setTimeout(() => void check(), 1500)
      } catch (checkError) { if (!disposed) { setPending(null); setError((checkError as Error).message) } }
    }
    void check()
    return () => { disposed = true; clearTimeout(timer) }
  }, [pending, onConnected])
  const connect = async () => {
    setBusy(true); setError('')
    try {
      const result = await desktopRequest<{ state: string; url: string }>('', 'POST', { server })
      setPending(result.state)
      await window.electronAPI?.openExternal?.(result.url)
    } catch (connectError) { setPending(null); setError((connectError as Error).message) }
    finally { setBusy(false) }
  }
  return (
    <form noValidate onSubmit={event => { event.preventDefault(); if (server.trim()) void connect() }} className="rounded-xl border border-dashed border-gray-300 p-4 dark:border-gray-700">
      <p className="flex items-center gap-2 text-sm font-medium text-gray-900 dark:text-gray-100"><Plus className="h-4 w-4" />Connect a server</p>
      <p className="mt-1 text-xs text-gray-500 dark:text-gray-400">Paste any link from your server's website, then sign in in your browser.</p>
      <div className="mt-3 flex flex-wrap gap-2">
        <input type="url" value={server} onChange={event => setServer(event.target.value)} placeholder="https://agents.example.com" disabled={busy || !!pending} aria-label="Server address"
          className="min-w-64 flex-1 rounded-lg border border-gray-300 bg-white px-3 py-2 text-sm dark:border-gray-600 dark:bg-gray-800" />
        <button disabled={busy || !!pending || !server.trim()} className="rounded-lg bg-primary px-3 py-2 text-sm font-medium text-primary-foreground hover:bg-primary/90 disabled:opacity-50">{pending ? 'Waiting for sign-in…' : busy ? 'Opening…' : 'Sign in with browser'}</button>
      </div>
      {pending && <p role="status" className="mt-2 text-xs text-gray-500">Finish signing in in your browser; this updates by itself.</p>}
      {error && <p role="alert" className="mt-2 text-xs text-red-600 dark:text-red-400">{error}</p>}
    </form>
  )
}
