import { useEffect, useState } from 'react'
import { ArrowUpRight, CircleCheck, Home, Plus, RefreshCw } from 'lucide-react'
import type { ActiveSessionInfo } from '../services/api-types'
import { workspaceStateApi, type HomeData, type HomeWork } from '../services/workspaceState'
import { useWorkspaceReturnStore } from '../stores/useWorkspaceReturnStore'
import { ProductTopBar, ProductTopBarActions, ProductTopBarMain } from './workspace/ProductTopBar'
import { ProductSurfaceSwitcher } from './ProductSurfaceSwitcher'
import { ProductSurfaceIcon } from './ProductSurfaceIcon'
import AccountControl from './topbar/AccountControl'
import { PRODUCT_SURFACE_LABELS } from '../products/productSurfaceConfig'
import { productRoutePath } from '../utils/productRoutes'
import { openGlobalActivitySession } from '../utils/globalProductNavigation'
import { OPEN_WELCOME_HOME_EVENT } from './WelcomeHome'

function navigate(path: string) {
  window.history.pushState({}, '', path)
  window.dispatchEvent(new PopStateEvent('popstate'))
}
export function HomePage() {
  const account = useWorkspaceReturnStore(state => state.account)
  const note = useWorkspaceReturnStore(state => state.note)
  const saveError = useWorkspaceReturnStore(state => state.saveError)
  const recent = useWorkspaceReturnStore(state => state.record?.recent)
  const [data, setData] = useState<HomeData | null>(null)
  const [error, setError] = useState('')
  const [refresh, setRefresh] = useState(0)
  const [showAll, setShowAll] = useState(false)
  useEffect(() => {
    const controller = new AbortController()
    let timer: ReturnType<typeof setTimeout>
    setData(null)
    const load = async () => {
      try {
        const result = await workspaceStateApi.home(controller.signal)
        if (!controller.signal.aborted) { setData(result); setError('') }
      } catch {
        if (!controller.signal.aborted) setError('Could not refresh your work. Please try again.')
      } finally {
        if (!controller.signal.aborted) timer = setTimeout(() => { if (document.visibilityState === 'visible') void load(); else timer = setTimeout(load, 15000) }, 15000)
      }
    }
    void load()
    return () => { controller.abort(); clearTimeout(timer) }
  }, [account, refresh])
  const rank = (work: HomeWork) => recent?.findIndex(item => item.surface === work.surface && item.id === work.id) ?? -1
  const work = [...(data?.work || [])].sort((a, b) => {
    const left = rank(a), right = rank(b)
    if (left >= 0 || right >= 0) return (left < 0 ? Infinity : left) - (right < 0 ? Infinity : right)
    return (b.updated_at || '').localeCompare(a.updated_at || '')
  })
  const openActivity = (session: ActiveSessionInfo) => {
    const target = data?.running_work?.[session.session_id]
    if (!target) return
    window.history.pushState({}, '', productRoutePath({ surface: target.surface, id: target.id }))
    useWorkspaceReturnStore.setState({ homeOpen: false })
    void openGlobalActivitySession(session, { source: 'home' })
  }
  const openWork = (item: HomeWork, view?: string) => navigate(productRoutePath({ surface: item.surface, id: item.id, view }))
  return <div className="flex h-screen bg-background text-foreground">
    <ProductTopBar><ProductTopBarMain><ProductSurfaceSwitcher /></ProductTopBarMain><ProductTopBarActions><AccountControl onOpenWalkthrough={() => window.dispatchEvent(new Event(OPEN_WELCOME_HOME_EVENT))} /></ProductTopBarActions></ProductTopBar>
    <main className="min-w-0 flex-1 overflow-y-auto px-5 py-8 sm:px-10">
      <div className="mx-auto max-w-4xl space-y-8">
        <header className="flex items-start justify-between gap-4"><div><h1 className="flex items-center gap-2 text-2xl font-semibold"><Home className="h-5 w-5 text-muted-foreground" />Home</h1><p className="mt-2 text-sm text-muted-foreground">Pick up your work and see what needs you.</p></div><button aria-label="Refresh Home" className="rounded-md border border-border p-2 hover:bg-muted" onClick={() => setRefresh(value => value + 1)}><RefreshCw className="h-4 w-4" /></button></header>
        {note && <p role="status" className="rounded-lg border border-border p-3 text-sm">{note}</p>}
        {(error || saveError) && <p role="alert" className="text-sm text-destructive">{error || saveError}</p>}
        {data?.warnings.map(warning => <p key={warning} role="status" className="text-sm text-muted-foreground">{warning}</p>)}
        {!data && !error && <p role="status" className="text-sm text-muted-foreground">Loading your work…</p>}
        {data && <>
          <section aria-labelledby="home-attention"><h2 id="home-attention" className="mb-3 text-sm font-semibold">Needs my attention <span className="ml-2 text-muted-foreground">{data.attention.length}</span></h2>
            <div className="overflow-hidden rounded-xl border border-border divide-y divide-border">{data.attention.length ? data.attention.map(item => <button key={`${item.work?.surface}:${item.work?.id}:${item.kind}:${item.id}`} className="flex w-full items-center gap-3 px-4 py-3 text-left hover:bg-muted/50" onClick={() => {
              if (!item.work) return
              if (item.session_id) {
                const session = data.running.find(session => session.session_id === item.session_id)
                if (session) { openActivity(session); return }
              }
              openWork(item.work, item.view)
            }}><span className="h-2 w-2 shrink-0 rounded-full bg-amber-500" /><span className="min-w-0 flex-1"><span className="block truncate text-sm font-medium">{item.title}</span><span className="mt-1 block text-xs text-muted-foreground">{item.work?.label} · {item.kind.replaceAll('_', ' ')}</span></span><ArrowUpRight className="h-4 w-4 shrink-0 text-muted-foreground" /></button>) : <p className="flex items-center gap-2 px-4 py-4 text-sm text-muted-foreground"><CircleCheck className="h-4 w-4" />Nothing needs your attention right now.</p>}</div>
          </section>
          <section aria-labelledby="home-work"><h2 id="home-work" className="mb-3 text-sm font-semibold">My existing work</h2>
            <div className="overflow-hidden rounded-xl border border-border divide-y divide-border">{(showAll ? work : work.slice(0, 6)).map(item => <button key={`${item.surface}:${item.id}`} onClick={() => openWork(item)} className="flex w-full items-center gap-3 px-4 py-3 text-left hover:bg-muted/50"><ProductSurfaceIcon surface={item.surface} className="h-7 w-7 shrink-0" /><span className="min-w-0 flex-1"><span className="block truncate text-sm font-medium">{item.label}</span><span className="block text-xs text-muted-foreground">{PRODUCT_SURFACE_LABELS[item.surface]}{rank(item) >= 0 ? ' · Recently used' : ''}</span></span><ArrowUpRight className="h-4 w-4 text-muted-foreground" /></button>)}{!work.length && <p className="px-4 py-4 text-sm text-muted-foreground">Your work and items shared with you will appear here.</p>}</div>
            {work.length > 6 && <button className="mt-3 text-sm text-muted-foreground hover:text-foreground" onClick={() => setShowAll(value => !value)}>{showAll ? 'Show less' : `Show all ${work.length}`}</button>}
          </section>
          <section aria-labelledby="home-running"><h2 id="home-running" className="mb-3 text-sm font-semibold">Running activity</h2><div className="overflow-hidden rounded-xl border border-border divide-y divide-border">{data.running.map(session => <button key={session.session_id} className="flex w-full items-center gap-3 px-4 py-3 text-left text-sm hover:bg-muted/50" onClick={() => openActivity(session)}><span className="h-2 w-2 rounded-full bg-sky-500" /><span className="flex-1 truncate">{session.title || session.query || 'Running work'}</span><ArrowUpRight className="h-4 w-4 text-muted-foreground" /></button>)}{!data.running.length && <p className="px-4 py-4 text-sm text-muted-foreground">No work is running.</p>}</div></section>
        </>}
        <footer className="border-t border-border pt-4"><button className="inline-flex items-center gap-2 text-sm text-muted-foreground hover:text-foreground" onClick={() => window.dispatchEvent(new CustomEvent('open-quick-switcher', { detail: { query: 'create' } }))}><Plus className="h-4 w-4" />Create new…</button><button className="ml-6 text-sm text-muted-foreground hover:text-foreground" onClick={() => window.dispatchEvent(new Event(OPEN_WELCOME_HOME_EVENT))}>Connect tools &amp; getting started</button></footer>
      </div>
    </main>
  </div>
}
