import { lazy, Suspense, useEffect, useState } from 'react'
import { Blocks, Cpu, Globe, KeyRound, Plug, Search, Sparkles } from 'lucide-react'
import { WorkspacePageHeader } from './workspace/WorkspacePageHeader'
import PersonalAccountsPanel from './integrations/PersonalAccountsPanel'
import { BrowserConnectionsPanel } from './BrowserConnectionsDialog'
import { OpenVaultButton } from './integrations/OpenVaultButton'
import { useAuthStore } from '../stores/useAuthStore'
import { useWorkspaceConnectionStore } from '../stores/useWorkspaceConnectionStore'
import api from '../services/api'
import { Button } from './ui/Button'

const CodingProvidersPanel = lazy(() => import('./providers/CodingProvidersPanel'))
const MCPConfigEditor = lazy(() => import('./MCPConfigEditor'))
const categories = [
  { id: 'apps', label: 'Apps', icon: Plug },
  { id: 'ai', label: 'AI accounts', icon: Cpu },
  { id: 'browser', label: 'Browser', icon: Globe },
  { id: 'mcp', label: 'MCP servers', icon: Blocks },
  { id: 'skills', label: 'Plugins & skills', icon: Sparkles },
  { id: 'secrets', label: 'Secrets', icon: KeyRound },
] as const
type Category = typeof categories[number]['id']

/** Account setup only: never pass a Crew path or alter a project's selections. */
export default function ConnectionsToolsPage() {
  const actor = useAuthStore(state => state.user?.id)
  const workspace = useWorkspaceConnectionStore(state => state.activeWorkspaceId)
  // Remount personal inventories on account/network changes; no previous account's rows survive.
  return <ConnectionsToolsContent key={JSON.stringify([workspace, actor])} />
}

function ConnectionsToolsContent() {
  const [category, setCategory] = useState<Category>('apps')
  const [search, setSearch] = useState('')
  const [app, setApp] = useState<'google' | 'slack' | null>(null)
  const matching = categories.filter(item => `${item.label} ${item.id === 'apps' ? 'Slack Google Gmail Drive Calendar GitHub WhatsApp' : item.id === 'ai' ? 'Claude Codex Cursor Muse models' : item.id === 'browser' ? 'Chrome extension' : item.id === 'secrets' ? 'Vault keys' : ''}`.toLowerCase().includes(search.trim().toLowerCase()))
  const choose = (next: Category) => { setCategory(next); setSearch(''); setApp(null) }
  return <section aria-label="Connections & tools" className="flex h-full min-h-0 flex-col bg-background">
    <WorkspacePageHeader page="connections" title="Connections & tools" icon={Plug} />
    <div className="min-h-0 flex-1 overflow-y-auto">
      <div className="mx-auto w-full max-w-5xl space-y-5 p-4 sm:p-6">
        <div><h2 className="text-xl font-semibold tracking-tight">Connections & tools</h2><p className="mt-1 text-sm text-muted-foreground">Manage your accounts and tools here. Choose what each Crew uses in that Crew’s settings.</p></div>
        <label className="flex items-center gap-2 rounded-lg border border-border px-3 py-2"><Search className="h-4 w-4 text-muted-foreground" aria-hidden="true" /><input aria-label="Find a connection or tool" placeholder="Find a connection or tool…" value={search} onChange={event => setSearch(event.target.value)} className="min-w-0 flex-1 bg-transparent text-sm outline-none" /></label>
        {search.trim() ? <div className="divide-y divide-border rounded-lg border border-border" aria-label="Connection search results">{matching.map(({ id, label, icon: Icon }) => <button key={id} type="button" onClick={() => choose(id)} className="flex w-full items-center gap-3 px-4 py-3 text-left text-sm hover:bg-muted"><Icon className="h-4 w-4 text-muted-foreground" aria-hidden="true" />{label}</button>)}{!matching.length && <p className="p-4 text-sm text-muted-foreground">No matching category. Try Slack, browser, models or secrets.</p>}</div> : <>
          <nav aria-label="Connection categories" className="flex gap-1 overflow-x-auto border-b border-border">{categories.map(({ id, label, icon: Icon }) => <button key={id} type="button" aria-current={category === id ? 'page' : undefined} onClick={() => choose(id)} className={`flex shrink-0 items-center gap-2 border-b-2 px-3 py-2 text-xs font-medium ${category === id ? 'border-foreground text-foreground' : 'border-transparent text-muted-foreground hover:text-foreground'}`}><Icon className="h-3.5 w-3.5" aria-hidden="true" />{label}</button>)}</nav>
          <Suspense fallback={<p role="status" className="text-sm text-muted-foreground">Loading settings…</p>}>
            {category === 'apps' && <div className="space-y-4">
              {app ? <><Button variant="ghost" size="sm" onClick={() => setApp(null)}>Back to apps</Button><PersonalAccountsPanel provider={app} /></> : <>
                <div className="divide-y divide-border rounded-lg border border-border">{([{ id: 'slack', name: 'Slack', description: 'Your Slack account and saved sign-ins.' }, { id: 'google', name: 'Google', description: 'Gmail, Drive, Calendar and other Google apps.' }] as const).map(item => <div key={item.id} className="flex items-center gap-4 px-4 py-3"><div className="min-w-0 flex-1"><h3 className="text-sm font-medium">{item.name}</h3><p className="mt-1 text-xs text-muted-foreground">{item.description}</p></div><Button variant="outline" size="sm" onClick={() => setApp(item.id)}>Manage</Button></div>)}</div>
                <p className="text-xs text-muted-foreground">GitHub and WhatsApp setup currently lives in each Crew’s Connections. This page does not change those selections.</p>
              </>}
            </div>}
            {category === 'ai' && <div className="h-[65vh] min-h-96"><CodingProvidersPanel embedded isOpen onClose={() => choose('apps')} /></div>}
            {category === 'browser' && <BrowserConnectionsPanel open onClose={() => {}} inline startSetup />}
            {category === 'mcp' && <div className="space-y-3"><p className="text-sm text-muted-foreground">Manage your personal MCP configuration. Shared MCP connections stay in Vault.</p><MCPConfigEditor /><OpenVaultButton panel="servers" /></div>}
            {category === 'skills' && <div className="space-y-2 rounded-lg border border-border p-4"><h3 className="text-sm font-medium">Skills belong to your work</h3><p className="text-sm text-muted-foreground">Open a Crew’s Skills panel to view, add or manage its skills. Plugin and tool selections also stay with that Crew; there is no separate account-wide plugin library.</p></div>}
            {category === 'secrets' && <AccountSecretNames />}
          </Suspense>
        </>}
      </div>
    </div>
  </section>
}

/** Names only, through the existing authorized inventory; no reveal or selection side effects. */
function AccountSecretNames() {
  const [names, setNames] = useState<string[] | null>(null)
  const [error, setError] = useState(false)
  const [refresh, setRefresh] = useState(0)
  useEffect(() => {
    let active = true
    setNames(null); setError(false)
    void api.get('/api/secrets/global', { skipSessionContext: true }).then(response => {
      if (active) setNames(response.data.map((row: { name: string }) => row.name))
    }).catch(() => { if (active) setError(true) })
    return () => { active = false }
  }, [refresh])
  return <div className="space-y-3"><div className="flex items-center justify-between"><p className="text-sm text-muted-foreground">Available secret names. Values stay hidden; access and management stay in Vault.</p><Button variant="ghost" size="sm" onClick={() => setRefresh(value => value + 1)}>Refresh</Button></div>{error ? <p role="alert" className="text-sm text-destructive">Could not load secrets. Try Refresh.</p> : names === null ? <p role="status" className="text-sm text-muted-foreground">Loading secrets…</p> : <ul className="divide-y divide-border rounded-lg border border-border">{names.map(name => <li key={name} className="flex items-center gap-2 px-4 py-3 font-mono text-xs"><KeyRound className="h-3.5 w-3.5 shrink-0 text-muted-foreground" aria-hidden="true" /><span className="break-all">{name}</span></li>)}{names.length === 0 && <li className="p-4 text-sm text-muted-foreground">No secrets available to your account.</li>}</ul>}<OpenVaultButton /></div>
}
