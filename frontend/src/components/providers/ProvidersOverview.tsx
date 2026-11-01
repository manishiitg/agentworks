import { useEffect, useState } from 'react'
import { Loader2, Plus, Server } from 'lucide-react'
import { desktopRequest } from '../../services/desktopConnections'
import type { ProviderManifestEntry } from '../../services/llm-config-api'
import { codingProviderName } from '../../utils/codingProviderName'
import { openConnectedServers } from '../workspace/ConnectedServersMenuItem'

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

/** The connected servers, one card each: who you are there and what each one gives you. */
export function ServersOverview({ providers }: { providers: ProviderManifestEntry[] }) {
  const { overview, error, loading } = useOverview(providers)
  const servers = (overview?.places ?? []).filter(place => place.id !== 'local')
  const name = (id: string) => codingProviderName(providers.find(provider => provider.id === id) ?? { id, display_name: id } as ProviderManifestEntry)
  return (
    <div>
      <h2 className="text-xl font-semibold text-gray-950 dark:text-white">Servers</h2>
      <p className="mt-1 mb-5 text-sm text-gray-600 dark:text-gray-300">The company servers this Mac app is connected to, and what each one gives you.</p>
      {error && <p role="alert" className="mb-4 rounded-lg bg-red-50 px-3 py-2 text-xs text-red-700 dark:bg-red-500/10 dark:text-red-300">{error}</p>}
      {!overview && loading && <p className="flex items-center gap-2 text-sm text-gray-500"><Loader2 className="h-4 w-4 animate-spin" />Checking your servers…</p>}
      <div className="grid gap-4">
        {servers.map(server => {
          const cells = Object.entries(overview?.cells ?? {}).map(([id, byPlace]) => ({ id, cell: byPlace[server.id] })).filter(entry => entry.cell && entry.cell.state !== 'absent')
          const company = cells.filter(entry => entry.cell!.use?.startsWith('Company'))
          const mine = cells.filter(entry => entry.cell!.use === 'Yours' || entry.cell!.use === 'Company + yours')
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
                  <button type="button" onClick={() => openConnectedServers('', server.id)} className="rounded-lg border border-gray-300 px-3 py-1.5 text-xs dark:border-gray-600">Switch account or disconnect</button>
                </div>
              </div>
              <p className="mt-4 text-xs font-semibold uppercase tracking-wide text-gray-500 dark:text-gray-400">Company accounts you can use from this Mac</p>
              <div className="mt-2 flex flex-wrap gap-2">
                {company.length === 0 && <span className="text-xs text-gray-400">None yet: an admin turns them on from each program's admin-managed account on {server.name}.</span>}
                {company.map(entry => <span key={entry.id} className="rounded-lg border border-gray-200 px-2.5 py-1.5 text-xs dark:border-gray-700"><strong className="font-medium">{name(entry.id)}</strong> <span className={entry.cell!.state === 'ready' ? 'text-emerald-600 dark:text-emerald-400' : 'text-gray-400'}>{entry.cell!.state === 'ready' ? '✓ on' : 'off'}</span></span>)}
              </div>
              <p className="mt-4 text-xs font-semibold uppercase tracking-wide text-gray-500 dark:text-gray-400">Your own accounts there</p>
              <div className="mt-2 flex flex-wrap gap-2">
                {mine.length === 0 && <span className="text-xs text-gray-400">None yet. Add one from Overview.</span>}
                {mine.map(entry => <span key={entry.id} className="rounded-lg border border-gray-200 px-2.5 py-1.5 text-xs dark:border-gray-700"><strong className="font-medium">{name(entry.id)}</strong> {entry.cell!.state === 'ready' ? <span className="text-emerald-600 dark:text-emerald-400">✓</span> : <span className="text-amber-600">sign in needed</span>}</span>)}
              </div>
            </section>
          )
        })}
        <button type="button" onClick={() => openConnectedServers()} className="flex items-center gap-2 rounded-xl border border-dashed border-gray-300 p-4 text-left text-sm text-gray-600 hover:bg-gray-50 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800/50">
          <Plus className="h-4 w-4" /><span><strong>Connect a server</strong>: enter its address and sign in in your browser. Its company accounts appear here by themselves.</span>
        </button>
      </div>
    </div>
  )
}
