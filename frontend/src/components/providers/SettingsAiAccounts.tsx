import { lazy, Suspense, useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Loader2, Plus, RefreshCw, Terminal, X } from 'lucide-react'
import { llmConfigService, type ProviderConnection, type ProviderManifestEntry } from '../../services/llm-config-api'
import { codingProviderName } from '../../utils/codingProviderName'
import { locationApiBase, useConnectedWorkStore } from '../../stores/useConnectedWorkStore'
import { getAuthToken } from '../../services/api'
import ProviderAccounts, { accountRelation } from './ProviderAccounts'
import { ServersOverview } from './ProvidersOverview'
import { AccountHealthSummary, refreshAccountHealth } from './AccountHealth'
import ModalPortal from '../ui/ModalPortal'
import { Button } from '../ui/Button'

const CodingProvidersPanel = lazy(() => import('./CodingProvidersPanel'))
const icons: Record<string, string> = {
  'claude-code': 'claude.ico', 'codex-cli': 'codex.png', 'cursor-cli': 'cursor.svg',
  'pi-cli': 'pi.svg', 'muse-cli': 'muse.ico', 'agy-cli': 'antigravity.svg',
}

/** Settings changes only presentation: every account action remains in ProviderAccounts. */
export default function SettingsAiAccounts({ onCosts, serversOnly = false }: { onCosts?: () => void; serversOnly?: boolean }) {
  const [providers, setProviders] = useState<ProviderManifestEntry[]>([])
  const [accounts, setAccounts] = useState<ProviderConnection[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [choosing, setChoosing] = useState(false)
  const picker = useRef<HTMLDialogElement>(null)
  useEffect(() => { if (choosing) picker.current?.showModal() }, [choosing])
  const [setup, setSetup] = useState<string>()
  // Group by Device regroups the same cards under This Mac and each connected server (Mac app only).
  const [groupBy, setGroupBy] = useState<'program' | 'device'>('program')
  const connections = useConnectedWorkStore(state => state.connections)
  const deviceView = window.electronAPI?.getDesktopConnection?.() === 'local' && connections.length > 1
  const signIn = usePlaceSignIn(groupBy === 'device' ? connections.map(connection => connection.id) : [])
  const refresh = useCallback(async () => {
    setLoading(true); setError('')
    try {
      const [manifest, records] = await Promise.all([llmConfigService.getProviderManifest(), llmConfigService.getProviderConnections()])
      const order = new Map(manifest.provider_order.map((id, index) => [id, index]))
      setProviders(manifest.providers.filter(provider => provider.integration_kind === 'coding_agent' && !provider.deprecated)
        .sort((a, b) => (order.get(a.id) ?? 999) - (order.get(b.id) ?? 999)))
      setAccounts(records)
    } catch { setError('Could not load AI accounts. Try again.') }
    finally { setLoading(false) }
  }, [])
  useEffect(() => {
    void refresh()
    if (window.electronAPI?.getDesktopConnection?.() === 'local') void useConnectedWorkStore.getState().refresh()
    window.addEventListener('provider-connections-changed', refresh)
    return () => window.removeEventListener('provider-connections-changed', refresh)
  }, [refresh])
  // Hide only untouched, unconfigured admin placeholders. Pending personal,
  // shared, admin-review and server-lent records must remain visible.
  const saved = useMemo(() => accounts.filter(record => accountRelation(record) !== 'server' || record.configured !== false), [accounts])
  const listed = providers.filter(provider => saved.some(record => record.provider === provider.id))
  // Account status checks this Mac (or this server) and every connected server whose company accounts are listed here.
  const healthPlaces = useMemo(() => [{ id: 'local', name: 'This Mac' }, ...connections.filter(place => place.id !== 'local' && accounts.some(record => record.via_connection === place.id))], [connections, accounts])
  const providerNames = useMemo(() => Object.fromEntries(providers.map(provider => [provider.id, codingProviderName(provider)])), [providers])
  const providerIcon = (provider: ProviderManifestEntry) => icons[provider.id]
    ? <img src={'/provider-icons/' + icons[provider.id]} alt="" className="h-6 w-6 rounded object-contain" />
    : <Terminal className="h-5 w-5 text-muted-foreground" aria-hidden />

  const programCard = (provider: ProviderManifestEntry, inventory: ProviderConnection[], place = '') => <section key={place + provider.id} aria-label={codingProviderName(provider) + ' accounts'} className="min-w-0 p-5">
    <div className="mb-4 flex items-center justify-between gap-3"><h3 className="flex items-center gap-3 text-sm font-semibold">{providerIcon(provider)}{codingProviderName(provider)}</h3>
      {(!place || place === 'local') && <Button variant="ghost" size="xs" onClick={() => setSetup(provider.id)}>Add account</Button>}</div>
    <ProviderAccounts directory provider={provider.id} providerLabel={codingProviderName(provider)} inventory={inventory} />
  </section>

  return <div className="space-y-6">
    {!serversOnly && <div className="flex flex-wrap items-start justify-between gap-4">
      <div><h2 className="text-2xl font-semibold tracking-tight">AI accounts</h2><p className="mt-1 text-sm text-muted-foreground">Manage the accounts your agents use.</p></div>
      <Button onClick={() => setChoosing(true)}><Plus className="h-4 w-4" />Add account</Button>
    </div>}
    {error && <div role="alert" className="text-sm text-destructive">{error}<Button variant="ghost" size="sm" onClick={() => void refresh()}>Retry</Button></div>}
    {loading && providers.length === 0 && <p role="status" className="flex items-center gap-2 text-sm text-muted-foreground"><Loader2 className="h-4 w-4 animate-spin" />Loading AI accounts…</p>}
    {serversOnly ? providers.length > 0 && <ServersOverview providers={providers} /> : <>
      <AccountHealthSummary places={healthPlaces} names={providerNames} />
      <div className="flex items-center justify-between text-xs text-muted-foreground"><span>Saved & available accounts</span><Button variant="ghost" size="icon" aria-label="Refresh AI accounts" disabled={loading} onClick={() => { refreshAccountHealth(); void refresh() }}><RefreshCw className={'h-4 w-4 ' + (loading ? 'animate-spin' : '')} /></Button></div>
      {deviceView && <div role="radiogroup" aria-label="Group by" className="flex items-center gap-2 text-xs text-muted-foreground"><span>Group by</span>
        {(['program', 'device'] as const).map(value => <button key={value} type="button" role="radio" aria-checked={groupBy === value} onClick={() => setGroupBy(value)}
          className={'rounded-md px-2 py-1 ' + (groupBy === value ? 'bg-muted text-foreground' : 'hover:text-foreground')}>{value === 'program' ? 'Program' : 'Device'}</button>)}
      </div>}
      {groupBy === 'program' || !deviceView ? listed.length > 0 && <div className="divide-y divide-border overflow-visible rounded-lg border border-border">
        {listed.map(provider => programCard(provider, accounts))}
      </div> : connections.map(place => {
        // This Mac: the Mac's own accounts. A server: the accounts this Mac uses from it.
        const here = accounts.filter(record => place.id === 'local' ? !record.via_connection : record.via_connection === place.id)
        const programs = providers.filter(provider => here.some(record => record.provider === provider.id))
        const count = signIn[place.id]
        return <section key={place.id} aria-label={(place.id === 'local' ? 'This Mac' : place.name) + ' accounts'} className="space-y-2">
          <h3 className="flex items-center gap-2 text-sm font-semibold">{place.id === 'local' ? 'This Mac' : place.name}
            <span className={'text-xs font-normal ' + (count ? 'text-amber-600 dark:text-amber-400' : 'text-muted-foreground')}>{count === undefined ? '' : count === null ? 'Not answering' : count ? (count === 1 ? '1 account needs sign-in' : `${count} accounts need sign-in`) : 'All signed in'}</span></h3>
          {programs.length > 0 ? <div className="divide-y divide-border overflow-visible rounded-lg border border-border">{programs.map(provider => programCard(provider, here, place.id))}</div>
            : <p className="text-sm text-muted-foreground">No accounts from here on this Mac.</p>}
        </section>
      })}
      {!loading && !error && listed.length === 0 && <div className="rounded-lg border border-dashed border-border px-5 py-10 text-center"><p className="text-sm">No AI accounts connected yet.</p><p className="mt-1 text-sm text-muted-foreground">Add an account to start using your preferred AI.</p><Button className="mt-4" variant="outline" onClick={() => setChoosing(true)}>Add account</Button></div>}
      <div className="flex flex-wrap justify-between gap-3 text-xs text-muted-foreground"><span>Choose the model inside each chat.</span>{onCosts && <button type="button" onClick={onCosts} className="hover:text-foreground">View usage & costs →</button>}</div>
    </>}
    {choosing && <ModalPortal><dialog ref={picker} aria-label="Add an AI account" onCancel={() => setChoosing(false)} className="fixed inset-0 z-[1000] m-auto max-h-[80vh] w-[calc(100%_-_2rem)] max-w-sm overflow-y-auto rounded-xl border border-border bg-popover p-5 text-foreground shadow-xl backdrop:bg-black/50">
        <div className="flex items-center justify-between"><h3 className="text-lg font-semibold">Add an AI account</h3><button autoFocus type="button" aria-label="Close provider picker" className="rounded p-1 text-muted-foreground hover:bg-muted" onClick={() => setChoosing(false)}><X className="h-4 w-4" /></button></div>
        <p className="mb-4 mt-1 text-xs text-muted-foreground">Choose a provider. Existing accounts stay connected.</p>
        {providers.map(provider => <button type="button" key={provider.id} className="flex w-full items-center gap-3 rounded-lg p-3 text-left hover:bg-muted focus-visible:ring-2 focus-visible:ring-ring" onClick={() => { setChoosing(false); setSetup(provider.id) }}>
          {providerIcon(provider)}<span className="text-sm">{codingProviderName(provider)}<span className="block text-xs text-muted-foreground">{provider.runtime_available === false ? 'Install and connect' : listed.includes(provider) ? 'Add another account' : 'Connect an account'}</span></span>
        </button>)}
        {!loading && providers.length === 0 && <p className="text-sm text-muted-foreground">No providers are available here.</p>}
    </dialog></ModalPortal>}
    {setup && <Suspense fallback={<p role="status" className="text-sm text-muted-foreground">Opening account setup…</p>}><CodingProvidersPanel key={setup} isOpen accountSetup initialProviderId={setup} onClose={() => { setSetup(undefined); void refresh() }} /></Suspense>}
  </div>
}

/** Each place counts its own accounts that need a new sign-in (null: not answering). The Mac only shows the answers. */
function usePlaceSignIn(ids: string[]): Record<string, number | null> {
  const [counts, setCounts] = useState<Record<string, number | null>>({})
  const key = ids.join(',')
  useEffect(() => {
    let live = true
    for (const id of key.split(',').filter(Boolean)) {
      void fetch(`${locationApiBase(id)}/api/provider-accounts/sign-in-needed`, { headers: { Authorization: `Bearer ${getAuthToken() || ''}` }, signal: AbortSignal.timeout(15_000) })
        .then(response => response.ok ? response.json() as Promise<{ sign_in_needed?: number }> : Promise.reject(new Error('unavailable')))
        .then(data => data.sign_in_needed ?? 0, () => null)
        .then(count => { if (live) setCounts(current => ({ ...current, [id]: count })) })
    }
    return () => { live = false }
  }, [key])
  return counts
}
