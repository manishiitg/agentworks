import { currentProductRoute } from '../../utils/productRoutes'
import { useProductUrlView } from '../../hooks/useProductUrlView'
import { BullpenUsageOverview } from './BullpenUsage'
import { lazy, Suspense, useEffect, useId, useRef, useState, type ReactNode } from 'react'
import { ChartNoAxesCombined, Check, Copy, MessagesSquare, Search, Terminal, UsersRound, ShieldCheck, X } from 'lucide-react'
import { ModePresetBar } from '../../components/ModePresetBar'
import { WorkspacePageHeader } from '../../components/workspace/WorkspacePageHeader'
import { WorkspacePageContent } from '../../components/workspace/WorkspacePageContent'
import LlmModalHost from '../../components/topbar/LlmModalHost'
import { AgentMessagingSetupPanel } from './AgentMessagingSetupPanel'
import { AdminPanel } from './AdminPanel'
import { Button } from '../../components/ui/Button'
import { usePersistentTab } from '../../hooks/usePersistentTab'
import { useRegisterPanelSwitcher } from '../../stores/usePanelSwitcherStore'
import { useAppStore } from '../../stores/useAppStore'
import { useAuthStore } from '../../stores/useAuthStore'
import { useLLMStore } from '../../stores/useLLMStore'
import AccountControl from '../../components/topbar/AccountControl'
import { bullpenApi, bullpenError, type BullpenAgent, type BullpenNotice } from '../../services/bullpenApi'

const AdminPages = lazy(() => import('../../components/AdminPages'))
const views = [
  { id: 'agents' as const, label: 'Agents', icon: UsersRound },
  { id: 'setup' as const, label: 'Connect an agent', icon: Terminal },
]
const providerNames: Record<string, string> = { codex: 'Codex', claude: 'Claude Code', muse: 'Muse Code', pi: 'Pi', cursor: 'Cursor', agy: 'agy' }

function BridgeDialog({ title, onClose, children }: { title: string; onClose: () => void; children: ReactNode }) {
  const dialog = useRef<HTMLDialogElement>(null)
  const titleId = useId()
  useEffect(() => {
    const element = dialog.current!
    element.showModal()
    return () => element.close()
  }, [])
  return <dialog ref={dialog} aria-labelledby={titleId} onCancel={event => { event.preventDefault(); onClose() }} className="fixed inset-0 m-auto max-h-[calc(100dvh-2rem)] w-[calc(100vw-2rem)] max-w-2xl overflow-hidden rounded-xl border border-border bg-background p-0 text-foreground shadow-xl backdrop:bg-black/60">
    <div className="flex max-h-[calc(100dvh-2rem)] flex-col">
      <header className="flex shrink-0 items-center justify-between gap-3 border-b border-border p-4"><h2 id={titleId} className="min-w-0 break-words text-lg font-semibold">{title}</h2><Button variant="ghost" size="icon" className="shrink-0" aria-label="Close" onClick={onClose}><X aria-hidden="true" className="h-4 w-4" /></Button></header>
      <div className="min-h-0 overflow-y-auto p-4">{children}</div>
    </div>
  </dialog>
}

function AgentAddress({ target }: { target: string }) {
  const [copied, setCopied] = useState(false)
  const [failed, setFailed] = useState(false)
  async function copy() {
    try { await navigator.clipboard.writeText(target); setCopied(true); setFailed(false) }
    catch { setFailed(true) }
  }
  return <div className="space-y-2">
    <Button variant="outline" size="sm" aria-label={'Copy address ' + target} onClick={() => void copy()}>
      {copied ? <Check aria-hidden="true" className="mr-2 h-3.5 w-3.5" /> : <Copy aria-hidden="true" className="mr-2 h-3.5 w-3.5" />}
      {copied ? 'Copied' : 'Copy address'}
    </Button>
    {(copied || failed) && <p role="status" className="max-w-sm text-sm text-muted-foreground">{failed ? <>Copy this address manually: <code className="break-all select-all">{target}</code></> : 'Ask your AI agent to message this address.'}</p>}
  </div>
}

function AgentDetails({ agent, onClose }: { agent: BullpenAgent; onClose: () => void }) {
  return <BridgeDialog title={agent.label || agent.agent_name} onClose={onClose}>
    <div className="space-y-5 text-sm">
      {agent.purpose && <p className="break-words leading-6">{agent.purpose}</p>}
      <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-5 gap-y-3">
        <dt className="text-muted-foreground">App</dt><dd>{agent.provider ? providerNames[agent.provider] || agent.provider : agent.type || 'Agent'}</dd>
        <dt className="text-muted-foreground">Address</dt><dd className="break-all font-mono">{agent.target}</dd>
        <dt className="text-muted-foreground">Connection</dt><dd>{agent.presence || 'Status unavailable'}</dd>
        <dt className="text-muted-foreground">Visibility</dt><dd>{agent.visibility_label}</dd>
        <dt className="text-muted-foreground">Last seen</dt><dd>{agent.last_seen_at ? <time dateTime={agent.last_seen_at}>{new Date(agent.last_seen_at).toLocaleString()}</time> : 'Unknown'}</dd>
        {agent.last_delivered_at && <><dt className="text-muted-foreground">Last delivered</dt><dd><time dateTime={agent.last_delivered_at}>{new Date(agent.last_delivered_at).toLocaleString()}</time></dd></>}
        {agent.repository && <><dt className="text-muted-foreground">Repository</dt><dd className="break-all">{agent.repository}{agent.branch ? `@${agent.branch}` : ''}</dd></>}
        {agent.activity && <><dt className="text-muted-foreground">Activity</dt><dd>{agent.activity}{agent.activity_at && <> · <time dateTime={agent.activity_at}>{new Date(agent.activity_at).toLocaleString()}</time></>}</dd></>}
      </dl>
      {agent.update_needed && <p className="rounded-lg bg-muted p-3">An AgentWorks update is available on this agent’s computer. Run <code>agentworks update</code> there.</p>}
      {agent.provider === 'cursor' && <p className="text-muted-foreground">Cursor receives messages when its chat takes a step. Messages wait while the chat is idle.</p>}
      <AgentAddress target={agent.target} />
    </div>
  </BridgeDialog>
}

function TokenUsage() {
  const [agents, setAgents] = useState<BullpenAgent[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [attempt, setAttempt] = useState(0)
  useEffect(() => {
    const controller = new AbortController()
    setLoading(true); setError('')
    void Promise.all([bullpenApi.agents(controller.signal), bullpenApi.agentUsage(controller.signal)]).then(([items, readings]) => {
      if (controller.signal.aborted) return
      const byId = new Map(readings.map(reading => [reading.agent_id, reading]))
      setAgents(items.map(agent => agent.shared ? agent : { ...agent, ...byId.get(agent.agent_id) }))
    }).catch(() => { if (!controller.signal.aborted) setError('Could not load token usage. Try again.') })
      .finally(() => { if (!controller.signal.aborted) setLoading(false) })
    return () => controller.abort()
  }, [attempt])
  if (loading) return <p role="status" className="text-sm text-muted-foreground">Loading token usage…</p>
  if (error) return <div role="alert" className="space-y-3 text-sm"><p>{error}</p><Button variant="outline" size="sm" onClick={() => setAttempt(value => value + 1)}>Try again</Button></div>
  return <BullpenUsageOverview agents={agents} onChange={() => setAttempt(value => value + 1)} />
}

function AgentDirectory() {
  const [agents, setAgents] = useState<BullpenAgent[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [query, setQuery] = useState('')
  const [attempt, setAttempt] = useState(0)
  const [selected, setSelected] = useState<string | null>(null)
  const [notices, setNotices] = useState<BullpenNotice[]>([])
  useEffect(() => {
    const controller = new AbortController()
    let pending = false
    const refresh = async () => {
      if (document.hidden || pending) return
      pending = true
      void bullpenApi.notices(controller.signal).then(items => { if (!controller.signal.aborted) setNotices(items) }).catch(() => undefined)
      try {
        const items = await bullpenApi.agents(controller.signal)
        if (!controller.signal.aborted) { setAgents(items); setError('') }
      } catch (reason) {
        if (!controller.signal.aborted) setError(bullpenError(reason))
      } finally {
        pending = false
        if (!controller.signal.aborted) setLoading(false)
      }
    }
    void refresh()
    const interval = window.setInterval(() => void refresh(), 30_000)
    window.addEventListener('focus', refresh)
    document.addEventListener('visibilitychange', refresh)
    return () => { controller.abort(); window.clearInterval(interval); window.removeEventListener('focus', refresh); document.removeEventListener('visibilitychange', refresh) }
  }, [attempt])
  const search = query.trim().toLowerCase()
  const visible = agents.filter(agent => [agent.label, agent.agent_name, agent.purpose, agent.provider].some(value => value?.toLowerCase().includes(search)))
    .sort((a, b) => Number(b.online) - Number(a.online) || a.agent_name.localeCompare(b.agent_name))
  const detail = agents.find(agent => agent.agent_id === selected)
  return <section aria-label="Agents" className="mx-auto max-w-4xl space-y-4">
    {notices.length > 1 ? <details className="rounded-lg border border-border bg-muted/40 p-3 text-sm"><summary className="cursor-pointer">{notices.length} notices from an administrator</summary><div className="mt-3 space-y-2">{notices.map(item => <div key={item.notice_id} role="status" className="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-border p-3 text-sm"><span className="min-w-0 break-words">{item.text}</span><Button variant="outline" size="sm" onClick={() => void bullpenApi.dismissNotice(item.notice_id).then(() => setNotices(list => list.filter(entry => entry.notice_id !== item.notice_id))).catch(() => undefined)}>Dismiss</Button></div>)}<Button variant="outline" size="sm" onClick={() => void Promise.all(notices.map(item => bullpenApi.dismissNotice(item.notice_id))).then(() => setNotices([])).catch(() => undefined)}>Dismiss all</Button></div></details> : notices.map(item => <div key={item.notice_id} role="status" className="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-border bg-muted/40 p-3 text-sm"><span className="min-w-0 break-words">{item.text}</span><Button variant="outline" size="sm" onClick={() => void bullpenApi.dismissNotice(item.notice_id).then(() => setNotices(list => list.filter(entry => entry.notice_id !== item.notice_id))).catch(() => undefined)}>Dismiss</Button></div>)}
    {agents.length > 0 && <div className="relative"><Search aria-hidden="true" className="absolute left-3 top-3 h-4 w-4 text-muted-foreground" /><input aria-label="Find an agent" value={query} onChange={event => setQuery(event.target.value)} placeholder="Find an agent" className="w-full rounded-lg border border-border bg-background py-2.5 pl-10 pr-3 text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring" /></div>}
    {error && <div role="alert" className="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-destructive/30 p-4 text-sm"><p>{error}</p><Button variant="outline" size="sm" onClick={() => setAttempt(value => value + 1)}>Try again</Button></div>}
    {loading ? <p role="status" className="py-12 text-center text-sm text-muted-foreground">Loading your agents…</p> : !error && !agents.length ? <div className="py-16 text-center"><MessagesSquare aria-hidden="true" className="mx-auto mb-4 h-8 w-8 text-muted-foreground" /><h2 className="font-semibold">No agents connected yet</h2><p className="mt-2 text-sm text-muted-foreground">Choose Connect an agent to get started.</p></div> : visible.length ? <div className="divide-y divide-border rounded-xl border border-border">
      {visible.map(agent => <article key={agent.agent_id} className="flex flex-wrap items-center justify-between gap-4 p-4 sm:px-5">
        <button type="button" aria-label={'Details for ' + (agent.label || agent.agent_name)} aria-haspopup="dialog" onClick={() => setSelected(agent.agent_id)} className="min-w-0 flex-1 rounded-md text-left outline-none hover:text-primary focus-visible:ring-2 focus-visible:ring-ring">
          <h2 className="break-words text-sm font-semibold">{agent.label || agent.agent_name}</h2>
          <p className="mt-1 text-sm text-muted-foreground">{agent.provider ? providerNames[agent.provider] || agent.provider : agent.type || 'Agent'}<span className="mx-2" aria-hidden="true">·</span><span className="inline-flex items-center gap-1.5"><span aria-hidden="true" className={`h-1.5 w-1.5 rounded-full ${agent.online ? 'bg-emerald-500' : 'bg-muted-foreground'}`} />{agent.availability_label}</span></p>
        </button>
        <span className="rounded-full bg-muted px-2.5 py-1 text-xs" title={agent.visibility_label === 'Published' ? 'People signed into this network can find and message this agent.' : 'Only you and your own agents can find and message this agent.'}>{agent.visibility_label}</span>
        <AgentAddress target={agent.target} />
      </article>)}
    </div> : agents.length > 0 ? <p className="py-8 text-center text-sm text-muted-foreground">No agents match your search.</p> : null}
    {detail && <AgentDetails agent={detail} onClose={() => setSelected(null)} />}
  </section>
}

export function BullpenSurface({ messagingOnly = false }: { messagingOnly?: boolean }) {
  const [view, setView] = usePersistentTab('bullpen:view', 'agents', ['agents', 'setup', 'admin'] as const, currentProductRoute()?.surface === 'bullpen' ? (currentProductRoute()?.view === 'setup' ? 'setup' : currentProductRoute()?.view === 'admin' ? 'admin' : 'agents') : undefined)
  useProductUrlView('bullpen', { view: view === 'agents' ? undefined : view })
  const [dialog, setDialog] = useState<'usage' | null>(null)
  const adminPage = useAppStore(state => state.adminPage)
  const showProviders = useLLMStore(state => state.showLLMModal)
  const user = useAuthStore(state => state.user)
  useRegisterPanelSwitcher('bullpen', user?.role === 'admin' ? [...views, { id: 'admin', label: 'Administration' }] : views, id => setView(id as 'agents' | 'setup' | 'admin'))
  return <div className="flex h-dvh min-h-0 min-w-0 bg-background text-foreground" data-testid="bullpen-surface">
    {!messagingOnly && <ModePresetBar reduced />}
    <main className="flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden">
      {!messagingOnly && showProviders ? <LlmModalHost /> : !messagingOnly && adminPage ? <Suspense fallback={<p className="p-6">Opening settings…</p>}><AdminPages /></Suspense> : <div className="flex h-full min-h-0 w-full flex-col">
        {messagingOnly ? <header className="flex flex-wrap items-center justify-between gap-3 border-b border-border px-4 py-3 sm:px-6">
          <h1><button type="button" onClick={() => setView('agents')} className="flex items-center gap-2 rounded-md text-base font-semibold focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"><MessagesSquare aria-hidden="true" className="h-5 w-5" />AgentBridge</button></h1>
          <nav aria-label="AgentBridge" className="flex flex-wrap items-center gap-2">
            <Button size="sm" onClick={() => setView('setup')}>Connect an agent</Button>
            {user?.role === 'admin' && <Button variant={view === 'admin' ? 'secondary' : 'ghost'} size="sm" onClick={() => setView('admin')}>Administration</Button>}
            <AccountControl messagingOnly onOpenUsage={() => setDialog('usage')} />
          </nav>
        </header> : <WorkspacePageHeader title={view === 'setup' ? 'Connect an agent' : view === 'admin' ? 'Administration' : 'AgentBridge'} icon={MessagesSquare} showBack={false} onProductHome={() => setView('agents')} productMenuItems={[
          ...views.map(item => ({ ...item, active: view === item.id, onSelect: () => setView(item.id) })),
          ...(user?.role === 'admin' ? [{ id: 'admin', label: 'Administration', icon: ShieldCheck, active: view === 'admin', onSelect: () => setView('admin') }] : []),
          { id: 'usage', label: 'Token usage', icon: ChartNoAxesCombined, onSelect: () => setDialog('usage') },
        ]}>{view !== 'setup' && <Button size="sm" onClick={() => setView('setup')}>Connect an agent</Button>}</WorkspacePageHeader>}
        <div className="min-h-0 flex-1 overflow-y-auto">
          <WorkspacePageContent className={view === 'setup' ? 'mx-auto w-full max-w-5xl' : undefined}>
            {view === 'setup' ? <AgentMessagingSetupPanel onOpenMcp={messagingOnly ? undefined : () => useAppStore.getState().setAdminPage('mcp')} onBack={() => setView('agents')} /> : view === 'admin' && user?.role === 'admin' ? <AdminPanel /> : user ? <AgentDirectory key={user.id} /> : <p>Sign in to see your agents.</p>}
          </WorkspacePageContent>

        </div>
      </div>}
    </main>
    {dialog && <BridgeDialog title="Token usage" onClose={() => setDialog(null)}>
      <TokenUsage />
    </BridgeDialog>}
  </div>
}
