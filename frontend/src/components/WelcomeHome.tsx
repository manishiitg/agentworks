import { openWorkspaceSettings } from '../utils/workspaceSettingsNavigation'
import { useCallback, useEffect, useMemo, useRef, useState, type CSSProperties, type FormEvent } from 'react'
import { ArrowRight, ArrowUp, Loader2, Terminal, Menu, LayoutDashboard } from 'lucide-react'
import { PRODUCT_CARDS } from './ProductSurfaceSwitcher'
import { useAuthStore } from '../stores/useAuthStore'
import { navigationProductSurfaceIDs, type ProductSurface } from '../products/productSurfaceConfig'
import { openProductWorkspace } from '../utils/productWorkspaceNavigation'
import { isGuideRemembered, rememberGuide } from '../utils/onboarding'
import { getRuntimeAppName, runtimeBrandingConfig } from '../runtime-branding'
import { useWorkspaceReturnStore } from '../stores/useWorkspaceReturnStore'
import { productUrlViewChanged } from '../utils/productUrlNavigation'
import WelcomeAiReadiness from './WelcomeAiReadiness'
import { useProductNavigationToolbarStore } from '../stores/useProductNavigationToolbarStore'
import { followAppLink, openAppProduct } from '../utils/appNavigation'
import { OPEN_HOME_EVENT } from '../utils/homeNavigation'
import { useWorkspaceConnectionStore } from '../stores/useWorkspaceConnectionStore'
import { captureChatIdentity, isChatIdentityCurrent } from '../utils/chatIdentity'
import { AgentWorksIcon, AGENTWORKS_SUBTITLE } from './ui/AgentWorksIdentity'
import { HomeUseCases } from './HomeUseCases'
import { HomeRecentWork } from './HomeRecentWork'
import { Button } from './ui/Button'
import api from '../services/api'
import { connectedLocations, hasDesktopConnections, useConnectedWorkStore } from '../stores/useConnectedWorkStore'
import { ServerIdentityIcon } from './workspace/ServerIdentityIcon'
import { serverIconColors } from '../utils/connectedWorkPresentation'
import { LocalSetup, LOCAL_SETUP_SEEN_KEY } from './LocalSetup'


export const WELCOME_HOME_DISMISSED_KEY = 'agentworks_welcome_home_v1_dismissed'
const WORK_PRODUCT_ORDER: ProductSurface[] = ['work', 'agentworks', 'relays']
const SERVICE_ORDER: ProductSurface[] = ['knowledgebase', 'mcp-gateway', 'bullpen']
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
  const connections = useConnectedWorkStore(connectedLocations)
  const currentConnection = hasDesktopConnections() && connections.length > 1 ? connections.find(connection => connection.id === server) : undefined
  const scope = `${server}:${user?.id || ''}`
  const prompt = useRef<HTMLTextAreaElement>(null)
  const [focusPrompt, setFocusPrompt] = useState(false)
  const operation = useRef(0)
  const [agentWorksError, setAgentWorksError] = useState('')
  const [agentWorksDraft, setAgentWorksDraft] = useState('')
  const [agentWorksSending, setAgentWorksSending] = useState(false)
  const products = useMemo(() => {
    const visible = new Set(navigationProductSurfaceIDs(user?.allowed_products))
    return PRODUCT_CARDS.filter(product => visible.has(product.id))
  }, [user?.allowed_products])
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

  // The new-user setup belongs to the Mac app's own view: it opens once on the first visit there, then from Home.
  const onThisMac = window.electronAPI?.getDesktopConnection?.() === 'local'
  const [setupOpen, setSetupOpen] = useState(() => onThisMac && !isGuideRemembered(LOCAL_SETUP_SEEN_KEY))
  const closeSetup = useCallback(() => { rememberGuide(LOCAL_SETUP_SEEN_KEY); setSetupOpen(false) }, [])
  if (!open) return null

  const branding = runtimeBrandingConfig()
  const appName = getRuntimeAppName(branding) || 'AgentWorks'
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

  return (
    <main aria-label="Home"
      style={{ '--welcome-sidebar-width': `${sidebarWidth}px` } as CSSProperties}
      className="fixed inset-0 z-[39] overflow-y-auto bg-background md:left-[var(--welcome-sidebar-width)]">
      <div className="mx-auto flex min-h-full max-h-none max-w-5xl flex-col gap-5 px-4 pb-12 pt-5 sm:px-8 sm:pt-6">
        <header className="flex justify-end md:hidden">
          <button type="button" aria-label="Open navigation" onClick={() => window.dispatchEvent(new Event('open-app-sidebar'))} className="rounded-md p-2 text-muted-foreground hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring md:hidden"><Menu className="h-5 w-5" aria-hidden="true" /></button>
        </header>

        <section aria-labelledby="home-heading" className="mx-auto w-full max-w-3xl pt-3 sm:pt-5">
          <h1 id="home-heading" className="text-center text-2xl font-semibold tracking-tight sm:text-3xl">What would you like to get done?</h1>
          <p className="mx-auto mt-2 max-w-xl text-center text-sm leading-6 text-muted-foreground">Start something new, automate a task, or pick up where you left off.</p>
          {currentConnection && <p className="mt-2 flex items-center justify-center gap-1.5 text-xs text-muted-foreground">{currentConnection.id !== 'local' && <ServerIdentityIcon connectionId={currentConnection.id} hidden className={`h-3 w-3 ${serverIconColors(connections)[currentConnection.server]}`} />}New work starts on {currentConnection.name}</p>}
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

        {workProducts.length > 0 && <HomeRecentWork key={`recent:${scope}`} />}

        {workProducts.length > 0 && <HomeUseCases key={`cases:${scope}`} products={workProducts.map(product => product.id)} onChoose={message => { setAgentWorksDraft(message); setFocusPrompt(true); requestAnimationFrame(() => prompt.current?.focus()) }} />}

        <section aria-labelledby="welcome-services">
          <h2 id="welcome-services" className="text-xs font-medium text-muted-foreground">Tools that work across your projects</h2>
          <div className="mt-3 grid gap-2 sm:grid-cols-2 lg:grid-cols-4" aria-label="Shared services">
            <a href="/dashboards" onClick={followAppLink} aria-label="Open Dashboards" className="flex min-w-0 items-center gap-3 rounded-xl border border-border p-3.5 text-left transition-colors hover:bg-muted/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
              <LayoutDashboard className="h-7 w-7 shrink-0" aria-hidden="true" />
              <span className="min-w-0"><span className="block text-sm font-semibold">Dashboards</span><span className="mt-0.5 block text-xs leading-5 text-muted-foreground">Your dashboards and those shared with you</span></span>
            </a>
            {services.map(product => {
              const Icon = product.icon
              return <button key={product.id} type="button" aria-label={`Open ${product.label}`} onClick={() => openProduct(product.id)} className="flex min-w-0 items-center gap-3 rounded-xl border border-border p-3.5 text-left transition-colors hover:bg-muted/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
                <Icon className="h-7 w-7 shrink-0" title="" />
                <span className="min-w-0"><span className="block text-sm font-semibold">{product.label}</span><span className="mt-0.5 block text-xs leading-5 text-muted-foreground">{SERVICE_DESCRIPTIONS[product.id] || product.description}</span></span>
              </button>
            })}
          </div>
        </section>

        <section aria-labelledby="welcome-tools" className="flex flex-col items-start justify-between gap-5 rounded-2xl border border-primary/20 bg-primary/[0.04] p-5 sm:flex-row sm:items-center sm:p-6">
          <div className="flex items-start gap-3">
            <span className="flex h-10 w-10 shrink-0 items-center justify-center rounded-xl bg-primary/10 text-primary"><Terminal className="h-5 w-5" aria-hidden="true" /></span>
            <div><h2 id="welcome-tools" className="text-base font-semibold">Already use an AI app?</h2><p className="mt-1 max-w-md text-xs leading-5 text-muted-foreground">Connect ChatGPT, Claude or your favorite coding app to {appName}.</p></div>
          </div>
          <Button type="button" variant="outline" onClick={openMcpSetup} className="shrink-0">Connect your AI app<ArrowRight className="ml-2 h-4 w-4" aria-hidden="true" /></Button>
        </section>

        {onThisMac && <section aria-labelledby="welcome-setup" className="flex flex-col items-start justify-between gap-4 rounded-2xl border border-dashed border-primary/40 p-5 sm:flex-row sm:items-center">
          <div><h2 id="welcome-setup" className="text-sm font-semibold">Start new-user setup</h2><p className="mt-1 max-w-md text-xs leading-5 text-muted-foreground">Check the AI programs on this Mac and optionally connect a server. Nothing you have is reset.</p></div>
          <Button type="button" onClick={() => setSetupOpen(true)} className="shrink-0">Start setup</Button>
        </section>}
        {setupOpen && <LocalSetup onClose={closeSetup} />}

      </div>
    </main>
  )
}
