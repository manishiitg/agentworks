import { openWorkspaceSettings } from '../utils/workspaceSettingsNavigation'
import { useCallback, useEffect, useMemo, useState, type CSSProperties, type FormEvent } from 'react'
import { ArrowRight, ArrowUp, Loader2, Terminal, Menu } from 'lucide-react'
import { Button } from './ui/Button'
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
import { productRoutePath } from '../utils/productRoutes'
import api from '../services/api'


export const WELCOME_HOME_DISMISSED_KEY = 'agentworks_welcome_home_v1_dismissed'
export const OPEN_WELCOME_HOME_EVENT = 'open-welcome-home'
const PERSONAL_PRODUCTS = new Set<string>(['sparkquill'])
const WORK_PRODUCT_ORDER: ProductSurface[] = ['work', 'agentworks', 'relays']
const SERVICE_ORDER: ProductSurface[] = ['knowledgebase', 'mcp-gateway', 'bullpen']
const STARTING_POINTS: Partial<Record<ProductSurface, { heading: string; example: string; createLabel: string; openLabel: string }>> = {
  work: { heading: 'Delegate everyday work', example: 'Create an agent to research, organize information or handle follow-ups. Connect your apps and give it instructions for the tasks you want done.', createLabel: 'Create a Crew', openLabel: 'Open existing Crews' },
  agentworks: { heading: 'Put agents to work toward a target', example: 'Set a target, like publishing four posts a week or clearing a backlog. Agents carry out the work and track progress.', createLabel: 'Create a Goal', openLabel: 'Open existing Goals' },
  relays: { heading: 'Automate a process from start to finish', example: 'Connect steps for tasks you repeat: meeting notes → summary → tasks, or every Monday → weekly update.', createLabel: 'Create a Flow', openLabel: 'Open existing Flows' },
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
    const show = () => {
      if (window.location.pathname !== '/home') window.history.pushState({}, '', '/home')
      setOpen(true)
    }
    const syncLocation = () => {
      if (window.location.pathname === '/home') show()
      else leaveHome()
    }
    window.addEventListener(OPEN_WELCOME_HOME_EVENT, show)
    window.addEventListener('popstate', syncLocation)
    window.addEventListener('product-navigation-changed', syncLocation)
    return () => {
      window.removeEventListener(OPEN_WELCOME_HOME_EVENT, show)
      window.removeEventListener('popstate', syncLocation)
      window.removeEventListener('product-navigation-changed', syncLocation)
    }
  }, [leaveHome])

  useEffect(() => {
    if (autoOpen && !isGuideRemembered(WELCOME_HOME_DISMISSED_KEY) && products.length > 1) {
      window.history.replaceState({}, '', '/home')
      setOpen(true)
      window.dispatchEvent(new Event(OPEN_WELCOME_HOME_EVENT))
    }
  }, [autoOpen, products.length])

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
    setAgentWorksError('')
    setAgentWorksSending(true)
    try {
      const { data } = await api.post<{ crew_id: string }>('/api/agentworks', { message })
      rememberGuide(WELCOME_HOME_DISMISSED_KEY)
      window.location.assign(productRoutePath({ surface: 'work', id: data.crew_id }))
    } catch (error) {
      const response = (error as { response?: { data?: unknown } }).response
      setAgentWorksError(typeof response?.data === 'string' ? response.data : 'Your message could not be sent. Try again.')
      setAgentWorksSending(false)
    }
  }
  const openMcpSetup = () => openWorkspaceSettings('mcp')
  const openProviders = () => openWorkspaceSettings('providers')

  return (
    <main aria-label="Getting started"
      style={{ '--welcome-sidebar-width': `${sidebarWidth}px` } as CSSProperties}
      className="fixed inset-0 z-[39] overflow-y-auto bg-background md:left-[var(--welcome-sidebar-width)]">
      <div className="mx-auto flex min-h-screen max-w-5xl flex-col gap-6 px-5 py-8 sm:px-8 sm:py-10">
        <header className="flex items-start justify-between gap-4">
          <div>
            <p className="flex items-center gap-2 text-sm text-muted-foreground">
              {mark && <img src={mark} alt="" className="h-6 w-6 rounded-md" />}
              Welcome to {appName}
            </p>
            <h1 className="mt-3 text-3xl font-semibold tracking-tight text-foreground sm:text-4xl">Automate your work with AI agents</h1>
            <p className="mt-3 text-sm leading-6 text-muted-foreground">Connect your apps, describe the work, and choose how it should run.</p>
          </div>
          <button type="button" aria-label="Open navigation" onClick={() => window.dispatchEvent(new Event('open-app-sidebar'))}
            className="rounded-md p-2 text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring md:hidden"><Menu className="h-5 w-5" aria-hidden="true" /></button>
        </header>

        {workProducts.length > 0 && <WelcomeAiReadiness products={workProducts.map(product => product.id)} onSetup={openProviders} crewReadyView={<section aria-label="Ask AgentWorks">
          <form onSubmit={askAgentWorks} className="rounded-2xl border border-border bg-muted/20 p-3 focus-within:border-primary/60 focus-within:ring-1 focus-within:ring-primary/20 sm:p-4">
            <label htmlFor="welcome-agentworks-message" className="sr-only">Tell AgentWorks what you want to get done</label>
            <textarea id="welcome-agentworks-message" value={agentWorksDraft} onChange={event => setAgentWorksDraft(event.target.value)} maxLength={20000} rows={2} disabled={agentWorksSending}
              onKeyDown={event => { if (event.key === 'Enter' && !event.shiftKey && !event.nativeEvent.isComposing) { event.preventDefault(); event.currentTarget.form?.requestSubmit() } }}
              placeholder="Tell AgentWorks what you want to get done" className="block w-full resize-none bg-transparent text-sm leading-6 outline-none placeholder:text-muted-foreground disabled:opacity-50" />
            <div className="mt-2 flex items-center justify-between gap-3">
              {agentWorksError ? <p role="alert" className="text-xs text-destructive">{agentWorksError}</p> : <span />}
              <button type="submit" disabled={agentWorksSending || !agentWorksDraft.trim()} className="inline-flex min-h-9 shrink-0 items-center gap-2 rounded-lg bg-primary px-3 text-sm font-medium text-primary-foreground hover:bg-primary/90 disabled:opacity-50">
                {agentWorksSending ? <Loader2 aria-hidden="true" className="h-4 w-4 animate-spin" /> : <ArrowUp aria-hidden="true" className="h-4 w-4" />}Send
              </button>
            </div>
          </form>
          <button type="button" onClick={openProviders} className="mt-2 rounded-md px-1 text-xs text-muted-foreground underline-offset-2 hover:text-foreground hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">Manage AI accounts</button>
        </section>} />}

        {workProducts.length > 0 && <section aria-labelledby="welcome-products">
          <h2 id="welcome-products" className="sr-only">Choose where to work</h2>
          <div className="grid gap-4 md:grid-cols-3">
            {workProducts.map(product => {
              const Icon = product.icon
              const copy = STARTING_POINTS[product.id]!
              const creation = creationItems.find(item => item.surface === product.id)
              const actionLabel = creation ? copy.createLabel : `Open ${product.label}`
              return <article key={product.id} data-welcome-product={product.id} className={`flex min-w-0 flex-col overflow-hidden rounded-2xl border bg-card ${product.id === 'work' ? 'border-primary/50' : 'border-border'}`}>
                <button type="button" aria-label={actionLabel} onClick={() => {
                  if (creation) { if (openQuickNavigation(creation)) leaveHome() }
                  else openProduct(product.id)
                }} className="group flex flex-1 flex-col items-start p-5 text-left transition-colors hover:bg-muted/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring">
                  <span className="flex w-full items-center justify-between gap-2">
                    <Icon className="h-10 w-10 shrink-0" title="" />
                    {product.id === 'work' && <span className="rounded-full bg-primary/10 px-2.5 py-1 text-[11px] font-medium text-primary">Start here</span>}
                  </span>
                  <span className="mt-4 text-xl font-semibold text-foreground">{product.label}</span>
                  <span className="mt-1.5 text-sm font-medium text-foreground">{copy.heading}</span>
                  <span className="mt-2 text-xs leading-5 text-muted-foreground">{copy.example}</span>
                  <span className="mt-auto flex items-center gap-2 pt-5 text-sm font-medium text-primary">{actionLabel}<ArrowRight className="h-4 w-4 transition-transform group-hover:translate-x-0.5" aria-hidden="true" /></span>
                </button>
                {creation && <button type="button" onClick={() => openProduct(product.id)} className="border-t border-border px-5 py-3 text-left text-xs text-muted-foreground hover:bg-muted/40 hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring">{copy.openLabel}</button>}
              </article>
            })}
          </div>
        </section>}

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

        <section aria-labelledby="welcome-tools" className="flex flex-col items-start justify-between gap-5 rounded-2xl border border-primary/20 bg-primary/[0.04] p-5 sm:flex-row sm:items-center sm:p-6">
          <div className="flex items-start gap-3">
            <span className="flex h-10 w-10 shrink-0 items-center justify-center rounded-xl bg-primary/10 text-primary"><Terminal className="h-5 w-5" aria-hidden="true" /></span>
            <div><h2 id="welcome-tools" className="text-base font-semibold">Already use an AI app?</h2><p className="mt-1 max-w-md text-xs leading-5 text-muted-foreground">Connect ChatGPT, Claude or your favorite coding app to {appName}.</p></div>
          </div>
          <Button type="button" variant="outline" onClick={openMcpSetup} className="shrink-0">Connect your AI app<ArrowRight className="ml-2 h-4 w-4" aria-hidden="true" /></Button>
        </section>

      </div>
    </main>
  )
}
