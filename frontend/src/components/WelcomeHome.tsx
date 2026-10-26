import { openWorkspaceSettings } from '../utils/workspaceSettingsNavigation'
import { useCallback, useEffect, useMemo, useRef, useState, type CSSProperties, type FormEvent } from 'react'
import { ArrowRight, ArrowUp, Loader2, Terminal, Menu, Plus, PanelsTopLeft } from 'lucide-react'
import { PRODUCT_CARDS } from './ProductSurfaceSwitcher'
import { useAuthStore } from '../stores/useAuthStore'
import { navigationProductSurfaceIDs, type ProductSurface } from '../products/productSurfaceConfig'
import { openProductWorkspace } from '../utils/productWorkspaceNavigation'
import { openQuickNavigation, quickNavigationItems } from '../utils/quickNavigation'
import { isGuideRemembered, rememberGuide } from '../utils/onboarding'
import { getRuntimeAppName, runtimeBrandAsset, runtimeBrandingConfig } from '../runtime-branding'
import { useWorkspaceReturnStore } from '../stores/useWorkspaceReturnStore'
import { productUrlViewChanged } from '../utils/productUrlNavigation'
import WelcomeAiReadiness from './WelcomeAiReadiness'
import { useProductNavigationToolbarStore } from '../stores/useProductNavigationToolbarStore'
import { openAppProduct } from '../utils/appNavigation'
import { OPEN_HOME_EVENT } from '../utils/homeNavigation'
import { useWorkspaceConnectionStore } from '../stores/useWorkspaceConnectionStore'
import { captureChatIdentity, isChatIdentityCurrent } from '../utils/chatIdentity'
import { AgentWorksIcon, AGENTWORKS_SUBTITLE } from './ui/AgentWorksIdentity'
import { HomeUseCases } from './HomeUseCases'
import { HomeRecentWork } from './HomeRecentWork'
import { useCommandDialogStore } from '../stores/useCommandDialogStore'
import api from '../services/api'


export const WELCOME_HOME_DISMISSED_KEY = 'agentworks_welcome_home_v1_dismissed'
const PERSONAL_PRODUCTS = new Set<string>(['sparkquill'])
const WORK_PRODUCT_ORDER: ProductSurface[] = ['work', 'agentworks', 'relays']
const SERVICE_ORDER: ProductSurface[] = ['knowledgebase', 'mcp-gateway', 'bullpen']
const STARTING_POINTS: Partial<Record<ProductSurface, { createLabel: string; detail: string }>> = {
  work: { createLabel: 'Create a Crew', detail: 'An expert agent your team can ask' },
  agentworks: { createLabel: 'Create a Goal', detail: 'Agents working toward your target' },
  relays: { createLabel: 'Create a Flow', detail: 'Repeatable steps your apps can call' },
}
const SERVICE_DESCRIPTIONS: Partial<Record<ProductSurface, string>> = {
  knowledgebase: 'Knowledge your agents can reuse',
  'mcp-gateway': 'Your connected tools and credentials',
  bullpen: 'Connect agents to work together',
}

export function WelcomeHome({ autoOpen = true }: { autoOpen?: boolean }) {
  // Single-product accounts go straight to their workspace; Getting started can still open this page.
  const [open, setOpen] = useState(() => window.location.pathname === '/home' || (autoOpen && !isGuideRemembered(WELCOME_HOME_DISMISSED_KEY) && navigationProductSurfaceIDs(useAuthStore.getState().user?.allowed_products).length !== 1))
  const sidebarWidth = useProductNavigationToolbarStore(state => state.sidebarWidth)
  const user = useAuthStore(state => state.user)
  const server = useWorkspaceConnectionStore(state => state.activeWorkspaceId)
  const scope = `${server}:${user?.id || ''}`
  const prompt = useRef<HTMLTextAreaElement>(null)
  const [focusPrompt, setFocusPrompt] = useState(false)
  const operation = useRef(0)
  const [agentWorksError, setAgentWorksError] = useState('')
  const [agentWorksDraft, setAgentWorksDraft] = useState('')
  const [agentWorksSending, setAgentWorksSending] = useState(false)
  const isMultiUserMode = useAuthStore(state => state.isMultiUserMode)
  const products = useMemo(() => {
    const visible = new Set(navigationProductSurfaceIDs(user?.allowed_products))
    return PRODUCT_CARDS.filter(product => visible.has(product.id) && !PERSONAL_PRODUCTS.has(product.id))
  }, [user?.allowed_products])
  const creationItems = useMemo(() => quickNavigationItems(user, 'work', isMultiUserMode).filter(item => item.action === 'create'), [user, isMultiUserMode])
  const leaveHome = useCallback(() => {
    rememberGuide(WELCOME_HOME_DISMISSED_KEY)
    useWorkspaceReturnStore.setState({ firstVisit: false })
    setOpen(false)
    if (window.location.pathname === '/home') productUrlViewChanged()
  }, [])

  useEffect(() => {
    const show = (event?: Event) => {
      setFocusPrompt(Boolean((event as CustomEvent | undefined)?.detail?.focusPrompt))
      if ((event as CustomEvent | undefined)?.detail?.focusPrompt) requestAnimationFrame(() => prompt.current?.focus())
      if (window.location.pathname !== '/home') window.history.pushState({}, '', '/home')
      setOpen(true)
    }
    const syncLocation = () => {
      if (window.location.pathname === '/home') show()
      else leaveHome()
    }
    window.addEventListener(OPEN_HOME_EVENT, show)
    window.addEventListener('popstate', syncLocation)
    window.addEventListener('product-navigation-changed', syncLocation)
    return () => {
      window.removeEventListener(OPEN_HOME_EVENT, show)
      window.removeEventListener('popstate', syncLocation)
      window.removeEventListener('product-navigation-changed', syncLocation)
    }
  }, [leaveHome])

  useEffect(() => {
    if (autoOpen && !isGuideRemembered(WELCOME_HOME_DISMISSED_KEY) && products.length > 1) {
      window.history.replaceState({}, '', '/home')
      setOpen(true)
      window.dispatchEvent(new Event(OPEN_HOME_EVENT))
    }
  }, [autoOpen, products.length])

  useEffect(() => {
    operation.current++; setAgentWorksDraft(''); setAgentWorksError(''); setAgentWorksSending(false)
  }, [scope])

  if (!open) return null

  const branding = runtimeBrandingConfig()
  const appName = getRuntimeAppName(branding) || 'AgentWorks'
  const mark = runtimeBrandAsset('markUrl', branding)
  const workProducts = WORK_PRODUCT_ORDER.flatMap(id => products.filter(product => product.id === id))
  const services = products.filter(product => !WORK_PRODUCT_ORDER.includes(product.id))
    .sort((a, b) => {
      const rank = (id: ProductSurface) => SERVICE_ORDER.includes(id) ? SERVICE_ORDER.indexOf(id) : SERVICE_ORDER.length
      return rank(a.id) - rank(b.id)
    })
  const openProduct = (surface: ProductSurface) => {
    if (!navigationProductSurfaceIDs(useAuthStore.getState().user?.allowed_products).includes(surface)) return
    openProductWorkspace(surface)
    leaveHome()
  }
  // The server makes the person's own AgentWorks on first use, sends the message into its main chat and says which
  // Crew it is; the reply streams there.
  const askAgentWorks = async (event: FormEvent) => {
    event.preventDefault()
    const message = agentWorksDraft.trim()
    if (!message || agentWorksSending) return
    const identity = captureChatIdentity()
    const sequence = ++operation.current
    const current = () => sequence === operation.current && isChatIdentityCurrent(identity)
    setAgentWorksError('')
    setAgentWorksSending(true)
    try {
      const { data } = await api.post<{ crew_id: string }>('/api/agentworks', { message })
      if (!current()) return
      setAgentWorksDraft('')
      setAgentWorksSending(false)
      leaveHome()
      openAppProduct({ surface: 'work', id: data.crew_id })
    } catch (error) {
      if (!current()) return
      const response = (error as { response?: { data?: unknown } }).response
      setAgentWorksError(typeof response?.data === 'string' ? response.data : 'Your message could not be sent. Try again.')
      setAgentWorksSending(false)
    }
  }
  const openMcpSetup = () => openWorkspaceSettings('mcp')
  const openProviders = () => openWorkspaceSettings('providers')
  const studioCreation = creationItems.find(item => item.surface === 'work')
  const openStudio = () => {
    if (!studioCreation || !openQuickNavigation(studioCreation)) return
    useCommandDialogStore.getState().requestProductCreate('work', 'studio')
    leaveHome()
  }

  return (
    <main aria-label="Home"
      style={{ '--welcome-sidebar-width': `${sidebarWidth}px` } as CSSProperties}
      className="fixed inset-0 z-[39] overflow-y-auto bg-background md:left-[var(--welcome-sidebar-width)]">
      <div className="mx-auto flex min-h-full max-w-5xl flex-col gap-5 px-4 py-5 sm:px-8 sm:py-6">
        <header className="flex items-center justify-between gap-4">
          <p className="flex items-center gap-2 pl-8 text-xs text-muted-foreground md:pl-0">{mark && <img src={mark} alt="" className="h-5 w-5 rounded-md" />}{appName}</p>
          <button type="button" aria-label="Open navigation" onClick={() => window.dispatchEvent(new Event('open-app-sidebar'))} className="rounded-md p-2 text-muted-foreground hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring md:hidden"><Menu className="h-5 w-5" aria-hidden="true" /></button>
        </header>

        <section aria-labelledby="home-heading" className="mx-auto w-full max-w-3xl pt-3 sm:pt-5">
          <h1 id="home-heading" className="text-center text-2xl font-semibold tracking-tight sm:text-3xl">What would you like to get done?</h1>
          <p className="mx-auto mt-2 max-w-xl text-center text-sm leading-6 text-muted-foreground">Start something new, automate a task, or pick up where you left off.</p>
          <div className="mt-5">{workProducts.length > 0 && <WelcomeAiReadiness products={workProducts.map(product => product.id)} onSetup={openProviders} crewReadyView={<section aria-label="Ask AgentWorks">
            <form onSubmit={askAgentWorks} className="rounded-2xl border border-border bg-muted/20 p-3 focus-within:border-primary/60 focus-within:ring-1 focus-within:ring-primary/20 sm:p-4">
              <label htmlFor="welcome-agentworks-message" className="sr-only">Tell AgentWorks what you want to get done</label>
              <textarea ref={prompt} autoFocus={focusPrompt} id="welcome-agentworks-message" value={agentWorksDraft} onChange={event => setAgentWorksDraft(event.target.value)} maxLength={20000} rows={3} disabled={agentWorksSending}
                onKeyDown={event => { if (event.key === 'Enter' && !event.shiftKey && !event.nativeEvent.isComposing) { event.preventDefault(); event.currentTarget.form?.requestSubmit() } }}
                placeholder="Tell AgentWorks what you want to get done" className="block w-full resize-none bg-transparent text-sm leading-6 outline-none placeholder:text-muted-foreground disabled:opacity-50" />
              <div className="mt-2 flex items-center justify-between gap-3">
                <span className="flex min-w-0 items-center gap-2"><AgentWorksIcon /><span className="text-xs font-medium">AgentWorks<span className="ml-2 hidden font-normal text-muted-foreground sm:inline">{AGENTWORKS_SUBTITLE}</span></span></span>
                <button type="submit" aria-label="Send to AgentWorks" title="Send to AgentWorks" disabled={agentWorksSending || !agentWorksDraft.trim()} className="grid h-8 w-8 shrink-0 place-items-center rounded-lg bg-primary text-primary-foreground hover:bg-primary/90 disabled:opacity-50">{agentWorksSending ? <Loader2 aria-hidden="true" className="h-4 w-4 animate-spin" /> : <ArrowUp aria-hidden="true" className="h-4 w-4" />}</button>
              </div>
              {agentWorksError && <p role="alert" className="mt-2 text-xs text-destructive">{agentWorksError}</p>}
            </form>
            <button type="button" onClick={openProviders} className="mt-2 rounded-md px-1 text-xs text-muted-foreground hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">Manage AI accounts</button>
          </section>} />}</div>
        </section>

        {workProducts.length > 0 && <HomeUseCases key={`cases:${scope}`} products={workProducts.map(product => product.id)} onChoose={message => { setAgentWorksDraft(message); setFocusPrompt(true); requestAnimationFrame(() => prompt.current?.focus()) }} />}

        {workProducts.length > 0 && <section aria-label="Direct product creation">
          <h2 className="mb-2 text-xs font-medium text-muted-foreground">{creationItems.length ? 'Or choose what you want to build' : 'Open your work'}</h2>
          <div className="grid grid-cols-2 gap-2 lg:grid-cols-4">
          {workProducts.map(product => {
            const creation = creationItems.find(item => item.surface === product.id)
            const copy = STARTING_POINTS[product.id]!
            const Icon = product.icon
            return <div key={product.id} data-welcome-product={product.id} className="min-w-0 rounded-xl border border-border">
              <button type="button" aria-label={creation ? copy.createLabel : `Open ${product.label}`} onClick={() => { if (creation) { if (openQuickNavigation(creation)) leaveHome() } else openProduct(product.id) }} className="block w-full rounded-t-xl px-3 pt-3 text-left hover:bg-muted/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring">
                <span className="flex items-center justify-between gap-2"><Icon className="h-5 w-5" /><span className="text-sm font-semibold">{product.label}</span>{creation && <Plus aria-hidden="true" className="h-3 w-3 text-muted-foreground" />}</span>
                <span className="mt-2 block min-h-10 text-xs leading-5 text-muted-foreground">{copy.detail}</span>
              </button>
              {creation && <button type="button" aria-label={`Open ${product.label}`} onClick={() => openProduct(product.id)} className="mb-1 w-full rounded-b-lg px-3 py-1 text-left text-[11px] text-muted-foreground hover:bg-muted hover:text-foreground">Open existing</button>}
            </div>
          })}
          {workProducts.some(product => product.id === 'work') && <button type="button" aria-label="Create Studio" disabled={!studioCreation} onClick={openStudio} title={studioCreation ? 'Start a private Code or Cowork workspace' : 'Ask an administrator for access to create a workspace'} className="min-w-0 rounded-xl border border-border p-3 text-left hover:bg-muted/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-50">
            <span className="flex items-center justify-between gap-2"><PanelsTopLeft aria-hidden="true" className="h-5 w-5 text-muted-foreground" /><span className="text-sm font-semibold">Studio</span><Plus aria-hidden="true" className="h-3 w-3 text-muted-foreground" /></span>
            <span className="mt-2 block text-xs leading-5 text-muted-foreground">Your own private AI workspace</span><span className="mt-2 block text-[11px] text-muted-foreground">Code or Cowork</span>
          </button>}
          </div>
        </section>}

        {workProducts.length > 0 && <HomeRecentWork key={`recent:${scope}`} />}

        {services.length > 0 && <section aria-labelledby="welcome-services">
          <h2 id="welcome-services" className="text-xs font-medium text-muted-foreground">Tools that work across your projects</h2>
          <div className="mt-3 grid gap-2 sm:grid-cols-3" aria-label="Shared services">
            {services.map(product => {
              const Icon = product.icon
              return <button key={product.id} type="button" aria-label={`Open ${product.label}`} onClick={() => openProduct(product.id)} className="flex min-w-0 items-center gap-3 rounded-xl border border-border p-3.5 text-left transition-colors hover:bg-muted/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
                <Icon className="h-7 w-7 shrink-0" title="" />
                <span className="min-w-0"><span className="block text-sm font-semibold">{product.label}</span><span className="mt-0.5 block text-xs leading-5 text-muted-foreground">{SERVICE_DESCRIPTIONS[product.id] || product.description}</span></span>
              </button>
            })}
          </div>
        </section>}

        <section aria-labelledby="welcome-tools" className="flex flex-wrap items-center justify-between gap-3 border-t border-border pt-4 text-xs text-muted-foreground">
          <span id="welcome-tools" className="flex items-center gap-2"><Terminal aria-hidden="true" className="h-4 w-4" />Use {appName} from your AI app</span>
          <button type="button" onClick={openMcpSetup} className="inline-flex min-h-8 items-center gap-2 rounded-md px-2 hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">Connect your AI app<ArrowRight aria-hidden="true" className="h-3.5 w-3.5" /></button>
        </section>

      </div>
    </main>
  )
}
